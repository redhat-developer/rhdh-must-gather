package collector

import (
	"context"
	"sync/atomic"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/redhat-developer/rhdh-must-gather/internal/kube"
	"github.com/redhat-developer/rhdh-must-gather/internal/namespace"
)

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

var Registry = map[string]Collector{
	"platform":     &Platform{},
	"route":        &Route{},
	"ingress":      &Ingress{},
	"cluster-info": &ClusterInfo{},
	"operator":     &Operator{},
	"orchestrator": &Orchestrator{},
	"helm":              &Helm{},
	"namespace-inspect": &NamespaceInspect{},
}
