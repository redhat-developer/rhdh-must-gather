package log

import (
	"bytes"
	"io"
	"os"
	"strings"
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

func TestLogOutput(t *testing.T) {
	tests := []struct {
		name    string
		logFunc func(string, ...any)
		wantTag string
	}{
		{"info", Info, "INFO"},
		{"warn", Warn, "WARN"},
		{"error", Error, "ERROR"},
		{"success", Success, "SUCCESS"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, w, _ := os.Pipe()
			oldStderr := os.Stderr
			os.Stderr = w

			tt.logFunc("test message %d", 42)

			_ = w.Close()
			os.Stderr = oldStderr

			var buf bytes.Buffer
			_, _ = io.Copy(&buf, r)
			output := buf.String()

			if !strings.Contains(output, tt.wantTag) {
				t.Errorf("expected [%s] in output, got %q", tt.wantTag, output)
			}
			if !strings.Contains(output, "test message 42") {
				t.Errorf("expected formatted message in output, got %q", output)
			}
		})
	}
}

func TestDebug_NotShownAtInfoLevel(t *testing.T) {
	t.Setenv("LOG_LEVEL", "info")
	Init()

	r, w, _ := os.Pipe()
	oldStderr := os.Stderr
	os.Stderr = w

	Debug("should not appear")

	_ = w.Close()
	os.Stderr = oldStderr

	var buf bytes.Buffer
	_, _ = io.Copy(&buf, r)
	if buf.Len() > 0 {
		t.Errorf("debug message should not appear at info level, got %q", buf.String())
	}
}

func TestDebug_ShownAtDebugLevel(t *testing.T) {
	t.Setenv("LOG_LEVEL", "debug")
	Init()

	r, w, _ := os.Pipe()
	oldStderr := os.Stderr
	os.Stderr = w

	Debug("debug message %s", "here")

	_ = w.Close()
	os.Stderr = oldStderr

	var buf bytes.Buffer
	_, _ = io.Copy(&buf, r)
	output := buf.String()
	if !strings.Contains(output, "DEBUG") {
		t.Errorf("expected [DEBUG] in output, got %q", output)
	}
	if !strings.Contains(output, "debug message here") {
		t.Errorf("expected formatted message, got %q", output)
	}
}
