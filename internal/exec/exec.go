package exec

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/redhat-developer/rhdh-must-gather/internal/log"
)

const defaultTimeout = 90

func Timeout() time.Duration {
	if s := os.Getenv("CMD_TIMEOUT"); s != "" {
		if v, err := strconv.Atoi(s); err == nil && v > 0 {
			return time.Duration(v) * time.Second
		}
	}
	return defaultTimeout * time.Second
}

func TimeoutContext(parent context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(parent, Timeout())
}

type CollectFunc func(ctx context.Context) ([]byte, error)

func Collect(ctx context.Context, interrupted *atomic.Bool, path string, description string, fn CollectFunc) error {
	if interrupted != nil && interrupted.Load() {
		return fmt.Errorf("interrupted")
	}

	if description != "" {
		log.Info("\tCollecting: %s", description)
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("creating output directory: %w", err)
	}

	tctx, cancel := TimeoutContext(ctx)
	defer cancel()

	data, err := fn(tctx)
	if err != nil {
		writeErrorFile(path, description, err)
		log.Warn("\tCommand timed out or failed: %s — %v", description, err)
		return nil
	}

	return os.WriteFile(path, data, 0o644)
}

func writeErrorFile(path, description string, err error) {
	content := fmt.Sprintf("Command failed or timed out: %s\nTimestamp: %s\nTimeout: %s\n\n=== Error Details ===\n%s\n",
		description, time.Now().Format(time.RFC3339), Timeout(), err)
	_ = os.WriteFile(path, []byte(content), 0o644)
}
