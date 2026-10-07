package collector

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestMatchesInstance(t *testing.T) {
	tests := []struct {
		label   string
		name    string
		pattern string
		want    bool
	}{
		{"prefix match", "rhdhsupp-308-backstage", "rhdhsupp-308", true},
		{"exact match", "rhdhsupp-308", "rhdhsupp-308", true},
		{"no match", "my-backstage", "rhdhsupp-308", false},
		{"empty name", "", "rhdhsupp-308", false},
		{"empty pattern", "rhdhsupp-308-backstage", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.label, func(t *testing.T) {
			if got := matchesInstance(tt.name, tt.pattern); got != tt.want {
				t.Errorf("matchesInstance(%q, %q) = %v, want %v", tt.name, tt.pattern, got, tt.want)
			}
		})
	}
}

func TestMatchesInstanceFilter(t *testing.T) {
	t.Run("no filter set", func(t *testing.T) {
		if !matchesInstanceFilter("anything", "anything", "") {
			t.Error("expected true when no filter set")
		}
	})

	t.Run("deploy name matches", func(t *testing.T) {
		if !matchesInstanceFilter("rhdhsupp-308-backstage", "other", "rhdhsupp-308") {
			t.Error("expected true when deploy name matches")
		}
	})

	t.Run("instance name matches", func(t *testing.T) {
		if !matchesInstanceFilter("other-deploy", "my-release", "my-release") {
			t.Error("expected true when instance name matches")
		}
	})

	t.Run("no match", func(t *testing.T) {
		if matchesInstanceFilter("other-deploy", "other-instance", "rhdhsupp-308") {
			t.Error("expected false when nothing matches")
		}
	})

	t.Run("multiple instances", func(t *testing.T) {
		if !matchesInstanceFilter("rhdhsupp-308-backstage", "", "foo, rhdhsupp-308, bar") {
			t.Error("expected true with multiple instances")
		}
	})
}

func TestHeapDumpTimeout(t *testing.T) {
	t.Run("default", func(t *testing.T) {
		t.Setenv("HEAP_DUMP_TIMEOUT", "")
		got := heapDumpTimeout()
		if got.Seconds() != 600 {
			t.Errorf("heapDumpTimeout() = %v, want 600s", got)
		}
	})

	t.Run("custom", func(t *testing.T) {
		t.Setenv("HEAP_DUMP_TIMEOUT", "120")
		got := heapDumpTimeout()
		if got.Seconds() != 120 {
			t.Errorf("heapDumpTimeout() = %v, want 120s", got)
		}
	})
}

func TestHasOwnerKind(t *testing.T) {
	tests := []struct {
		name string
		pod  corev1.Pod
		kind string
		want bool
	}{
		{
			name: "matching owner",
			pod: corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					OwnerReferences: []metav1.OwnerReference{
						{Kind: "ReplicaSet", Name: "dep-abc"},
					},
				},
			},
			kind: "ReplicaSet",
			want: true,
		},
		{
			name: "no matching owner",
			pod: corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					OwnerReferences: []metav1.OwnerReference{
						{Kind: "StatefulSet", Name: "sts-1"},
					},
				},
			},
			kind: "ReplicaSet",
			want: false,
		},
		{
			name: "no owner refs",
			pod:  corev1.Pod{},
			kind: "ReplicaSet",
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := hasOwnerKind(tt.pod, tt.kind); got != tt.want {
				t.Errorf("hasOwnerKind() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestWarnLivenessProbeTimeout(t *testing.T) {
	t.Run("no warning when probe timeout exceeds heap timeout", func(t *testing.T) {
		pod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: "test-pod"},
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{
					{
						Name: backstageContainer,
						LivenessProbe: &corev1.Probe{
							FailureThreshold: 100,
							PeriodSeconds:    10,
						},
					},
				},
			},
		}
		warnLivenessProbeTimeout(pod, 600*time.Second)
	})

	t.Run("warns when probe timeout is less than heap timeout", func(t *testing.T) {
		pod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: "test-pod"},
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{
					{
						Name: backstageContainer,
						LivenessProbe: &corev1.Probe{
							FailureThreshold: 3,
							PeriodSeconds:    10,
						},
					},
				},
			},
		}
		warnLivenessProbeTimeout(pod, 600*time.Second)
	})

	t.Run("uses defaults when thresholds are zero", func(t *testing.T) {
		pod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: "test-pod"},
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{
					{
						Name:          backstageContainer,
						LivenessProbe: &corev1.Probe{},
					},
				},
			},
		}
		warnLivenessProbeTimeout(pod, 600*time.Second)
	})

	t.Run("no crash on nil probe", func(t *testing.T) {
		pod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: "test-pod"},
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{
					{Name: backstageContainer},
				},
			},
		}
		warnLivenessProbeTimeout(pod, 600*time.Second)
	})

	t.Run("skips non-backstage containers", func(t *testing.T) {
		pod := &corev1.Pod{
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{
					{
						Name: "other-container",
						LivenessProbe: &corev1.Probe{
							FailureThreshold: 1,
							PeriodSeconds:    1,
						},
					},
				},
			},
		}
		warnLivenessProbeTimeout(pod, 600*time.Second)
	})
}

func TestCollectHeapDumps_Disabled(t *testing.T) {
	cfg := newTestConfig(t, t.TempDir())
	cfg.WithHeapDumps = false
	collectHeapDumps(cfg, "rhdh", "app=rhdh", t.TempDir(), "dep", "instance", "deployment")
}

func TestCollectHeapDumps_FilteredOut(t *testing.T) {
	cfg := newTestConfig(t, t.TempDir())
	cfg.WithHeapDumps = true
	cfg.HeapDumpInstances = "other-instance"
	collectHeapDumps(cfg, "rhdh", "app=rhdh", t.TempDir(), "dep", "instance", "deployment")
}

func TestCollectHeapDumps_NoPods(t *testing.T) {
	dir := t.TempDir()
	cfg := newTestConfig(t, dir)
	cfg.WithHeapDumps = true

	outDir := filepath.Join(dir, "output")
	collectHeapDumps(cfg, "rhdh", "app=rhdh", outDir, "dep", "instance", "deployment")

	noPodsFile := filepath.Join(outDir, "heap-dumps", "no-pods.txt")
	if _, err := os.Stat(noPodsFile); err != nil {
		t.Error("expected no-pods.txt when no running pods found")
	}
}

func TestWriteCollectionFailedGuidance(t *testing.T) {
	t.Run("inspector method", func(t *testing.T) {
		dir := t.TempDir()
		writeCollectionFailedGuidance(dir, "inspector", "1234", "my-pod", backstageContainer, "rhdh")

		data, err := os.ReadFile(filepath.Join(dir, "collection-failed.txt"))
		if err != nil {
			t.Fatalf("ReadFile: %v", err)
		}
		content := string(data)
		if !strings.Contains(content, "Heap Dump Collection Failed") {
			t.Error("expected header")
		}
		if !strings.Contains(content, "inspector") {
			t.Error("expected method name")
		}
		if !strings.Contains(content, "PID: 1234") {
			t.Error("expected PID")
		}
		if !strings.Contains(content, "Why Inspector Protocol Failed") {
			t.Error("expected inspector-specific guidance")
		}
	})

	t.Run("sigusr2 method", func(t *testing.T) {
		dir := t.TempDir()
		writeCollectionFailedGuidance(dir, "sigusr2", "5678", "my-pod", backstageContainer, "rhdh")

		data, err := os.ReadFile(filepath.Join(dir, "collection-failed.txt"))
		if err != nil {
			t.Fatalf("ReadFile: %v", err)
		}
		content := string(data)
		if !strings.Contains(content, "Why SIGUSR2 Method Failed") {
			t.Error("expected sigusr2-specific guidance")
		}
		if !strings.Contains(content, "--heapsnapshot-signal=SIGUSR2") {
			t.Error("expected NODE_OPTIONS guidance")
		}
	})
}

func TestAppendLog(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.log")

	appendLog(path, "line one: %s\n", "hello")
	appendLog(path, "line two: %d\n", 42)

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "line one: hello") {
		t.Error("expected first line")
	}
	if !strings.Contains(content, "line two: 42") {
		t.Error("expected second line")
	}
}

func TestFindRunningPods(t *testing.T) {
	pod1 := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "rhdh-abc-123",
			Namespace: "rhdh",
			Labels:    map[string]string{"app": "rhdh"},
			OwnerReferences: []metav1.OwnerReference{
				{Kind: "ReplicaSet", Name: "rhdh-abc"},
			},
		},
		Status: corev1.PodStatus{Phase: corev1.PodRunning},
	}
	pod2 := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "rhdh-def-456",
			Namespace: "rhdh",
			Labels:    map[string]string{"app": "rhdh"},
			OwnerReferences: []metav1.OwnerReference{
				{Kind: "StatefulSet", Name: "rhdh-sts"},
			},
		},
		Status: corev1.PodStatus{Phase: corev1.PodRunning},
	}

	cfg := newTestConfig(t, t.TempDir(), withTypedObjs(pod1, pod2))

	t.Run("deployment kind filters by ReplicaSet owner", func(t *testing.T) {
		names := findRunningPods(cfg, "rhdh", "app=rhdh", string(KindDeployment))
		if len(names) != 1 || names[0] != "rhdh-abc-123" {
			t.Errorf("got %v, want [rhdh-abc-123]", names)
		}
	})

	t.Run("statefulset kind filters by StatefulSet owner", func(t *testing.T) {
		names := findRunningPods(cfg, "rhdh", "app=rhdh", string(KindStatefulSet))
		if len(names) != 1 || names[0] != "rhdh-def-456" {
			t.Errorf("got %v, want [rhdh-def-456]", names)
		}
	})

	t.Run("empty kind returns all", func(t *testing.T) {
		names := findRunningPods(cfg, "rhdh", "app=rhdh", "")
		if len(names) != 2 {
			t.Errorf("got %d pods, want 2", len(names))
		}
	})

	t.Run("no pods in namespace", func(t *testing.T) {
		names := findRunningPods(cfg, "other-ns", "app=rhdh", string(KindDeployment))
		if len(names) != 0 {
			t.Errorf("got %v, want empty", names)
		}
	})
}

func TestHumanSize(t *testing.T) {
	tests := []struct {
		name  string
		bytes int64
		want  string
	}{
		{"zero", 0, "0B"},
		{"bytes", 512, "512B"},
		{"1KB", 1024, "1KB"},
		{"rounds down", 1536, "1KB"},
		{"1MB", 1048576, "1MB"},
		{"100MB", 104857600, "100MB"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := humanSize(tt.bytes); got != tt.want {
				t.Errorf("humanSize(%d) = %q, want %q", tt.bytes, got, tt.want)
			}
		})
	}
}
