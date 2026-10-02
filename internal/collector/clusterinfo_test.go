package collector

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestClusterInfo_Name(t *testing.T) {
	c := &ClusterInfo{}
	if got := c.Name(); got != "cluster-info" {
		t.Errorf("Name() = %q, want %q", got, "cluster-info")
	}
}

func TestWriteYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sub", "test.yaml")

	data := map[string]string{"key": "value"}
	writeYAML(path, data)

	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading file: %v", err)
	}
	if len(content) == 0 {
		t.Fatal("expected non-empty output")
	}
}

func TestWriteYAML_CreatesParentDirs(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a", "b", "c", "test.yaml")

	writeYAML(path, "hello")

	if _, err := os.Stat(path); err != nil {
		t.Errorf("expected file to exist: %v", err)
	}
}

func TestClusterInfo_Run(t *testing.T) {
	dir := t.TempDir()

	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "test-ns"}}
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "test-pod", Namespace: "test-ns"}}

	cfg := newTestConfig(t, dir, withTypedObjs(ns, pod))

	c := &ClusterInfo{}
	if err := c.Run(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}

	clusterDir := filepath.Join(dir, "cluster-info")
	if _, err := os.Stat(clusterDir); err != nil {
		t.Fatal("cluster-info directory not created")
	}

	nsDir := filepath.Join(clusterDir, "test-ns")
	if _, err := os.Stat(filepath.Join(nsDir, "pods.yaml")); err != nil {
		t.Error("pods.yaml not created for namespace")
	}
	if _, err := os.Stat(filepath.Join(nsDir, "events.yaml")); err != nil {
		t.Error("events.yaml not created for namespace")
	}
}

func TestClusterInfo_Run_Interrupted(t *testing.T) {
	dir := t.TempDir()

	ns1 := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "ns1"}}
	ns2 := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "ns2"}}

	cfg := newTestConfig(t, dir, withTypedObjs(ns1, ns2), withInterrupted())

	c := &ClusterInfo{}
	if err := c.Run(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}

	clusterDir := filepath.Join(dir, "cluster-info")
	entries, _ := os.ReadDir(clusterDir)
	nsCount := 0
	for _, e := range entries {
		if e.IsDir() {
			nsCount++
		}
	}
	if nsCount > 0 {
		t.Error("expected no namespace directories when interrupted before processing")
	}
}
