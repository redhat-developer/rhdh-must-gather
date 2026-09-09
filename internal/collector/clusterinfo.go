package collector

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/redhat-developer/rhdh-must-gather/internal/log"
)

type ClusterInfo struct{}

func (c *ClusterInfo) Name() string { return "cluster-info" }

func (c *ClusterInfo) Run(ctx context.Context, cfg *Config) error {
	outDir := filepath.Join(cfg.BasePath, "cluster-info")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return fmt.Errorf("creating cluster-info output directory: %w", err)
	}

	kubectl := findKubectl()
	if kubectl == "" {
		log.Warn("No kubectl or oc command available for cluster-info dump")
		return os.WriteFile(filepath.Join(outDir, "error.txt"),
			[]byte("No kubectl or oc command available\n"), 0o644)
	}

	cmd := exec.CommandContext(ctx, kubectl, "cluster-info", "dump",
		"--all-namespaces", "--output-directory="+outDir)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		log.Warn("cluster-info dump failed: %v", err)
	}

	return nil
}

func findKubectl() string {
	for _, name := range []string{"kubectl", "oc"} {
		if path, err := exec.LookPath(name); err == nil {
			return path
		}
	}
	return ""
}
