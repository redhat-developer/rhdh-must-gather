package namespace

import (
	"os"
	"strings"
)

// ParseNamespaces splits a comma-separated namespace string into a trimmed
// slice, returning nil when the input is empty or whitespace-only.
func ParseNamespaces(raw string) []string {
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	result := make([]string, 0, len(parts))
	for _, p := range parts {
		ns := strings.TrimSpace(p)
		if ns != "" {
			result = append(result, ns)
		}
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

// Includes returns true if ns appears in targets, or if targets is empty
// (meaning no filter is active).
func Includes(targets []string, ns string) bool {
	if len(targets) == 0 {
		return true
	}
	for _, t := range targets {
		if ns == t {
			return true
		}
	}
	return false
}

// TargetNamespaces reads RHDH_TARGET_NAMESPACES from the environment and
// parses it into a namespace list.
func TargetNamespaces() []string {
	return ParseNamespaces(os.Getenv("RHDH_TARGET_NAMESPACES"))
}

// ShouldInclude returns true if ns passes the RHDH_TARGET_NAMESPACES filter.
func ShouldInclude(ns string) bool {
	return Includes(TargetNamespaces(), ns)
}
