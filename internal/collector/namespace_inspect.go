package collector

import (
	"bufio"
	"context"
	"fmt"
	"os"
	osExec "os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"helm.sh/helm/v4/pkg/action"
	"helm.sh/helm/v4/pkg/release"

	"github.com/redhat-developer/rhdh-must-gather/internal/exec"
	"github.com/redhat-developer/rhdh-must-gather/internal/log"
)

type NamespaceInspect struct{}

func (n *NamespaceInspect) Name() string { return "namespace-inspect" }

func (n *NamespaceInspect) Run(ctx context.Context, cfg *Config) error {
	log.Info("Starting Namespace inspect collection...")
	outDir := filepath.Join(cfg.BasePath, "namespace-inspect")
	_ = os.MkdirAll(outDir, 0o755)

	if _, err := osExec.LookPath("oc"); err != nil {
		log.Warn("'oc' command not found. Namespace inspect requires OpenShift CLI (oc).")
		log.Warn("Skipping Namespace inspect. Install 'oc' to enable this feature.")
		_ = os.WriteFile(filepath.Join(outDir, "skipped.txt"),
			[]byte("oc command not available - Namespace inspect skipped\n"), 0o644)
		return nil
	}

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
	n.writeSummary(outDir, namespaces, cfg.WithSecrets)

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

func (n *NamespaceInspect) runInspect(ctx context.Context, cfg *Config, outDir string, namespaces []string) {
	args := []string{"adm", "inspect", "--dest-dir=" + outDir}
	for _, ns := range namespaces {
		args = append(args, "namespace/"+ns)
	}

	if since := os.Getenv("MUST_GATHER_SINCE"); since != "" {
		args = append(args, "--since="+since)
		log.Debug("Adding --since=%s to inspect command", since)
	}
	if sinceTime := os.Getenv("MUST_GATHER_SINCE_TIME"); sinceTime != "" {
		args = append(args, "--since-time="+sinceTime)
		log.Debug("Adding --since-time=%s to inspect command", sinceTime)
	}

	timeout := exec.Timeout() * time.Duration(len(namespaces))
	log.Debug("Using timeout: %s for %d namespace(s)", timeout, len(namespaces))

	inspCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	log.Info("Inspecting all namespaces in a single command: %s", strings.Join(namespaces, " "))

	cmd := osExec.CommandContext(inspCtx, "oc", args...)
	cmd.Env = cfg.Env

	logFile := filepath.Join(outDir, "inspect.log")
	out, err := cmd.CombinedOutput()
	_ = os.WriteFile(logFile, out, 0o644)
	if err != nil {
		log.Warn("Namespace inspect timed out or failed (timeout: %s)", timeout)
		f, fErr := os.OpenFile(logFile, os.O_APPEND|os.O_WRONLY, 0o644)
		if fErr == nil {
			_, _ = fmt.Fprintf(f, "\nInspection failed or timed out (%s for %d namespaces)\nTimestamp: %s\n",
				timeout, len(namespaces), time.Now().Format(time.RFC3339))
			_ = f.Close()
		}
	} else {
		log.Info("Completed inspection of all %d namespace(s)", len(namespaces))
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

func (n *NamespaceInspect) writeSummary(outDir string, namespaces []string, withSecrets bool) {
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
	fmt.Fprintf(&sb, "  MUST_GATHER_SINCE: %s\n", envOrNone("MUST_GATHER_SINCE"))
	fmt.Fprintf(&sb, "  MUST_GATHER_SINCE_TIME: %s\n", envOrNone("MUST_GATHER_SINCE_TIME"))
	sb.WriteString("\nData collected per namespace:\n")
	sb.WriteString("  - All Kubernetes resources (YAML definitions)\n")
	sb.WriteString("  - Pod logs (current and previous)\n")
	sb.WriteString("  - Events\n")
	sb.WriteString("  - Resource descriptions\n")
	sb.WriteString("  - Network configurations\n")
	if withSecrets {
		sb.WriteString("  - Secrets (included and will be sanitized)\n")
	} else {
		sb.WriteString("  - Secrets (excluded - use --with-secrets to collect)\n")
	}
	fmt.Fprintf(&sb, "\nOutput directory: %s\n", outDir)

	_ = os.WriteFile(filepath.Join(outDir, "inspection-summary.txt"), []byte(sb.String()), 0o644)
}

func envOrNone(key string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return "none"
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
