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

func TestCollectWorkload_Deployment(t *testing.T) {
	dir := t.TempDir()

	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "backstage-rhdh",
			Namespace: "rhdh",
		},
		Spec: appsv1.DeploymentSpec{
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"app": "rhdh"},
			},
		},
	}
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "backstage-rhdh-abc-123",
			Namespace: "rhdh",
			Labels:    map[string]string{"app": "rhdh"},
			OwnerReferences: []metav1.OwnerReference{
				{Kind: "ReplicaSet", Name: "backstage-rhdh-abc"},
			},
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{Name: "backstage-backend"}},
		},
		// Use Succeeded phase so exec-based goroutines (CollectProcesses,
		// CollectPodData) are not spawned — they require a real REST client.
		Status: corev1.PodStatus{Phase: corev1.PodSucceeded},
	}

	cfg := newTestConfig(t, dir, withTypedObjs(dep, pod))
	ref := WorkloadRef{Namespace: "rhdh", Name: "backstage-rhdh", Kind: KindDeployment, InstanceName: "rhdh"}
	outDir := filepath.Join(dir, "workload")

	err := CollectWorkload(context.Background(), cfg, ref, outDir)
	if err != nil {
		t.Fatalf("CollectWorkload: %v", err)
	}

	if _, err := os.Stat(filepath.Join(outDir, "deployment.yaml")); err != nil {
		t.Error("expected deployment.yaml")
	}
	if _, err := os.Stat(filepath.Join(outDir, "pods")); err != nil {
		t.Error("expected pods directory")
	}
	if _, err := os.Stat(filepath.Join(outDir, "pods", "pods.yaml")); err != nil {
		t.Error("expected pods/pods.yaml")
	}
	if _, err := os.Stat(filepath.Join(outDir, "pods", "pods.txt")); err != nil {
		t.Error("expected pods/pods.txt")
	}
}

func TestCollectWorkload_StatefulSet(t *testing.T) {
	dir := t.TempDir()

	sts := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "backstage-sts",
			Namespace: "rhdh",
		},
		Spec: appsv1.StatefulSetSpec{
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"app": "rhdh"},
			},
		},
	}
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "backstage-sts-0",
			Namespace: "rhdh",
			Labels:    map[string]string{"app": "rhdh"},
			OwnerReferences: []metav1.OwnerReference{
				{Kind: "StatefulSet", Name: "backstage-sts"},
			},
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{Name: "backstage-backend"}},
		},
		Status: corev1.PodStatus{Phase: corev1.PodSucceeded},
	}

	cfg := newTestConfig(t, dir, withTypedObjs(sts, pod))
	ref := WorkloadRef{Namespace: "rhdh", Name: "backstage-sts", Kind: KindStatefulSet, InstanceName: "rhdh"}
	outDir := filepath.Join(dir, "workload")

	err := CollectWorkload(context.Background(), cfg, ref, outDir)
	if err != nil {
		t.Fatalf("CollectWorkload: %v", err)
	}

	if _, err := os.Stat(filepath.Join(outDir, "statefulset.yaml")); err != nil {
		t.Error("expected statefulset.yaml")
	}
	if _, err := os.Stat(filepath.Join(outDir, "pods", "pods.yaml")); err != nil {
		t.Error("expected pods/pods.yaml")
	}
}

func TestCollectWorkload_RunningPodsWithPodOps(t *testing.T) {
	dir := t.TempDir()

	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "backstage-rhdh",
			Namespace: "rhdh",
		},
		Spec: appsv1.DeploymentSpec{
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"app": "rhdh"},
			},
		},
	}
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "backstage-rhdh-abc-123",
			Namespace: "rhdh",
			Labels:    map[string]string{"app": "rhdh"},
			OwnerReferences: []metav1.OwnerReference{
				{Kind: "ReplicaSet", Name: "backstage-rhdh-abc"},
			},
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{Name: "backstage-backend"}},
		},
		Status: corev1.PodStatus{Phase: corev1.PodRunning},
	}

	ops := &fakePodOps{
		execOutput: "mock exec output\n",
		logOutput:  "mock log line\n",
	}
	cfg := newTestConfig(t, dir, withTypedObjs(dep, pod), withPodOps(ops))
	ref := WorkloadRef{Namespace: "rhdh", Name: "backstage-rhdh", Kind: KindDeployment, InstanceName: "rhdh"}
	outDir := filepath.Join(dir, "workload")

	err := CollectWorkload(context.Background(), cfg, ref, outDir)
	if err != nil {
		t.Fatalf("CollectWorkload: %v", err)
	}

	if _, err := os.Stat(filepath.Join(outDir, "logs")); err != nil {
		t.Error("expected logs directory for Running pod")
	}
	if _, err := os.Stat(filepath.Join(outDir, "processes")); err != nil {
		t.Error("expected processes directory for Running pod")
	}
	if _, err := os.Stat(filepath.Join(outDir, "data")); err != nil {
		t.Error("expected data directory for Running pod")
	}
}

func TestWriteAggregatedStatefulSetLogs_WithPodOps(t *testing.T) {
	dir := t.TempDir()

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "db-0",
			Namespace: "rhdh",
			Labels:    map[string]string{"app": "db"},
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{Name: "postgres"}},
		},
	}

	ops := &fakePodOps{logOutput: "postgres log entry\n"}
	cfg := newTestConfig(t, dir, withTypedObjs(pod), withPodOps(ops))

	stsDir := filepath.Join(dir, "sts")
	_ = os.MkdirAll(stsDir, 0o755)
	writeAggregatedStatefulSetLogs(context.Background(), cfg, "rhdh", "app=db", stsDir)

	data, err := os.ReadFile(filepath.Join(stsDir, "logs-db.txt"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "postgres log entry") {
		t.Errorf("expected log content, got: %s", content)
	}
	if !strings.Contains(content, "[pod/db-0/postgres]") {
		t.Errorf("expected pod prefix, got: %s", content)
	}
}

func TestCollectWorkload_NoPods(t *testing.T) {
	dir := t.TempDir()

	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "backstage-rhdh",
			Namespace: "rhdh",
		},
		Spec: appsv1.DeploymentSpec{
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"app": "rhdh"},
			},
		},
	}

	cfg := newTestConfig(t, dir, withTypedObjs(dep))
	ref := WorkloadRef{Namespace: "rhdh", Name: "backstage-rhdh", Kind: KindDeployment, InstanceName: "rhdh"}
	outDir := filepath.Join(dir, "workload")

	err := CollectWorkload(context.Background(), cfg, ref, outDir)
	if err != nil {
		t.Fatalf("CollectWorkload: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(outDir, "pods", "pods.txt"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !strings.Contains(string(data), "No pods found") {
		t.Error("expected 'No pods found' message")
	}
}

func TestCollectWorkload_NotFound(t *testing.T) {
	cfg := newTestConfig(t, t.TempDir())
	ref := WorkloadRef{Namespace: "rhdh", Name: "nonexistent", Kind: KindDeployment}

	err := CollectWorkload(context.Background(), cfg, ref, t.TempDir())
	if err == nil {
		t.Error("expected error for nonexistent deployment")
	}
}

func TestCollectWorkload_Interrupted(t *testing.T) {
	dir := t.TempDir()

	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "backstage-rhdh",
			Namespace: "rhdh",
		},
		Spec: appsv1.DeploymentSpec{
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"app": "rhdh"},
			},
		},
	}
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "backstage-rhdh-abc-123",
			Namespace: "rhdh",
			Labels:    map[string]string{"app": "rhdh"},
			OwnerReferences: []metav1.OwnerReference{
				{Kind: "ReplicaSet", Name: "backstage-rhdh-abc"},
			},
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{Name: "backstage-backend"}},
		},
		Status: corev1.PodStatus{Phase: corev1.PodSucceeded},
	}

	cfg := newTestConfig(t, dir, withTypedObjs(dep, pod), withInterrupted())
	ref := WorkloadRef{Namespace: "rhdh", Name: "backstage-rhdh", Kind: KindDeployment, InstanceName: "rhdh"}
	outDir := filepath.Join(dir, "workload")

	err := CollectWorkload(context.Background(), cfg, ref, outDir)
	if err != nil {
		t.Fatalf("CollectWorkload: %v", err)
	}

	// With interrupted flag, log streaming goroutines return early
	if _, err := os.Stat(filepath.Join(outDir, "logs")); !os.IsNotExist(err) {
		t.Error("expected no logs dir when interrupted")
	}
}

func TestWriteAggregatedStatefulSetLogs_WithPods(t *testing.T) {
	dir := t.TempDir()

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "db-0",
			Namespace: "rhdh",
			Labels:    map[string]string{"app": "db"},
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{Name: "postgres"}},
		},
	}

	cfg := newTestConfig(t, dir, withTypedObjs(pod))

	stsDir := filepath.Join(dir, "sts")
	_ = os.MkdirAll(stsDir, 0o755)
	writeAggregatedStatefulSetLogs(context.Background(), cfg, "rhdh", "app=db", stsDir)

	// Files are created even though log streaming fails with fake client
	if _, err := os.Stat(filepath.Join(stsDir, "logs-db.txt")); err != nil {
		t.Error("expected logs-db.txt")
	}
	if _, err := os.Stat(filepath.Join(stsDir, "logs-db-previous.txt")); err != nil {
		t.Error("expected logs-db-previous.txt")
	}
}

func TestWriteAggregatedStatefulSetLogs_NoPods(t *testing.T) {
	dir := t.TempDir()

	cfg := newTestConfig(t, dir)

	stsDir := filepath.Join(dir, "sts")
	_ = os.MkdirAll(stsDir, 0o755)
	writeAggregatedStatefulSetLogs(context.Background(), cfg, "rhdh", "app=db", stsDir)

	if _, err := os.Stat(filepath.Join(stsDir, "logs-db.txt")); !os.IsNotExist(err) {
		t.Error("expected no logs-db.txt when no pods found")
	}
}
