package collector

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestParseSections(t *testing.T) {
	input := `===ID===
uid=1000(default)
===ENV===
BACKSTAGE_VERSION=1.2.3
NODE_OPTIONS=--max-old-space-size=4096
===VERSIONS===
BACKSTAGE_VERSION=1.2.3
RHDH_VERSION=1.5.0
===NODE_VERSION===
v20.11.1`

	sections := parseSections(input)

	if sections["ID"] != "uid=1000(default)" {
		t.Errorf("ID = %q, want uid=1000(default)", sections["ID"])
	}
	if sections["NODE_VERSION"] != "v20.11.1" {
		t.Errorf("NODE_VERSION = %q, want v20.11.1", sections["NODE_VERSION"])
	}
	if sections["VERSIONS"] == "" {
		t.Error("VERSIONS section is empty")
	}
}

func TestParseSections_Empty(t *testing.T) {
	sections := parseSections("")
	if len(sections) != 0 {
		t.Errorf("expected empty map, got %v", sections)
	}
}

func TestParseSections_NestedFileMarkers(t *testing.T) {
	input := `===LS===
drwxr-xr-x 3 root root 4096 Jan  1 00:00 plugin-a
===CONFIG===
some config
===PACKAGES===
===FILE:/opt/app-root/src/dynamic-plugins-root/plugin-a/package.json===
{"name": "plugin-a"}
===FILE:/opt/app-root/src/dynamic-plugins-root/plugin-b/package.json===
{"name": "plugin-b"}`

	sections := parseSections(input)

	if !strings.Contains(sections["PACKAGES"], "===FILE:") {
		t.Errorf("PACKAGES section should preserve ===FILE:...=== markers, got: %q", sections["PACKAGES"])
	}
	if !strings.Contains(sections["PACKAGES"], "plugin-a") {
		t.Error("PACKAGES section missing plugin-a content")
	}
	if !strings.Contains(sections["PACKAGES"], "plugin-b") {
		t.Error("PACKAGES section missing plugin-b content")
	}
}

func TestParseKeyValues(t *testing.T) {
	input := `BACKSTAGE_VERSION=1.2.3
RHDH_VERSION=1.5.0
UPSTREAM_REPO=
MIDSTREAM_REPO=https://example.com`

	kv := parseKeyValues(input)

	if kv["BACKSTAGE_VERSION"] != "1.2.3" {
		t.Errorf("BACKSTAGE_VERSION = %q, want 1.2.3", kv["BACKSTAGE_VERSION"])
	}
	if kv["RHDH_VERSION"] != "1.5.0" {
		t.Errorf("RHDH_VERSION = %q, want 1.5.0", kv["RHDH_VERSION"])
	}
	if kv["UPSTREAM_REPO"] != "" {
		t.Errorf("UPSTREAM_REPO = %q, want empty", kv["UPSTREAM_REPO"])
	}
	if kv["MIDSTREAM_REPO"] != "https://example.com" {
		t.Errorf("MIDSTREAM_REPO = %q, want https://example.com", kv["MIDSTREAM_REPO"])
	}
}

func TestHasContainer(t *testing.T) {
	pod := &corev1.Pod{
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{Name: "backstage-backend"},
				{Name: "sidecar"},
			},
		},
	}

	if !hasContainer(pod, "backstage-backend") {
		t.Error("expected backstage-backend to be found")
	}
	if hasContainer(pod, "nonexistent") {
		t.Error("expected nonexistent to not be found")
	}
}

func TestWritePluginPackageJSON(t *testing.T) {
	dir := t.TempDir()
	input := `===FILE:/opt/app-root/src/dynamic-plugins-root/plugin-a/package.json===
{"name": "plugin-a", "version": "1.0.0"}
===FILE:/opt/app-root/src/dynamic-plugins-root/plugin-b/package.json===
{"name": "plugin-b", "version": "2.0.0"}`

	writePluginPackageJSON(dir, input)

	assertFileContains(t, dir+"/dynamic-plugins-root/plugin-a/package.json", "plugin-a")
	assertFileContains(t, dir+"/dynamic-plugins-root/plugin-b/package.json", "plugin-b")
}

func TestWritePluginPackageJSON_Empty(t *testing.T) {
	dir := t.TempDir()
	writePluginPackageJSON(dir, "")
}

func TestWritePluginFile(t *testing.T) {
	dir := t.TempDir()

	t.Run("with prefix", func(t *testing.T) {
		writePluginFile(dir, "/opt/app-root/src/dynamic-plugins-root/my-plugin/package.json", `{"name":"my-plugin"}`)
		assertFileContains(t, filepath.Join(dir, "my-plugin", "package.json"), "my-plugin")
	})

	t.Run("without prefix", func(t *testing.T) {
		writePluginFile(dir, "/some/other/path/file.json", `{"name":"other"}`)
		assertFileContains(t, filepath.Join(dir, "file.json"), "other")
	})
}

func TestParseSections_MultipleValues(t *testing.T) {
	input := `===ID===
root
===ENV===
KEY1=val1
KEY2=val2
KEY3=val3`

	sections := parseSections(input)
	if sections["ID"] != "root" {
		t.Errorf("ID = %q, want root", sections["ID"])
	}
	env := sections["ENV"]
	if !strings.Contains(env, "KEY1=val1") || !strings.Contains(env, "KEY3=val3") {
		t.Errorf("ENV = %q, expected all key-value pairs", env)
	}
}

func TestParseKeyValues_Empty(t *testing.T) {
	kv := parseKeyValues("")
	if len(kv) != 0 {
		t.Errorf("expected empty map, got %v", kv)
	}
}

func TestWriteBackstageJSON_WithVersion(t *testing.T) {
	dir := t.TempDir()
	cfg := newTestConfig(t, dir)

	writeBackstageJSON(context.Background(), cfg, "rhdh", "pod-1", dir, "1.35.1")

	data, err := os.ReadFile(filepath.Join(dir, "backstage.json"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	var result map[string]string
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if result["version"] != "1.35.1" {
		t.Errorf("version = %q, want 1.35.1", result["version"])
	}
	if result["source"] != "BACKSTAGE_VERSION env var" {
		t.Errorf("source = %q, want 'BACKSTAGE_VERSION env var'", result["source"])
	}
}

func TestWriteBuildMetadata_WithVersions(t *testing.T) {
	dir := t.TempDir()
	cfg := newTestConfig(t, dir)

	versions := map[string]string{
		"RHDH_VERSION":  "1.5.0",
		"UPSTREAM_REPO": "https://github.com/backstage/backstage",
		"MIDSTREAM_REPO": "https://example.com/rhdh",
	}
	writeBuildMetadata(context.Background(), cfg, "rhdh", "pod-1", dir, versions)

	data, err := os.ReadFile(filepath.Join(dir, "build-metadata.json"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	var result map[string]string
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if result["rhdh_version"] != "1.5.0" {
		t.Errorf("rhdh_version = %q, want 1.5.0", result["rhdh_version"])
	}
	if result["source"] != "environment variables" {
		t.Errorf("source = %q, want 'environment variables'", result["source"])
	}
}

func assertFileContains(t *testing.T, path, substr string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Errorf("reading %s: %v", path, err)
		return
	}
	if !strings.Contains(string(data), substr) {
		t.Errorf("%s does not contain %q, got: %s", path, substr, data)
	}
}

func TestCollectPodData_Success(t *testing.T) {
	dir := t.TempDir()

	envOutput := `===ID===
uid=1000(default)
===ENV===
BACKSTAGE_VERSION=1.35.1
NODE_OPTIONS=--max-old-space-size=4096
===VERSIONS===
BACKSTAGE_VERSION=1.35.1
RHDH_VERSION=1.5.0
UPSTREAM_REPO=
MIDSTREAM_REPO=
===NODE_VERSION===
v20.11.1`

	pluginsOutput := `===LS===
drwxr-xr-x 3 root root 4096 Jan  1 00:00 plugin-a
===CONFIG===
dynamic-plugins-config
===PACKAGES===
===FILE:/opt/app-root/src/dynamic-plugins-root/plugin-a/package.json===
{"name": "plugin-a"}`

	ops := &scriptablePodOps{
		execResults: []execResult{
			{output: envOutput},
			{output: pluginsOutput},
		},
	}

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "rhdh-pod", Namespace: "rhdh"},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{Name: "backstage-backend"}},
		},
	}
	cfg := newTestConfig(t, dir, withPodOps(ops))
	outDir := filepath.Join(dir, "pod-data")
	CollectPodData(context.Background(), cfg, "rhdh", pod, outDir)

	assertFileContains(t, filepath.Join(outDir, "app-container-userid.txt"), "uid=1000(default)")
	assertFileContains(t, filepath.Join(outDir, "env-vars.txt"), "BACKSTAGE_VERSION=1.35.1")
	assertFileContains(t, filepath.Join(outDir, "node-version.txt"), "v20.11.1")
	assertFileContains(t, filepath.Join(outDir, "backstage.json"), "1.35.1")
	assertFileContains(t, filepath.Join(outDir, "build-metadata.json"), "1.5.0")
	assertFileContains(t, filepath.Join(outDir, "dynamic-plugins-root.fs.txt"), "plugin-a")
	assertFileContains(t, filepath.Join(outDir, "app-config.dynamic-plugins.yaml"), "dynamic-plugins-config")
}

func TestCollectPodData_ExecError(t *testing.T) {
	dir := t.TempDir()
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "rhdh-pod", Namespace: "rhdh"},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{Name: "backstage-backend"}},
		},
	}
	ops := &fakePodOps{execErr: fmt.Errorf("connection refused")}
	cfg := newTestConfig(t, dir, withPodOps(ops))
	outDir := filepath.Join(dir, "pod-data")
	CollectPodData(context.Background(), cfg, "rhdh", pod, outDir)

	assertFileContains(t, filepath.Join(outDir, "collection-error.txt"), "connection refused")
}

func TestCollectPodData_NoBackstageContainer(t *testing.T) {
	dir := t.TempDir()
	cfg := newTestConfig(t, dir)
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "nginx-pod", Namespace: "default"},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{Name: "nginx"}},
		},
	}

	outDir := filepath.Join(dir, "pod-data")
	CollectPodData(context.Background(), cfg, "default", pod, outDir)

	// No files should be created since the pod lacks backstage-backend
	if _, err := os.Stat(outDir); !os.IsNotExist(err) {
		t.Error("expected no output dir when pod has no backstage-backend container")
	}
}

func TestCollectProcesses(t *testing.T) {
	dir := t.TempDir()
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "rhdh-pod", Namespace: "rhdh"},
		Spec: corev1.PodSpec{
			InitContainers: []corev1.Container{{Name: "install-plugins"}},
			Containers:     []corev1.Container{{Name: "backstage-backend"}},
		},
	}
	ops := &fakePodOps{execOutput: "PID 1 node /app/index.js\n"}
	cfg := newTestConfig(t, dir, withPodOps(ops))
	outDir := filepath.Join(dir, "processes")

	CollectProcesses(context.Background(), cfg, "rhdh", pod, outDir)

	assertFileContains(t, filepath.Join(outDir, "container=install-plugins.txt"), "PID 1 node")
	assertFileContains(t, filepath.Join(outDir, "container=backstage-backend.txt"), "PID 1 node")
}

func TestCollectProcesses_ExecError(t *testing.T) {
	dir := t.TempDir()
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "rhdh-pod", Namespace: "rhdh"},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{Name: "backstage-backend"}},
		},
	}
	ops := &fakePodOps{execErr: fmt.Errorf("exec failed")}
	cfg := newTestConfig(t, dir, withPodOps(ops))
	outDir := filepath.Join(dir, "processes")

	CollectProcesses(context.Background(), cfg, "rhdh", pod, outDir)

	assertFileContains(t, filepath.Join(outDir, "container=backstage-backend.txt"), "Failed to collect processes")
}

func TestCollectPodLogs_CreatesDirectoryStructure(t *testing.T) {
	dir := t.TempDir()

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "rhdh-pod", Namespace: "rhdh"},
		Spec: corev1.PodSpec{
			InitContainers: []corev1.Container{{Name: "install-plugins"}},
			Containers:     []corev1.Container{{Name: "backstage-backend"}, {Name: "sidecar"}},
		},
	}

	cfg := newTestConfig(t, dir)
	outDir := filepath.Join(dir, "logs")
	CollectPodLogs(context.Background(), cfg, "rhdh", pod, outDir)

	for _, c := range []string{"install-plugins", "backstage-backend", "sidecar"} {
		cDir := filepath.Join(outDir, "container="+c)
		if _, err := os.Stat(filepath.Join(cDir, "current.txt")); err != nil {
			t.Errorf("expected current.txt for container %s", c)
		}
		if _, err := os.Stat(filepath.Join(cDir, "previous.txt")); err != nil {
			t.Errorf("expected previous.txt for container %s", c)
		}
	}

	// Aggregated logs should also be created
	if _, err := os.Stat(filepath.Join(outDir, "logs-app.current.txt")); err != nil {
		t.Error("expected logs-app.current.txt")
	}
	if _, err := os.Stat(filepath.Join(outDir, "logs-app.previous.txt")); err != nil {
		t.Error("expected logs-app.previous.txt")
	}
}

func TestStreamAndSaveLogs_Success(t *testing.T) {
	dir := t.TempDir()
	ops := &fakePodOps{logOutput: "line one\nline two\n"}
	cfg := newTestConfig(t, dir, withPodOps(ops))

	outPath := filepath.Join(dir, "logs.txt")
	streamAndSaveLogs(context.Background(), cfg, "rhdh", "pod-1", "backstage-backend", false, outPath)

	assertFileContains(t, outPath, "line one")
	assertFileContains(t, outPath, "line two")
}

func TestStreamAndSaveLogs_Error(t *testing.T) {
	dir := t.TempDir()
	ops := &fakePodOps{logErr: fmt.Errorf("stream unavailable")}
	cfg := newTestConfig(t, dir, withPodOps(ops))

	outPath := filepath.Join(dir, "logs.txt")
	streamAndSaveLogs(context.Background(), cfg, "rhdh", "pod-1", "backstage-backend", false, outPath)

	assertFileContains(t, outPath, "Failed to get logs")
}

func TestCollectPodLogs_WithPodOps(t *testing.T) {
	dir := t.TempDir()
	ops := &fakePodOps{logOutput: "app log line\n"}
	cfg := newTestConfig(t, dir, withPodOps(ops))

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "rhdh-pod", Namespace: "rhdh"},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{Name: "backstage-backend"}},
		},
	}
	outDir := filepath.Join(dir, "logs")
	CollectPodLogs(context.Background(), cfg, "rhdh", pod, outDir)

	assertFileContains(t, filepath.Join(outDir, "container=backstage-backend", "current.txt"), "app log line")
	assertFileContains(t, filepath.Join(outDir, "logs-app.current.txt"), "app log line")
}

func TestWriteBackstageJSON_Fallback(t *testing.T) {
	dir := t.TempDir()
	ops := &fakePodOps{execOutput: `{"version":"1.33.0","source":"file"}`}
	cfg := newTestConfig(t, dir, withPodOps(ops))

	writeBackstageJSON(context.Background(), cfg, "rhdh", "pod-1", dir, "")

	assertFileContains(t, filepath.Join(dir, "backstage.json"), "1.33.0")
}

func TestWriteBuildMetadata_Fallback(t *testing.T) {
	dir := t.TempDir()
	ops := &fakePodOps{execOutput: `{"rhdh_version":"1.4.0"}`}
	cfg := newTestConfig(t, dir, withPodOps(ops))

	versions := map[string]string{
		"RHDH_VERSION":  "",
		"UPSTREAM_REPO": "",
		"MIDSTREAM_REPO": "",
	}
	writeBuildMetadata(context.Background(), cfg, "rhdh", "pod-1", dir, versions)

	assertFileContains(t, filepath.Join(dir, "build-metadata.json"), "1.4.0")
}
