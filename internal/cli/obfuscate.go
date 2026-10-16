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

const envObfuscateWorkers = "OBFUSCATE_WORKERS"

func defaultObfuscateWorkers() int {
	return max(1, effectiveCPUs()/2)
}

// effectiveCPUs returns the container's CPU quota (cgroup v2 then v1),
// falling back to runtime.GOMAXPROCS which reports host CPUs.
func effectiveCPUs() int {
	if n := cgroupCPUQuota(); n > 0 {
		return n
	}
	return runtime.GOMAXPROCS(0)
}

func cgroupCPUQuota() int {
	if data, err := os.ReadFile("/sys/fs/cgroup/cpu.max"); err == nil {
		if n := parseCPUMax(strings.TrimSpace(string(data))); n > 0 {
			return n
		}
	}
	quota, err1 := os.ReadFile("/sys/fs/cgroup/cpu/cpu.cfs_quota_us")
	period, err2 := os.ReadFile("/sys/fs/cgroup/cpu/cpu.cfs_period_us")
	if err1 == nil && err2 == nil {
		return parseCPUQuotaPeriod(strings.TrimSpace(string(quota)), strings.TrimSpace(string(period)))
	}
	return 0
}

// parseCPUMax parses cgroup v2 cpu.max ("quota period" or "max period").
func parseCPUMax(s string) int {
	fields := strings.Fields(s)
	if len(fields) != 2 || fields[0] == "max" {
		return 0
	}
	quota, err := strconv.Atoi(fields[0])
	if err != nil || quota <= 0 {
		return 0
	}
	period, err := strconv.Atoi(fields[1])
	if err != nil || period <= 0 {
		return 0
	}
	return quota / period
}

// parseCPUQuotaPeriod parses cgroup v1 cpu.cfs_quota_us / cpu.cfs_period_us.
func parseCPUQuotaPeriod(quotaStr, periodStr string) int {
	quota, err := strconv.Atoi(quotaStr)
	if err != nil || quota <= 0 {
		return 0
	}
	period, err := strconv.Atoi(periodStr)
	if err != nil || period <= 0 {
		return 0
	}
	return quota / period
}

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
	flags.IntVar(&workers, "workers", defaultObfuscateWorkers(), "Number of must-gather-clean workers")
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

func obfuscateWorkers() (int, error) {
	s := os.Getenv(envObfuscateWorkers)
	if s == "" {
		return defaultObfuscateWorkers(), nil
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
