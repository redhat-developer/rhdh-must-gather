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
		"platform":         true,
		"helm":             true,
		"operator":         true,
		"orchestrator":     true,
		"route":            true,
		"ingress":          true,
		"namespace-inspect": true,
		"cluster-info":     true,
		"network-policies": true,
	}

	for _, r := range Registrations {
		t.Run(r.Name, func(t *testing.T) {
			if r.Collector.Name() != r.Name {
				t.Errorf("Name() = %q, want %q", r.Collector.Name(), r.Name)
			}
			if !expected[r.Name] {
				t.Errorf("unexpected collector %q in Registrations", r.Name)
			}
			delete(expected, r.Name)
		})
	}

	for name := range expected {
		t.Errorf("missing collector %q in Registrations", name)
	}
}

func TestRegistrationModes(t *testing.T) {
	modes := make(map[string]RunMode)
	for _, r := range Registrations {
		modes[r.Name] = r.Mode
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

func TestRunAfterOrdering(t *testing.T) {
	position := make(map[string]int)
	for i, r := range Registrations {
		position[r.Name] = i
	}

	for _, r := range Registrations {
		for _, dep := range r.RunAfter {
			depPos, ok := position[dep]
			if !ok {
				t.Errorf("%s declares RunAfter %q, but %q is not registered", r.Name, dep, dep)
				continue
			}
			if depPos >= position[r.Name] {
				t.Errorf("%s (position %d) declares RunAfter %q (position %d): dependency must appear earlier",
					r.Name, position[r.Name], dep, depPos)
			}
		}
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
