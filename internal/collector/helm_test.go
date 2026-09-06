package collector

import (
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
)

func TestFilterSecretsFromYAML(t *testing.T) {
	input := `apiVersion: v1
kind: ConfigMap
metadata:
  name: my-config
data:
  key: value
---
apiVersion: v1
kind: Secret
metadata:
  name: my-secret
data:
  password: cGFzc3dvcmQ=
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: my-deploy
`
	result := filterSecretsFromYAML(input)

	if strings.Contains(result, "kind: Secret") {
		t.Error("Secret should be filtered out")
	}
	if !strings.Contains(result, "kind: ConfigMap") {
		t.Error("ConfigMap should be preserved")
	}
	if !strings.Contains(result, "kind: Deployment") {
		t.Error("Deployment should be preserved")
	}
}

func TestFilterSecretsFromYAML_NoSecrets(t *testing.T) {
	input := `apiVersion: v1
kind: ConfigMap
metadata:
  name: my-config
`
	result := filterSecretsFromYAML(input)
	if !strings.Contains(result, "kind: ConfigMap") {
		t.Error("ConfigMap should be preserved")
	}
}

func TestFilterSecretsFromYAML_AllSecrets(t *testing.T) {
	input := `apiVersion: v1
kind: Secret
metadata:
  name: secret1
---
apiVersion: v1
kind: Secret
metadata:
  name: secret2
`
	result := filterSecretsFromYAML(input)
	if strings.Contains(result, "Secret") {
		t.Error("All secrets should be filtered out")
	}
}

func TestFilterSecretsFromYAML_Empty(t *testing.T) {
	result := filterSecretsFromYAML("")
	if result != "" {
		t.Errorf("expected empty string, got %q", result)
	}
}

func TestExtractWorkloadNames(t *testing.T) {
	manifest := `apiVersion: v1
kind: Service
metadata:
  name: my-service
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: backstage-rhdh
---
apiVersion: apps/v1
kind: StatefulSet
metadata:
  name: backstage-psql-rhdh
`
	deploy, sts := extractWorkloadNames(manifest)
	if deploy != "backstage-rhdh" {
		t.Errorf("deploy = %q, want backstage-rhdh", deploy)
	}
	if sts != "backstage-psql-rhdh" {
		t.Errorf("sts = %q, want backstage-psql-rhdh", sts)
	}
}

func TestExtractWorkloadNames_NoWorkloads(t *testing.T) {
	manifest := `apiVersion: v1
kind: Service
metadata:
  name: my-service
`
	deploy, sts := extractWorkloadNames(manifest)
	if deploy != "" {
		t.Errorf("deploy = %q, want empty", deploy)
	}
	if sts != "" {
		t.Errorf("sts = %q, want empty", sts)
	}
}

func TestIsSecretDocument(t *testing.T) {
	tests := []struct {
		yaml string
		want bool
	}{
		{"kind: Secret\napiVersion: v1\nmetadata:\n  name: s", true},
		{"kind: ConfigMap\napiVersion: v1\nmetadata:\n  name: c", false},
		{"kind: Deployment\napiVersion: apps/v1\nmetadata:\n  name: d", false},
	}

	for _, tt := range tests {
		// We need to simulate what the YAML decoder produces
		// Testing the filterSecretsFromYAML function indirectly instead
		result := filterSecretsFromYAML(tt.yaml)
		hasContent := strings.TrimSpace(result) != ""
		if tt.want && hasContent {
			t.Errorf("expected Secret to be filtered from: %s", tt.yaml)
		}
		if !tt.want && !hasContent {
			t.Errorf("expected non-Secret to be preserved: %s", tt.yaml)
		}
	}
}

func TestIsRHDHHelmWorkload(t *testing.T) {
	tests := []struct {
		name   string
		labels map[string]string
		want   bool
	}{
		{"helm chart label", map[string]string{"helm.sh/chart": "backstage-1.0.0"}, true},
		{"app name label", map[string]string{"app.kubernetes.io/name": "rhdh"}, true},
		{"instance label", map[string]string{"app.kubernetes.io/instance": "developer-hub"}, true},
		{"unrelated labels", map[string]string{"app": "nginx"}, false},
		{"empty labels", map[string]string{}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Use empty PodSpec (no image matching)
			got := isRHDHHelmWorkload(tt.labels, emptyPodSpec())
			if got != tt.want {
				t.Errorf("isRHDHHelmWorkload(%v) = %v, want %v", tt.labels, got, tt.want)
			}
		})
	}
}

func TestHelmName(t *testing.T) {
	h := &Helm{}
	if got := h.Name(); got != "helm" {
		t.Errorf("Name() = %q, want helm", got)
	}
}

func emptyPodSpec() corev1.PodSpec {
	return corev1.PodSpec{}
}
