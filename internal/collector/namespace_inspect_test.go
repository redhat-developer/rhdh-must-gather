package collector

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	kcmdutil "k8s.io/kubectl/pkg/cmd/util"

	"helm.sh/helm/v4/pkg/action"
	kubefake "helm.sh/helm/v4/pkg/kube/fake"
	"helm.sh/helm/v4/pkg/storage"
	"helm.sh/helm/v4/pkg/storage/driver"
)

func TestNamespaceInspectName(t *testing.T) {
	n := &NamespaceInspect{}
	if got := n.Name(); got != "namespace-inspect" {
		t.Errorf("Name() = %q, want namespace-inspect", got)
	}
}

func TestMatchesAnyPattern(t *testing.T) {
	patterns := []string{"backstage", "rhdh", "developer-hub"}

	tests := []struct {
		name  string
		value string
		want  bool
	}{
		{"backstage prefix", "backstage-chart-1.0", true},
		{"rhdh case insensitive", "RHDH-Helm", true},
		{"developer-hub prefix", "developer-hub-app", true},
		{"unrelated", "postgres", false},
		{"empty string", "", false},
		{"capitalized", "Backstage", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := matchesAnyPattern(tt.value, patterns); got != tt.want {
				t.Errorf("matchesAnyPattern(%q) = %v, want %v", tt.value, got, tt.want)
			}
		})
	}
}

func TestContainsImagePattern(t *testing.T) {
	patterns := []string{"quay.io/rhdh", "registry.redhat.io/rhdh", "ghcr.io/backstage/backstage"}

	tests := []struct {
		name       string
		containers []corev1.Container
		want       bool
	}{
		{
			name:       "matching image",
			containers: []corev1.Container{{Image: "quay.io/rhdh/rhdh-hub-rhel9:1.5"}},
			want:       true,
		},
		{
			name:       "no match",
			containers: []corev1.Container{{Image: "postgres:15"}},
			want:       false,
		},
		{
			name:       "empty containers",
			containers: nil,
			want:       false,
		},
		{
			name: "multiple containers one match",
			containers: []corev1.Container{
				{Image: "postgres:15"},
				{Image: "ghcr.io/backstage/backstage:latest"},
			},
			want: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := containsImagePattern(tt.containers, patterns); got != tt.want {
				t.Errorf("containsImagePattern() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestRemoveSecrets(t *testing.T) {
	t.Run("removes secrets dir and files", func(t *testing.T) {
		dir := t.TempDir()
		secretsDir := filepath.Join(dir, "ns1", "secrets")
		_ = os.MkdirAll(secretsDir, 0o755)
		_ = os.WriteFile(filepath.Join(secretsDir, "db-creds.yaml"), []byte("secret data"), 0o644)
		_ = os.WriteFile(filepath.Join(dir, "ns1", "secrets.yaml"), []byte("secret list"), 0o644)
		_ = os.WriteFile(filepath.Join(dir, "ns1", "configmaps.yaml"), []byte("config data"), 0o644)

		n := &NamespaceInspect{}
		cfg := &Config{WithSecrets: false}
		n.removeSecrets(cfg, dir)

		if _, err := os.Stat(secretsDir); !os.IsNotExist(err) {
			t.Error("expected secrets dir to be removed")
		}
		if _, err := os.Stat(filepath.Join(dir, "ns1", "secrets.yaml")); !os.IsNotExist(err) {
			t.Error("expected secrets.yaml to be removed")
		}
		if _, err := os.Stat(filepath.Join(dir, "ns1", "configmaps.yaml")); err != nil {
			t.Error("expected configmaps.yaml to be preserved")
		}
	})

	t.Run("skips when withSecrets is true", func(t *testing.T) {
		dir := t.TempDir()
		secretsDir := filepath.Join(dir, "secrets")
		_ = os.MkdirAll(secretsDir, 0o755)
		_ = os.WriteFile(filepath.Join(secretsDir, "data.yaml"), []byte("secret"), 0o644)

		n := &NamespaceInspect{}
		cfg := &Config{WithSecrets: true}
		n.removeSecrets(cfg, dir)

		if _, err := os.Stat(secretsDir); err != nil {
			t.Error("expected secrets dir to remain when withSecrets=true")
		}
	})
}

func TestWriteSummary(t *testing.T) {
	dir := t.TempDir()
	n := &NamespaceInspect{}
	cfg := &Config{Since: 5 * time.Minute}
	n.writeSummary(cfg, dir, []string{"ns1", "ns2"})

	data, err := os.ReadFile(filepath.Join(dir, "inspection-summary.txt"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "Number of namespaces inspected: 2") {
		t.Error("expected namespace count")
	}
	if !strings.Contains(content, "ns1") || !strings.Contains(content, "ns2") {
		t.Error("expected namespace names")
	}
	if !strings.Contains(content, "Secrets (excluded") {
		t.Error("expected secrets excluded note")
	}
}

func TestWriteSummary_WithSecrets(t *testing.T) {
	dir := t.TempDir()
	n := &NamespaceInspect{}
	cfg := &Config{WithSecrets: true, SinceTime: "2024-01-01T00:00:00Z"}
	n.writeSummary(cfg, dir, []string{"ns1"})

	data, err := os.ReadFile(filepath.Join(dir, "inspection-summary.txt"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "Secrets (included") {
		t.Error("expected secrets included note")
	}
	if !strings.Contains(content, "since-time: 2024-01-01T00:00:00Z") {
		t.Error("expected since-time value")
	}
}

func TestDetectStandaloneNamespaces(t *testing.T) {
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "backstage-rhdh",
			Namespace: "rhdh-ns",
			Labels: map[string]string{
				"app.kubernetes.io/managed-by": "Helm",
				"app.kubernetes.io/name":       "backstage",
			},
		},
	}
	unrelatedDep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "nginx",
			Namespace: "other-ns",
			Labels: map[string]string{
				"app.kubernetes.io/managed-by": "Helm",
				"app.kubernetes.io/name":       "nginx",
			},
		},
	}

	cfg := newTestConfig(t, t.TempDir(), withTypedObjs(dep, unrelatedDep))
	n := &NamespaceInspect{}
	nsSet := make(map[string]struct{})
	n.detectStandaloneNamespaces(context.Background(), cfg, nsSet)

	if _, ok := nsSet["rhdh-ns"]; !ok {
		t.Error("expected rhdh-ns to be detected")
	}
	if _, ok := nsSet["other-ns"]; ok {
		t.Error("expected other-ns to not be detected")
	}
}

func TestDetectOperatorNamespaces(t *testing.T) {
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "rhdh-operator-controller",
			Namespace: "rhdh-operator",
			Labels:    map[string]string{"app": "rhdh-operator"},
		},
	}

	cfg := newTestConfig(t, t.TempDir(), withTypedObjs(dep))
	n := &NamespaceInspect{}
	nsSet := make(map[string]struct{})
	n.detectOperatorNamespaces(context.Background(), cfg, nsSet)

	if _, ok := nsSet["rhdh-operator"]; !ok {
		t.Error("expected rhdh-operator namespace to be detected")
	}
}

func TestDetectCRNamespaces(t *testing.T) {
	backstageGVR := schema.GroupVersionResource{
		Group: "rhdh.redhat.com", Version: "v1alpha5", Resource: "backstages",
	}
	cr := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "rhdh.redhat.com/v1alpha5",
			"kind":       "Backstage",
			"metadata": map[string]any{
				"name":      "my-backstage",
				"namespace": "backstage-ns",
			},
		},
	}

	cfg := newTestConfig(t, t.TempDir(),
		withAPIGroups("rhdh.redhat.com/v1alpha5"),
		withDynamicObjs(
			map[schema.GroupVersionResource]string{backstageGVR: "BackstageList"},
			cr,
		),
	)
	n := &NamespaceInspect{}
	nsSet := make(map[string]struct{})
	n.detectCRNamespaces(context.Background(), cfg, nsSet)

	if _, ok := nsSet["backstage-ns"]; !ok {
		t.Error("expected backstage-ns to be detected from CR")
	}
}

func TestAddOrchestratorNamespaces(t *testing.T) {
	dir := t.TempDir()
	orchDir := filepath.Join(dir, "orchestrator")
	_ = os.MkdirAll(orchDir, 0o755)
	_ = os.WriteFile(filepath.Join(orchDir, "detected-namespaces.txt"),
		[]byte("orch-ns1\norch-ns2\n"), 0o644)

	cfg := &Config{BasePath: dir}
	n := &NamespaceInspect{}
	nsSet := make(map[string]struct{})
	n.addOrchestratorNamespaces(cfg, nsSet)

	if _, ok := nsSet["orch-ns1"]; !ok {
		t.Error("expected orch-ns1")
	}
	if _, ok := nsSet["orch-ns2"]; !ok {
		t.Error("expected orch-ns2")
	}
}

func TestAddOrchestratorNamespaces_NoFile(t *testing.T) {
	cfg := &Config{BasePath: t.TempDir()}
	n := &NamespaceInspect{}
	nsSet := make(map[string]struct{})
	n.addOrchestratorNamespaces(cfg, nsSet)

	if len(nsSet) != 0 {
		t.Errorf("expected empty set when file doesn't exist, got %v", nsSet)
	}
}

func TestResolveNamespaces_Targeted(t *testing.T) {
	cfg := newTestConfig(t, t.TempDir())
	cfg.TargetNamespaces = []string{"ns1", "ns2"}

	n := &NamespaceInspect{}
	namespaces := n.resolveNamespaces(context.Background(), cfg)

	if len(namespaces) != 2 || namespaces[0] != "ns1" || namespaces[1] != "ns2" {
		t.Errorf("got %v, want [ns1 ns2]", namespaces)
	}
}

func TestRedirectKlog(t *testing.T) {
	dir := t.TempDir()
	_, cleanup := redirectKlog(dir)
	defer cleanup()

	logPath := filepath.Join(dir, "inspect.log")
	if _, err := os.Stat(logPath); err != nil {
		t.Error("expected inspect.log to be created")
	}
}

func TestRunInspectCmd_Success(t *testing.T) {
	cmd := &cobra.Command{
		Use: "test",
		RunE: func(cmd *cobra.Command, args []string) error {
			return nil
		},
	}
	if err := runInspectCmd(cmd); err != nil {
		t.Errorf("expected nil error, got %v", err)
	}
}

func TestRunInspectCmd_FatalRecovery(t *testing.T) {
	cmd := &cobra.Command{
		Use: "test",
		RunE: func(cmd *cobra.Command, args []string) error {
			kcmdutil.CheckErr(fmt.Errorf("simulated fatal"))
			return nil
		},
	}
	err := runInspectCmd(cmd)
	if err == nil {
		t.Fatal("expected error from fatal recovery")
	}
	if !strings.Contains(err.Error(), "simulated fatal") {
		t.Errorf("error = %q, expected to contain 'simulated fatal'", err.Error())
	}
}

func TestInspectFatalError(t *testing.T) {
	e := inspectFatalError("test error message")
	if e.Error() != "test error message" {
		t.Errorf("Error() = %q, want 'test error message'", e.Error())
	}
}

func TestDetectHelmNamespaces_NoConfig(t *testing.T) {
	cfg := newTestConfig(t, t.TempDir())
	n := &NamespaceInspect{}
	nsSet := make(map[string]struct{})

	// With a fake rest.Config, newHelmActionConfig fails gracefully
	n.detectHelmNamespaces(context.Background(), cfg, nsSet)

	if len(nsSet) != 0 {
		t.Errorf("expected empty nsSet, got %v", nsSet)
	}
}

func TestDetectHelmNamespaces_WithRHDHRelease(t *testing.T) {
	rel := makeTestRelease("rhdh", "rhdh-ns", 1, "backstage", "1.5.0", "1.4.0", "")

	store := storage.Init(driver.NewMemory())
	_ = store.Create(rel)
	actionCfg := &action.Configuration{Releases: store, KubeClient: &kubefake.PrintingKubeClient{Out: io.Discard}}

	cfg := newTestConfig(t, t.TempDir())
	cfg.HelmConfigFactory = func(_ string) (*action.Configuration, error) {
		return actionCfg, nil
	}

	n := &NamespaceInspect{}
	nsSet := make(map[string]struct{})
	n.detectHelmNamespaces(context.Background(), cfg, nsSet)

	if _, ok := nsSet["rhdh-ns"]; !ok {
		t.Error("expected rhdh-ns to be detected from RHDH Helm release")
	}
}

func TestDetectHelmNamespaces_NonRHDHRelease(t *testing.T) {
	rel := makeTestRelease("nginx", "default", 1, "nginx", "1.0.0", "1.0.0", "")

	store := storage.Init(driver.NewMemory())
	_ = store.Create(rel)
	actionCfg := &action.Configuration{Releases: store, KubeClient: &kubefake.PrintingKubeClient{Out: io.Discard}}

	cfg := newTestConfig(t, t.TempDir())
	cfg.HelmConfigFactory = func(_ string) (*action.Configuration, error) {
		return actionCfg, nil
	}

	n := &NamespaceInspect{}
	nsSet := make(map[string]struct{})
	n.detectHelmNamespaces(context.Background(), cfg, nsSet)

	if len(nsSet) != 0 {
		t.Errorf("expected empty nsSet for non-RHDH release, got %v", nsSet)
	}
}

