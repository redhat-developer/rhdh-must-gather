package obfuscate

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/rest"

	"github.com/redhat-developer/rhdh-must-gather/internal/kube"
)

func TestDiscoverFromClusterAndEnv(t *testing.T) {
	dns := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "config.openshift.io/v1",
		"kind":       "DNS",
		"metadata":   map[string]any{"name": "cluster"},
		"spec":       map[string]any{"baseDomain": "cluster.example.com"},
	}}
	ing := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "operator.openshift.io/v1",
		"kind":       "IngressController",
		"metadata": map[string]any{
			"name":      "default",
			"namespace": "openshift-ingress-operator",
		},
		"status": map[string]any{"domain": "apps.cluster.example.com"},
	}}
	scheme := runtime.NewScheme()
	listKinds := map[schema.GroupVersionResource]string{
		{Group: "config.openshift.io", Version: "v1", Resource: "dnses"}:                "DNSList",
		{Group: "operator.openshift.io", Version: "v1", Resource: "ingresscontrollers"}: "IngressControllerList",
	}
	client := &kube.Client{
		Dynamic: dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme, listKinds, dns, ing),
		Config:  &rest.Config{Host: "https://api.cluster.example.com:6443"},
	}
	t.Setenv(EnvDomains, "extra.example.com, kubernetes.default.svc.cluster.local")

	got := Discover(context.Background(), client, []string{"rhdh"})
	want := []string{
		"cluster.example.com",
		"apps.cluster.example.com",
		"api.cluster.example.com",
		"extra.example.com",
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("Discover() = %#v, want %#v", got, want)
	}
}

func TestDiscoverEnvOnlyWhenClientMissing(t *testing.T) {
	t.Setenv(EnvDomains, "customer.example.com, 127.0.0.1")
	got := Discover(context.Background(), nil, nil)
	if len(got) != 1 || got[0] != "customer.example.com" {
		t.Fatalf("Discover() = %#v", got)
	}
}

func TestDiscoverUsesIngressAndRouteHostsWithoutOpenShiftDomains(t *testing.T) {
	ingress := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "networking.k8s.io/v1",
		"kind":       "Ingress",
		"metadata":   map[string]any{"name": "console", "namespace": "rhdh"},
		"spec": map[string]any{
			"rules": []any{map[string]any{"host": "console.example.com"}},
			"tls":   []any{map[string]any{"hosts": []any{"api.example.com"}}},
		},
	}}
	other := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "networking.k8s.io/v1",
		"kind":       "Ingress",
		"metadata":   map[string]any{"name": "other", "namespace": "kube-system"},
		"spec":       map[string]any{"rules": []any{map[string]any{"host": "other.example.net"}}},
	}}
	route := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "route.openshift.io/v1",
		"kind":       "Route",
		"metadata":   map[string]any{"name": "backstage", "namespace": "rhdh"},
		"spec":       map[string]any{"host": "backstage.example.com"},
	}}
	scheme := runtime.NewScheme()
	listKinds := map[schema.GroupVersionResource]string{
		{Group: "networking.k8s.io", Version: "v1", Resource: "ingresses"}: "IngressList",
		{Group: "route.openshift.io", Version: "v1", Resource: "routes"}:   "RouteList",
	}
	client := &kube.Client{
		Dynamic: dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme, listKinds, ingress, other, route),
		Config:  &rest.Config{Host: "https://k8s.example.com:6443"},
	}
	t.Setenv(EnvDomains, "")

	got := Discover(context.Background(), client, []string{"rhdh"})
	want := []string{
		"console.example.com",
		"api.example.com",
		"backstage.example.com",
		"k8s.example.com",
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("Discover() = %#v, want %#v", got, want)
	}
}

func TestDiscoverKeepsOpenShiftDomainsAheadOfIngressHosts(t *testing.T) {
	dns := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "config.openshift.io/v1",
		"kind":       "DNS",
		"metadata":   map[string]any{"name": "cluster"},
		"spec":       map[string]any{"baseDomain": "cluster.example.com"},
	}}
	ingress := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "networking.k8s.io/v1",
		"kind":       "Ingress",
		"metadata":   map[string]any{"name": "console", "namespace": "rhdh"},
		"spec":       map[string]any{"rules": []any{map[string]any{"host": "other.example.net"}}},
	}}
	scheme := runtime.NewScheme()
	listKinds := map[schema.GroupVersionResource]string{
		{Group: "config.openshift.io", Version: "v1", Resource: "dnses"}:   "DNSList",
		{Group: "networking.k8s.io", Version: "v1", Resource: "ingresses"}: "IngressList",
	}
	client := &kube.Client{
		Dynamic: dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme, listKinds, dns, ingress),
		Config:  &rest.Config{Host: "https://api.cluster.example.com:6443"},
	}
	t.Setenv(EnvDomains, "")

	got := Discover(context.Background(), client, []string{"rhdh"})
	for _, name := range got {
		if name == "other.example.net" {
			t.Fatalf("OpenShift discovery should not add individual Ingress hosts: %#v", got)
		}
	}
	if strings.Join(got, ",") != "cluster.example.com,api.cluster.example.com" {
		t.Fatalf("Discover() = %#v", got)
	}
}

func TestApplyObfuscatesWithoutDroppingResourcesOrReport(t *testing.T) {
	dir := t.TempDir()
	ip := "10.20.30.40"
	mac := "52:54:00:12:34:56"
	mustWrite(t, filepath.Join(dir, "resources", "app-config.yaml"), ""+
		"apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: app-config\n"+
		"data:\n  endpoint: http://"+ip+":8080\n  host: logs.apps.example.com\n")
	mustWrite(t, filepath.Join(dir, "resources", "my-secret.yaml"), ""+
		"apiVersion: v1\nkind: Secret\nmetadata:\n  name: my-secret\ndata:\n  password: \"[REDACTED]\"\n")
	mustWrite(t, filepath.Join(dir, "nodes", ip, "kubelet.log"),
		"connected to "+ip+" from "+ip+" via 127.0.0.1 mac "+mac+" host logs.apps.example.com\n")

	if err := Apply(dir, []string{"example.com"}, func(config, input, output, report string) error {
		// workers=1 avoids a data race in must-gather-clean's NoopOmitter
		return Clean(config, input, output, report, 1)
	}); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}

	if _, err := os.Stat(filepath.Join(dir, stagingDirName)); !os.IsNotExist(err) {
		t.Fatalf("staging directory still present: %v", err)
	}
	assertNoReversibleReport(t, dir)

	cm := mustRead(t, filepath.Join(dir, "resources", "app-config.yaml"))
	if !strings.Contains(cm, "kind: ConfigMap") || !strings.Contains(cm, "name: app-config") {
		t.Fatalf("ConfigMap was dropped or rewritten:\n%s", cm)
	}
	secret := mustRead(t, filepath.Join(dir, "resources", "my-secret.yaml"))
	if !strings.Contains(secret, "kind: Secret") || !strings.Contains(secret, "name: my-secret") {
		t.Fatalf("Secret was dropped or rewritten:\n%s", secret)
	}
	if strings.Contains(secret, "report.yaml") {
		t.Fatalf("secret file mentions report.yaml")
	}

	logPath := findFile(t, dir, "kubelet.log")
	body := mustRead(t, logPath)
	if strings.Contains(body, ip) || strings.Contains(logPath, ip) {
		t.Fatalf("IP %s still present in %s or %s", ip, logPath, body)
	}
	if !strings.Contains(body, "127.0.0.1") {
		t.Fatalf("loopback address should be preserved:\n%s", body)
	}
	if strings.Contains(body, mac) || strings.Contains(strings.ToLower(body), "example.com") {
		t.Fatalf("MAC or domain still present:\n%s", body)
	}
	token := ipToken(body)
	if token == "" {
		t.Fatalf("expected consistent IPv4 token in log:\n%s", body)
	}
	if !strings.Contains(logPath, token) {
		t.Fatalf("path %s does not use the same IP token %s as the log", logPath, token)
	}
	if strings.Count(body, token) < 2 {
		t.Fatalf("IP token %s was not reused for both occurrences:\n%s", token, body)
	}
	if strings.Contains(cm, ip) || strings.Contains(strings.ToLower(cm), "example.com") {
		t.Fatalf("ConfigMap still contains the IP or domain:\n%s", cm)
	}
}

func TestApplyFailureKeepsOriginalTree(t *testing.T) {
	dir := t.TempDir()
	original := "connected to 10.20.30.40\n"
	path := filepath.Join(dir, "nodes", "10.20.30.40", "kubelet.log")
	mustWrite(t, path, original)

	err := Apply(dir, []string{"example.com"}, func(string, string, string, string) error {
		return errors.New("clean failed")
	})
	if err == nil {
		t.Fatal("Apply() error = nil, want failure")
	}
	if got := mustRead(t, path); got != original {
		t.Fatalf("original file changed:\n%s", got)
	}
	if _, statErr := os.Stat(filepath.Join(dir, stagingDirName)); !os.IsNotExist(statErr) {
		t.Fatalf("staging directory left behind: %v", statErr)
	}
	assertNoReversibleReport(t, dir)
}

func TestApplyKeepsResourceNamedReport(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "_configmaps", "report.yaml"), ""+
		"apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: report\ndata:\n  note: keep me\n")

	if err := Apply(dir, nil, func(config, input, output, report string) error {
		return Clean(config, input, output, report, 1)
	}); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}

	body := mustRead(t, filepath.Join(dir, "_configmaps", "report.yaml"))
	if !strings.Contains(body, "kind: ConfigMap") || !strings.Contains(body, "name: report") {
		t.Fatalf("resource named report was dropped:\n%s", body)
	}
	assertNoReversibleReport(t, dir)
}

func assertNoReversibleReport(t *testing.T, root string) {
	t.Helper()
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		body, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		if strings.Contains(string(body), "replacedWith:") {
			t.Errorf("published tree contains the reversible report %s", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func findFile(t *testing.T, root, name string) string {
	t.Helper()
	var found string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && d.Name() == name {
			found = path
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if found == "" {
		t.Fatalf("file %s not found under %s", name, root)
	}
	return found
}

func ipToken(body string) string {
	const prefix = "x-ipv4-"
	i := strings.Index(body, prefix)
	if i < 0 {
		return ""
	}
	rest := body[i:]
	j := strings.Index(rest, "-x")
	if j < 0 {
		return ""
	}
	return rest[:j+2]
}

func mustWrite(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func mustRead(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
