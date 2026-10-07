package collector

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
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
