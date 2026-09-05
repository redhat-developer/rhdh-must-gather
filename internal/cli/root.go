package cli

import (
	"fmt"
	"os"
	"strings"

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

func buildEnv(opts *gatherOptions) []string {
	env := os.Environ()
	set := func(key, value string) {
		for i, e := range env {
			if strings.HasPrefix(e, key+"=") {
				env[i] = key + "=" + value
				return
			}
		}
		env = append(env, key+"="+value)
	}

	if opts.namespaces != "" {
		set("RHDH_TARGET_NAMESPACES", opts.namespaces)
	}
	set("RHDH_WITH_SECRETS", boolStr(opts.withSecrets))
	set("RHDH_WITH_HEAP_DUMPS", boolStr(opts.withHeapDumps))
	set("RHDH_HEAP_DUMP_METHOD", opts.heapDumpMethod)
	if opts.heapDumpInstances != "" {
		set("RHDH_HEAP_DUMP_INSTANCES", opts.heapDumpInstances)
	}

	return env
}

func boolStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

func Execute() error {
	return newRootCmd().Execute()
}
