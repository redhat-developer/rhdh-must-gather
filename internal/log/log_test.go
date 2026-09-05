package log

import (
	"testing"
)

func TestInit_Levels(t *testing.T) {
	tests := []struct {
		env  string
		want Level
	}{
		{"info", LevelInfo},
		{"INFO", LevelInfo},
		{"debug", LevelDebug},
		{"DEBUG", LevelDebug},
		{"trace", LevelTrace},
		{"TRACE", LevelTrace},
		{"", LevelInfo},
		{"unknown", LevelInfo},
	}

	for _, tt := range tests {
		t.Run(tt.env, func(t *testing.T) {
			t.Setenv("LOG_LEVEL", tt.env)
			Init()
			if currentLevel != tt.want {
				t.Errorf("LOG_LEVEL=%q: got level %d, want %d", tt.env, currentLevel, tt.want)
			}
		})
	}
}

func TestIsDebug(t *testing.T) {
	t.Setenv("LOG_LEVEL", "info")
	Init()
	if IsDebug() {
		t.Error("IsDebug() should be false at info level")
	}

	t.Setenv("LOG_LEVEL", "debug")
	Init()
	if !IsDebug() {
		t.Error("IsDebug() should be true at debug level")
	}

	t.Setenv("LOG_LEVEL", "trace")
	Init()
	if !IsDebug() {
		t.Error("IsDebug() should be true at trace level")
	}
}
