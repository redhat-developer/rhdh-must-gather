package cli

import (
	"context"
	"fmt"
	"os"
	osExec "os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/redhat-developer/rhdh-must-gather/internal/collector"
	"github.com/redhat-developer/rhdh-must-gather/internal/exec"
	"github.com/redhat-developer/rhdh-must-gather/internal/kube"
	"github.com/redhat-developer/rhdh-must-gather/internal/log"
	"github.com/redhat-developer/rhdh-must-gather/internal/sanitize"
)

func runGather(cmd *cobra.Command, opts *gatherOptions) error {
	log.Init()

	basePath := os.Getenv("BASE_COLLECTION_PATH")
	if basePath == "" {
		basePath = "/must-gather"
	}
	logLevel := os.Getenv("LOG_LEVEL")
	if logLevel == "" {
		logLevel = "info"
	}

	if err := os.MkdirAll(basePath, 0o755); err != nil {
		return fmt.Errorf("creating output directory: %w", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var interrupted atomic.Bool

	defer func() {
		log.Info("done with data collection. Now sanitizing data...")
		sanitize.Run(basePath, &interrupted)
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		interrupted.Store(true)
		cancel()
		log.Warn("Interrupt requested, stopping after current step...")
	}()
	defer signal.Stop(sigCh)

	log.Info("Starting RHDH must-gather collection...")
	log.Info("Output directory: %s", basePath)
	log.Info("Log level: %s", logLevel)

	ver := getVersion()
	versionFile := filepath.Join(basePath, "version")
	if err := os.WriteFile(versionFile, []byte("rhdh-must-gather\n"+ver+"\n"), 0o644); err != nil {
		return fmt.Errorf("writing version file: %w", err)
	}

	kubeClient, err := kube.NewClient()
	if err != nil {
		return fmt.Errorf("failed to create Kubernetes client: %w", err)
	}

	env := buildEnv(opts)

	collectorCfg := &collector.Config{
		Client:        kubeClient,
		BasePath:      basePath,
		Interrupted:   &interrupted,
		WithSecrets:   opts.withSecrets,
		WithHeapDumps: opts.withHeapDumps,
		Env:           env,
	}

	scripts := buildScriptList(cmd, opts)
	log.Info("running the following collectors: %s", strings.Join(scripts, " "))

	if opts.withSecrets {
		log.Warn("Secret collection enabled - sensitive data will be included (and sanitized)")
	} else {
		log.Info("Secret collection disabled by default (use --with-secrets to enable)")
	}
	if opts.withHeapDumps {
		heapTimeout := getEnvDefault("HEAP_DUMP_TIMEOUT", "600")
		if opts.heapDumpInstances != "" {
			log.Warn("Heap dump collection enabled (method: %s, timeout: %ss, instances: %s)",
				opts.heapDumpMethod, heapTimeout, opts.heapDumpInstances)
		} else {
			log.Warn("Heap dump collection enabled (method: %s, timeout: %ss, all instances)",
				opts.heapDumpMethod, heapTimeout)
		}
		log.Warn("Heap snapshots block the Node.js event loop. Pods with short liveness probe timeouts may restart.")
		log.Warn("Consider increasing failureThreshold or timeoutSeconds on liveness probes before collecting.")
	}
	if opts.namespaces != "" {
		log.Info("Limiting collection to namespaces: %s", opts.namespaces)
	}

	for _, name := range scripts {
		if interrupted.Load() {
			break
		}
		c, ok := collector.Registry[name]
		if !ok {
			log.Warn("Unknown collector %q, skipping", name)
			continue
		}
		log.Info("running %s", c.Name())
		if err := c.Run(ctx, collectorCfg); err != nil {
			log.Warn("Failed to run %s: %v, continuing with next collector...", c.Name(), err)
		}
	}

	if !interrupted.Load() {
		collectPodLogs(ctx, kubeClient, basePath)
	}

	syscall.Sync()
	return nil
}

// collectPodLogs collects logs from the must-gather pod itself when running
// inside a pod (replaces logs.sh).
func collectPodLogs(ctx context.Context, client *kube.Client, basePath string) {
	nsFile := "/var/run/secrets/kubernetes.io/serviceaccount/namespace"
	nsBytes, err := os.ReadFile(nsFile)
	if err != nil {
		return
	}
	ns := strings.TrimSpace(string(nsBytes))
	podName := os.Getenv("POD_NAME")
	if podName == "" {
		return
	}

	log.Info("Collecting must-gather pod logs...")
	kubectl := "kubectl"
	if _, err := osExec.LookPath("oc"); err == nil {
		kubectl = "oc"
	}
	tctx, tcancel := exec.TimeoutContext(ctx)
	defer tcancel()
	out, err := osExec.CommandContext(tctx, kubectl, "logs", "--timestamps=true",
		"-n", ns, podName, "-c", "gather").CombinedOutput()
	if err != nil {
		log.Warn("Failed to collect must-gather pod logs: %v", err)
		return
	}
	_ = os.WriteFile(filepath.Join(basePath, "must-gather.log"), out, 0o644)
}

func getEnvDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
