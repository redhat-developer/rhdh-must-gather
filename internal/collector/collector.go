package collector

import (
	"context"
	"sync/atomic"

	"github.com/redhat-developer/rhdh-must-gather/internal/kube"
	"github.com/redhat-developer/rhdh-must-gather/internal/namespace"
)

type Config struct {
	Client        *kube.Client
	BasePath      string
	Interrupted   *atomic.Bool
	WithSecrets   bool
	WithHeapDumps bool
	ScriptDir     string
	Env           []string
}

type Collector interface {
	Name() string
	Run(ctx context.Context, cfg *Config) error
}

func (c *Config) IsInterrupted() bool {
	return c.Interrupted != nil && c.Interrupted.Load()
}

func (c *Config) Namespaces() []string {
	return namespace.TargetNamespaces()
}

func (c *Config) ShouldInclude(ns string) bool {
	return namespace.ShouldInclude(ns)
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
