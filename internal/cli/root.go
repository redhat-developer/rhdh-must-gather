package cli

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"
)

var mandatoryScripts = []string{
	"platform",
	"helm",
	"operator",
	"orchestrator",
	"route",
	"ingress",
	"namespace-inspect",
}

type gatherOptions struct {
	namespaces        string
	withSecrets       bool
	withHeapDumps     bool
	heapDumpMethod    string
	heapDumpInstances string
	clusterInfo       bool
	since             string
	sinceTime         string
}

func newRootCmd() *cobra.Command {
	opts := &gatherOptions{}

	cmd := &cobra.Command{
		Use:   "gather",
		Short: "RHDH must-gather tool",
		Long: `A diagnostic data collection tool for Red Hat Developer Hub (RHDH) deployments
on Kubernetes and OpenShift clusters. Collects logs, configurations, and resources
from both Helm-based and Operator-managed RHDH instances.`,
		SilenceUsage:  true,
		SilenceErrors: true,
		Version:       getVersion(),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runGather(cmd, opts)
		},
		PreRunE: func(cmd *cobra.Command, args []string) error {
			method := opts.heapDumpMethod
			if method != "inspector" && method != "sigusr2" {
				return fmt.Errorf("--heap-dump-method must be 'inspector' or 'sigusr2', got %q", method)
			}
			if opts.since != "" && opts.sinceTime != "" {
				return fmt.Errorf("at most one of --since or --since-time may be specified")
			}
			if opts.since != "" {
				d, err := time.ParseDuration(opts.since)
				if err != nil {
					return fmt.Errorf("--since must be a valid Go duration (e.g. 5s, 2m, 3h): %w", err)
				}
				if d <= 0 {
					return fmt.Errorf("--since must be a positive duration, got %s", d)
				}
				if d < time.Second {
					return fmt.Errorf("--since must be at least 1s, got %s", d)
				}
			}
			if opts.sinceTime != "" {
				if _, err := time.Parse(time.RFC3339, opts.sinceTime); err != nil {
					return fmt.Errorf("--since-time must be a valid RFC3339 timestamp (e.g. 2006-01-02T15:04:05Z): %w", err)
				}
			}
			return nil
		},
	}

	cmd.FParseErrWhitelist.UnknownFlags = true

	flags := cmd.Flags()
	flags.StringVar(&opts.namespaces, "namespaces", "", "Collect data only from specified comma-separated namespaces")
	flags.BoolVar(&opts.withSecrets, "with-secrets", false, "Include Kubernetes Secrets in collection (opt-in, disabled by default)")
	flags.BoolVar(&opts.withHeapDumps, "with-heap-dumps", false, "Collect heap dumps from running backstage-backend processes")
	flags.StringVar(&opts.heapDumpMethod, "heap-dump-method", "inspector", "Heap dump collection method: inspector or sigusr2")
	flags.StringVar(&opts.heapDumpInstances, "heap-dump-instances", "", "Comma-separated list of instance names to collect heap dumps from")
	flags.BoolVar(&opts.clusterInfo, "cluster-info", false, "Collect cluster-wide diagnostic information")
	flags.StringVar(&opts.since, "since", "", "Only collect logs newer than a relative duration (e.g. 5s, 2m, 3h)")
	flags.StringVar(&opts.sinceTime, "since-time", "", "Only collect logs after a specific date (RFC3339, e.g. 2006-01-02T15:04:05Z)")

	for _, script := range mandatoryScripts {
		flags.Bool("without-"+script, false, "Skip "+script+" data collection")
	}

	cmd.SetVersionTemplate("rhdh-must-gather {{.Version}}\n")

	return cmd
}

func buildScriptList(cmd *cobra.Command, opts *gatherOptions) []string {
	scripts := make([]string, 0, len(mandatoryScripts)+1)
	for _, s := range mandatoryScripts {
		excluded, _ := cmd.Flags().GetBool("without-" + s)
		if !excluded {
			scripts = append(scripts, s)
		}
	}
	if opts.clusterInfo {
		scripts = append(scripts, "cluster-info")
	}
	return scripts
}

func Execute() error {
	return newRootCmd().Execute()
}
