package exec

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func TestTimeout_Default(t *testing.T) {
	t.Setenv("CMD_TIMEOUT", "")
	d := Timeout()
	if d != 90*time.Second {
		t.Errorf("got %v, want 90s", d)
	}
}

func TestTimeout_Custom(t *testing.T) {
	t.Setenv("CMD_TIMEOUT", "30")
	d := Timeout()
	if d != 30*time.Second {
		t.Errorf("got %v, want 30s", d)
	}
}

func TestTimeout_Invalid(t *testing.T) {
	t.Setenv("CMD_TIMEOUT", "abc")
	d := Timeout()
	if d != 90*time.Second {
		t.Errorf("got %v, want 90s (default)", d)
	}
}

func TestCollect_Success(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sub", "output.txt")

	err := Collect(context.Background(), nil, path, "test data", func(ctx context.Context) ([]byte, error) {
		return []byte("hello"), nil
	})
	if err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "hello" {
		t.Errorf("got %q, want %q", data, "hello")
	}
}

func TestCollect_Error_WritesErrorFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "output.txt")

	err := Collect(context.Background(), nil, path, "failing command", func(ctx context.Context) ([]byte, error) {
		return nil, fmt.Errorf("connection refused")
	})
	if err != nil {
		t.Fatalf("Collect should return nil on command failure, got: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) == 0 {
		t.Error("expected error file to have content")
	}
}

func TestCollect_Interrupted(t *testing.T) {
	var interrupted atomic.Bool
	interrupted.Store(true)

	err := Collect(context.Background(), &interrupted, "/tmp/unused", "test", func(ctx context.Context) ([]byte, error) {
		t.Error("function should not be called when interrupted")
		return nil, nil
	})
	if err == nil {
		t.Error("expected error when interrupted")
	}
}

func TestTimeoutContext(t *testing.T) {
	t.Setenv("CMD_TIMEOUT", "1")
	ctx, cancel := TimeoutContext(context.Background())
	defer cancel()

	deadline, ok := ctx.Deadline()
	if !ok {
		t.Fatal("expected deadline to be set")
	}
	remaining := time.Until(deadline)
	if remaining > 2*time.Second || remaining < 0 {
		t.Errorf("unexpected remaining time: %v", remaining)
	}
}
