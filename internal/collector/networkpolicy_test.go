package collector

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	fakeclientset "k8s.io/client-go/kubernetes/fake"

	"github.com/redhat-developer/rhdh-must-gather/internal/kube"
)

func TestNetworkPoliciesName(t *testing.T) {
	n := &NetworkPolicies{}
	if got := n.Name(); got != "network-policies" {
		t.Errorf("Name() = %q, want network-policies", got)
	}
}

func npTestConfig(t *testing.T, basePath string, objs ...runtime.Object) *Config {
	t.Helper()
	return &Config{
		BasePath:    basePath,
		Interrupted: new(atomic.Bool),
		Client: &kube.Client{
			Clientset: fakeclientset.NewSimpleClientset(objs...),
		},
	}
}

func mustRead(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	return string(data)
}

func TestNetworkPolicies_TargetedNamespacesCollectFiles(t *testing.T) {
	dir := t.TempDir()
	ns := "rhdh-prod"
	cfg := npTestConfig(t, dir,
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{
			Name:   ns,
			Labels: map[string]string{"kubernetes.io/metadata.name": ns},
		}},
		&corev1.Pod{ObjectMeta: metav1.ObjectMeta{
			Name:      "backstage",
			Namespace: ns,
			Labels:    map[string]string{"app.kubernetes.io/name": "developer-hub"},
		}},
		&corev1.Service{ObjectMeta: metav1.ObjectMeta{
			Name:      "backstage",
			Namespace: ns,
		}},
		&networkingv1.NetworkPolicy{
			ObjectMeta: metav1.ObjectMeta{Name: "allow-https", Namespace: ns},
			Spec: networkingv1.NetworkPolicySpec{
				PodSelector: metav1.LabelSelector{MatchLabels: map[string]string{"app.kubernetes.io/name": "developer-hub"}},
				PolicyTypes: []networkingv1.PolicyType{networkingv1.PolicyTypeIngress},
			},
		},
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "openshift-monitoring"}},
	)
	cfg.TargetNamespaces = []string{ns}

	if err := (&NetworkPolicies{}).Run(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}

	nsDir := filepath.Join(dir, "network-policies", "ns="+ns)
	for _, name := range []string{
		"networkpolicies.yaml",
		"networkpolicies.json",
		"networkpolicies.txt",
		"networkpolicies.labels.txt",
		"networkpolicies.describe.txt",
		"namespace.yaml",
		"pods-show-labels.txt",
		"services.yaml",
	} {
		path := filepath.Join(nsDir, name)
		if _, err := os.Stat(path); err != nil {
			t.Errorf("missing %s: %v", name, err)
		}
	}

	summary := mustRead(t, filepath.Join(dir, "network-policies", "summary.txt"))
	if !strings.Contains(summary, ns) {
		t.Errorf("summary missing namespace %q", ns)
	}
	if !strings.Contains(summary, "How to troubleshoot") {
		t.Error("summary missing troubleshooting section")
	}
	if !strings.Contains(summary, "allow-https") {
		t.Error("summary missing policy name")
	}

	detected := mustRead(t, filepath.Join(dir, "network-policies", "detected-namespaces.txt"))
	if !strings.Contains(detected, ns) {
		t.Errorf("detected-namespaces.txt missing %q", ns)
	}

	if _, err := os.Stat(filepath.Join(dir, "network-policies", "peer-namespaces", "openshift-monitoring.yaml")); err != nil {
		t.Errorf("missing peer namespace file: %v", err)
	}
}

func TestNetworkPolicies_TargetedIgnoresOrchestrator(t *testing.T) {
	dir := t.TempDir()
	orchDir := filepath.Join(dir, "orchestrator")
	if err := os.MkdirAll(orchDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(orchDir, "detected-namespaces.txt"), []byte("knative-serving\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := npTestConfig(t, dir)
	cfg.TargetNamespaces = []string{"rhdh-prod", "rhdh-staging"}

	if err := (&NetworkPolicies{}).Run(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}

	detected := mustRead(t, filepath.Join(dir, "network-policies", "detected-namespaces.txt"))
	if !strings.Contains(detected, "rhdh-prod") || !strings.Contains(detected, "rhdh-staging") {
		t.Errorf("detected-namespaces.txt missing targets: %q", detected)
	}
	if strings.Contains(detected, "knative-serving") {
		t.Error("targeted run should not add orchestrator namespaces")
	}
}

func TestNetworkPolicies_AutoDetectEmpty(t *testing.T) {
	dir := t.TempDir()
	cfg := npTestConfig(t, dir)

	if err := (&NetworkPolicies{}).Run(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(filepath.Join(dir, "network-policies", "no-namespaces.txt")); err != nil {
		t.Fatalf("expected no-namespaces.txt: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "network-policies", "summary.txt")); err == nil {
		t.Error("summary.txt should not exist when no namespaces are found")
	}
}

func TestNetworkPolicies_AutoDetectMergesOrchestrator(t *testing.T) {
	dir := t.TempDir()
	orchDir := filepath.Join(dir, "orchestrator")
	if err := os.MkdirAll(orchDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(orchDir, "detected-namespaces.txt"), []byte("knative-serving\n  knative-serving\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := npTestConfig(t, dir)
	if err := (&NetworkPolicies{}).Run(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}

	detected := mustRead(t, filepath.Join(dir, "network-policies", "detected-namespaces.txt"))
	if !strings.Contains(detected, "knative-serving") {
		t.Errorf("expected orchestrator namespace, got %q", detected)
	}
	count := 0
	for _, line := range strings.Split(strings.TrimSpace(detected), "\n") {
		if strings.TrimSpace(line) == "knative-serving" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("knative-serving appeared %d times, want 1", count)
	}
}
