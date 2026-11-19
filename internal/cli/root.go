package cli

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/redhat-developer/rhdh-must-gather/internal/collector"
	"github.com/redhat-developer/rhdh-must-gather/internal/kube"
	"github.com/redhat-developer/rhdh-must-gather/internal/log"
)

type gatherOptions struct {
	namespaces        string
	withSecrets       bool
	withHeapDumps     bool
	heapDumpMethod    string
	heapDumpInstances string
	since             string
	sinceTime         string
	clientFactory     func() (*kube.Client, error)
	cleanFunc         func(configPath, inputPath, outputPath, reportDir string) error
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
			warnDeprecatedCollectorFlags(cmd)
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
	flags.StringVar(&opts.heapDumpMethod, "heap-dump-method", "inspector", "Heap dump collection method: inspector or sigusr2 (sigusr2 is deprecated and will be removed in a future release)")
	flags.StringVar(&opts.heapDumpInstances, "heap-dump-instances", "", "Comma-separated list of instance names to collect heap dumps from")
	flags.StringVar(&opts.since, "since", "", "Only collect logs newer than a relative duration (e.g. 5s, 2m, 3h)")
	flags.StringVar(&opts.sinceTime, "since-time", "", "Only collect logs after a specific date (RFC3339, e.g. 2006-01-02T15:04:05Z)")

	for _, r := range collector.All() {
		switch r.RunMode {
		case collector.Default:
			flags.Bool("without-"+r.Name, false, flagDesc(r, "Skip "+r.Name+" data collection"))
		case collector.OptIn:
			flags.Bool("with-"+r.Name, false, flagDesc(r, "Include "+r.Name+" data collection"))
			// Aliases are only honoured for OptIn collectors, so only register
			// them there; a flag nobody reads is worse than no flag.
			for _, alias := range r.FlagAliases {
				flags.Bool(alias, false, "Deprecated: use --with-"+r.Name+" instead")
				_ = flags.MarkHidden(alias) // cannot fail: registered on the line above
			}
		}
	}

	cmd.SetVersionTemplate("rhdh-must-gather {{.Version}}\n")
	// The hidden obfuscate command makes this a parent command. Without an
	// explicit Args func, Cobra then rejects positional tokens as unknown commands.
	cmd.Args = cobra.ArbitraryArgs
	cmd.AddCommand(newObfuscateCmd())

	return cmd
}

func buildScriptList(cmd *cobra.Command) []string {
	regs := collector.InRunOrder()
	scripts := make([]string, 0, len(regs))
	for _, r := range regs {
		if collectorEnabled(cmd, r) {
			scripts = append(scripts, r.Name)
		}
	}
	return scripts
}

// collectorEnabled reports whether r should run for the given command line.
func collectorEnabled(cmd *cobra.Command, r collector.Registration) bool {
	switch r.RunMode {
	case collector.AlwaysOn:
		return true
	case collector.Default:
		excluded, _ := cmd.Flags().GetBool("without-" + r.Name)
		return !excluded
	case collector.OptIn:
		included, _ := cmd.Flags().GetBool("with-" + r.Name)
		for _, alias := range r.FlagAliases {
			if v, _ := cmd.Flags().GetBool(alias); v {
				included = true
			}
		}
		return included
	}
	return false
}

// flagDesc returns the help text for a collector's flag.
func flagDesc(r collector.Registration, fallback string) string {
	if r.FlagDesc != "" {
		return r.FlagDesc
	}
	return fallback
}

// warnDeprecatedCollectorFlags reports use of deprecated collector flag aliases
// (e.g. --cluster-info). It is called once per invocation so the warning is
// printed even when the replacement flag was passed as well.
//
// Aliases only exist for OptIn collectors — TestRegistryIntegrity rejects any
// other combination — so the replacement flag is always --with-<name>.
func warnDeprecatedCollectorFlags(cmd *cobra.Command) {
	for _, r := range collector.All() {
		for _, alias := range r.FlagAliases {
			if v, _ := cmd.Flags().GetBool(alias); v {
				log.Warn("--%s is deprecated, use --with-%s instead", alias, r.Name)
			}
		}
	}
}

func Execute() error {
	return newRootCmd().Execute()
}
