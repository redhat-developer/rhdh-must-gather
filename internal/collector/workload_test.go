package collector

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestFilterPodsByOwner_Deployment(t *testing.T) {
	pods := []corev1.Pod{
		{
			ObjectMeta: metav1.ObjectMeta{
				Name: "pod-from-rs",
				OwnerReferences: []metav1.OwnerReference{
					{Kind: "ReplicaSet", Name: "my-dep-abc123"},
				},
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{
				Name: "pod-from-sts",
				OwnerReferences: []metav1.OwnerReference{
					{Kind: "StatefulSet", Name: "my-sts"},
				},
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{
				Name: "orphan-pod",
			},
		},
	}

	filtered := filterPodsByOwner(pods, KindDeployment)
	if len(filtered) != 1 {
		t.Fatalf("got %d pods, want 1", len(filtered))
	}
	if filtered[0].Name != "pod-from-rs" {
		t.Errorf("got %q, want pod-from-rs", filtered[0].Name)
	}
}

func TestFilterPodsByOwner_StatefulSet(t *testing.T) {
	pods := []corev1.Pod{
		{
			ObjectMeta: metav1.ObjectMeta{
				Name: "pod-from-rs",
				OwnerReferences: []metav1.OwnerReference{
					{Kind: "ReplicaSet", Name: "my-dep-abc123"},
				},
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{
				Name: "pod-from-sts",
				OwnerReferences: []metav1.OwnerReference{
					{Kind: "StatefulSet", Name: "my-sts"},
				},
			},
		},
	}

	filtered := filterPodsByOwner(pods, KindStatefulSet)
	if len(filtered) != 1 {
		t.Fatalf("got %d pods, want 1", len(filtered))
	}
	if filtered[0].Name != "pod-from-sts" {
		t.Errorf("got %q, want pod-from-sts", filtered[0].Name)
	}
}

func TestFilterPodsByOwner_Empty(t *testing.T) {
	filtered := filterPodsByOwner(nil, KindDeployment)
	if len(filtered) != 0 {
		t.Errorf("got %d pods, want 0", len(filtered))
	}
}

func TestWritePodTable(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pods.txt")

	pods := []corev1.Pod{
		{
			ObjectMeta: metav1.ObjectMeta{Name: "pod-a"},
			Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "app"}, {Name: "sidecar"}}},
			Status: corev1.PodStatus{
				Phase: corev1.PodRunning,
				ContainerStatuses: []corev1.ContainerStatus{
					{Ready: true},
					{Ready: false},
				},
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{Name: "pod-b"},
			Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "app"}}},
			Status:     corev1.PodStatus{Phase: corev1.PodPending},
		},
	}

	writePodTable(path, pods)

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	if !strings.Contains(content, "pod-a") {
		t.Error("expected pod-a in output")
	}
	if !strings.Contains(content, "Running") {
		t.Error("expected Running status")
	}
	if !strings.Contains(content, "1/2") {
		t.Error("expected 1/2 ready count")
	}
	if !strings.Contains(content, "Pending") {
		t.Error("expected Pending status")
	}
}

func TestWriteRolloutHistoryText_ReplicaSets(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "history.txt")

	rsList := []appsv1.ReplicaSet{
		{
			ObjectMeta: metav1.ObjectMeta{
				Name: "dep-abc",
				Annotations: map[string]string{
					"deployment.kubernetes.io/revision": "1",
					"kubernetes.io/change-cause":        "initial deploy",
				},
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{
				Name:        "dep-def",
				Annotations: map[string]string{"deployment.kubernetes.io/revision": "2"},
			},
		},
	}

	writeRolloutHistoryText(path, "deployment", rsList)

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	if !strings.Contains(content, "deployment rollout history") {
		t.Error("expected kind in header")
	}
	if !strings.Contains(content, "initial deploy") {
		t.Error("expected change-cause")
	}
	if !strings.Contains(content, "<none>") {
		t.Error("expected <none> for missing change-cause")
	}
}

func TestWriteRolloutHistoryText_ControllerRevisions(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "history.txt")

	crList := []appsv1.ControllerRevision{
		{ObjectMeta: metav1.ObjectMeta{Name: "sts-1"}, Revision: 1},
		{ObjectMeta: metav1.ObjectMeta{Name: "sts-2"}, Revision: 2},
	}

	writeRolloutHistoryText(path, "statefulset", crList)

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "statefulset rollout history") {
		t.Error("expected kind in header")
	}
}

func TestCollectNamespaceData(t *testing.T) {
	dir := t.TempDir()

	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "app-config", Namespace: "rhdh"},
	}

	cfg := newTestConfig(t, dir, withTypedObjs(cm))

	outDir := filepath.Join(dir, "ns-data")
	CollectNamespaceData(context.Background(), cfg, "rhdh", outDir, false)

	if _, err := os.Stat(filepath.Join(outDir, "_configmaps", "app-config.yaml")); err != nil {
		t.Error("expected configmap YAML file")
	}
	if _, err := os.Stat(filepath.Join(outDir, "_secrets")); !os.IsNotExist(err) {
		t.Error("expected no _secrets dir when withSecrets=false")
	}
}

func TestCollectNamespaceData_WithSecrets(t *testing.T) {
	dir := t.TempDir()

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "db-creds", Namespace: "rhdh"},
	}
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "app-config", Namespace: "rhdh"},
	}

	cfg := newTestConfig(t, dir, withTypedObjs(cm, secret))

	outDir := filepath.Join(dir, "ns-data")
	CollectNamespaceData(context.Background(), cfg, "rhdh", outDir, true)

	if _, err := os.Stat(filepath.Join(outDir, "_secrets", "db-creds.yaml")); err != nil {
		t.Error("expected secret YAML file when withSecrets=true")
	}
}

func TestCollectRolloutHistory_Deployment(t *testing.T) {
	dir := t.TempDir()

	rs := &appsv1.ReplicaSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "dep-abc",
			Namespace: "rhdh",
			Labels:    map[string]string{"app": "rhdh"},
			Annotations: map[string]string{
				"deployment.kubernetes.io/revision": "1",
				"kubernetes.io/change-cause":        "initial",
			},
		},
	}

	cfg := newTestConfig(t, dir, withTypedObjs(rs))

	collectRolloutHistory(context.Background(), cfg, "rhdh", KindDeployment, map[string]string{"app": "rhdh"}, dir)

	histPath := filepath.Join(dir, "rollout-history", "history.txt")
	data, err := os.ReadFile(histPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "deployment rollout history") {
		t.Error("expected deployment header")
	}
	if !strings.Contains(content, "initial") {
		t.Error("expected change-cause")
	}

	rsPath := filepath.Join(dir, "rollout-history", "replicasets", "replicasets.yaml")
	if _, err := os.Stat(rsPath); err != nil {
		t.Error("expected replicasets.yaml file")
	}
}

func TestCollectRolloutHistory_StatefulSet(t *testing.T) {
	dir := t.TempDir()

	cr := &appsv1.ControllerRevision{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "sts-rev-1",
			Namespace: "rhdh",
			Labels:    map[string]string{"app": "rhdh"},
		},
		Revision: 1,
	}

	cfg := newTestConfig(t, dir, withTypedObjs(cr))

	collectRolloutHistory(context.Background(), cfg, "rhdh", KindStatefulSet, map[string]string{"app": "rhdh"}, dir)

	histPath := filepath.Join(dir, "rollout-history", "history.txt")
	data, err := os.ReadFile(histPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !strings.Contains(string(data), "statefulset rollout history") {
		t.Error("expected statefulset header")
	}

	crPath := filepath.Join(dir, "rollout-history", "controllerrevisions", "controllerrevisions.yaml")
	if _, err := os.Stat(crPath); err != nil {
		t.Error("expected controllerrevisions.yaml file")
	}
}

func TestCollectDBStatefulSet(t *testing.T) {
	dir := t.TempDir()

	sts := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "backstage-psql",
			Namespace: "rhdh",
		},
		Spec: appsv1.StatefulSetSpec{
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"app": "postgresql"},
			},
		},
	}
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "backstage-psql-0",
			Namespace: "rhdh",
			Labels:    map[string]string{"app": "postgresql"},
		},
		Status: corev1.PodStatus{Phase: corev1.PodRunning},
	}

	cfg := newTestConfig(t, dir, withTypedObjs(sts, pod))

	err := CollectDBStatefulSet(context.Background(), cfg, "rhdh", "backstage-psql", dir)
	if err != nil {
		t.Fatalf("CollectDBStatefulSet: %v", err)
	}

	stsYAML := filepath.Join(dir, "db-statefulset", "db-statefulset.yaml")
	if _, err := os.Stat(stsYAML); err != nil {
		t.Error("expected db-statefulset.yaml file")
	}

	podsYAML := filepath.Join(dir, "db-statefulset", "pods", "pods.yaml")
	if _, err := os.Stat(podsYAML); err != nil {
		t.Error("expected pods.yaml file")
	}
}

func TestCollectDBStatefulSet_EmptyName(t *testing.T) {
	cfg := newTestConfig(t, t.TempDir())
	err := CollectDBStatefulSet(context.Background(), cfg, "rhdh", "", t.TempDir())
	if err != nil {
		t.Errorf("expected nil error for empty name, got %v", err)
	}
}

func TestOwnerRefKind(t *testing.T) {
	tests := []struct {
		name string
		kind WorkloadKind
		want string
	}{
		{"deployment", KindDeployment, "ReplicaSet"},
		{"statefulset", KindStatefulSet, "StatefulSet"},
		{"unknown", "unknown", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ownerRefKind(tt.kind)
			if got != tt.want {
				t.Errorf("ownerRefKind(%q) = %q, want %q", tt.kind, got, tt.want)
			}
		})
	}
}
