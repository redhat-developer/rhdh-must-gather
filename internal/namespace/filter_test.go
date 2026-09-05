package namespace

import (
	"testing"
)

func TestTargetNamespaces_Empty(t *testing.T) {
	t.Setenv("RHDH_TARGET_NAMESPACES", "")
	if ns := TargetNamespaces(); ns != nil {
		t.Errorf("got %v, want nil", ns)
	}
}

func TestTargetNamespaces_Single(t *testing.T) {
	t.Setenv("RHDH_TARGET_NAMESPACES", "rhdh-prod")
	ns := TargetNamespaces()
	if len(ns) != 1 || ns[0] != "rhdh-prod" {
		t.Errorf("got %v, want [rhdh-prod]", ns)
	}
}

func TestTargetNamespaces_Multiple(t *testing.T) {
	t.Setenv("RHDH_TARGET_NAMESPACES", "ns1, ns2 ,ns3")
	ns := TargetNamespaces()
	expected := []string{"ns1", "ns2", "ns3"}
	if len(ns) != len(expected) {
		t.Fatalf("got %v, want %v", ns, expected)
	}
	for i, want := range expected {
		if ns[i] != want {
			t.Errorf("ns[%d] = %q, want %q", i, ns[i], want)
		}
	}
}

func TestTargetNamespaces_WhitespaceOnly(t *testing.T) {
	t.Setenv("RHDH_TARGET_NAMESPACES", " , , ")
	if ns := TargetNamespaces(); ns != nil {
		t.Errorf("got %v, want nil", ns)
	}
}

func TestShouldInclude_NoFilter(t *testing.T) {
	t.Setenv("RHDH_TARGET_NAMESPACES", "")
	if !ShouldInclude("any-ns") {
		t.Error("should include all namespaces when no filter set")
	}
}

func TestShouldInclude_Match(t *testing.T) {
	t.Setenv("RHDH_TARGET_NAMESPACES", "ns1,ns2")
	if !ShouldInclude("ns1") {
		t.Error("ns1 should be included")
	}
	if !ShouldInclude("ns2") {
		t.Error("ns2 should be included")
	}
}

func TestShouldInclude_NoMatch(t *testing.T) {
	t.Setenv("RHDH_TARGET_NAMESPACES", "ns1,ns2")
	if ShouldInclude("ns3") {
		t.Error("ns3 should not be included")
	}
}
