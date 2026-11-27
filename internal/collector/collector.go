package collector

import (
	"context"
	"fmt"
	"io"
	"slices"
	"strings"
	"sync/atomic"
	"time"

	"helm.sh/helm/v4/pkg/action"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/redhat-developer/rhdh-must-gather/internal/kube"
	"github.com/redhat-developer/rhdh-must-gather/internal/namespace"
)

// PodOps abstracts pod operations that require a live API server.
type PodOps interface {
	Exec(ctx context.Context, ns, podName, container, script string) (string, error)
	ExecToFile(ctx context.Context, ns, podName, container, script, destPath string) error
	GetLogStream(ctx context.Context, ns, podName string, opts *corev1.PodLogOptions) (io.ReadCloser, error)
}

type Config struct {
	Client            *kube.Client
	BasePath          string
	Interrupted       *atomic.Bool
	WithSecrets       bool
	WithHeapDumps     bool
	Since             time.Duration
	SinceTime         string
	TargetNamespaces  []string
	HeapDumpMethod    string
	HeapDumpInstances string
	PodOps            PodOps
	HelmConfigFactory func(namespace string) (*action.Configuration, error)
}

type Collector interface {
	Name() string
	Run(ctx context.Context, cfg *Config) error
}

func (c *Config) IsInterrupted() bool {
	return c.Interrupted != nil && c.Interrupted.Load()
}

func (c *Config) Namespaces() []string {
	return c.TargetNamespaces
}

func (c *Config) ShouldInclude(ns string) bool {
	return namespace.Includes(c.TargetNamespaces, ns)
}

// ApplyLogSince sets SinceSeconds/SinceTime on opts based on the configured
// --since or --since-time value. Only one may be set; the CLI validates this.
func (c *Config) ApplyLogSince(opts *corev1.PodLogOptions) {
	if c.Since >= time.Second {
		s := int64(c.Since.Round(time.Second).Seconds())
		if s > 0 {
			opts.SinceSeconds = &s
		}
	}
	if c.SinceTime != "" {
		t, err := time.Parse(time.RFC3339, c.SinceTime)
		if err == nil {
			mt := metav1.NewTime(t)
			opts.SinceTime = &mt
		}
	}
}

func (c *Config) podOps() PodOps {
	if c.PodOps != nil {
		return c.PodOps
	}
	return &kubePodOps{config: c.Client.Config, client: c.Client.Clientset}
}

func (c *Config) helmActionConfig(namespace string) (*action.Configuration, error) {
	if c.HelmConfigFactory != nil {
		return c.HelmConfigFactory(namespace)
	}
	return newHelmActionConfig(c, namespace)
}

// RunMode describes how a collector is enabled from the command line.
type RunMode int

const (
	Default  RunMode = iota // Runs by default; user can disable with --without-<name>
	AlwaysOn                // Always runs; no CLI flag
	OptIn                   // Off by default; user enables with --with-<name>
)

// Registration ties a Collector to the metadata that drives flag generation and
// execution order, so that adding a collector is a single entry change.
type Registration struct {
	Name        string
	Collector   Collector
	RunMode     RunMode
	FlagDesc    string   // CLI flag help text; auto-generated when empty
	RunAfter    []string // collectors that must run first; enforced by InRunOrder
	FlagAliases []string // deprecated flag names; registered as hidden flags (OptIn only)
}

// registry is the single source of truth for the collectors shipped by this
// tool. Order is meaningful: it is the base execution order, except where a
// collector's RunAfter requires otherwise (see InRunOrder).
//
// namespace-inspect and network-policies both read the orchestrator's
// detected-namespaces.txt from the collection directory, which is why they
// declare a RunAfter dependency on orchestrator.
var registry = []Registration{
	{Name: "platform", Collector: &Platform{}, RunMode: Default},
	{Name: "helm", Collector: &Helm{}, RunMode: Default},
	{Name: "operator", Collector: &Operator{}, RunMode: Default},
	{Name: "orchestrator", Collector: &Orchestrator{}, RunMode: Default},
	{Name: "route", Collector: &Route{}, RunMode: Default},
	{Name: "ingress", Collector: &Ingress{}, RunMode: Default},
	{Name: "namespace-inspect", Collector: &NamespaceInspect{}, RunMode: Default, RunAfter: []string{"orchestrator"}},
	{Name: "network-policies", Collector: &NetworkPolicies{}, RunMode: AlwaysOn, RunAfter: []string{"orchestrator"}},
	{Name: "cluster-info", Collector: &ClusterInfo{}, RunMode: OptIn, FlagDesc: "Collect cluster-wide diagnostic information", FlagAliases: []string{"cluster-info"}},
}

var byName = func() map[string]Collector {
	m := make(map[string]Collector, len(registry))
	for _, r := range registry {
		m[r.Name] = r.Collector
	}
	return m
}()

// All returns a copy of the registry in its declared order. Callers may iterate
// the result without being able to mutate the registry itself.
func All() []Registration {
	return slices.Clone(registry)
}

// Get looks up a registered collector by name. Every name handed to callers
// originates from this registry, so a miss indicates a programming error rather
// than user input; TestRegistryIntegrity keeps that invariant honest.
func Get(name string) (Collector, bool) {
	c, ok := byName[name]
	return c, ok
}

// InRunOrder returns the registry in execution order. Declared order is the
// base, adjusted only as far as each collector's RunAfter requires, so
// unrelated collectors keep their original relative order. A misconfigured
// registry (dangling or circular RunAfter) is a programming error and panics at
// startup; TestRegistryIntegrity catches it in CI first.
func InRunOrder() []Registration {
	ordered, err := inRunOrder(registry)
	if err != nil {
		panic(err)
	}
	return ordered
}

func inRunOrder(regs []Registration) ([]Registration, error) {
	index := make(map[string]int, len(regs))
	for i, r := range regs {
		index[r.Name] = i
	}

	dependents := make([][]int, len(regs))
	indegree := make([]int, len(regs))
	for i, r := range regs {
		for _, dep := range r.RunAfter {
			j, ok := index[dep]
			if !ok {
				return nil, fmt.Errorf("%q declares RunAfter %q, which is not registered", r.Name, dep)
			}
			dependents[j] = append(dependents[j], i)
			indegree[i]++
		}
	}

	var ready []int
	for i := range regs {
		if indegree[i] == 0 {
			ready = append(ready, i)
		}
	}

	ordered := make([]Registration, 0, len(regs))
	for len(ready) > 0 {
		// Take the lowest declared position so independent collectors stay in
		// registry order.
		next := 0
		for k := 1; k < len(ready); k++ {
			if ready[k] < ready[next] {
				next = k
			}
		}
		i := ready[next]
		ready = append(ready[:next], ready[next+1:]...)

		ordered = append(ordered, regs[i])
		for _, d := range dependents[i] {
			indegree[d]--
			if indegree[d] == 0 {
				ready = append(ready, d)
			}
		}
	}

	if len(ordered) != len(regs) {
		stuck := make([]string, 0, len(regs))
		for i := range regs {
			if indegree[i] > 0 {
				stuck = append(stuck, regs[i].Name)
			}
		}
		return nil, fmt.Errorf("collector RunAfter cycle involving %s", strings.Join(stuck, ", "))
	}
	return ordered, nil
}
