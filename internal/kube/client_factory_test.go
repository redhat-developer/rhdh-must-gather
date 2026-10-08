package kube

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNewClient_Success(t *testing.T) {
	kubeconfig := `apiVersion: v1
kind: Config
clusters:
- cluster:
    server: https://fake-server:6443
  name: test
contexts:
- context:
    cluster: test
    user: test
  name: test
current-context: test
users:
- name: test
  user:
    token: fake-token
`
	f := filepath.Join(t.TempDir(), "kubeconfig")
	if err := os.WriteFile(f, []byte(kubeconfig), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KUBECONFIG", f)
	t.Setenv("KUBERNETES_SERVICE_HOST", "")

	c, err := NewClient()
	if err != nil {
		t.Fatalf("NewClient() error: %v", err)
	}
	if c.Clientset == nil {
		t.Error("expected non-nil Clientset")
	}
	if c.Dynamic == nil {
		t.Error("expected non-nil Dynamic")
	}
	if c.Discovery == nil {
		t.Error("expected non-nil Discovery")
	}
	if c.Config == nil {
		t.Error("expected non-nil Config")
	}
}

func TestNewClient_NoConfig(t *testing.T) {
	t.Setenv("KUBECONFIG", filepath.Join(t.TempDir(), "nonexistent"))
	t.Setenv("KUBERNETES_SERVICE_HOST", "")

	_, err := NewClient()
	if err == nil {
		t.Fatal("expected error when no kubeconfig exists")
	}
}

func TestNewClient_EmptyConfig(t *testing.T) {
	t.Setenv("KUBECONFIG", "/dev/null")
	t.Setenv("KUBERNETES_SERVICE_HOST", "")

	_, err := NewClient()
	if err == nil {
		t.Fatal("expected error when kubeconfig is empty")
	}
}
