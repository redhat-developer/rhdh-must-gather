package collector

import (
	"os"
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
