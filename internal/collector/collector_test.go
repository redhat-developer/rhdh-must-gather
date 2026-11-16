package collector

import (
	"sync/atomic"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
)

func TestConfig_IsInterrupted(t *testing.T) {
	tests := []struct {
		name string
		cfg  *Config
		want bool
	}{
		{
			name: "nil interrupted",
			cfg:  &Config{},
			want: false,
		},
		{
			name: "not interrupted",
			cfg:  &Config{Interrupted: new(atomic.Bool)},
			want: false,
		},
		{
			name: "interrupted",
			cfg: func() *Config {
				b := new(atomic.Bool)
				b.Store(true)
				return &Config{Interrupted: b}
			}(),
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.cfg.IsInterrupted(); got != tt.want {
				t.Errorf("IsInterrupted() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestConfig_Namespaces(t *testing.T) {
	t.Run("nil", func(t *testing.T) {
		cfg := &Config{}
		if got := cfg.Namespaces(); got != nil {
			t.Errorf("Namespaces() = %v, want nil", got)
		}
	})

	t.Run("populated", func(t *testing.T) {
		cfg := &Config{TargetNamespaces: []string{"ns1", "ns2"}}
		got := cfg.Namespaces()
		if len(got) != 2 || got[0] != "ns1" || got[1] != "ns2" {
			t.Errorf("Namespaces() = %v, want [ns1 ns2]", got)
		}
	})
}

func TestConfig_ShouldInclude(t *testing.T) {
	tests := []struct {
		name       string
		namespaces []string
		ns         string
		want       bool
	}{
		{
			name:       "no filter includes all",
			namespaces: nil,
			ns:         "anything",
			want:       true,
		},
		{
			name:       "match",
			namespaces: []string{"ns1", "ns2"},
			ns:         "ns2",
			want:       true,
		},
		{
			name:       "no match",
			namespaces: []string{"ns1", "ns2"},
			ns:         "ns3",
			want:       false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &Config{TargetNamespaces: tt.namespaces}
			if got := cfg.ShouldInclude(tt.ns); got != tt.want {
				t.Errorf("ShouldInclude(%q) = %v, want %v", tt.ns, got, tt.want)
			}
		})
	}
}

func TestConfig_ApplyLogSince(t *testing.T) {
	t.Run("since duration", func(t *testing.T) {
		cfg := &Config{Since: 5 * time.Minute}
		opts := &corev1.PodLogOptions{}
		cfg.ApplyLogSince(opts)

		if opts.SinceSeconds == nil {
			t.Fatal("expected SinceSeconds to be set")
		}
		if *opts.SinceSeconds != 300 {
			t.Errorf("SinceSeconds = %d, want 300", *opts.SinceSeconds)
		}
		if opts.SinceTime != nil {
			t.Error("expected SinceTime to be nil")
		}
	})

	t.Run("since time", func(t *testing.T) {
		cfg := &Config{SinceTime: "2024-01-15T10:00:00Z"}
		opts := &corev1.PodLogOptions{}
		cfg.ApplyLogSince(opts)

		if opts.SinceTime == nil {
			t.Fatal("expected SinceTime to be set")
		}
		if opts.SinceTime.Year() != 2024 || opts.SinceTime.Month() != 1 {
			t.Errorf("SinceTime = %v, want 2024-01-15", opts.SinceTime)
		}
	})

	t.Run("neither set", func(t *testing.T) {
		cfg := &Config{}
		opts := &corev1.PodLogOptions{}
		cfg.ApplyLogSince(opts)

		if opts.SinceSeconds != nil {
			t.Error("expected SinceSeconds to be nil")
		}
		if opts.SinceTime != nil {
			t.Error("expected SinceTime to be nil")
		}
	})

	t.Run("invalid since time", func(t *testing.T) {
		cfg := &Config{SinceTime: "not-a-time"}
		opts := &corev1.PodLogOptions{}
		cfg.ApplyLogSince(opts)

		if opts.SinceTime != nil {
			t.Error("expected SinceTime to be nil for invalid input")
		}
	})
}

func TestRegistrations(t *testing.T) {
	expected := map[string]bool{
		"platform":          true,
		"helm":              true,
		"operator":          true,
		"orchestrator":      true,
		"route":             true,
		"ingress":           true,
		"namespace-inspect": true,
		"cluster-info":      true,
		"network-policies":  true,
	}

	for _, r := range registry {
		t.Run(r.Name, func(t *testing.T) {
			if r.Collector.Name() != r.Name {
				t.Errorf("Name() = %q, want %q", r.Collector.Name(), r.Name)
			}
			if !expected[r.Name] {
				t.Errorf("unexpected collector %q in registry", r.Name)
			}
			delete(expected, r.Name)
		})
	}

	for name := range expected {
		t.Errorf("missing collector %q in registry", name)
	}
}

func TestRegistrationModes(t *testing.T) {
	modes := make(map[string]RunMode)
	for _, r := range registry {
		modes[r.Name] = r.RunMode
	}

	if modes["network-policies"] != AlwaysOn {
		t.Error("network-policies should be AlwaysOn")
	}
	if modes["cluster-info"] != OptIn {
		t.Error("cluster-info should be OptIn")
	}
	for _, name := range []string{"platform", "helm", "operator", "orchestrator", "route", "ingress", "namespace-inspect"} {
		if modes[name] != Default {
			t.Errorf("%s should be Default, got %d", name, modes[name])
		}
	}
}

// TestRegistryIntegrity guards the invariants that let gather.go skip a lookup
// check: every emitted name exists, every collector is usable, and ordering
// metadata is consistent.
func TestRegistryIntegrity(t *testing.T) {
	positions := make(map[string]int, len(registry))
	for i, r := range registry {
		if r.Name == "" {
			t.Errorf("registry entry %d has an empty name", i)
			continue
		}
		if prev, dup := positions[r.Name]; dup {
			t.Errorf("collector %q registered twice (positions %d and %d)", r.Name, prev, i)
		}
		positions[r.Name] = i
	}

	for _, r := range registry {
		if r.Collector == nil {
			t.Errorf("collector %q has a nil Collector", r.Name)
			continue
		}
		if r.Collector.Name() != r.Name {
			t.Errorf("collector %q: Collector.Name() = %q", r.Name, r.Collector.Name())
		}
		if r.RunMode < Default || r.RunMode > OptIn {
			t.Errorf("collector %q has invalid RunMode %d", r.Name, r.RunMode)
		}
		if len(r.FlagAliases) > 0 && r.RunMode != OptIn {
			t.Errorf("collector %q declares flag aliases but is not OptIn", r.Name)
		}
		for _, alias := range r.FlagAliases {
			if alias == "" {
				t.Errorf("collector %q has an empty flag alias", r.Name)
			}
			if alias == "with-"+r.Name || alias == "without-"+r.Name {
				t.Errorf("collector %q: flag alias %q collides with its own generated flag", r.Name, alias)
			}
		}
	}

	ordered, err := inRunOrder(registry)
	if err != nil {
		t.Fatalf("registry ordering: %v", err)
	}
	if len(ordered) != len(registry) {
		t.Errorf("inRunOrder returned %d collectors, want %d", len(ordered), len(registry))
	}
}

func TestInRunOrder_DeclaredOrderIsAlreadyValid(t *testing.T) {
	ordered := InRunOrder()

	if len(ordered) != len(registry) {
		t.Fatalf("InRunOrder returned %d collectors, want %d", len(ordered), len(registry))
	}
	for i, r := range ordered {
		if r.Name != registry[i].Name {
			t.Fatalf("InRunOrder[%d] = %q, want %q: declared order should already respect RunAfter",
				i, r.Name, registry[i].Name)
		}
	}
}

func TestInRunOrder_ReordersDependencies(t *testing.T) {
	// Same registrations, deliberately declared in the wrong order.
	shuffled := []Registration{
		{Name: "network-policies", RunMode: AlwaysOn, RunAfter: []string{"orchestrator"}},
		{Name: "namespace-inspect", RunMode: Default, RunAfter: []string{"orchestrator"}},
		{Name: "route", RunMode: Default},
		{Name: "orchestrator", RunMode: Default},
	}

	ordered, err := inRunOrder(shuffled)
	if err != nil {
		t.Fatalf("inRunOrder: %v", err)
	}

	positions := make(map[string]int, len(ordered))
	for i, r := range ordered {
		positions[r.Name] = i
	}
	if len(positions) != len(shuffled) {
		t.Fatalf("inRunOrder lost or duplicated collectors: got %d, want %d", len(positions), len(shuffled))
	}
	for _, r := range shuffled {
		for _, dep := range r.RunAfter {
			if positions[dep] >= positions[r.Name] {
				t.Errorf("%q (position %d) did not run after %q (position %d)",
					r.Name, positions[r.Name], dep, positions[dep])
			}
		}
	}
	// Ties keep declared order: route precedes orchestrator in the input.
	if positions["route"] > positions["orchestrator"] {
		t.Errorf("inRunOrder should be stable: route (position %d) moved after orchestrator (position %d)",
			positions["route"], positions["orchestrator"])
	}
}

func TestInRunOrder_RejectsDanglingAndCircularDependencies(t *testing.T) {
	t.Run("dangling", func(t *testing.T) {
		_, err := inRunOrder([]Registration{
			{Name: "a", RunMode: Default, RunAfter: []string{"missing"}},
		})
		if err == nil {
			t.Fatal("expected an error for an unregistered RunAfter target")
		}
	})

	t.Run("cycle", func(t *testing.T) {
		_, err := inRunOrder([]Registration{
			{Name: "a", RunMode: Default, RunAfter: []string{"b"}},
			{Name: "b", RunMode: Default, RunAfter: []string{"a"}},
		})
		if err == nil {
			t.Fatal("expected an error for a RunAfter cycle")
		}
	})
}

func TestAll_ReturnsCopy(t *testing.T) {
	all := All()
	if len(all) != len(registry) {
		t.Fatalf("All() returned %d collectors, want %d", len(all), len(registry))
	}

	all[0].Name = "mutated"
	if registry[0].Name == "mutated" {
		t.Error("All() exposed the underlying registry slice to mutation")
	}
}

func TestGet(t *testing.T) {
	c, ok := Get("platform")
	if !ok {
		t.Fatal("Get(platform) returned false")
	}
	if c.Name() != "platform" {
		t.Errorf("Get(platform).Name() = %q", c.Name())
	}

	_, ok = Get("nonexistent")
	if ok {
		t.Error("Get(nonexistent) should return false")
	}
}
