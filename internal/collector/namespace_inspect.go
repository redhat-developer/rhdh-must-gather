package collector

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/cli-runtime/pkg/genericiooptions"
	"k8s.io/klog/v2"
	kcmdutil "k8s.io/kubectl/pkg/cmd/util"
	"helm.sh/helm/v4/pkg/action"
	"helm.sh/helm/v4/pkg/release"

	"github.com/openshift/oc/pkg/cli/admin/inspect"
	"github.com/redhat-developer/rhdh-must-gather/internal/log"
)

type NamespaceInspect struct{}

func (n *NamespaceInspect) Name() string { return "namespace-inspect" }

func (n *NamespaceInspect) Run(ctx context.Context, cfg *Config) error {
	log.Info("Starting Namespace inspect collection...")
	outDir := filepath.Join(cfg.BasePath, "namespace-inspect")
	_ = os.MkdirAll(outDir, 0o755)

	namespaces := n.resolveNamespaces(ctx, cfg)
	if len(namespaces) == 0 {
		log.Warn("No RHDH deployments or orchestrator components found in cluster")
		_ = os.WriteFile(filepath.Join(outDir, "no-namespaces.txt"),
			[]byte("No namespaces detected\n"), 0o644)
		return nil
	}

	log.Info("Found %d namespace(s) to inspect: %s", len(namespaces), strings.Join(namespaces, " "))

	n.runInspect(ctx, cfg, outDir, namespaces)
	n.removeSecrets(cfg, outDir)
	n.writeSummary(cfg, outDir, namespaces)

	log.Info("Namespace inspect collection completed.")
	return nil
}

func (n *NamespaceInspect) resolveNamespaces(ctx context.Context, cfg *Config) []string {
	targetNS := cfg.Namespaces()
	if len(targetNS) > 0 {
		log.Info("Inspecting targeted namespaces: %s", strings.Join(targetNS, ", "))
		return targetNS
	}

	log.Info("Auto-detecting namespaces with RHDH deployments...")
	nsSet := make(map[string]struct{})

	n.detectHelmNamespaces(ctx, cfg, nsSet)
	n.detectStandaloneNamespaces(ctx, cfg, nsSet)
	n.detectOperatorNamespaces(ctx, cfg, nsSet)
	n.detectCRNamespaces(ctx, cfg, nsSet)
	n.addOrchestratorNamespaces(cfg, nsSet)

	namespaces := make([]string, 0, len(nsSet))
	for ns := range nsSet {
		namespaces = append(namespaces, ns)
	}
	sort.Strings(namespaces)
	return namespaces
}

func (n *NamespaceInspect) detectHelmNamespaces(_ context.Context, cfg *Config, nsSet map[string]struct{}) {
	actionCfg, err := newHelmActionConfig(cfg, "")
	if err != nil {
		return
	}
	listAction := action.NewList(actionCfg)
	listAction.AllNamespaces = true

	releases, err := listAction.Run()
	if err != nil {
		return
	}

	for _, rel := range releases {
		acc, aErr := release.NewAccessor(rel)
		if aErr != nil {
			continue
		}
		chartName := strings.ToLower(chartNameFromAccessor(acc))
		if strings.Contains(chartName, "backstage") || strings.Contains(chartName, "rhdh") || strings.Contains(chartName, "developer-hub") {
			nsSet[acc.Namespace()] = struct{}{}
		}
	}
}

func (n *NamespaceInspect) detectStandaloneNamespaces(ctx context.Context, cfg *Config, nsSet map[string]struct{}) {
	client := cfg.Client.Clientset
	deps, err := client.AppsV1().Deployments("").List(ctx, metav1.ListOptions{
		LabelSelector: "app.kubernetes.io/managed-by=Helm",
	})
	if err != nil {
		return
	}

	rdhPattern := []string{"backstage", "rhdh", "developer-hub"}
	imagePattern := []string{"quay.io/rhdh", "registry.redhat.io/rhdh", "ghcr.io/backstage/backstage"}

	for _, dep := range deps.Items {
		labels := dep.Labels
		if matchesAnyPattern(labels["helm.sh/chart"], rdhPattern) ||
			matchesAnyPattern(labels["app.kubernetes.io/name"], rdhPattern) ||
			matchesAnyPattern(labels["app.kubernetes.io/instance"], rdhPattern) ||
			containsImagePattern(dep.Spec.Template.Spec.Containers, imagePattern) ||
			containsImagePattern(dep.Spec.Template.Spec.InitContainers, imagePattern) {
			nsSet[dep.Namespace] = struct{}{}
		}
	}
}

func (n *NamespaceInspect) detectOperatorNamespaces(ctx context.Context, cfg *Config, nsSet map[string]struct{}) {
	client := cfg.Client.Clientset
	deps, err := client.AppsV1().Deployments("").List(ctx, metav1.ListOptions{
		LabelSelector: "app=rhdh-operator",
	})
	if err != nil {
		return
	}
	for _, dep := range deps.Items {
		nsSet[dep.Namespace] = struct{}{}
		log.Info("Auto-detected RHDH operator namespace: %s", dep.Namespace)
	}
}

func (n *NamespaceInspect) detectCRNamespaces(ctx context.Context, cfg *Config, nsSet map[string]struct{}) {
	dynClient := cfg.Client.Dynamic
	if dynClient == nil {
		return
	}
	version, err := cfg.Client.PreferredVersion("rhdh.redhat.com")
	if err != nil {
		return
	}
	gvr := schema.GroupVersionResource{
		Group: "rhdh.redhat.com", Version: version, Resource: "backstages",
	}
	crs, err := dynClient.Resource(gvr).Namespace("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return
	}
	for _, cr := range crs.Items {
		nsSet[cr.GetNamespace()] = struct{}{}
	}
}

func (n *NamespaceInspect) addOrchestratorNamespaces(cfg *Config, nsSet map[string]struct{}) {
	nsFile := filepath.Join(cfg.BasePath, "orchestrator", "detected-namespaces.txt")
	f, err := os.Open(nsFile)
	if err != nil {
		return
	}
	defer func() { _ = f.Close() }()

	log.Info("Adding orchestrator-related namespaces to inspection...")
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		ns := strings.TrimSpace(scanner.Text())
		if ns == "" {
			continue
		}
		if _, exists := nsSet[ns]; !exists {
			nsSet[ns] = struct{}{}
			log.Info("Added orchestrator namespace: %s", ns)
		}
	}
}

func (n *NamespaceInspect) runInspect(_ context.Context, cfg *Config, outDir string, namespaces []string) {
	args := make([]string, len(namespaces))
	for i, ns := range namespaces {
		args[i] = "namespace/" + ns
	}

	log.Info("Inspecting all namespaces: %s", strings.Join(namespaces, " "))

	// Redirect klog and stderr to a log file to avoid noisy warnings from the
	// oc inspect library (e.g., "the server doesn't have a resource type ...").
	klogFile, klogCleanup := redirectKlog(outDir)
	defer klogCleanup()

	streams := genericiooptions.IOStreams{In: os.Stdin, Out: klogFile, ErrOut: klogFile}

	// Use NewCmdInspect to get a cobra command with properly bound flags,
	// including the unexported since/sinceTime fields on InspectOptions.
	// Setting flag values via cmd.Flags().Set() writes through the bound
	// pointers into the internal InspectOptions.
	cmd := inspect.NewCmdInspect(streams)
	_ = cmd.Flags().Set("dest-dir", outDir)
	if cfg.Since > 0 {
		_ = cmd.Flags().Set("since", cfg.Since.String())
	}
	if cfg.SinceTime != "" {
		_ = cmd.Flags().Set("since-time", cfg.SinceTime)
	}
	cmd.SetArgs(args)

	// The oc inspect command uses kcmdutil.CheckErr which calls os.Exit on
	// error. Override the fatal handler to convert errors into a panic that
	// we recover from, so errors are non-fatal to our process.
	if err := runInspectCmd(cmd); err != nil {
		log.Info("Namespace inspect completed with non-fatal errors (see inspect.log for details)")
		_, _ = fmt.Fprintln(klogFile, err)
	} else {
		log.Info("Completed inspection of all %d namespace(s)", len(namespaces))
	}
}

type inspectFatalError string

func (e inspectFatalError) Error() string { return string(e) }

func runInspectCmd(cmd *cobra.Command) (retErr error) {
	kcmdutil.BehaviorOnFatal(func(msg string, _ int) {
		panic(inspectFatalError(msg))
	})
	defer kcmdutil.DefaultBehaviorOnFatal()
	defer func() {
		if r := recover(); r != nil {
			if e, ok := r.(inspectFatalError); ok {
				retErr = e
			} else {
				panic(r)
			}
		}
	}()
	return cmd.Execute()
}

// redirectKlog sends klog output (used by the oc inspect library) to a file
// instead of stderr, and returns a cleanup function that restores klog to its
// original destination. klog defaults to logtostderr=true, which bypasses
// SetOutput, so we must disable it first.
func redirectKlog(outDir string) (io.Writer, func()) {
	logPath := filepath.Join(outDir, "inspect.log")
	f, err := os.Create(logPath)
	if err != nil {
		log.Warn("Failed to create inspect.log, inspect warnings will appear in stderr: %v", err)
		return os.Stderr, func() {}
	}
	klog.LogToStderr(false)
	klog.SetOutput(f)
	return f, func() {
		klog.SetOutput(os.Stderr)
		klog.LogToStderr(true)
		_ = f.Close()
	}
}

func (n *NamespaceInspect) removeSecrets(cfg *Config, outDir string) {
	if cfg.WithSecrets {
		log.Warn("Secrets included in collection - they will be sanitized automatically")
		return
	}
	log.Info("Removing secret files (use --with-secrets to collect them)")

	_ = filepath.WalkDir(outDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		name := d.Name()
		if d.IsDir() && name == "secrets" {
			_ = os.RemoveAll(path)
			return filepath.SkipDir
		}
		if !d.IsDir() && (name == "secrets.yaml" || name == "secrets") {
			_ = os.Remove(path)
		}
		return nil
	})
	log.Info("Secret files excluded from collection")
}

func (n *NamespaceInspect) writeSummary(cfg *Config, outDir string, namespaces []string) {
	var sb strings.Builder
	sb.WriteString("Namespace inspect Summary\n")
	sb.WriteString("============================\n\n")
	fmt.Fprintf(&sb, "Inspection timestamp: %s\n", time.Now().Format(time.RFC1123))
	fmt.Fprintf(&sb, "Number of namespaces inspected: %d\n\n", len(namespaces))
	sb.WriteString("Inspected namespaces:\n")
	for _, ns := range namespaces {
		fmt.Fprintf(&sb, "  - %s\n", ns)
	}
	sb.WriteString("\nTime constraints:\n")
	if cfg.Since > 0 {
		fmt.Fprintf(&sb, "  since: %s\n", cfg.Since)
	} else {
		sb.WriteString("  since: none\n")
	}
	if cfg.SinceTime != "" {
		fmt.Fprintf(&sb, "  since-time: %s\n", cfg.SinceTime)
	} else {
		sb.WriteString("  since-time: none\n")
	}
	sb.WriteString("\nData collected per namespace:\n")
	sb.WriteString("  - All Kubernetes resources (YAML definitions)\n")
	sb.WriteString("  - Pod logs (current and previous)\n")
	sb.WriteString("  - Events\n")
	sb.WriteString("  - Resource descriptions\n")
	sb.WriteString("  - Network configurations\n")
	if cfg.WithSecrets {
		sb.WriteString("  - Secrets (included and will be sanitized)\n")
	} else {
		sb.WriteString("  - Secrets (excluded - use --with-secrets to collect)\n")
	}
	fmt.Fprintf(&sb, "\nOutput directory: %s\n", outDir)

	_ = os.WriteFile(filepath.Join(outDir, "inspection-summary.txt"), []byte(sb.String()), 0o644)
}

func matchesAnyPattern(value string, patterns []string) bool {
	lower := strings.ToLower(value)
	for _, p := range patterns {
		if strings.Contains(lower, p) {
			return true
		}
	}
	return false
}

func containsImagePattern(containers []corev1.Container, patterns []string) bool {
	for _, c := range containers {
		lower := strings.ToLower(c.Image)
		for _, p := range patterns {
			if strings.Contains(lower, p) {
				return true
			}
		}
	}
	return false
}
