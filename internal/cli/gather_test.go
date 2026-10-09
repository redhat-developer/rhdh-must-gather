package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	fakediscovery "k8s.io/client-go/discovery/fake"
	fakedynamic "k8s.io/client-go/dynamic/fake"
	fakeclientset "k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/rest"

	"github.com/redhat-developer/rhdh-must-gather/internal/kube"
)

func TestGetEnvDefault(t *testing.T) {
	t.Run("returns env value when set", func(t *testing.T) {
		t.Setenv("TEST_GET_ENV_DEFAULT", "custom")
		if got := getEnvDefault("TEST_GET_ENV_DEFAULT", "fallback"); got != "custom" {
			t.Errorf("getEnvDefault() = %q, want %q", got, "custom")
		}
	})

	t.Run("returns fallback when unset", func(t *testing.T) {
		t.Setenv("TEST_GET_ENV_DEFAULT", "")
		if got := getEnvDefault("TEST_GET_ENV_DEFAULT", "fallback"); got != "fallback" {
			t.Errorf("getEnvDefault() = %q, want %q", got, "fallback")
		}
	})
}

func TestResolveSince_CLIFlagPrecedence(t *testing.T) {
	t.Setenv("MUST_GATHER_SINCE", "10h")
	t.Setenv("MUST_GATHER_SINCE_TIME", "2024-01-01T00:00:00Z")

	d, st := resolveSince(&gatherOptions{since: "5m"})
	if d != 5*time.Minute {
		t.Errorf("since = %v, want 5m", d)
	}
	if st != "" {
		t.Errorf("sinceTime = %q, want empty (CLI flag group takes precedence)", st)
	}
}

func TestResolveSince_CLISinceTimePrecedence(t *testing.T) {
	t.Setenv("MUST_GATHER_SINCE", "10h")

	d, st := resolveSince(&gatherOptions{sinceTime: "2024-06-15T12:00:00Z"})
	if d != 0 {
		t.Errorf("since = %v, want 0", d)
	}
	if st != "2024-06-15T12:00:00Z" {
		t.Errorf("sinceTime = %q, want 2024-06-15T12:00:00Z", st)
	}
}

func TestResolveSince_EnvFallback(t *testing.T) {
	t.Setenv("MUST_GATHER_SINCE", "2h")
	t.Setenv("MUST_GATHER_SINCE_TIME", "")

	d, st := resolveSince(&gatherOptions{})
	if d != 2*time.Hour {
		t.Errorf("since = %v, want 2h", d)
	}
	if st != "" {
		t.Errorf("sinceTime = %q, want empty", st)
	}
}

func TestResolveSince_EnvSinceTimeFallback(t *testing.T) {
	t.Setenv("MUST_GATHER_SINCE", "")
	t.Setenv("MUST_GATHER_SINCE_TIME", "2024-01-01T00:00:00Z")

	d, st := resolveSince(&gatherOptions{})
	if d != 0 {
		t.Errorf("since = %v, want 0", d)
	}
	if st != "2024-01-01T00:00:00Z" {
		t.Errorf("sinceTime = %q, want 2024-01-01T00:00:00Z", st)
	}
}

func TestResolveSince_BothEnvVarsConflict(t *testing.T) {
	t.Setenv("MUST_GATHER_SINCE", "1h")
	t.Setenv("MUST_GATHER_SINCE_TIME", "2024-01-01T00:00:00Z")

	d, st := resolveSince(&gatherOptions{})
	if d != time.Hour {
		t.Errorf("since = %v, want 1h (should win over since-time)", d)
	}
	if st != "" {
		t.Errorf("sinceTime = %q, want empty (since should take precedence)", st)
	}
}

func TestResolveSince_InvalidEnvIgnored(t *testing.T) {
	t.Setenv("MUST_GATHER_SINCE", "not-a-duration")
	t.Setenv("MUST_GATHER_SINCE_TIME", "")

	d, st := resolveSince(&gatherOptions{})
	if d != 0 {
		t.Errorf("since = %v, want 0 (invalid should be ignored)", d)
	}
	if st != "" {
		t.Errorf("sinceTime = %q, want empty", st)
	}
}

func TestResolveSince_NegativeDurationIgnored(t *testing.T) {
	t.Setenv("MUST_GATHER_SINCE", "-1h")
	t.Setenv("MUST_GATHER_SINCE_TIME", "")

	d, _ := resolveSince(&gatherOptions{})
	if d != 0 {
		t.Errorf("since = %v, want 0 (negative should be ignored)", d)
	}
}

func TestResolveSince_SubSecondIgnored(t *testing.T) {
	t.Setenv("MUST_GATHER_SINCE", "500ms")
	t.Setenv("MUST_GATHER_SINCE_TIME", "")

	d, _ := resolveSince(&gatherOptions{})
	if d != 0 {
		t.Errorf("since = %v, want 0 (sub-second should be ignored)", d)
	}
}

func TestResolveNamespaces_CLIFlag(t *testing.T) {
	t.Setenv("RHDH_TARGET_NAMESPACES", "env-ns")
	got := resolveNamespaces(&gatherOptions{namespaces: "ns1,ns2"})
	if len(got) != 2 || got[0] != "ns1" || got[1] != "ns2" {
		t.Errorf("resolveNamespaces() = %v, want [ns1 ns2]", got)
	}
}

func TestResolveNamespaces_EnvFallback(t *testing.T) {
	t.Setenv("RHDH_TARGET_NAMESPACES", "env-ns1, env-ns2")
	got := resolveNamespaces(&gatherOptions{})
	if len(got) != 2 || got[0] != "env-ns1" || got[1] != "env-ns2" {
		t.Errorf("resolveNamespaces() = %v, want [env-ns1 env-ns2]", got)
	}
}

func TestResolveNamespaces_Empty(t *testing.T) {
	t.Setenv("RHDH_TARGET_NAMESPACES", "")
	got := resolveNamespaces(&gatherOptions{})
	if got != nil {
		t.Errorf("resolveNamespaces() = %v, want nil", got)
	}
}

func TestResolveHeapDumpMethod_CLIExplicit(t *testing.T) {
	t.Setenv("RHDH_HEAP_DUMP_METHOD", "sigusr2")
	cmd := newRootCmd()
	_ = cmd.ParseFlags([]string{"--heap-dump-method", "inspector"})
	got := resolveHeapDumpMethod(cmd, &gatherOptions{heapDumpMethod: "inspector"})
	if got != "inspector" {
		t.Errorf("resolveHeapDumpMethod() = %q, want inspector (CLI explicit)", got)
	}
}

func TestResolveHeapDumpMethod_EnvFallback(t *testing.T) {
	t.Setenv("RHDH_HEAP_DUMP_METHOD", "sigusr2")
	cmd := newRootCmd()
	_ = cmd.ParseFlags([]string{})
	got := resolveHeapDumpMethod(cmd, &gatherOptions{heapDumpMethod: "inspector"})
	if got != "sigusr2" {
		t.Errorf("resolveHeapDumpMethod() = %q, want sigusr2 (env fallback)", got)
	}
}

func TestResolveHeapDumpMethod_Default(t *testing.T) {
	t.Setenv("RHDH_HEAP_DUMP_METHOD", "")
	cmd := newRootCmd()
	_ = cmd.ParseFlags([]string{})
	got := resolveHeapDumpMethod(cmd, &gatherOptions{heapDumpMethod: "inspector"})
	if got != "inspector" {
		t.Errorf("resolveHeapDumpMethod() = %q, want inspector (default)", got)
	}
}

func TestResolveHeapDumpInstances_CLIFlag(t *testing.T) {
	t.Setenv("RHDH_HEAP_DUMP_INSTANCES", "env-instance")
	got := resolveHeapDumpInstances(&gatherOptions{heapDumpInstances: "cli-instance"})
	if got != "cli-instance" {
		t.Errorf("resolveHeapDumpInstances() = %q, want cli-instance", got)
	}
}

func TestResolveHeapDumpInstances_EnvFallback(t *testing.T) {
	t.Setenv("RHDH_HEAP_DUMP_INSTANCES", "env-instance")
	got := resolveHeapDumpInstances(&gatherOptions{})
	if got != "env-instance" {
		t.Errorf("resolveHeapDumpInstances() = %q, want env-instance", got)
	}
}

func noopClean(_, _, _, _ string) error { return nil }

func fakeClientFactory() (*kube.Client, error) {
	fakeClient := fakeclientset.NewSimpleClientset()
	fd := fakeClient.Discovery().(*fakediscovery.FakeDiscovery)
	scheme := runtime.NewScheme()
	dynClient := fakedynamic.NewSimpleDynamicClientWithCustomListKinds(scheme, map[schema.GroupVersionResource]string{
		{Group: "networking.k8s.io", Version: "v1", Resource: "ingresses"}:                 "IngressList",
		{Group: "config.openshift.io", Version: "v1", Resource: "dnses"}:                   "DNSList",
		{Group: "operator.openshift.io", Version: "v1", Resource: "ingresscontrollers"}:     "IngressControllerList",
		{Group: "route.openshift.io", Version: "v1", Resource: "routes"}:                    "RouteList",
		{Group: "apps", Version: "v1", Resource: "deployments"}:                             "DeploymentList",
		{Group: "apps", Version: "v1", Resource: "statefulsets"}:                             "StatefulSetList",
		{Group: "apps", Version: "v1", Resource: "replicasets"}:                              "ReplicaSetList",
		{Group: "rhdh.redhat.com", Version: "v1alpha3", Resource: "backstages"}:              "BackstageList",
		{Group: "networking.k8s.io", Version: "v1", Resource: "networkpolicies"}:             "NetworkPolicyList",
		{Group: "apiextensions.k8s.io", Version: "v1", Resource: "customresourcedefinitions"}: "CustomResourceDefinitionList",
	})

	return &kube.Client{
		Clientset: fakeClient,
		Discovery: fd,
		Dynamic:   dynClient,
		Config:    &rest.Config{Host: "https://fake-server:6443"},
	}, nil
}

func TestRunGather_FullFlow(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("BASE_COLLECTION_PATH", dir)

	cmd := newRootCmd()
	// Exclude all collectors to avoid actual collection
	args := make([]string, 0, len(mandatoryScripts))
	for _, s := range mandatoryScripts {
		args = append(args, "--without-"+s)
	}
	_ = cmd.ParseFlags(args)

	opts := &gatherOptions{
		heapDumpMethod: "inspector",
		clientFactory:  fakeClientFactory,
		cleanFunc:      noopClean,
	}
	err := runGather(cmd, opts)
	if err != nil {
		t.Fatalf("runGather: %v", err)
	}

	versionFile := filepath.Join(dir, "version")
	data, err := os.ReadFile(versionFile)
	if err != nil {
		t.Fatalf("version file not created: %v", err)
	}
	if !strings.Contains(string(data), "rhdh-must-gather") {
		t.Error("version file missing expected content")
	}

	if _, err := os.Stat(filepath.Join(dir, "sanitization-report.txt")); err != nil {
		t.Error("expected sanitization to run (sanitization-report.txt)")
	}
}

func TestRunGather_ClientFactoryError(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("BASE_COLLECTION_PATH", dir)

	cmd := newRootCmd()
	_ = cmd.ParseFlags([]string{})

	opts := &gatherOptions{
		heapDumpMethod: "inspector",
		cleanFunc:      noopClean,
		clientFactory: func() (*kube.Client, error) {
			return nil, fmt.Errorf("no cluster available")
		},
	}
	err := runGather(cmd, opts)
	if err == nil {
		t.Fatal("expected error when client factory fails")
	}
	if !strings.Contains(err.Error(), "no cluster available") {
		t.Errorf("error = %q, want to contain 'no cluster available'", err)
	}
}

func TestRunGather_WithSecrets(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("BASE_COLLECTION_PATH", dir)

	cmd := newRootCmd()
	args := make([]string, 0, len(mandatoryScripts))
	for _, s := range mandatoryScripts {
		args = append(args, "--without-"+s)
	}
	_ = cmd.ParseFlags(args)

	opts := &gatherOptions{
		heapDumpMethod: "inspector",
		withSecrets:    true,
		clientFactory:  fakeClientFactory,
		cleanFunc:      noopClean,
	}
	err := runGather(cmd, opts)
	if err != nil {
		t.Fatalf("runGather: %v", err)
	}
}

func TestRunGather_WithHeapDumps(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("BASE_COLLECTION_PATH", dir)

	cmd := newRootCmd()
	args := make([]string, 0, len(mandatoryScripts))
	for _, s := range mandatoryScripts {
		args = append(args, "--without-"+s)
	}
	_ = cmd.ParseFlags(args)

	opts := &gatherOptions{
		heapDumpMethod:    "sigusr2",
		withHeapDumps:     true,
		heapDumpInstances: "my-instance",
		clientFactory:     fakeClientFactory,
		cleanFunc:         noopClean,
	}
	err := runGather(cmd, opts)
	if err != nil {
		t.Fatalf("runGather: %v", err)
	}
}

func TestRunGather_WithNamespaces(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("BASE_COLLECTION_PATH", dir)

	cmd := newRootCmd()
	args := make([]string, 0, len(mandatoryScripts))
	for _, s := range mandatoryScripts {
		args = append(args, "--without-"+s)
	}
	_ = cmd.ParseFlags(args)

	opts := &gatherOptions{
		heapDumpMethod: "inspector",
		namespaces:     "ns1,ns2",
		clientFactory:  fakeClientFactory,
		cleanFunc:      noopClean,
	}
	err := runGather(cmd, opts)
	if err != nil {
		t.Fatalf("runGather: %v", err)
	}
}

func TestRunGather_UnknownCollector(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("BASE_COLLECTION_PATH", dir)

	cmd := newRootCmd()
	// Exclude all mandatory scripts
	args := make([]string, 0, len(mandatoryScripts))
	for _, s := range mandatoryScripts {
		args = append(args, "--without-"+s)
	}
	_ = cmd.ParseFlags(args)

	// Add a nonexistent collector to the mandatory list temporarily
	origScripts := mandatoryScripts
	mandatoryScripts = []string{"nonexistent-collector"}
	defer func() { mandatoryScripts = origScripts }()

	// Re-parse without exclusions so the unknown collector is included
	cmd2 := newRootCmd()
	_ = cmd2.ParseFlags([]string{})

	opts := &gatherOptions{
		heapDumpMethod: "inspector",
		clientFactory:  fakeClientFactory,
		cleanFunc:      noopClean,
	}
	err := runGather(cmd2, opts)
	if err != nil {
		t.Fatalf("runGather should not fail for unknown collector: %v", err)
	}
}

func TestCollectPodLogs_NotInPod(t *testing.T) {
	client, _ := fakeClientFactory()
	dir := t.TempDir()

	collectPodLogs(t.Context(), client, dir)

	if _, err := os.Stat(filepath.Join(dir, "must-gather.log")); !os.IsNotExist(err) {
		t.Error("expected no log file when not running in a pod")
	}
}

func TestCollectPodLogs_NoPodName(t *testing.T) {
	client, _ := fakeClientFactory()
	dir := t.TempDir()

	nsFile := filepath.Join(dir, "namespace")
	_ = os.WriteFile(nsFile, []byte("test-ns"), 0o644)
	orig := serviceAccountNSFile
	serviceAccountNSFile = nsFile
	defer func() { serviceAccountNSFile = orig }()

	t.Setenv("POD_NAME", "")

	outDir := t.TempDir()
	collectPodLogs(t.Context(), client, outDir)

	if _, err := os.Stat(filepath.Join(outDir, "must-gather.log")); !os.IsNotExist(err) {
		t.Error("expected no log file when POD_NAME is empty")
	}
}

func TestCollectPodLogs_Success(t *testing.T) {
	client, _ := fakeClientFactory()
	dir := t.TempDir()

	nsFile := filepath.Join(dir, "namespace")
	_ = os.WriteFile(nsFile, []byte("test-ns"), 0o644)
	orig := serviceAccountNSFile
	serviceAccountNSFile = nsFile
	defer func() { serviceAccountNSFile = orig }()

	t.Setenv("POD_NAME", "my-gather-pod")

	outDir := t.TempDir()
	collectPodLogs(t.Context(), client, outDir)

	if _, err := os.Stat(filepath.Join(outDir, "must-gather.log")); err != nil {
		t.Error("expected must-gather.log to be created")
	}
}
