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
	"k8s.io/apimachinery/pkg/runtime/schema"
)

func TestSetGVK(t *testing.T) {
	pod := &corev1.Pod{}
	setGVK(pod, "Pod", "v1")

	gvk := pod.GetObjectKind().GroupVersionKind()
	if gvk.Kind != "Pod" {
		t.Errorf("Kind = %q, want Pod", gvk.Kind)
	}
	if gvk.Version != "v1" {
		t.Errorf("Version = %q, want v1", gvk.Version)
	}
}

func TestWriteResource(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "resource.yaml")

	data := map[string]string{"name": "test"}
	writeResource(path, data)

	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading file: %v", err)
	}
	if !strings.Contains(string(content), "name: test") {
		t.Errorf("content = %q, want 'name: test'", string(content))
	}
}

func TestWriteCollectError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sub", "error.txt")

	writeCollectError(path, "list pods", os.ErrPermission)

	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading file: %v", err)
	}
	s := string(content)
	if !strings.Contains(s, "list pods") {
		t.Error("expected description in output")
	}
	if !strings.Contains(s, "permission denied") {
		t.Error("expected error message in output")
	}
}

func TestKnownGroupKinds(t *testing.T) {
	tests := []struct {
		input string
		kind  string
		group string
	}{
		{"pod", "Pod", ""},
		{"pods", "Pod", ""},
		{"deployment", "Deployment", "apps"},
		{"deployments", "Deployment", "apps"},
		{"statefulset", "StatefulSet", "apps"},
		{"replicaset", "ReplicaSet", "apps"},
		{"configmap", "ConfigMap", ""},
		{"configmaps", "ConfigMap", ""},
		{"crd", "CustomResourceDefinition", "apiextensions.k8s.io"},
		{"controllerrevision", "ControllerRevision", "apps"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			gk, ok := knownGroupKinds[tt.input]
			if !ok {
				t.Fatalf("knownGroupKinds missing %q", tt.input)
			}
			if gk.Kind != tt.kind {
				t.Errorf("Kind = %q, want %q", gk.Kind, tt.kind)
			}
			if gk.Group != tt.group {
				t.Errorf("Group = %q, want %q", gk.Group, tt.group)
			}
		})
	}
}

func TestListResourceNames(t *testing.T) {
	pod1 := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "pod-a", Namespace: "ns1"}}
	pod2 := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "pod-b", Namespace: "ns1"}}
	pod3 := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "pod-c", Namespace: "ns2"}}
	dep1 := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "dep-a", Namespace: "ns1"}}

	cfg := newTestConfig(t, "", withTypedObjs(pod1, pod2, pod3, dep1))

	t.Run("pods in namespace", func(t *testing.T) {
		names, err := listResourceNames(context.Background(), cfg,
			schema.GroupKind{Kind: "Pod"}, "ns1", "")
		if err != nil {
			t.Fatal(err)
		}
		if len(names) != 2 {
			t.Fatalf("got %d names, want 2", len(names))
		}
	})

	t.Run("deployments in namespace", func(t *testing.T) {
		names, err := listResourceNames(context.Background(), cfg,
			schema.GroupKind{Group: "apps", Kind: "Deployment"}, "ns1", "")
		if err != nil {
			t.Fatal(err)
		}
		if len(names) != 1 || names[0] != "dep-a" {
			t.Errorf("got %v, want [dep-a]", names)
		}
	})

	t.Run("empty namespace", func(t *testing.T) {
		names, err := listResourceNames(context.Background(), cfg,
			schema.GroupKind{Kind: "Pod"}, "ns-empty", "")
		if err != nil {
			t.Fatal(err)
		}
		if len(names) != 0 {
			t.Errorf("got %d names, want 0", len(names))
		}
	})

	t.Run("unknown kind", func(t *testing.T) {
		_, err := listResourceNames(context.Background(), cfg,
			schema.GroupKind{Kind: "Unknown"}, "ns1", "")
		if err == nil {
			t.Error("expected error for unknown kind")
		}
	})
}

func TestResolveCRDType(t *testing.T) {
	cfg := newTestConfig(t, "",
		withAPIGroups("rhdh.redhat.com/v1alpha5", "sonataflow.org/v1alpha08"),
	)

	t.Run("fully qualified", func(t *testing.T) {
		gvr, err := resolveCRDType(cfg, "sonataflow.sonataflow.org")
		if err != nil {
			t.Fatal(err)
		}
		if gvr.Group != "sonataflow.org" {
			t.Errorf("Group = %q, want sonataflow.org", gvr.Group)
		}
		if gvr.Version != "v1alpha08" {
			t.Errorf("Version = %q, want v1alpha08", gvr.Version)
		}
		if gvr.Resource != "sonataflows" {
			t.Errorf("Resource = %q, want sonataflows", gvr.Resource)
		}
	})

	t.Run("short name backstage", func(t *testing.T) {
		gvr, err := resolveCRDType(cfg, "backstage")
		if err != nil {
			t.Fatal(err)
		}
		if gvr.Group != "rhdh.redhat.com" {
			t.Errorf("Group = %q, want rhdh.redhat.com", gvr.Group)
		}
		if gvr.Version != "v1alpha5" {
			t.Errorf("Version = %q, want v1alpha5", gvr.Version)
		}
		if gvr.Resource != "backstages" {
			t.Errorf("Resource = %q, want backstages", gvr.Resource)
		}
	})

	t.Run("unknown short name", func(t *testing.T) {
		_, err := resolveCRDType(cfg, "unknown")
		if err == nil {
			t.Error("expected error for unknown CRD type")
		}
	})
}
