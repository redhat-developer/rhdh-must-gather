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

func TestRegistry(t *testing.T) {
	expected := []string{
		"platform", "route", "ingress", "cluster-info",
		"operator", "orchestrator", "helm", "namespace-inspect",
	}

	for _, name := range expected {
		t.Run(name, func(t *testing.T) {
			c, ok := Registry[name]
			if !ok {
				t.Fatalf("Registry missing collector %q", name)
			}
			if c.Name() != name {
				t.Errorf("Name() = %q, want %q", c.Name(), name)
			}
		})
	}

	if len(Registry) != len(expected) {
		t.Errorf("Registry has %d collectors, want %d", len(Registry), len(expected))
	}
}
