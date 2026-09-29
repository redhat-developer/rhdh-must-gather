package cli

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/redhat-developer/rhdh-must-gather/internal/obfuscate"
)

func newObfuscateCmd() *cobra.Command {
	var input, output, reportDir, config string
	var workers int

	cmd := &cobra.Command{
		Use:    "obfuscate",
		Short:  "Obfuscate an existing must-gather directory",
		Hidden: true,
		RunE: func(cmd *cobra.Command, args []string) error {
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
	flags.IntVar(&workers, "workers", runtime.GOMAXPROCS(0), "Number of must-gather-clean workers")
	for _, name := range []string{"config", "input", "output", "report-dir"} {
		if err := cmd.MarkFlagRequired(name); err != nil {
			panic(err)
		}
	}
	return cmd
}

// runCleanSubprocess re-executes this binary so must-gather-clean's klog.Exitf
// cannot terminate the collector. The parent keeps the original tree when the
// child fails.
func runCleanSubprocess(configPath, inputPath, outputPath, reportDir string) error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("finding gather executable: %w", err)
	}
	workers := runtime.GOMAXPROCS(0)
	if workers < 1 {
		workers = 1
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
