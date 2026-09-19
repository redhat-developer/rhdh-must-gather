package cli

import (
	"testing"

	"github.com/spf13/cobra"
)

func TestBuildScriptList_Default(t *testing.T) {
	cmd := newRootCmd()
	cmd.SetArgs([]string{})
	if err := cmd.ParseFlags([]string{}); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}

	opts := &gatherOptions{}
	scripts := buildScriptList(cmd, opts)

	expected := []string{"platform", "helm", "operator", "orchestrator", "route", "ingress", "namespace-inspect"}
	if len(scripts) != len(expected) {
		t.Fatalf("got %d scripts, want %d: %v", len(scripts), len(expected), scripts)
	}
	for i, s := range expected {
		if scripts[i] != s {
			t.Errorf("scripts[%d] = %q, want %q", i, scripts[i], s)
		}
	}
}

func TestBuildScriptList_WithClusterInfo(t *testing.T) {
	cmd := newRootCmd()
	if err := cmd.ParseFlags([]string{"--cluster-info"}); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}

	opts := &gatherOptions{clusterInfo: true}
	scripts := buildScriptList(cmd, opts)

	last := scripts[len(scripts)-1]
	if last != "cluster-info" {
		t.Errorf("last script = %q, want %q", last, "cluster-info")
	}
	if len(scripts) != len(mandatoryScripts)+1 {
		t.Errorf("got %d scripts, want %d", len(scripts), len(mandatoryScripts)+1)
	}
}

func TestBuildScriptList_WithExclusions(t *testing.T) {
	cmd := newRootCmd()
	if err := cmd.ParseFlags([]string{"--without-helm", "--without-operator"}); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}

	opts := &gatherOptions{}
	scripts := buildScriptList(cmd, opts)

	for _, s := range scripts {
		if s == "helm" || s == "operator" {
			t.Errorf("excluded script %q still in list", s)
		}
	}
	if len(scripts) != len(mandatoryScripts)-2 {
		t.Errorf("got %d scripts, want %d", len(scripts), len(mandatoryScripts)-2)
	}
}

func TestBuildScriptList_ExcludeAll(t *testing.T) {
	cmd := newRootCmd()
	args := make([]string, 0, len(mandatoryScripts))
	for _, s := range mandatoryScripts {
		args = append(args, "--without-"+s)
	}
	if err := cmd.ParseFlags(args); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}

	opts := &gatherOptions{}
	scripts := buildScriptList(cmd, opts)

	if len(scripts) != 0 {
		t.Errorf("got %d scripts, want 0: %v", len(scripts), scripts)
	}
}

func TestHeapDumpMethodValidation(t *testing.T) {
	tests := []struct {
		method  string
		wantErr bool
	}{
		{"inspector", false},
		{"sigusr2", false},
		{"invalid", true},
		{"", true},
	}

	for _, tt := range tests {
		cmd := newRootCmd()
		cmd.RunE = func(cmd *cobra.Command, args []string) error { return nil }
		cmd.SetArgs([]string{"--heap-dump-method", tt.method})
		err := cmd.Execute()
		if (err != nil) != tt.wantErr {
			t.Errorf("method=%q: got err=%v, wantErr=%v", tt.method, err, tt.wantErr)
		}
	}
}

func TestGetVersion_EnvOverride(t *testing.T) {
	t.Setenv("RHDH_MUST_GATHER_VERSION", "1.2.3-test")
	v := getVersion()
	if v != "1.2.3-test" {
		t.Errorf("getVersion() = %q, want %q", v, "1.2.3-test")
	}
}

func TestGetVersion_Compiled(t *testing.T) {
	t.Setenv("RHDH_MUST_GATHER_VERSION", "")
	v := getVersion()
	if v != version {
		t.Errorf("getVersion() = %q, want compiled-in %q", v, version)
	}
}

func TestUnknownFlagsAllowed(t *testing.T) {
	cmd := newRootCmd()
	cmd.RunE = func(cmd *cobra.Command, args []string) error { return nil }
	cmd.SetArgs([]string{"--some-unknown-flag"})
	err := cmd.Execute()
	if err != nil {
		t.Errorf("unknown flag should be allowed, got: %v", err)
	}
}
