package collector

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func TestIsRHDHRelated(t *testing.T) {
	tests := []struct {
		name string
		want bool
	}{
		{"rhdh-operator.v1.5.0", true},
		{"backstage-operator.v1.0.0", true},
		{"developer-hub-operator.v1.0.0", true},
		{"RHDH-Operator", true},
		{"my-Backstage-app", true},
		{"some-other-operator", false},
		{"cert-manager.v1.0.0", false},
		{"", false},
	}

	for _, tt := range tests {
		got := isRHDHRelated(tt.name)
		if got != tt.want {
			t.Errorf("isRHDHRelated(%q) = %v, want %v", tt.name, got, tt.want)
		}
	}
}

func TestFilterRHDHResources(t *testing.T) {
	items := []unstructured.Unstructured{
		{Object: map[string]any{"metadata": map[string]any{"name": "rhdh-operator.v1.5.0"}}},
		{Object: map[string]any{"metadata": map[string]any{"name": "cert-manager.v1.0.0"}}},
		{Object: map[string]any{"metadata": map[string]any{"name": "backstage-operator.v1.0.0"}}},
	}

	filtered := filterRHDHResources(items)
	if len(filtered) != 2 {
		t.Fatalf("got %d items, want 2", len(filtered))
	}
	if filtered[0].GetName() != "rhdh-operator.v1.5.0" {
		t.Errorf("first item = %q, want rhdh-operator.v1.5.0", filtered[0].GetName())
	}
	if filtered[1].GetName() != "backstage-operator.v1.0.0" {
		t.Errorf("second item = %q, want backstage-operator.v1.0.0", filtered[1].GetName())
	}
}

func TestFilterRHDHResources_Empty(t *testing.T) {
	items := []unstructured.Unstructured{
		{Object: map[string]any{"metadata": map[string]any{"name": "cert-manager"}}},
	}
	filtered := filterRHDHResources(items)
	if len(filtered) != 0 {
		t.Errorf("got %d items, want 0", len(filtered))
	}
}

func TestWriteDynamicTable(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test-table.txt")

	items := []unstructured.Unstructured{
		{Object: map[string]any{
			"metadata": map[string]any{
				"namespace": "ns1",
				"name":      "rhdh-operator.v1.5.0",
			},
			"spec": map[string]any{
				"displayName": "RHDH Operator",
				"version":     "1.5.0",
			},
			"status": map[string]any{
				"phase": "Succeeded",
			},
		}},
	}

	writeDynamicTable(path, items, csvColumns)

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	if !strings.Contains(content, "NAMESPACE") {
		t.Error("missing NAMESPACE header")
	}
	if !strings.Contains(content, "rhdh-operator.v1.5.0") {
		t.Error("missing operator name")
	}
	if !strings.Contains(content, "Succeeded") {
		t.Error("missing phase")
	}
}

func TestWriteDynamicTable_Empty(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "empty.txt")

	writeDynamicTable(path, nil, operatorGroupColumns)

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "No resources found") {
		t.Error("expected 'No resources found' message")
	}
}

func TestGetFieldAsString(t *testing.T) {
	obj := map[string]any{
		"metadata": map[string]any{
			"name": "test",
		},
		"spec": map[string]any{
			"approved":  true,
			"replicas":  float64(3),
			"approval":  "Automatic",
		},
	}

	tests := []struct {
		fields []string
		want   string
	}{
		{[]string{"metadata", "name"}, "test"},
		{[]string{"spec", "approved"}, "true"},
		{[]string{"spec", "replicas"}, "3"},
		{[]string{"spec", "approval"}, "Automatic"},
		{[]string{"spec", "missing"}, ""},
		{[]string{"nonexistent"}, ""},
	}

	for _, tt := range tests {
		got := getFieldAsString(obj, tt.fields...)
		if got != tt.want {
			t.Errorf("getFieldAsString(%v) = %q, want %q", tt.fields, got, tt.want)
		}
	}
}

func TestWriteDualWorkloadWarning(t *testing.T) {
	warning := writeDualWorkloadWarning("rhdh-ns", "backstage-my-rhdh", "StatefulSet", "Deployment")

	if !strings.Contains(warning, "WARNING: Duplicate RHDH workloads detected") {
		t.Error("missing warning header")
	}
	if !strings.Contains(warning, "backstage-my-rhdh") {
		t.Error("missing deploy name")
	}
	if !strings.Contains(warning, "rhdh-ns") {
		t.Error("missing namespace")
	}
	if !strings.Contains(warning, "StatefulSet") {
		t.Error("missing intended kind")
	}
	if !strings.Contains(warning, "delete deployment backstage-my-rhdh") {
		t.Error("missing recommended action")
	}
}

func TestWriteDualWorkloadWarning_NoIntendedKind(t *testing.T) {
	warning := writeDualWorkloadWarning("ns", "backstage-app", "", "StatefulSet")

	if !strings.Contains(warning, "defaults to Deployment") {
		t.Error("expected defaults-to-Deployment message")
	}
}

func TestPodReadyContainers(t *testing.T) {
	pod := &corev1.Pod{
		Status: corev1.PodStatus{
			ContainerStatuses: []corev1.ContainerStatus{
				{Name: "main", Ready: true},
				{Name: "sidecar", Ready: false},
				{Name: "init", Ready: true},
			},
		},
	}

	if got := podReadyContainers(pod); got != 2 {
		t.Errorf("podReadyContainers = %d, want 2", got)
	}
}

func TestPodReadyContainers_Empty(t *testing.T) {
	pod := &corev1.Pod{}
	if got := podReadyContainers(pod); got != 0 {
		t.Errorf("podReadyContainers = %d, want 0", got)
	}
}

func TestPtrVal(t *testing.T) {
	v := int32(3)
	if got := ptrVal(&v); got != 3 {
		t.Errorf("ptrVal(&3) = %d, want 3", got)
	}
	if got := ptrVal(nil); got != 0 {
		t.Errorf("ptrVal(nil) = %d, want 0", got)
	}
}

func TestWriteDeploymentSummaryTable(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "deps.txt")

	deps := []deploymentSummary{
		{Namespace: "ns1", Name: "rhdh-operator", Ready: 1, Desired: 1},
		{Namespace: "ns2", Name: "rhdh-operator", Ready: 0, Desired: 1},
	}

	writeDeploymentSummaryTable(path, deps)

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	if !strings.Contains(content, "NAMESPACE") {
		t.Error("expected NAMESPACE header when namespaces present")
	}
	if !strings.Contains(content, "1/1") {
		t.Error("missing ready count")
	}
	if !strings.Contains(content, "0/1") {
		t.Error("missing not-ready count")
	}
}

func TestWriteDeploymentSummaryTable_NoNamespace(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "deps.txt")

	deps := []deploymentSummary{
		{Name: "rhdh-operator", Ready: 1, Desired: 1},
	}

	writeDeploymentSummaryTable(path, deps)

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	if strings.Contains(content, "NAMESPACE") {
		t.Error("unexpected NAMESPACE header when no namespaces")
	}
}

func TestWriteDeploymentSummaryTable_Empty(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "deps.txt")

	writeDeploymentSummaryTable(path, nil)

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "No resources found") {
		t.Error("expected 'No resources found'")
	}
}
