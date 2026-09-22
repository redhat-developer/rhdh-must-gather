package collector

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
)

func TestOwnedOKPDeployments(t *testing.T) {
	controller := true
	notController := false
	crUID := types.UID("developer-hub-uid")

	deployment := func(name, container string, owner metav1.OwnerReference) appsv1.Deployment {
		return appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{Name: name, OwnerReferences: []metav1.OwnerReference{owner}},
			Spec: appsv1.DeploymentSpec{Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{
				Containers: []corev1.Container{{Name: container}},
			}}},
		}
	}
	owner := func(name string, uid types.UID, controller *bool) metav1.OwnerReference {
		return metav1.OwnerReference{
			APIVersion: "rhdh.redhat.com/v1alpha5",
			Kind:       "Backstage",
			Name:       name,
			UID:        uid,
			Controller: controller,
		}
	}

	deployments := []appsv1.Deployment{
		deployment("z-okp", "okp", owner("developer-hub", crUID, &controller)),
		deployment("a-okp", "okp", owner("developer-hub", crUID, &controller)),
		deployment("backstage-developer-hub", "okp", owner("developer-hub", crUID, &controller)),
		deployment("wrong-container", "worker", owner("developer-hub", crUID, &controller)),
		deployment("wrong-cr", "okp", owner("another-hub", crUID, &controller)),
		deployment("wrong-uid", "okp", owner("developer-hub", "old-uid", &controller)),
		deployment("not-controller", "okp", owner("developer-hub", crUID, &notController)),
	}

	got := ownedOKPDeployments(deployments, "developer-hub", crUID, "backstage-developer-hub")
	var gotNames []string
	for _, item := range got {
		gotNames = append(gotNames, item.Name)
	}
	want := []string{"a-okp", "z-okp"}
	if !reflect.DeepEqual(gotNames, want) {
		t.Errorf("ownedOKPDeployments() = %q, want %q", gotNames, want)
	}
}

func TestOwnedOKPDeployments_IAOnly(t *testing.T) {
	controller := true
	deployments := []appsv1.Deployment{{
		ObjectMeta: metav1.ObjectMeta{
			Name: "backstage-developer-hub",
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: "rhdh.redhat.com/v1alpha5",
				Kind:       "Backstage",
				Name:       "developer-hub",
				UID:        "developer-hub-uid",
				Controller: &controller,
			}},
		},
		Spec: appsv1.DeploymentSpec{Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{
			Containers: []corev1.Container{{Name: "backstage-backend"}, {Name: "lightspeed-core"}},
		}}},
	}}

	got := ownedOKPDeployments(deployments, "developer-hub", "developer-hub-uid", "backstage-developer-hub")
	if len(got) != 0 {
		t.Errorf("ownedOKPDeployments() returned %d workloads for an IA-only deployment", len(got))
	}
}

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
			"approved": true,
			"replicas": float64(3),
			"approval": "Automatic",
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
