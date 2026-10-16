package collector

import (
	"context"
	"io"
	"sync/atomic"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"helm.sh/helm/v4/pkg/action"

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

type RunMode int

const (
	Default  RunMode = iota // Runs by default; user can disable with --without-<name>
	AlwaysOn               // Always runs; no CLI flag
	OptIn                  // Off by default; user enables with --with-<name>
)

type Registration struct {
	Name      string
	Collector Collector
	Mode      RunMode
	FlagDesc  string // CLI flag help text; auto-generated if empty
}

var Registrations = []Registration{
	{Name: "platform", Collector: &Platform{}, Mode: Default},
	{Name: "helm", Collector: &Helm{}, Mode: Default},
	{Name: "operator", Collector: &Operator{}, Mode: Default},
	{Name: "orchestrator", Collector: &Orchestrator{}, Mode: Default},
	{Name: "route", Collector: &Route{}, Mode: Default},
	{Name: "ingress", Collector: &Ingress{}, Mode: Default},
	{Name: "namespace-inspect", Collector: &NamespaceInspect{}, Mode: Default},
	{Name: "network-policies", Collector: &NetworkPolicies{}, Mode: AlwaysOn},
	{Name: "cluster-info", Collector: &ClusterInfo{}, Mode: OptIn, FlagDesc: "Collect cluster-wide diagnostic information"},
}

func Get(name string) (Collector, bool) {
	for _, r := range Registrations {
		if r.Name == name {
			return r.Collector, true
		}
	}
	return nil, false
}
