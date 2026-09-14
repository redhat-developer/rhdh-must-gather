package collector

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"

	chartv2 "helm.sh/helm/v4/pkg/chart/v2"
	"helm.sh/helm/v4/pkg/release"
	"helm.sh/helm/v4/pkg/release/common"
	releasev1 "helm.sh/helm/v4/pkg/release/v1"
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

func makeTestRelease(name, ns string, version int, chartName, chartVersion, appVersion, description string) *releasev1.Release {
	return &releasev1.Release{
		Name:      name,
		Namespace: ns,
		Version:   version,
		Info: &releasev1.Info{
			Status:       common.StatusDeployed,
			LastDeployed: time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC),
			Description:  description,
		},
		Chart: &chartv2.Chart{
			Metadata: &chartv2.Metadata{
				Name:       chartName,
				Version:    chartVersion,
				AppVersion: appVersion,
			},
		},
	}
}

func TestChartLabel(t *testing.T) {
	rel := makeTestRelease("my-release", "default", 1, "backstage", "1.5.0", "1.4.0", "")
	acc, err := release.NewAccessor(rel)
	if err != nil {
		t.Fatalf("NewAccessor: %v", err)
	}
	got := chartLabel(acc)
	if got != "backstage-1.5.0" {
		t.Errorf("chartLabel() = %q, want %q", got, "backstage-1.5.0")
	}
}

func TestChartLabel_NoVersion(t *testing.T) {
	rel := makeTestRelease("my-release", "default", 1, "backstage", "", "1.4.0", "")
	acc, err := release.NewAccessor(rel)
	if err != nil {
		t.Fatalf("NewAccessor: %v", err)
	}
	got := chartLabel(acc)
	if got != "backstage-" {
		t.Errorf("chartLabel(no version) = %q, want %q", got, "backstage-")
	}
}

func TestChartAppVersion(t *testing.T) {
	rel := makeTestRelease("my-release", "default", 1, "backstage", "1.5.0", "1.4.0", "")
	acc, err := release.NewAccessor(rel)
	if err != nil {
		t.Fatalf("NewAccessor: %v", err)
	}
	got := chartAppVersion(acc)
	if got != "1.4.0" {
		t.Errorf("chartAppVersion() = %q, want %q", got, "1.4.0")
	}
}

func TestChartAppVersion_Empty(t *testing.T) {
	rel := makeTestRelease("my-release", "default", 1, "backstage", "1.5.0", "", "")
	acc, err := release.NewAccessor(rel)
	if err != nil {
		t.Fatalf("NewAccessor: %v", err)
	}
	got := chartAppVersion(acc)
	if got != "" {
		t.Errorf("chartAppVersion(no appVersion) = %q, want empty", got)
	}
}

func TestReleaseDescription(t *testing.T) {
	rel := makeTestRelease("my-release", "default", 1, "backstage", "1.0.0", "", "Install complete")
	got := releaseDescription(rel)
	if got != "Install complete" {
		t.Errorf("releaseDescription() = %q, want %q", got, "Install complete")
	}
}

func TestReleaseDescription_NilInfo(t *testing.T) {
	rel := &releasev1.Release{Name: "no-info"}
	got := releaseDescription(rel)
	if got != "" {
		t.Errorf("releaseDescription(nil info) = %q, want empty", got)
	}
}

func TestWriteReleasesJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "releases.json")

	rel := makeTestRelease("rhdh", "rhdh-operator", 3, "backstage", "1.5.0", "1.4.0", "Upgrade complete")
	acc, err := release.NewAccessor(rel)
	if err != nil {
		t.Fatalf("NewAccessor: %v", err)
	}

	h := &Helm{}
	h.writeReleasesJSON(path, []release.Accessor{acc})

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	var items []map[string]any
	if err := json.Unmarshal(data, &items); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("got %d items, want 1", len(items))
	}
	if items[0]["name"] != "rhdh" {
		t.Errorf("name = %v, want rhdh", items[0]["name"])
	}
	if items[0]["chart"] != "backstage-1.5.0" {
		t.Errorf("chart = %v, want backstage-1.5.0", items[0]["chart"])
	}
	if items[0]["app_version"] != "1.4.0" {
		t.Errorf("app_version = %v, want 1.4.0", items[0]["app_version"])
	}
	ts, ok := items[0]["updated"].(string)
	if !ok {
		t.Fatal("updated is not a string")
	}
	if _, err := time.Parse(time.RFC3339, ts); err != nil {
		t.Errorf("updated %q is not valid RFC3339: %v", ts, err)
	}
}

func TestWriteReleasesJSON_Empty(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "releases.json")

	h := &Helm{}
	h.writeReleasesJSON(path, nil)

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	var items []map[string]any
	if err := json.Unmarshal(data, &items); err != nil {
		t.Fatalf("Unmarshal: %v (data=%s)", err, data)
	}
	if len(items) != 0 {
		t.Errorf("got %d items, want 0", len(items))
	}
}
