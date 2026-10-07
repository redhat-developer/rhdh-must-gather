package collector

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"helm.sh/helm/v4/pkg/action"
	chartv2 "helm.sh/helm/v4/pkg/chart/v2"
	kubefake "helm.sh/helm/v4/pkg/kube/fake"
	"helm.sh/helm/v4/pkg/release"
	"helm.sh/helm/v4/pkg/release/common"
	releasev1 "helm.sh/helm/v4/pkg/release/v1"
	"helm.sh/helm/v4/pkg/storage"
	"helm.sh/helm/v4/pkg/storage/driver"
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
kind: Deployment
metadata:
  name: backstage-rhdh-ia-okp
---
apiVersion: apps/v1
kind: StatefulSet
metadata:
  name: backstage-psql-rhdh
`
	deployments, sts := extractWorkloadNames(manifest)
	wantDeployments := []string{"backstage-rhdh", "backstage-rhdh-ia-okp"}
	if !reflect.DeepEqual(deployments, wantDeployments) {
		t.Errorf("deployments = %q, want %q", deployments, wantDeployments)
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
	deployments, sts := extractWorkloadNames(manifest)
	if len(deployments) != 0 {
		t.Errorf("deployments = %q, want empty", deployments)
	}
	if sts != "" {
		t.Errorf("sts = %q, want empty", sts)
	}
}

func TestDeploymentHasContainer(t *testing.T) {
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "rhdh"},
		Spec: appsv1.DeploymentSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{Name: "backstage-backend"},
						{Name: "lightspeed-core"},
					},
				},
			},
		},
	}

	if !deploymentHasContainer(dep, "backstage-backend") {
		t.Error("expected backstage-backend container to identify the RHDH Deployment")
	}
	if deploymentHasContainer(dep, "okp") {
		t.Error("did not expect an OKP container in the RHDH Deployment")
	}
}

func TestSelectPrimaryDeployment(t *testing.T) {
	deployment := func(name string, containers ...string) *appsv1.Deployment {
		dep := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: name}}
		for _, container := range containers {
			dep.Spec.Template.Spec.Containers = append(dep.Spec.Template.Spec.Containers, corev1.Container{Name: container})
		}
		return dep
	}

	tests := []struct {
		name        string
		deployments []*appsv1.Deployment
		want        string
	}{
		{
			name: "multiple primary candidates use the first name",
			deployments: []*appsv1.Deployment{
				deployment("z-rhdh", "backstage-backend"),
				deployment("okp", "okp"),
				deployment("a-rhdh", "backstage-backend"),
			},
			want: "a-rhdh",
		},
		{
			name: "first name is the fallback when no candidate matches",
			deployments: []*appsv1.Deployment{
				deployment("z-dependency", "worker"),
				deployment("a-dependency", "okp"),
			},
			want: "a-dependency",
		},
		{
			name: "empty list has no primary",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := selectPrimaryDeployment(tt.deployments)
			if got == nil {
				if tt.want != "" {
					t.Fatalf("selectPrimaryDeployment() = nil, want %q", tt.want)
				}
				return
			}
			if got.Name != tt.want {
				t.Errorf("selectPrimaryDeployment() = %q, want %q", got.Name, tt.want)
			}
		})
	}
}

func TestIsSecretDocument(t *testing.T) {
	tests := []struct {
		name string
		yaml string
		want bool
	}{
		{"secret", "kind: Secret\napiVersion: v1\nmetadata:\n  name: s", true},
		{"configmap", "kind: ConfigMap\napiVersion: v1\nmetadata:\n  name: c", false},
		{"deployment", "kind: Deployment\napiVersion: apps/v1\nmetadata:\n  name: d", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := filterSecretsFromYAML(tt.yaml)
			hasContent := strings.TrimSpace(result) != ""
			if tt.want && hasContent {
				t.Errorf("expected Secret to be filtered from: %s", tt.yaml)
			}
			if !tt.want && !hasContent {
				t.Errorf("expected non-Secret to be preserved: %s", tt.yaml)
			}
		})
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

func TestWorkloadKey(t *testing.T) {
	tests := []struct {
		kind WorkloadKind
		ns   string
		name string
		want string
	}{
		{KindDeployment, "ns1", "my-deploy", "deployment/ns1/my-deploy"},
		{KindStatefulSet, "ns2", "my-sts", "statefulset/ns2/my-sts"},
	}
	for _, tt := range tests {
		got := workloadKey(tt.kind, tt.ns, tt.name)
		if got != tt.want {
			t.Errorf("workloadKey(%q, %q, %q) = %q, want %q", tt.kind, tt.ns, tt.name, got, tt.want)
		}
	}
}

func TestIsRHDHHelmWorkload_ImageMatch(t *testing.T) {
	tests := []struct {
		name    string
		podSpec corev1.PodSpec
		want    bool
	}{
		{
			name: "quay.io rhdh image",
			podSpec: corev1.PodSpec{
				Containers: []corev1.Container{
					{Name: "backstage", Image: "quay.io/rhdh/rhdh-hub-rhel9:latest"},
				},
			},
			want: true,
		},
		{
			name: "registry.redhat.io rhdh image",
			podSpec: corev1.PodSpec{
				Containers: []corev1.Container{
					{Name: "backstage", Image: "registry.redhat.io/rhdh/rhdh-rhel9-operator:1.5"},
				},
			},
			want: true,
		},
		{
			name: "ghcr.io backstage image",
			podSpec: corev1.PodSpec{
				Containers: []corev1.Container{
					{Name: "backstage", Image: "ghcr.io/backstage/backstage:latest"},
				},
			},
			want: true,
		},
		{
			name: "rhdh init container image",
			podSpec: corev1.PodSpec{
				InitContainers: []corev1.Container{
					{Name: "init", Image: "quay.io/rhdh/rhdh-hub-rhel9:latest"},
				},
			},
			want: true,
		},
		{
			name: "unrelated image",
			podSpec: corev1.PodSpec{
				Containers: []corev1.Container{
					{Name: "app", Image: "postgres:15"},
				},
			},
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isRHDHHelmWorkload(map[string]string{}, tt.podSpec)
			if got != tt.want {
				t.Errorf("isRHDHHelmWorkload() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestWriteReleasesTable(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "releases.txt")

	rel := makeTestRelease("rhdh", "rhdh-ns", 3, "backstage", "1.5.0", "1.4.0", "")
	acc, err := release.NewAccessor(rel)
	if err != nil {
		t.Fatalf("NewAccessor: %v", err)
	}

	h := &Helm{}
	h.writeReleasesTable(path, []release.Accessor{acc})

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "NAME") || !strings.Contains(content, "NAMESPACE") {
		t.Error("expected table header")
	}
	if !strings.Contains(content, "rhdh") {
		t.Error("expected release name")
	}
	if !strings.Contains(content, "rhdh-ns") {
		t.Error("expected namespace")
	}
	if !strings.Contains(content, "backstage-1.5.0") {
		t.Error("expected chart label")
	}
}

func TestWriteReleasesTable_Empty(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "releases.txt")

	h := &Helm{}
	h.writeReleasesTable(path, nil)

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !strings.Contains(string(data), "No RHDH-related Helm releases found") {
		t.Error("expected no-releases message")
	}
}

func TestWriteHistoryText(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "history.txt")

	releases := []release.Releaser{
		makeTestRelease("rhdh", "ns", 1, "backstage", "1.0.0", "1.0.0", "Install complete"),
		makeTestRelease("rhdh", "ns", 2, "backstage", "1.1.0", "1.1.0", "Upgrade complete"),
	}

	h := &Helm{}
	h.writeHistoryText(path, releases)

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "REVISION") {
		t.Error("expected header")
	}
	if !strings.Contains(content, "Install complete") {
		t.Error("expected first description")
	}
	if !strings.Contains(content, "Upgrade complete") {
		t.Error("expected second description")
	}
}

func TestHistoryToMap(t *testing.T) {
	releases := []release.Releaser{
		makeTestRelease("rhdh", "ns", 1, "backstage", "1.0.0", "1.0.0", "Install complete"),
		makeTestRelease("rhdh", "ns", 2, "backstage", "1.1.0", "1.1.0", "Upgrade"),
	}

	h := &Helm{}
	result := h.historyToMap(releases)

	if len(result) != 2 {
		t.Fatalf("got %d items, want 2", len(result))
	}
	if result[0]["revision"] != 1 {
		t.Errorf("revision = %v, want 1", result[0]["revision"])
	}
	if result[0]["description"] != "Install complete" {
		t.Errorf("description = %v, want Install complete", result[0]["description"])
	}
	if result[1]["chart"] != "backstage-1.1.0" {
		t.Errorf("chart = %v, want backstage-1.1.0", result[1]["chart"])
	}
}

func TestHistoryToMap_Empty(t *testing.T) {
	h := &Helm{}
	result := h.historyToMap(nil)
	if len(result) != 0 {
		t.Errorf("got %d items, want 0", len(result))
	}
}

func TestFormatReleaseStatus(t *testing.T) {
	rel := makeTestRelease("rhdh", "rhdh-ns", 5, "backstage", "1.5.0", "1.4.0", "Upgrade complete")
	acc, err := release.NewAccessor(rel)
	if err != nil {
		t.Fatalf("NewAccessor: %v", err)
	}

	output := formatReleaseStatus(acc, rel)
	if !strings.Contains(output, "NAME: rhdh") {
		t.Error("expected NAME")
	}
	if !strings.Contains(output, "NAMESPACE: rhdh-ns") {
		t.Error("expected NAMESPACE")
	}
	if !strings.Contains(output, "REVISION: 5") {
		t.Error("expected REVISION")
	}
	if !strings.Contains(output, "DESCRIPTION: Upgrade complete") {
		t.Error("expected DESCRIPTION")
	}
	if !strings.Contains(output, "STATUS: deployed") {
		t.Error("expected STATUS")
	}
}

func TestWriteStandaloneNote(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "standalone-note.txt")

	h := &Helm{}
	h.writeStandaloneNote(path, "my-namespace", "my-instance")

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "Standalone RHDH Helm Deployment") {
		t.Error("expected header")
	}
	if !strings.Contains(content, "Namespace: my-namespace") {
		t.Error("expected namespace")
	}
	if !strings.Contains(content, "Workload: my-instance") {
		t.Error("expected workload name")
	}
}

func TestWriteHelmMetadata_Deployment(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "helm-metadata.txt")

	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "backstage-rhdh",
			Namespace: "rhdh",
			Labels: map[string]string{
				"helm.sh/chart":                "backstage-1.5.0",
				"app.kubernetes.io/name":       "backstage",
				"app.kubernetes.io/instance":   "rhdh",
				"app.kubernetes.io/version":    "1.4.0",
				"app.kubernetes.io/managed-by": "Helm",
			},
		},
	}

	cfg := newTestConfig(t, dir, withTypedObjs(dep))
	h := &Helm{}
	h.writeHelmMetadata(context.Background(), cfg, "rhdh", "backstage-rhdh", KindDeployment, path)

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "Helm Chart: backstage-1.5.0") {
		t.Error("expected chart label")
	}
	if !strings.Contains(content, "App Instance: rhdh") {
		t.Error("expected instance label")
	}
}

func TestWriteHelmMetadata_StatefulSet(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "helm-metadata.txt")

	sts := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "backstage-sts",
			Namespace: "rhdh",
			Labels: map[string]string{
				"helm.sh/chart":                "backstage-1.5.0",
				"app.kubernetes.io/name":       "backstage",
				"app.kubernetes.io/managed-by": "Helm",
			},
		},
	}

	cfg := newTestConfig(t, dir, withTypedObjs(sts))
	h := &Helm{}
	h.writeHelmMetadata(context.Background(), cfg, "rhdh", "backstage-sts", KindStatefulSet, path)

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "Helm Chart: backstage-1.5.0") {
		t.Error("expected chart label")
	}
	if !strings.Contains(content, "App Version: N/A") {
		t.Error("expected N/A for missing label")
	}
}

func TestWriteHelmMetadata_NotFound(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "helm-metadata.txt")

	cfg := newTestConfig(t, dir)
	h := &Helm{}
	h.writeHelmMetadata(context.Background(), cfg, "rhdh", "nonexistent", KindDeployment, path)

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !strings.Contains(string(data), "Could not extract metadata") {
		t.Error("expected fallback message")
	}
}

func TestChartNameFromAccessor(t *testing.T) {
	rel := makeTestRelease("rhdh", "ns", 1, "backstage", "1.0.0", "", "")
	acc, err := release.NewAccessor(rel)
	if err != nil {
		t.Fatalf("NewAccessor: %v", err)
	}
	got := chartNameFromAccessor(acc)
	if got != "backstage" {
		t.Errorf("chartNameFromAccessor() = %q, want backstage", got)
	}
}

func rhdhDeployment(name, ns string) *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: ns,
			Labels: map[string]string{
				"app.kubernetes.io/managed-by": "Helm",
				"app.kubernetes.io/name":       "backstage",
				"app.kubernetes.io/instance":   "rhdh",
			},
		},
		Spec: appsv1.DeploymentSpec{
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"app": "rhdh"},
			},
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{Name: "backstage-backend", Image: "quay.io/rhdh/rhdh-hub-rhel9:latest"},
					},
				},
			},
		},
	}
}

func TestGatherStandaloneDeployments_RHDHDeployment(t *testing.T) {
	dir := t.TempDir()
	dep := rhdhDeployment("backstage-rhdh", "rhdh-ns")

	cfg := newTestConfig(t, dir, withTypedObjs(dep))
	h := &Helm{}
	helmDir := filepath.Join(dir, "helm")
	_ = os.MkdirAll(helmDir, 0o755)

	count := h.gatherStandaloneDeployments(context.Background(), cfg, helmDir, make(map[string]bool), make(map[string]bool))
	if count != 1 {
		t.Fatalf("count = %d, want 1", count)
	}

	noteFile := filepath.Join(helmDir, "standalone", "ns=rhdh-ns", "rhdh", "standalone-note.txt")
	if _, err := os.Stat(noteFile); err != nil {
		t.Error("expected standalone-note.txt to be created")
	}
}

func TestGatherStandaloneDeployments_UnrelatedDeployment(t *testing.T) {
	dir := t.TempDir()
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "nginx",
			Namespace: "default",
			Labels: map[string]string{
				"app.kubernetes.io/managed-by": "Helm",
				"app.kubernetes.io/name":       "nginx",
			},
		},
		Spec: appsv1.DeploymentSpec{
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"app": "nginx"},
			},
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{Name: "nginx", Image: "nginx:latest"},
					},
				},
			},
		},
	}

	cfg := newTestConfig(t, dir, withTypedObjs(dep))
	h := &Helm{}
	helmDir := filepath.Join(dir, "helm")
	_ = os.MkdirAll(helmDir, 0o755)

	count := h.gatherStandaloneDeployments(context.Background(), cfg, helmDir, make(map[string]bool), make(map[string]bool))
	if count != 0 {
		t.Errorf("count = %d, want 0 for unrelated deployment", count)
	}
}

func TestGatherStandaloneDeployments_AlreadyProcessed(t *testing.T) {
	dir := t.TempDir()
	dep := rhdhDeployment("backstage-rhdh", "rhdh-ns")

	cfg := newTestConfig(t, dir, withTypedObjs(dep))
	h := &Helm{}
	helmDir := filepath.Join(dir, "helm")
	_ = os.MkdirAll(helmDir, 0o755)

	processed := map[string]bool{
		workloadKey(KindDeployment, "rhdh-ns", "backstage-rhdh"): true,
	}
	count := h.gatherStandaloneDeployments(context.Background(), cfg, helmDir, make(map[string]bool), processed)
	if count != 0 {
		t.Errorf("count = %d, want 0 for already-processed workload", count)
	}
}

func TestGatherStandaloneDeployments_NoDeployments(t *testing.T) {
	dir := t.TempDir()
	cfg := newTestConfig(t, dir)
	h := &Helm{}
	helmDir := filepath.Join(dir, "helm")
	_ = os.MkdirAll(helmDir, 0o755)

	count := h.gatherStandaloneDeployments(context.Background(), cfg, helmDir, make(map[string]bool), make(map[string]bool))
	if count != 0 {
		t.Errorf("count = %d, want 0", count)
	}
}

func TestGatherStandaloneDeployments_TargetNamespaces(t *testing.T) {
	dir := t.TempDir()
	dep := rhdhDeployment("backstage-rhdh", "other-ns")

	cfg := newTestConfig(t, dir, withTypedObjs(dep))
	cfg.TargetNamespaces = []string{"rhdh-ns"}
	h := &Helm{}
	helmDir := filepath.Join(dir, "helm")
	_ = os.MkdirAll(helmDir, 0o755)

	count := h.gatherStandaloneDeployments(context.Background(), cfg, helmDir, make(map[string]bool), make(map[string]bool))
	if count != 0 {
		t.Errorf("count = %d, want 0 for filtered namespace", count)
	}
}

func TestGatherStandaloneDeployments_StatefulSet(t *testing.T) {
	dir := t.TempDir()
	sts := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "backstage-sts",
			Namespace: "rhdh-ns",
			Labels: map[string]string{
				"app.kubernetes.io/managed-by": "Helm",
				"app.kubernetes.io/name":       "backstage",
				"app.kubernetes.io/instance":   "rhdh",
			},
		},
		Spec: appsv1.StatefulSetSpec{
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"app": "rhdh"},
			},
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{Name: "backstage-backend", Image: "quay.io/rhdh/rhdh-hub-rhel9:latest"},
					},
				},
			},
		},
	}

	cfg := newTestConfig(t, dir, withTypedObjs(sts))
	h := &Helm{}
	helmDir := filepath.Join(dir, "helm")
	_ = os.MkdirAll(helmDir, 0o755)

	count := h.gatherStandaloneDeployments(context.Background(), cfg, helmDir, make(map[string]bool), make(map[string]bool))
	if count != 1 {
		t.Fatalf("count = %d, want 1 for StatefulSet", count)
	}

	stsDir := filepath.Join(helmDir, "standalone", "ns=rhdh-ns", "rhdh", "statefulset")
	if _, err := os.Stat(stsDir); err != nil {
		t.Error("expected statefulset directory to be created")
	}
}

func TestGatherStandaloneDeployments_MustGatherExcluded(t *testing.T) {
	dir := t.TempDir()
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "rhdh-must-gather",
			Namespace: "rhdh-ns",
			Labels: map[string]string{
				"app.kubernetes.io/managed-by": "Helm",
				"app.kubernetes.io/name":       "rhdh-must-gather",
			},
		},
		Spec: appsv1.DeploymentSpec{
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"app": "rhdh"},
			},
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{Name: "backstage-backend", Image: "quay.io/rhdh/rhdh-hub-rhel9:latest"},
					},
				},
			},
		},
	}

	cfg := newTestConfig(t, dir, withTypedObjs(dep))
	h := &Helm{}
	helmDir := filepath.Join(dir, "helm")
	_ = os.MkdirAll(helmDir, 0o755)

	count := h.gatherStandaloneDeployments(context.Background(), cfg, helmDir, make(map[string]bool), make(map[string]bool))
	if count != 0 {
		t.Errorf("count = %d, want 0 for must-gather deployment", count)
	}
}

func TestCollectDependentServices_WithDependentDeployment(t *testing.T) {
	dir := t.TempDir()

	mainDep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "backstage-rhdh",
			Namespace: "rhdh-ns",
			Labels: map[string]string{
				"app.kubernetes.io/managed-by": "Helm",
				"app.kubernetes.io/instance":   "rhdh",
			},
		},
		Spec: appsv1.DeploymentSpec{
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "rhdh"}},
		},
	}

	depDep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "rhdh-postgresql",
			Namespace: "rhdh-ns",
			Labels: map[string]string{
				"app.kubernetes.io/managed-by": "Helm",
				"app.kubernetes.io/instance":   "rhdh",
			},
		},
		Spec: appsv1.DeploymentSpec{
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "postgresql"}},
		},
	}

	cfg := newTestConfig(t, dir, withTypedObjs(mainDep, depDep))
	h := &Helm{}

	wlDir := filepath.Join(dir, "workload")
	_ = os.MkdirAll(wlDir, 0o755)

	processedWorkloads := make(map[string]bool)
	h.collectDependentServices(context.Background(), cfg, "rhdh-ns", "backstage-rhdh", KindDeployment, wlDir, processedWorkloads)

	depYAML := filepath.Join(wlDir, "dependencies", "rhdh-postgresql", "deployment.yaml")
	if _, err := os.Stat(depYAML); err != nil {
		t.Error("expected dependent deployment YAML to be created")
	}

	if !processedWorkloads[workloadKey(KindDeployment, "rhdh-ns", "rhdh-postgresql")] {
		t.Error("expected dependent to be marked as processed")
	}
}

func TestCollectDependentServices_OKPDeployment(t *testing.T) {
	dir := t.TempDir()

	mainDep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "backstage-rhdh",
			Namespace: "rhdh-ns",
			Labels: map[string]string{
				"app.kubernetes.io/managed-by": "Helm",
				"app.kubernetes.io/instance":   "rhdh",
			},
		},
		Spec: appsv1.DeploymentSpec{
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "rhdh"}},
		},
	}

	okpDep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "rhdh-okp",
			Namespace: "rhdh-ns",
			Labels: map[string]string{
				"app.kubernetes.io/managed-by": "Helm",
				"app.kubernetes.io/instance":   "rhdh",
			},
		},
		Spec: appsv1.DeploymentSpec{
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "okp"}},
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{Name: "okp"}},
				},
			},
		},
	}

	cfg := newTestConfig(t, dir, withTypedObjs(mainDep, okpDep))
	h := &Helm{}

	wlDir := filepath.Join(dir, "workload")
	_ = os.MkdirAll(wlDir, 0o755)

	processedWorkloads := make(map[string]bool)
	h.collectDependentServices(context.Background(), cfg, "rhdh-ns", "backstage-rhdh", KindDeployment, wlDir, processedWorkloads)

	okpDir := filepath.Join(wlDir, "okp-deployment")
	if _, err := os.Stat(okpDir); err != nil {
		t.Error("expected okp-deployment directory to be created")
	}

	if !processedWorkloads[workloadKey(KindDeployment, "rhdh-ns", "rhdh-okp")] {
		t.Error("expected OKP deployment to be marked as processed")
	}
}

func TestCollectDependentServices_NoInstanceLabel(t *testing.T) {
	dir := t.TempDir()

	mainDep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "backstage-rhdh",
			Namespace: "rhdh-ns",
			Labels:    map[string]string{},
		},
		Spec: appsv1.DeploymentSpec{
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "rhdh"}},
		},
	}

	cfg := newTestConfig(t, dir, withTypedObjs(mainDep))
	h := &Helm{}

	wlDir := filepath.Join(dir, "workload")
	_ = os.MkdirAll(wlDir, 0o755)

	processedWorkloads := make(map[string]bool)
	h.collectDependentServices(context.Background(), cfg, "rhdh-ns", "backstage-rhdh", KindDeployment, wlDir, processedWorkloads)

	depsDir := filepath.Join(wlDir, "dependencies")
	if _, err := os.Stat(depsDir); !os.IsNotExist(err) {
		t.Error("expected no dependencies dir when no instance label")
	}
}

func TestCollectDependentServices_AlreadyProcessed(t *testing.T) {
	dir := t.TempDir()

	mainDep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "backstage-rhdh",
			Namespace: "rhdh-ns",
			Labels: map[string]string{
				"app.kubernetes.io/managed-by": "Helm",
				"app.kubernetes.io/instance":   "rhdh",
			},
		},
		Spec: appsv1.DeploymentSpec{
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "rhdh"}},
		},
	}

	depDep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "rhdh-postgresql",
			Namespace: "rhdh-ns",
			Labels: map[string]string{
				"app.kubernetes.io/managed-by": "Helm",
				"app.kubernetes.io/instance":   "rhdh",
			},
		},
		Spec: appsv1.DeploymentSpec{
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "postgresql"}},
		},
	}

	cfg := newTestConfig(t, dir, withTypedObjs(mainDep, depDep))
	h := &Helm{}

	wlDir := filepath.Join(dir, "workload")
	_ = os.MkdirAll(wlDir, 0o755)

	processedWorkloads := map[string]bool{
		workloadKey(KindDeployment, "rhdh-ns", "rhdh-postgresql"): true,
	}
	h.collectDependentServices(context.Background(), cfg, "rhdh-ns", "backstage-rhdh", KindDeployment, wlDir, processedWorkloads)

	depYAML := filepath.Join(wlDir, "dependencies", "rhdh-postgresql", "deployment.yaml")
	if _, err := os.Stat(depYAML); !os.IsNotExist(err) {
		t.Error("expected no deployment YAML for already-processed dependent")
	}
}

func TestCollectDependentServices_StatefulSet(t *testing.T) {
	dir := t.TempDir()

	mainSTS := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "backstage-rhdh",
			Namespace: "rhdh-ns",
			Labels: map[string]string{
				"app.kubernetes.io/managed-by": "Helm",
				"app.kubernetes.io/instance":   "rhdh",
			},
		},
		Spec: appsv1.StatefulSetSpec{
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "rhdh"}},
		},
	}

	depSTS := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "rhdh-postgresql",
			Namespace: "rhdh-ns",
			Labels: map[string]string{
				"app.kubernetes.io/managed-by": "Helm",
				"app.kubernetes.io/instance":   "rhdh",
			},
		},
		Spec: appsv1.StatefulSetSpec{
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "postgresql"}},
		},
	}

	cfg := newTestConfig(t, dir, withTypedObjs(mainSTS, depSTS))
	h := &Helm{}

	wlDir := filepath.Join(dir, "workload")
	_ = os.MkdirAll(wlDir, 0o755)

	processedWorkloads := make(map[string]bool)
	h.collectDependentServices(context.Background(), cfg, "rhdh-ns", "backstage-rhdh", KindStatefulSet, wlDir, processedWorkloads)

	depYAML := filepath.Join(wlDir, "dependencies", "rhdh-postgresql", "statefulset.yaml")
	if _, err := os.Stat(depYAML); err != nil {
		t.Error("expected dependent statefulset YAML to be created")
	}

	if !processedWorkloads[workloadKey(KindStatefulSet, "rhdh-ns", "rhdh-postgresql")] {
		t.Error("expected dependent STS to be marked as processed")
	}
}

func TestCollectDependentLogs_NoPods(t *testing.T) {
	dir := t.TempDir()
	cfg := newTestConfig(t, dir)
	h := &Helm{}

	depDir := filepath.Join(dir, "dep")
	_ = os.MkdirAll(depDir, 0o755)

	matchLabels := map[string]string{"app": "postgresql"}
	h.collectDependentLogs(context.Background(), cfg, "rhdh-ns", "postgresql", "rhdh", &matchLabels, depDir)

	entries, err := os.ReadDir(depDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "logs-") {
			t.Error("expected no log files when no pods exist")
		}
	}
}

func TestCollectDependentLogs_WithPods(t *testing.T) {
	dir := t.TempDir()

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "postgresql-0",
			Namespace: "rhdh-ns",
			Labels: map[string]string{
				"app.kubernetes.io/instance": "rhdh",
				"app.kubernetes.io/name":     "postgresql",
			},
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{Name: "postgresql"}},
		},
		Status: corev1.PodStatus{Phase: corev1.PodRunning},
	}

	cfg := newTestConfig(t, dir, withTypedObjs(pod))
	h := &Helm{}

	depDir := filepath.Join(dir, "dep")
	_ = os.MkdirAll(depDir, 0o755)

	matchLabels := map[string]string{"app.kubernetes.io/instance": "rhdh", "app.kubernetes.io/name": "postgresql"}
	h.collectDependentLogs(context.Background(), cfg, "rhdh-ns", "postgresql", "rhdh", &matchLabels, depDir)

	logFile := filepath.Join(depDir, "logs-postgresql-0.txt")
	if _, err := os.Stat(logFile); err != nil {
		t.Error("expected log file to be created for pod")
	}
}

func TestExtractWorkloadNames_OnlyFirstSTS(t *testing.T) {
	manifest := `apiVersion: apps/v1
kind: StatefulSet
metadata:
  name: first-sts
---
apiVersion: apps/v1
kind: StatefulSet
metadata:
  name: second-sts
`
	_, stsName := extractWorkloadNames(manifest)
	if stsName != "first-sts" {
		t.Errorf("stsName = %q, want first-sts (should only keep first)", stsName)
	}
}

func fakeHelmConfig(releases ...*releasev1.Release) func(string) (*action.Configuration, error) {
	store := storage.Init(driver.NewMemory())
	for _, rel := range releases {
		_ = store.Create(rel)
	}
	return func(_ string) (*action.Configuration, error) {
		return &action.Configuration{
			Releases:   store,
			KubeClient: &kubefake.PrintingKubeClient{Out: io.Discard},
		}, nil
	}
}

func TestGatherNativeReleases_ConfigError(t *testing.T) {
	dir := t.TempDir()
	cfg := newTestConfig(t, dir)
	cfg.HelmConfigFactory = func(_ string) (*action.Configuration, error) {
		return nil, fmt.Errorf("helm init failed")
	}

	h := &Helm{}
	helmDir := filepath.Join(dir, "helm")
	_ = os.MkdirAll(helmDir, 0o755)

	count := h.gatherNativeReleases(context.Background(), cfg, helmDir, make(map[string]bool), make(map[string]bool))
	if count != 0 {
		t.Errorf("count = %d, want 0", count)
	}

	data, err := os.ReadFile(filepath.Join(helmDir, "all-rhdh-releases.txt"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !strings.Contains(string(data), "helm init failed") {
		t.Error("expected error message in output file")
	}
}

func TestGatherNativeReleases_NoRHDHReleases(t *testing.T) {
	dir := t.TempDir()
	rel := makeTestRelease("nginx-release", "default", 1, "nginx", "1.0.0", "1.0.0", "")
	cfg := newTestConfig(t, dir)
	cfg.HelmConfigFactory = fakeHelmConfig(rel)

	h := &Helm{}
	helmDir := filepath.Join(dir, "helm")
	_ = os.MkdirAll(helmDir, 0o755)

	count := h.gatherNativeReleases(context.Background(), cfg, helmDir, make(map[string]bool), make(map[string]bool))
	if count != 0 {
		t.Errorf("count = %d, want 0 for non-RHDH release", count)
	}
}

func TestGatherNativeReleases_MustGatherExcluded(t *testing.T) {
	dir := t.TempDir()
	rel := makeTestRelease("rhdh-must-gather", "rhdh-ns", 1, "rhdh-must-gather", "1.0.0", "1.0.0", "")
	cfg := newTestConfig(t, dir)
	cfg.HelmConfigFactory = fakeHelmConfig(rel)

	h := &Helm{}
	helmDir := filepath.Join(dir, "helm")
	_ = os.MkdirAll(helmDir, 0o755)

	count := h.gatherNativeReleases(context.Background(), cfg, helmDir, make(map[string]bool), make(map[string]bool))
	if count != 0 {
		t.Errorf("count = %d, want 0 for must-gather release", count)
	}
}

func TestGatherNativeReleases_WithRHDHRelease(t *testing.T) {
	dir := t.TempDir()
	manifest := "apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: backstage-rhdh\n"
	rel := makeTestRelease("rhdh", "rhdh-ns", 1, "backstage", "1.5.0", "1.4.0", "Install complete")
	rel.Manifest = manifest
	rel.Config = map[string]any{"global": map[string]any{"host": "rhdh.example.com"}}

	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "backstage-rhdh",
			Namespace: "rhdh-ns",
		},
		Spec: appsv1.DeploymentSpec{
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"app": "rhdh"},
			},
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{Name: "backstage-backend", Image: "quay.io/rhdh/rhdh-hub-rhel9:latest"},
					},
				},
			},
		},
	}

	cfg := newTestConfig(t, dir, withTypedObjs(dep))
	cfg.HelmConfigFactory = fakeHelmConfig(rel)

	h := &Helm{}
	helmDir := filepath.Join(dir, "helm")
	_ = os.MkdirAll(helmDir, 0o755)

	count := h.gatherNativeReleases(context.Background(), cfg, helmDir, make(map[string]bool), make(map[string]bool))
	if count != 1 {
		t.Fatalf("count = %d, want 1", count)
	}

	data, err := os.ReadFile(filepath.Join(helmDir, "all-rhdh-releases.txt"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !strings.Contains(string(data), "rhdh") {
		t.Error("expected release name in table")
	}

	releaseDir := filepath.Join(helmDir, "releases", "ns=rhdh-ns", "rhdh")
	if _, err := os.Stat(releaseDir); err != nil {
		t.Error("expected release directory to be created")
	}
}

func TestGatherNativeReleases_TargetNamespaces(t *testing.T) {
	dir := t.TempDir()
	rel := makeTestRelease("rhdh", "other-ns", 1, "backstage", "1.5.0", "1.4.0", "")

	cfg := newTestConfig(t, dir)
	cfg.HelmConfigFactory = fakeHelmConfig(rel)
	cfg.TargetNamespaces = []string{"rhdh-ns"}

	h := &Helm{}
	helmDir := filepath.Join(dir, "helm")
	_ = os.MkdirAll(helmDir, 0o755)

	count := h.gatherNativeReleases(context.Background(), cfg, helmDir, make(map[string]bool), make(map[string]bool))
	if count != 0 {
		t.Errorf("count = %d, want 0 for filtered namespace", count)
	}
}

func TestCollectReleaseData_WritesValuesAndManifest(t *testing.T) {
	dir := t.TempDir()

	manifest := "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: rhdh-config\n"
	rel := makeTestRelease("rhdh", "rhdh-ns", 1, "backstage", "1.5.0", "1.4.0", "Install complete")
	rel.Manifest = manifest
	rel.Config = map[string]any{"upstream": map[string]any{"backstage": map[string]any{"title": "My RHDH"}}}

	cfg := newTestConfig(t, dir)
	cfg.HelmConfigFactory = fakeHelmConfig(rel)

	h := &Helm{}
	releaseDir := filepath.Join(dir, "release-data")
	_ = os.MkdirAll(releaseDir, 0o755)

	h.collectReleaseData(context.Background(), cfg, "rhdh-ns", "rhdh", releaseDir, make(map[string]bool))

	valuesData, err := os.ReadFile(filepath.Join(releaseDir, "values.yaml"))
	if err != nil {
		t.Fatalf("ReadFile values.yaml: %v", err)
	}
	if !strings.Contains(string(valuesData), "My RHDH") {
		t.Error("expected user values in values.yaml")
	}

	manifestData, err := os.ReadFile(filepath.Join(releaseDir, "manifest.yaml"))
	if err != nil {
		t.Fatalf("ReadFile manifest.yaml: %v", err)
	}
	if !strings.Contains(string(manifestData), "rhdh-config") {
		t.Error("expected manifest content")
	}

	notesData, err := os.ReadFile(filepath.Join(releaseDir, "notes.txt"))
	if err != nil {
		t.Fatalf("ReadFile notes.txt: %v", err)
	}
	_ = notesData

	if _, err := os.Stat(filepath.Join(releaseDir, "history.txt")); err != nil {
		t.Error("expected history.txt to be created")
	}
	if _, err := os.Stat(filepath.Join(releaseDir, "status.txt")); err != nil {
		t.Error("expected status.txt to be created")
	}
}

func TestCollectReleaseData_FilterSecrets(t *testing.T) {
	dir := t.TempDir()

	manifest := "apiVersion: v1\nkind: Secret\nmetadata:\n  name: db-creds\ndata:\n  password: cGFzcw==\n---\napiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: rhdh-config\n"
	rel := makeTestRelease("rhdh", "rhdh-ns", 1, "backstage", "1.5.0", "1.4.0", "")
	rel.Manifest = manifest

	cfg := newTestConfig(t, dir)
	cfg.HelmConfigFactory = fakeHelmConfig(rel)
	cfg.WithSecrets = false

	h := &Helm{}
	releaseDir := filepath.Join(dir, "release-data")
	_ = os.MkdirAll(releaseDir, 0o755)

	h.collectReleaseData(context.Background(), cfg, "rhdh-ns", "rhdh", releaseDir, make(map[string]bool))

	data, err := os.ReadFile(filepath.Join(releaseDir, "manifest.yaml"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	content := string(data)
	if strings.Contains(content, "Secret") {
		t.Error("expected secrets to be filtered from manifest")
	}
	if !strings.Contains(content, "ConfigMap") {
		t.Error("expected non-secret content to remain in manifest")
	}
}

func TestCollectReleaseData_WithSecrets(t *testing.T) {
	dir := t.TempDir()

	manifest := "apiVersion: v1\nkind: Secret\nmetadata:\n  name: db-creds\ndata:\n  password: cGFzcw==\n"
	rel := makeTestRelease("rhdh", "rhdh-ns", 1, "backstage", "1.5.0", "1.4.0", "")
	rel.Manifest = manifest

	cfg := newTestConfig(t, dir)
	cfg.HelmConfigFactory = fakeHelmConfig(rel)
	cfg.WithSecrets = true

	h := &Helm{}
	releaseDir := filepath.Join(dir, "release-data")
	_ = os.MkdirAll(releaseDir, 0o755)

	h.collectReleaseData(context.Background(), cfg, "rhdh-ns", "rhdh", releaseDir, make(map[string]bool))

	data, err := os.ReadFile(filepath.Join(releaseDir, "manifest.yaml"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !strings.Contains(string(data), "Secret") {
		t.Error("expected secrets to remain when WithSecrets=true")
	}
}

func TestCollectReleaseData_ConfigError(t *testing.T) {
	dir := t.TempDir()
	cfg := newTestConfig(t, dir)
	cfg.HelmConfigFactory = func(_ string) (*action.Configuration, error) {
		return nil, fmt.Errorf("config init failed")
	}

	h := &Helm{}
	releaseDir := filepath.Join(dir, "release-data")
	_ = os.MkdirAll(releaseDir, 0o755)

	h.collectReleaseData(context.Background(), cfg, "rhdh-ns", "rhdh", releaseDir, make(map[string]bool))

	data, err := os.ReadFile(filepath.Join(releaseDir, "error.txt"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !strings.Contains(string(data), "config init failed") {
		t.Error("expected error message in error.txt")
	}
}

func TestCollectReleaseData_ExtractsWorkloads(t *testing.T) {
	dir := t.TempDir()

	manifest := "apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: backstage-rhdh\n---\napiVersion: apps/v1\nkind: StatefulSet\nmetadata:\n  name: backstage-psql\n"
	rel := makeTestRelease("rhdh", "rhdh-ns", 1, "backstage", "1.5.0", "1.4.0", "")
	rel.Manifest = manifest

	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "backstage-rhdh",
			Namespace: "rhdh-ns",
		},
		Spec: appsv1.DeploymentSpec{
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"app": "rhdh"},
			},
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{Name: "backstage-backend"},
					},
				},
			},
		},
	}

	sts := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "backstage-psql",
			Namespace: "rhdh-ns",
		},
		Spec: appsv1.StatefulSetSpec{
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"app": "psql"},
			},
		},
	}

	cfg := newTestConfig(t, dir, withTypedObjs(dep, sts))
	cfg.HelmConfigFactory = fakeHelmConfig(rel)

	h := &Helm{}
	releaseDir := filepath.Join(dir, "release-data")
	_ = os.MkdirAll(releaseDir, 0o755)

	processedWorkloads := make(map[string]bool)
	h.collectReleaseData(context.Background(), cfg, "rhdh-ns", "rhdh", releaseDir, processedWorkloads)

	if _, err := os.Stat(filepath.Join(releaseDir, "deployment")); err != nil {
		t.Error("expected deployment directory for primary workload")
	}

	if !processedWorkloads[workloadKey(KindDeployment, "rhdh-ns", "backstage-rhdh")] {
		t.Error("expected deployment to be marked as processed")
	}
	if !processedWorkloads[workloadKey(KindStatefulSet, "rhdh-ns", "backstage-psql")] {
		t.Error("expected statefulset to be marked as processed")
	}
}

func TestHelmRun_NoReleases(t *testing.T) {
	dir := t.TempDir()
	cfg := newTestConfig(t, dir)
	cfg.HelmConfigFactory = fakeHelmConfig()

	h := &Helm{}
	err := h.Run(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, "helm", "no-releases.txt"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !strings.Contains(string(data), "No RHDH-related Helm releases") {
		t.Error("expected no-releases message")
	}
}

func TestHelmRun_WithRelease(t *testing.T) {
	dir := t.TempDir()
	manifest := "apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: backstage-rhdh\n"
	rel := makeTestRelease("rhdh", "rhdh-ns", 1, "backstage", "1.5.0", "1.4.0", "Install complete")
	rel.Manifest = manifest
	rel.Config = map[string]any{"key": "value"}

	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "backstage-rhdh",
			Namespace: "rhdh-ns",
		},
		Spec: appsv1.DeploymentSpec{
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"app": "rhdh"},
			},
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{Name: "backstage-backend", Image: "quay.io/rhdh/rhdh-hub-rhel9:latest"},
					},
				},
			},
		},
	}

	cfg := newTestConfig(t, dir, withTypedObjs(dep))
	cfg.HelmConfigFactory = fakeHelmConfig(rel)

	h := &Helm{}
	err := h.Run(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	helmDir := filepath.Join(dir, "helm")
	if _, err := os.Stat(filepath.Join(helmDir, "all-rhdh-releases.txt")); err != nil {
		t.Error("expected releases table file")
	}
	if _, err := os.Stat(filepath.Join(helmDir, "all-rhdh-releases.json")); err != nil {
		t.Error("expected releases JSON file")
	}

	releaseDir := filepath.Join(helmDir, "releases", "ns=rhdh-ns", "rhdh")
	if _, err := os.Stat(filepath.Join(releaseDir, "values.yaml")); err != nil {
		t.Error("expected values.yaml in release dir")
	}
	if _, err := os.Stat(filepath.Join(releaseDir, "manifest.yaml")); err != nil {
		t.Error("expected manifest.yaml in release dir")
	}
}
