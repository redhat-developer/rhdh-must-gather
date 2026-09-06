package cli

import (
	"testing"
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
