package cli

import (
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"

	"github.com/spf13/cobra"
)

func runGather(cmd *cobra.Command, opts *gatherOptions) error {
	basePath := os.Getenv("BASE_COLLECTION_PATH")
	if basePath == "" {
		basePath = "/must-gather"
	}
	logLevel := os.Getenv("LOG_LEVEL")
	if logLevel == "" {
		logLevel = "info"
	}

	scriptDir, err := resolveScriptDir()
	if err != nil {
		return err
	}

	if err := os.MkdirAll(basePath, 0o755); err != nil {
		return fmt.Errorf("creating output directory: %w", err)
	}

	env := buildEnv(opts)

	defer func() {
		logInfo("done with data collection. Now sanitizing data...")
		_ = runScript(scriptDir, "sanitize", env, basePath)
	}()

	var interrupted atomic.Bool
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		interrupted.Store(true)
		logWarn("Interrupt requested, stopping after current step...")
	}()
	defer signal.Stop(sigCh)

	logInfo("Starting RHDH must-gather collection...")
	logInfo("Output directory: %s", basePath)
	logInfo("Log level: %s", logLevel)

	ver := getVersion()
	versionFile := filepath.Join(basePath, "version")
	if err := os.WriteFile(versionFile, []byte("rhdh-must-gather\n"+ver+"\n"), 0o644); err != nil {
		return fmt.Errorf("writing version file: %w", err)
	}

	if err := runInit(scriptDir, env); err != nil {
		logError("Failed to initialize must-gather environment")
		return err
	}

	scripts := buildScriptList(cmd, opts)
	logInfo("running the following scripts: %s", strings.Join(scripts, " "))

	if opts.withSecrets {
		logWarn("Secret collection enabled - sensitive data will be included (and sanitized)")
	} else {
		logInfo("Secret collection disabled by default (use --with-secrets to enable)")
	}
	if opts.withHeapDumps {
		heapTimeout := getEnvDefault("HEAP_DUMP_TIMEOUT", "600")
		if opts.heapDumpInstances != "" {
			logWarn("Heap dump collection enabled (method: %s, timeout: %ss, instances: %s)",
				opts.heapDumpMethod, heapTimeout, opts.heapDumpInstances)
		} else {
			logWarn("Heap dump collection enabled (method: %s, timeout: %ss, all instances)",
				opts.heapDumpMethod, heapTimeout)
		}
		logWarn("Heap snapshots block the Node.js event loop. Pods with short liveness probe timeouts may restart.")
		logWarn("Consider increasing failureThreshold or timeoutSeconds on liveness probes before collecting.")
	}
	if opts.namespaces != "" {
		logInfo("Limiting collection to namespaces: %s", opts.namespaces)
	}

	for _, script := range scripts {
		if interrupted.Load() {
			break
		}
		name := "gather_" + script
		logInfo("running %s", name)
		exitCode := runScript(scriptDir, name, env)
		if exitCode == 130 || exitCode == 143 {
			return &exitError{code: exitCode}
		}
		if exitCode != 0 {
			logWarn("Failed to run %s, continuing with next script...", name)
		}
	}

	if !interrupted.Load() {
		logInfo("running logs")
		exitCode := runScript(scriptDir, "logs.sh", env)
		if exitCode == 130 || exitCode == 143 {
			return &exitError{code: exitCode}
		}
		if exitCode != 0 {
			logWarn("Failed to run logs.sh, continuing...")
		}
	}

	syscall.Sync()
	return nil
}

func resolveScriptDir() (string, error) {
	if dir := os.Getenv("RHDH_SCRIPT_DIR"); dir != "" {
		if hasCommonSh(dir) {
			return dir, nil
		}
		return "", fmt.Errorf("RHDH_SCRIPT_DIR=%s does not contain common.sh", dir)
	}

	exe, err := os.Executable()
	if err == nil {
		dir := filepath.Dir(exe)
		if hasCommonSh(dir) {
			return dir, nil
		}
	}

	cwd, err := os.Getwd()
	if err == nil {
		dir := filepath.Join(cwd, "collection-scripts")
		if hasCommonSh(dir) {
			return dir, nil
		}
	}

	return "", fmt.Errorf("cannot find collection scripts: set RHDH_SCRIPT_DIR or run from the project root")
}

func hasCommonSh(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, "common.sh"))
	return err == nil
}

func runInit(scriptDir string, env []string) error {
	script := fmt.Sprintf("source '%s/common.sh' && init_must_gather", scriptDir)
	c := exec.Command("bash", "-c", script)
	c.Env = env
	c.Stdout = os.Stderr
	c.Stderr = os.Stderr
	return c.Run()
}

// runScript executes a script from scriptDir. Any extra args are passed as
// positional arguments (used by sanitize to receive the base path).
func runScript(scriptDir, name string, env []string, args ...string) int {
	path := filepath.Join(scriptDir, name)
	c := exec.Command(path, args...)
	c.Env = env
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	if err := c.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return exitErr.ExitCode()
		}
		return 1
	}
	return 0
}

func getEnvDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

type exitError struct {
	code int
}

func (e *exitError) Error() string {
	return fmt.Sprintf("exit code %d", e.code)
}
