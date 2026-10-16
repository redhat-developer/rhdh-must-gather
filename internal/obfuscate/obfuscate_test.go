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
	fakediscovery "k8s.io/client-go/discovery/fake"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	fakeclientset "k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/rest"
	ktesting "k8s.io/client-go/testing"

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

	assertNoWorkDir(t, dir)
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
	assertNoReversibleReport(t, dir)
}

func TestApplyFailureRecoverOmittedFiles(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "logs", "app.log"), "node 10.20.30.40\n")
	mustWrite(t, filepath.Join(dir, "heap-dumps", "pod=x", "container=c", "heap.heapsnapshot"), "binary-data")

	err := Apply(dir, nil, func(string, string, string, string) error {
		return errors.New("clean failed")
	})
	if err == nil {
		t.Fatal("Apply() error = nil, want failure")
	}
	if got := mustRead(t, filepath.Join(dir, "logs", "app.log")); got != "node 10.20.30.40\n" {
		t.Errorf("log = %q, want original", got)
	}
	if got := mustRead(t, filepath.Join(dir, "heap-dumps", "pod=x", "container=c", "heap.heapsnapshot")); got != "binary-data" {
		t.Errorf("heapsnapshot = %q, want 'binary-data' — omitted files were not recovered", got)
	}
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

func assertNoWorkDir(t *testing.T, root string) {
	t.Helper()
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".obfuscate-") {
			t.Fatalf("obfuscation work directory left behind: %s", e.Name())
		}
	}
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

func TestRun_WithFakeClient(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "kubelet.log"), "node 10.9.8.7\n")

	dns := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "config.openshift.io/v1",
		"kind":       "DNS",
		"metadata":   map[string]any{"name": "cluster"},
		"spec":       map[string]any{"baseDomain": "cluster.example.com"},
	}}
	scheme := runtime.NewScheme()
	listKinds := map[schema.GroupVersionResource]string{
		{Group: "config.openshift.io", Version: "v1", Resource: "dnses"}: "DNSList",
	}
	client := &kube.Client{
		Dynamic: dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme, listKinds, dns),
		Config:  &rest.Config{Host: "https://api.cluster.example.com:6443"},
	}

	err := Run(context.Background(), client, dir, nil, func(config, input, output, report string) error {
		return Clean(config, input, output, report, 1)
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	body := mustRead(t, filepath.Join(dir, "kubelet.log"))
	if strings.Contains(body, "10.9.8.7") {
		t.Fatalf("IP was not obfuscated: %s", body)
	}
}

func TestRun_NilContext(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "data.log"), "hello\n")

	//nolint:staticcheck // testing nil ctx path
	err := Run(nil, nil, dir, nil, func(config, input, output, report string) error {
		return Clean(config, input, output, report, 1)
	})
	if err != nil {
		t.Fatalf("Run(nil ctx) error = %v", err)
	}
}

func TestApply_NilClean(t *testing.T) {
	dir := t.TempDir()
	err := Apply(dir, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "not configured") {
		t.Fatalf("Apply(nil clean) error = %v, want 'not configured'", err)
	}
}

func TestApply_NotADirectory(t *testing.T) {
	f := filepath.Join(t.TempDir(), "file.txt")
	mustWrite(t, f, "data")
	err := Apply(f, nil, func(string, string, string, string) error { return nil })
	if err == nil || !strings.Contains(err.Error(), "not a directory") {
		t.Fatalf("Apply(file) error = %v, want 'not a directory'", err)
	}
}

func TestApply_NonexistentPath(t *testing.T) {
	err := Apply("/nonexistent/path/xyz", nil, func(string, string, string, string) error { return nil })
	if err == nil || !strings.Contains(err.Error(), "obfuscation input") {
		t.Fatalf("Apply(nonexistent) error = %v, want 'obfuscation input'", err)
	}
}

func TestClean_WorkersClamped(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "data.log"), "hello 10.0.0.1\n")
	output := filepath.Join(t.TempDir(), "out")
	report := t.TempDir()

	config := filepath.Join(t.TempDir(), "config.yaml")
	mustWrite(t, config, "config:\n  obfuscate:\n    - type: IP\n      replacementType: Consistent\n      target: All\n")

	err := Clean(config, dir, output, report, 0)
	if err != nil {
		t.Fatalf("Clean(workers=0) error = %v", err)
	}
	if _, err := os.Stat(output); err != nil {
		t.Fatalf("output not created: %v", err)
	}
}

func TestApiServerHost(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{"empty", "", ""},
		{"whitespace", "  ", ""},
		{"ip bare", "10.0.0.1", ""},
		{"ip with scheme", "https://10.0.0.1:6443", ""},
		{"hostname with scheme", "https://api.example.com:6443", "api.example.com"},
		{"hostname no scheme", "api.example.com:6443", "api.example.com"},
		{"hostname no port", "api.example.com", "api.example.com"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := apiServerHost(tt.raw)
			if got != tt.want {
				t.Errorf("apiServerHost(%q) = %q, want %q", tt.raw, got, tt.want)
			}
		})
	}
}

func TestUsableDomain(t *testing.T) {
	tests := []struct {
		domain string
		want   bool
	}{
		{"example.com", true},
		{"sub.example.com", true},
		{"", false},
		{"nodot", false},
		{"foo bar.com", false},
		{"foo/bar.com", false},
		{"foo\\bar.com", false},
		{"192.168.1.1", false},
		{"cluster.local", false},
		{"svc.cluster.local", false},
		{"kubernetes.default.svc", false},
		{"kubernetes.default.svc.cluster.local", false},
		{"myapp.ns.svc", false},
		{"myapp.ns.svc.cluster.local", false},
		{"localhost", false},
	}
	for _, tt := range tests {
		t.Run(tt.domain, func(t *testing.T) {
			got := usableDomain(tt.domain)
			if got != tt.want {
				t.Errorf("usableDomain(%q) = %v, want %v", tt.domain, got, tt.want)
			}
		})
	}
}

func TestListNamespaced_SpecificNamespaceError(t *testing.T) {
	scheme := runtime.NewScheme()
	gvr := schema.GroupVersionResource{Group: "networking.k8s.io", Version: "v1", Resource: "ingresses"}
	dynClient := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme, map[schema.GroupVersionResource]string{
		gvr: "IngressList",
	})
	dynClient.PrependReactor("list", "ingresses", func(action ktesting.Action) (bool, runtime.Object, error) {
		return true, nil, errors.New("forbidden")
	})
	client := &kube.Client{Dynamic: dynClient}

	got := listNamespaced(context.Background(), client, gvr, []string{"ns1"}, "Ingress")
	if len(got) != 0 {
		t.Fatalf("expected empty result on error, got %d items", len(got))
	}
}

func TestListNamespaced_AllNamespacesError(t *testing.T) {
	scheme := runtime.NewScheme()
	gvr := schema.GroupVersionResource{Group: "networking.k8s.io", Version: "v1", Resource: "ingresses"}
	dynClient := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme, map[schema.GroupVersionResource]string{
		gvr: "IngressList",
	})
	dynClient.PrependReactor("list", "ingresses", func(action ktesting.Action) (bool, runtime.Object, error) {
		return true, nil, errors.New("forbidden")
	})
	client := &kube.Client{Dynamic: dynClient}

	got := listNamespaced(context.Background(), client, gvr, nil, "Ingress")
	if len(got) != 0 {
		t.Fatalf("expected empty result on error, got %d items", len(got))
	}
}

func TestApiGroupPresent_NilDiscovery(t *testing.T) {
	client := &kube.Client{Discovery: nil}
	present, err := apiGroupPresent(client, "anything")
	if err != nil {
		t.Fatalf("apiGroupPresent() error = %v", err)
	}
	if !present {
		t.Fatal("expected true when Discovery is nil")
	}
}

func TestApiGroupPresent_WithDiscovery(t *testing.T) {
	fakeClient := fakeclientset.NewSimpleClientset()
	fd := fakeClient.Discovery().(*fakediscovery.FakeDiscovery)
	client := &kube.Client{Discovery: fd}

	present, err := apiGroupPresent(client, "nonexistent.group.io")
	if err != nil {
		t.Fatalf("apiGroupPresent() error = %v", err)
	}
	if present {
		t.Fatal("expected false for nonexistent group")
	}
}


func TestRelocateAndRestore(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "file.txt"), "hello")
	mustWrite(t, filepath.Join(dir, "sub", "nested.txt"), "world")
	skipDir := ".work"
	if err := os.Mkdir(filepath.Join(dir, skipDir), 0o755); err != nil {
		t.Fatal(err)
	}

	dst := filepath.Join(dir, skipDir, "input")
	if err := os.Mkdir(dst, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := relocate(dir, dst, skipDir); err != nil {
		t.Fatalf("relocate() error = %v", err)
	}

	if got := mustRead(t, filepath.Join(dst, "file.txt")); got != "hello" {
		t.Errorf("file.txt = %q, want hello", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "file.txt")); !os.IsNotExist(err) {
		t.Fatal("file.txt should have been moved out of dir")
	}
	if _, err := os.Stat(filepath.Join(dir, skipDir)); err != nil {
		t.Fatal("skip directory should still be in dir")
	}

	restoreInput(dst, dir)
	if got := mustRead(t, filepath.Join(dir, "file.txt")); got != "hello" {
		t.Errorf("restored file.txt = %q, want hello", got)
	}
}

func TestExtractOmittedFiles(t *testing.T) {
	input := t.TempDir()
	mustWrite(t, filepath.Join(input, "helm", "heap-dumps", "pod=x", "container=c", "heap.heapsnapshot"), "snapshot-data")
	mustWrite(t, filepath.Join(input, "helm", "heap-dumps", "pod=x", "container=c", "heap.fallback.heapsnapshot"), "fallback-data")
	mustWrite(t, filepath.Join(input, "operator", "heap-dumps", "pod=y", "container=c", "dump.PARTIAL.heapsnapshot"), "partial-data")
	mustWrite(t, filepath.Join(input, "logs", "app.log"), "keep me")

	omitted := t.TempDir()
	if err := extractOmittedFiles(input, omitted); err != nil {
		t.Fatalf("extractOmittedFiles() error = %v", err)
	}

	// Heap snapshots should be moved to omitted dir
	if got := mustRead(t, filepath.Join(omitted, "helm", "heap-dumps", "pod=x", "container=c", "heap.heapsnapshot")); got != "snapshot-data" {
		t.Errorf("heapsnapshot = %q, want 'snapshot-data'", got)
	}
	if got := mustRead(t, filepath.Join(omitted, "helm", "heap-dumps", "pod=x", "container=c", "heap.fallback.heapsnapshot")); got != "fallback-data" {
		t.Errorf("fallback = %q, want 'fallback-data'", got)
	}
	if got := mustRead(t, filepath.Join(omitted, "operator", "heap-dumps", "pod=y", "container=c", "dump.PARTIAL.heapsnapshot")); got != "partial-data" {
		t.Errorf("partial = %q, want 'partial-data'", got)
	}

	// Heap snapshots should no longer exist in input
	if _, err := os.Stat(filepath.Join(input, "helm", "heap-dumps", "pod=x", "container=c", "heap.heapsnapshot")); err == nil {
		t.Error("heapsnapshot should have been removed from input")
	}

	// Non-matching files should remain in input
	if got := mustRead(t, filepath.Join(input, "logs", "app.log")); got != "keep me" {
		t.Errorf("app.log = %q, want 'keep me'", got)
	}
}

func TestExtractOmittedFiles_NoHeapSnapshots(t *testing.T) {
	input := t.TempDir()
	mustWrite(t, filepath.Join(input, "logs", "app.log"), "log data")

	omitted := t.TempDir()
	if err := extractOmittedFiles(input, omitted); err != nil {
		t.Fatalf("extractOmittedFiles() error = %v", err)
	}
	// Input should be unchanged
	if got := mustRead(t, filepath.Join(input, "logs", "app.log")); got != "log data" {
		t.Errorf("app.log = %q, want 'log data'", got)
	}
}

func TestExtractOmittedFiles_SkipsSymlinks(t *testing.T) {
	input := t.TempDir()
	snapshotDir := filepath.Join(input, "heap-dumps", "pod=x", "container=c")
	mustWrite(t, filepath.Join(snapshotDir, "real.heapsnapshot"), "real-data")
	if err := os.Symlink("real.heapsnapshot", filepath.Join(snapshotDir, "link.heapsnapshot")); err != nil {
		t.Skipf("symlinks not supported: %v", err)
	}

	omitted := t.TempDir()
	if err := extractOmittedFiles(input, omitted); err != nil {
		t.Fatalf("extractOmittedFiles() error = %v", err)
	}

	// Only regular files should be extracted
	if got := mustRead(t, filepath.Join(omitted, "heap-dumps", "pod=x", "container=c", "real.heapsnapshot")); got != "real-data" {
		t.Errorf("real snapshot = %q, want 'real-data'", got)
	}
	if _, err := os.Lstat(filepath.Join(omitted, "heap-dumps", "pod=x", "container=c", "link.heapsnapshot")); err == nil {
		t.Error("symlink should not have been extracted")
	}
}

func TestRestoreOmittedFiles(t *testing.T) {
	omitted := t.TempDir()
	mustWrite(t, filepath.Join(omitted, "helm", "heap-dumps", "pod=x", "container=c", "heap.heapsnapshot"), "snapshot-data")

	output := t.TempDir()
	mustWrite(t, filepath.Join(output, "logs", "app.log"), "obfuscated")

	if err := restoreOmittedFiles(omitted, output); err != nil {
		t.Fatalf("restoreOmittedFiles() error = %v", err)
	}

	if got := mustRead(t, filepath.Join(output, "helm", "heap-dumps", "pod=x", "container=c", "heap.heapsnapshot")); got != "snapshot-data" {
		t.Errorf("heapsnapshot = %q, want 'snapshot-data'", got)
	}
	// Original output files should still be there
	if got := mustRead(t, filepath.Join(output, "logs", "app.log")); got != "obfuscated" {
		t.Errorf("app.log = %q, want 'obfuscated'", got)
	}
}

func TestApplyPreservesHeapSnapshots(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "logs", "app.log"), "node 10.20.30.40\n")
	dumpDir := filepath.Join(dir, "heap-dumps", "pod=x", "container=c")
	mustWrite(t, filepath.Join(dumpDir, "heap.heapsnapshot"), "v8-snapshot-binary-data")
	mustWrite(t, filepath.Join(dumpDir, "metadata.json"), "ip 10.20.30.40\n")

	if err := Apply(dir, nil, func(config, input, output, report string) error {
		return Clean(config, input, output, report, 1)
	}); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}

	// Heap snapshot should be preserved with original content (not obfuscated)
	got := mustRead(t, filepath.Join(dir, "heap-dumps", "pod=x", "container=c", "heap.heapsnapshot"))
	if got != "v8-snapshot-binary-data" {
		t.Errorf("heapsnapshot = %q, want 'v8-snapshot-binary-data'", got)
	}
	// Sibling file in same dir should be obfuscated
	meta := mustRead(t, filepath.Join(dir, "heap-dumps", "pod=x", "container=c", "metadata.json"))
	if strings.Contains(meta, "10.20.30.40") {
		t.Errorf("IP still present in metadata.json: %s", meta)
	}
	// IP in log should be obfuscated
	logBody := mustRead(t, filepath.Join(dir, "logs", "app.log"))
	if strings.Contains(logBody, "10.20.30.40") {
		t.Errorf("IP still present in log: %s", logBody)
	}
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
