package cli

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/redhat-developer/rhdh-must-gather/internal/obfuscate"
)

const (
	envObfuscateWorkers     = "RHDH_OBFUSCATE_WORKERS"
	defaultObfuscateWorkers = 2
)

func newObfuscateCmd() *cobra.Command {
	var input, output, reportDir, config string
	var workers int

	cmd := &cobra.Command{
		Use:    "obfuscate",
		Short:  "Obfuscate an existing must-gather directory",
		Hidden: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if !cmd.Flags().Changed("workers") {
				w, err := obfuscateWorkers()
				if err != nil {
					return err
				}
				workers = w
			}
			if workers == 0 {
				return nil
			}
			return obfuscate.Clean(config, input, output, reportDir, workers)
		},
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	flags := cmd.Flags()
	flags.StringVar(&config, "config", "", "Path to the must-gather-clean config file")
	flags.StringVar(&input, "input", "", "Directory of the collected must-gather")
	flags.StringVar(&output, "output", "", "Directory for the obfuscated output")
	flags.StringVar(&reportDir, "report-dir", "", "Directory for report.yaml, which must stay out of the published gather")
	flags.IntVar(&workers, "workers", defaultObfuscateWorkers, "Number of must-gather-clean workers")
	for _, name := range []string{"config", "input", "output", "report-dir"} {
		if err := cmd.MarkFlagRequired(name); err != nil {
			panic(err)
		}
	}
	return cmd
}

// runCleanSubprocess re-executes this binary so must-gather-clean's klog.Exitf
// cannot terminate the collector. That Exitf runs on the library's own error
// goroutine, so a BehaviorOnFatal hook in this process does not catch it.
// The parent keeps the original tree when the child fails.
func runCleanSubprocess(configPath, inputPath, outputPath, reportDir string) error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("finding gather executable: %w", err)
	}
	workers, err := obfuscateWorkers()
	if err != nil {
		return err
	}
	if workers == 0 {
		return nil
	}
	cmd := exec.Command(exe, obfuscateCommandArgs(configPath, inputPath, outputPath, reportDir, workers)...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("must-gather-clean failed: %w", err)
	}
	return nil
}

func obfuscateCommandArgs(configPath, inputPath, outputPath, reportDir string, workers int) []string {
	return []string{
		"obfuscate",
		"--config", configPath,
		"--input", inputPath,
		"--output", outputPath,
		"--report-dir", reportDir,
		"--workers", strconv.Itoa(workers),
	}
}

// obfuscateWorkers returns the number of parallel workers for must-gather-clean.
// Default is 2 because the obfuscation subprocess shares its cgroup memory
// limit with the parent gather process, and each worker loads entire YAML/JSON
// files into memory (os.ReadFile) for Kubernetes resource detection. Using
// runtime.GOMAXPROCS (host CPU count, not the container limit) caused OOM
// kills in the 512Mi init container. Override with RHDH_OBFUSCATE_WORKERS;
// set to 0 to skip obfuscation, or "all" to use all available CPUs.
func obfuscateWorkers() (int, error) {
	s := os.Getenv(envObfuscateWorkers)
	if s == "" {
		return defaultObfuscateWorkers, nil
	}
	if strings.EqualFold(s, "all") {
		return runtime.GOMAXPROCS(0), nil
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, fmt.Errorf("%s=%q: must be a non-negative integer or \"all\"", envObfuscateWorkers, s)
	}
	if n < 0 {
		return 0, fmt.Errorf("%s=%d: must be a non-negative integer or \"all\"", envObfuscateWorkers, n)
	}
	return n, nil
}
