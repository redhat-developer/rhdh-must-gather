package namespace

import (
	"reflect"
	"testing"
)

func TestParseNamespaces(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  []string
	}{
		{"empty", "", nil},
		{"single", "rhdh-prod", []string{"rhdh-prod"}},
		{"multiple with spaces", "ns1, ns2 ,ns3", []string{"ns1", "ns2", "ns3"}},
		{"whitespace only", " , , ", nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ParseNamespaces(tt.input)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("ParseNamespaces(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestIncludes(t *testing.T) {
	tests := []struct {
		name    string
		targets []string
		ns      string
		want    bool
	}{
		{"no filter includes all", nil, "any-ns", true},
		{"match first", []string{"ns1", "ns2"}, "ns1", true},
		{"match second", []string{"ns1", "ns2"}, "ns2", true},
		{"no match", []string{"ns1", "ns2"}, "ns3", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Includes(tt.targets, tt.ns); got != tt.want {
				t.Errorf("Includes(%v, %q) = %v, want %v", tt.targets, tt.ns, got, tt.want)
			}
		})
	}
}

func TestTargetNamespaces(t *testing.T) {
	tests := []struct {
		name string
		env  string
		want []string
	}{
		{"empty", "", nil},
		{"single", "rhdh-prod", []string{"rhdh-prod"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("RHDH_TARGET_NAMESPACES", tt.env)
			got := TargetNamespaces()
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("TargetNamespaces() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestShouldInclude(t *testing.T) {
	tests := []struct {
		name string
		env  string
		ns   string
		want bool
	}{
		{"no filter", "", "any-ns", true},
		{"match", "ns1,ns2", "ns1", true},
		{"no match", "ns1,ns2", "ns3", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("RHDH_TARGET_NAMESPACES", tt.env)
			if got := ShouldInclude(tt.ns); got != tt.want {
				t.Errorf("ShouldInclude(%q) = %v, want %v", tt.ns, got, tt.want)
			}
		})
	}
}
