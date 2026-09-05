package cli

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveScriptDir_EnvOverride(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "common.sh"), []byte("# stub"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("RHDH_SCRIPT_DIR", dir)

	got, err := resolveScriptDir()
	if err != nil {
		t.Fatalf("resolveScriptDir: %v", err)
	}
	if got != dir {
		t.Errorf("got %q, want %q", got, dir)
	}
}

func TestResolveScriptDir_EnvOverride_Invalid(t *testing.T) {
	t.Setenv("RHDH_SCRIPT_DIR", "/nonexistent/path")

	_, err := resolveScriptDir()
	if err == nil {
		t.Fatal("expected error for invalid RHDH_SCRIPT_DIR")
	}
}

func TestResolveScriptDir_CollectionScripts(t *testing.T) {
	t.Setenv("RHDH_SCRIPT_DIR", "")

	dir := t.TempDir()
	csDir := filepath.Join(dir, "collection-scripts")
	if err := os.MkdirAll(csDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(csDir, "common.sh"), []byte("# stub"), 0o644); err != nil {
		t.Fatal(err)
	}

	origDir, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(origDir) }()

	got, err := resolveScriptDir()
	if err != nil {
		t.Fatalf("resolveScriptDir: %v", err)
	}
	if got != csDir {
		t.Errorf("got %q, want %q", got, csDir)
	}
}

func TestResolveScriptDir_NotFound(t *testing.T) {
	t.Setenv("RHDH_SCRIPT_DIR", "")

	dir := t.TempDir()
	origDir, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(origDir) }()

	_, err := resolveScriptDir()
	if err == nil {
		t.Fatal("expected error when no scripts found")
	}
}

func TestHasCommonSh(t *testing.T) {
	dir := t.TempDir()
	if hasCommonSh(dir) {
		t.Error("hasCommonSh should return false for empty dir")
	}

	if err := os.WriteFile(filepath.Join(dir, "common.sh"), []byte("# stub"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !hasCommonSh(dir) {
		t.Error("hasCommonSh should return true when common.sh exists")
	}
}
