package collector

import (
	"testing"
)

func TestMatchesInstance(t *testing.T) {
	tests := []struct {
		name    string
		pattern string
		want    bool
	}{
		{"rhdhsupp-308-backstage", "rhdhsupp-308", true},
		{"rhdhsupp-308", "rhdhsupp-308", true},
		{"my-backstage", "rhdhsupp-308", false},
		{"", "rhdhsupp-308", false},
		{"rhdhsupp-308-backstage", "", false},
	}
	for _, tt := range tests {
		if got := matchesInstance(tt.name, tt.pattern); got != tt.want {
			t.Errorf("matchesInstance(%q, %q) = %v, want %v", tt.name, tt.pattern, got, tt.want)
		}
	}
}

func TestMatchesInstanceFilter(t *testing.T) {
	t.Run("no filter set", func(t *testing.T) {
		t.Setenv("RHDH_HEAP_DUMP_INSTANCES", "")
		if !matchesInstanceFilter("anything", "anything") {
			t.Error("expected true when no filter set")
		}
	})

	t.Run("deploy name matches", func(t *testing.T) {
		t.Setenv("RHDH_HEAP_DUMP_INSTANCES", "rhdhsupp-308")
		if !matchesInstanceFilter("rhdhsupp-308-backstage", "other") {
			t.Error("expected true when deploy name matches")
		}
	})

	t.Run("instance name matches", func(t *testing.T) {
		t.Setenv("RHDH_HEAP_DUMP_INSTANCES", "my-release")
		if !matchesInstanceFilter("other-deploy", "my-release") {
			t.Error("expected true when instance name matches")
		}
	})

	t.Run("no match", func(t *testing.T) {
		t.Setenv("RHDH_HEAP_DUMP_INSTANCES", "rhdhsupp-308")
		if matchesInstanceFilter("other-deploy", "other-instance") {
			t.Error("expected false when nothing matches")
		}
	})

	t.Run("multiple instances", func(t *testing.T) {
		t.Setenv("RHDH_HEAP_DUMP_INSTANCES", "foo, rhdhsupp-308, bar")
		if !matchesInstanceFilter("rhdhsupp-308-backstage", "") {
			t.Error("expected true with multiple instances")
		}
	})
}

func TestHeapDumpTimeout(t *testing.T) {
	t.Run("default", func(t *testing.T) {
		t.Setenv("HEAP_DUMP_TIMEOUT", "")
		got := heapDumpTimeout()
		if got.Seconds() != 600 {
			t.Errorf("heapDumpTimeout() = %v, want 600s", got)
		}
	})

	t.Run("custom", func(t *testing.T) {
		t.Setenv("HEAP_DUMP_TIMEOUT", "120")
		got := heapDumpTimeout()
		if got.Seconds() != 120 {
			t.Errorf("heapDumpTimeout() = %v, want 120s", got)
		}
	})
}

func TestHeapDumpMethod(t *testing.T) {
	t.Run("default", func(t *testing.T) {
		t.Setenv("RHDH_HEAP_DUMP_METHOD", "")
		if got := heapDumpMethod(); got != "inspector" {
			t.Errorf("heapDumpMethod() = %q, want inspector", got)
		}
	})

	t.Run("sigusr2", func(t *testing.T) {
		t.Setenv("RHDH_HEAP_DUMP_METHOD", "sigusr2")
		if got := heapDumpMethod(); got != "sigusr2" {
			t.Errorf("heapDumpMethod() = %q, want sigusr2", got)
		}
	})
}

func TestHumanSize(t *testing.T) {
	tests := []struct {
		bytes int64
		want  string
	}{
		{0, "0B"},
		{512, "512B"},
		{1024, "1KB"},
		{1536, "1KB"},
		{1048576, "1MB"},
		{104857600, "100MB"},
	}
	for _, tt := range tests {
		if got := humanSize(tt.bytes); got != tt.want {
			t.Errorf("humanSize(%d) = %q, want %q", tt.bytes, got, tt.want)
		}
	}
}
