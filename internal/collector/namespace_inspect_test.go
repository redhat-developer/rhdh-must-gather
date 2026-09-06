package collector

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
)

func TestNamespaceInspectName(t *testing.T) {
	n := &NamespaceInspect{}
	if got := n.Name(); got != "namespace-inspect" {
		t.Errorf("Name() = %q, want namespace-inspect", got)
	}
}

func TestMatchesAnyPattern(t *testing.T) {
	patterns := []string{"backstage", "rhdh", "developer-hub"}

	tests := []struct {
		value string
		want  bool
	}{
		{"backstage-chart-1.0", true},
		{"RHDH-Helm", true},
		{"developer-hub-app", true},
		{"postgres", false},
		{"", false},
		{"Backstage", true},
	}
	for _, tt := range tests {
		if got := matchesAnyPattern(tt.value, patterns); got != tt.want {
			t.Errorf("matchesAnyPattern(%q) = %v, want %v", tt.value, got, tt.want)
		}
	}
}

func TestContainsImagePattern(t *testing.T) {
	patterns := []string{"quay.io/rhdh", "registry.redhat.io/rhdh", "ghcr.io/backstage/backstage"}

	tests := []struct {
		name       string
		containers []corev1.Container
		want       bool
	}{
		{
			name:       "matching image",
			containers: []corev1.Container{{Image: "quay.io/rhdh/rhdh-hub-rhel9:1.5"}},
			want:       true,
		},
		{
			name:       "no match",
			containers: []corev1.Container{{Image: "postgres:15"}},
			want:       false,
		},
		{
			name:       "empty containers",
			containers: nil,
			want:       false,
		},
		{
			name: "multiple containers one match",
			containers: []corev1.Container{
				{Image: "postgres:15"},
				{Image: "ghcr.io/backstage/backstage:latest"},
			},
			want: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := containsImagePattern(tt.containers, patterns); got != tt.want {
				t.Errorf("containsImagePattern() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestEnvOrNone(t *testing.T) {
	t.Setenv("TEST_ENV_OR_NONE_SET", "value123")

	if got := envOrNone("TEST_ENV_OR_NONE_SET"); got != "value123" {
		t.Errorf("envOrNone(set) = %q, want value123", got)
	}
	if got := envOrNone("TEST_ENV_OR_NONE_UNSET"); got != "none" {
		t.Errorf("envOrNone(unset) = %q, want none", got)
	}
}
