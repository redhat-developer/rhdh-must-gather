package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	corev1 "k8s.io/api/core/v1"

	"github.com/redhat-developer/rhdh-must-gather/internal/collector"
	"github.com/redhat-developer/rhdh-must-gather/internal/kube"
	"github.com/redhat-developer/rhdh-must-gather/internal/log"
	"github.com/redhat-developer/rhdh-must-gather/internal/namespace"
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
	sanitizeStop := make(chan struct{})

	defer func() {
		log.Info("done with data collection. Now sanitizing data...")
		sanitize.Run(basePath, sanitizeStop)
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		interrupted.Store(true)
		cancel()
		log.Warn("Interrupt requested, stopping after current step...")
		// A second signal during sanitization stops it immediately
		<-sigCh
		close(sanitizeStop)
		log.Warn("Second interrupt, aborting sanitization...")
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

	since, sinceTime := resolveSince(opts)

	collectorCfg := &collector.Config{
		Client:            kubeClient,
		BasePath:          basePath,
		Interrupted:       &interrupted,
		WithSecrets:       opts.withSecrets,
		WithHeapDumps:     opts.withHeapDumps,
		Since:             since,
		SinceTime:         sinceTime,
		TargetNamespaces:  resolveNamespaces(opts),
		HeapDumpMethod:    resolveHeapDumpMethod(cmd, opts),
		HeapDumpInstances: resolveHeapDumpInstances(opts),
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
		if collectorCfg.HeapDumpInstances != "" {
			log.Warn("Heap dump collection enabled (method: %s, timeout: %ss, instances: %s)",
				collectorCfg.HeapDumpMethod, heapTimeout, collectorCfg.HeapDumpInstances)
		} else {
			log.Warn("Heap dump collection enabled (method: %s, timeout: %ss, all instances)",
				collectorCfg.HeapDumpMethod, heapTimeout)
		}
		log.Warn("Heap snapshots block the Node.js event loop. Pods with short liveness probe timeouts may restart.")
		log.Warn("Consider increasing failureThreshold or timeoutSeconds on liveness probes before collecting.")
	}
	if len(collectorCfg.TargetNamespaces) > 0 {
		log.Info("Limiting collection to namespaces: %s", strings.Join(collectorCfg.TargetNamespaces, ", "))
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
	timestamps := true
	req := client.Clientset.CoreV1().Pods(ns).GetLogs(podName, &corev1.PodLogOptions{
		Container:  "gather",
		Timestamps: timestamps,
	})
	stream, err := req.Stream(ctx)
	if err != nil {
		log.Warn("Failed to collect must-gather pod logs: %v", err)
		return
	}
	defer func() { _ = stream.Close() }()

	destPath := filepath.Join(basePath, "must-gather.log")
	tmpPath := destPath + ".tmp"
	f, err := os.Create(tmpPath)
	if err != nil {
		log.Warn("Failed to create must-gather log file: %v", err)
		return
	}
	_, copyErr := io.Copy(f, stream)
	closeErr := f.Close()
	if copyErr != nil || closeErr != nil {
		_ = os.Remove(tmpPath)
		log.Warn("Failed to write must-gather pod logs: copy=%v close=%v", copyErr, closeErr)
		return
	}
	_ = os.Rename(tmpPath, destPath)
}

// resolveSince returns the since duration and sinceTime string. CLI flags
// take precedence as a group: if either flag is set, both env vars are
// ignored. Otherwise both env vars are read. The env vars MUST_GATHER_SINCE
// and MUST_GATHER_SINCE_TIME are set by "oc adm must-gather --since=..." when
// running inside a must-gather pod.
func resolveSince(opts *gatherOptions) (time.Duration, string) {
	since := opts.since
	sinceTime := opts.sinceTime

	// CLI flags were validated in PreRunE; if either is set, use only CLI.
	// Otherwise fall back to env vars.
	if since == "" && sinceTime == "" {
		since = os.Getenv("MUST_GATHER_SINCE")
		sinceTime = os.Getenv("MUST_GATHER_SINCE_TIME")
	}

	// Env vars may conflict; pick since over since-time if both are set.
	if since != "" && sinceTime != "" {
		log.Warn("Both MUST_GATHER_SINCE and MUST_GATHER_SINCE_TIME are set; using MUST_GATHER_SINCE")
		sinceTime = ""
	}

	var d time.Duration
	if since != "" {
		parsed, err := time.ParseDuration(since)
		if err != nil {
			log.Warn("Ignoring invalid since value %q: %v", since, err)
		} else if parsed <= 0 {
			log.Warn("Ignoring since value %q: must be a positive duration", since)
		} else if parsed < time.Second {
			log.Warn("Ignoring since value %q: must be at least 1s", since)
		} else {
			d = parsed
			log.Info("Log collection limited to last %s", d)
		}
	}
	if sinceTime != "" {
		if _, err := time.Parse(time.RFC3339, sinceTime); err == nil {
			log.Info("Log collection limited to logs after %s", sinceTime)
		} else {
			log.Warn("Ignoring invalid since-time value %q: %v", sinceTime, err)
			sinceTime = ""
		}
	}
	return d, sinceTime
}

func resolveNamespaces(opts *gatherOptions) []string {
	raw := opts.namespaces
	if raw == "" {
		raw = os.Getenv("RHDH_TARGET_NAMESPACES")
	}
	return namespace.ParseNamespaces(raw)
}

func resolveHeapDumpMethod(cmd *cobra.Command, opts *gatherOptions) string {
	if cmd.Flags().Changed("heap-dump-method") {
		return opts.heapDumpMethod
	}
	if m := os.Getenv("RHDH_HEAP_DUMP_METHOD"); m != "" {
		return m
	}
	return opts.heapDumpMethod
}

func resolveHeapDumpInstances(opts *gatherOptions) string {
	if opts.heapDumpInstances != "" {
		return opts.heapDumpInstances
	}
	return os.Getenv("RHDH_HEAP_DUMP_INSTANCES")
}

func getEnvDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
