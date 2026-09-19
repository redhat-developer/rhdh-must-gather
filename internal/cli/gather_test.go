package cli

import (
	"testing"
	"time"
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
