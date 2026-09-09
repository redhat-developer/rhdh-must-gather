package namespace

import (
	"os"
	"strings"
)

func TargetNamespaces() []string {
	raw := os.Getenv("RHDH_TARGET_NAMESPACES")
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

func ShouldInclude(ns string) bool {
	targets := TargetNamespaces()
	if targets == nil {
		return true
	}
	for _, t := range targets {
		if ns == t {
			return true
		}
	}
	return false
}
