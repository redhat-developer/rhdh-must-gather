package collector

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"

	"go.yaml.in/yaml/v3"
	"helm.sh/helm/v4/pkg/action"
	"helm.sh/helm/v4/pkg/chart"
	"helm.sh/helm/v4/pkg/release"

	"github.com/redhat-developer/rhdh-must-gather/internal/log"
)

type Helm struct{}

func (h *Helm) Name() string { return "helm" }

var (
	rhdhPatternRE      = regexp.MustCompile(`(?i)(backstage|rhdh|developer-hub)`)
	rhdhImagePatternRE = regexp.MustCompile(`(?i)(quay\.io/rhdh|registry\.redhat\.io/rhdh|ghcr\.io/backstage/backstage)`)
	mustGatherRE       = regexp.MustCompile(`(?i)must-gather`)
)

func (h *Helm) Run(ctx context.Context, cfg *Config) error {
	log.Info("Starting Helm data collection...")
	helmDir := filepath.Join(cfg.BasePath, "helm")
	_ = os.MkdirAll(helmDir, 0o755)

	processedNS := make(map[string]bool)
	processedWorkloads := make(map[string]bool)

	// Phase 1: Native Helm releases
	log.Info("Phase 1: Detecting native Helm releases...")
	totalNative := h.gatherNativeReleases(ctx, cfg, helmDir, processedNS, processedWorkloads)

	// Phase 2: Standalone Helm deployments
	log.Info("Phase 2: Detecting standalone Helm deployments...")
	totalStandalone := h.gatherStandaloneDeployments(ctx, cfg, helmDir, processedNS, processedWorkloads)

	if totalNative == 0 && totalStandalone == 0 {
		log.Warn("No RHDH-related Helm releases found (native or standalone)")
		_ = os.WriteFile(filepath.Join(helmDir, "no-releases.txt"),
			[]byte("No RHDH-related Helm releases found (native or standalone)\n"), 0o644)
	}

	log.Success("Helm data collection completed. Found: %d native release(s), %d standalone deployment(s)", totalNative, totalStandalone)
	return nil
}

func (h *Helm) gatherNativeReleases(ctx context.Context, cfg *Config, helmDir string, processedNS map[string]bool, processedWorkloads map[string]bool) int {
	actionCfg, err := newHelmActionConfig(cfg, "")
	if err != nil {
		log.Warn("Failed to initialize Helm action config: %v", err)
		_ = os.WriteFile(filepath.Join(helmDir, "all-rhdh-releases.txt"),
			[]byte(fmt.Sprintf("Failed to initialize Helm: %v\n", err)), 0o644)
		return 0
	}

	listAction := action.NewList(actionCfg)
	listAction.AllNamespaces = true
	if ns := cfg.Namespaces(); ns != nil {
		listAction.AllNamespaces = false
	}

	allReleases, err := listAction.Run()
	if err != nil {
		log.Warn("Failed to list Helm releases: %v", err)
		writeCollectError(filepath.Join(helmDir, "all-rhdh-releases.txt"), "helm list", err)
		return 0
	}

	var rhdhReleases []release.Accessor
	for _, rel := range allReleases {
		acc, err := release.NewAccessor(rel)
		if err != nil {
			continue
		}
		chartName := chartNameFromAccessor(acc)
		if rhdhPatternRE.MatchString(chartName) && !mustGatherRE.MatchString(chartName) {
			if cfg.ShouldInclude(acc.Namespace()) {
				rhdhReleases = append(rhdhReleases, acc)
			}
		}
	}

	h.writeReleasesTable(filepath.Join(helmDir, "all-rhdh-releases.txt"), rhdhReleases)

	if len(rhdhReleases) == 0 {
		log.Info("No native Helm releases found, will check for standalone Helm deployments...")
		return 0
	}

	// Collect namespace data for release namespaces
	for _, acc := range rhdhReleases {
		ns := acc.Namespace()
		if processedNS[ns] {
			continue
		}
		log.Info("--> Processing namespace %s containing at least one Helm release", ns)
		nsDir := filepath.Join(helmDir, "releases", "ns="+ns)
		_ = os.MkdirAll(nsDir, 0o755)
		CollectNamespaceData(ctx, cfg, ns, nsDir, cfg.WithSecrets)
		processedNS[ns] = true
	}

	// Process each release
	for _, acc := range rhdhReleases {
		ns := acc.Namespace()
		name := acc.Name()
		log.Info("--> Processing Helm release %s in namespace %s", name, ns)

		releaseDir := filepath.Join(helmDir, "releases", "ns="+ns, name)
		_ = os.MkdirAll(releaseDir, 0o755)

		h.collectReleaseData(ctx, cfg, ns, name, releaseDir, processedWorkloads)
	}

	return len(rhdhReleases)
}

func (h *Helm) collectReleaseData(ctx context.Context, cfg *Config, ns, name, releaseDir string, processedWorkloads map[string]bool) {
	nsCfg, err := newHelmActionConfig(cfg, ns)
	if err != nil {
		writeCollectError(filepath.Join(releaseDir, "error.txt"), "init helm config for "+ns, err)
		return
	}

	// Get values
	getValues := action.NewGetValues(nsCfg)
	vals, err := getValues.Run(name)
	if err != nil {
		writeCollectError(filepath.Join(releaseDir, "values.yaml"), "helm get values "+name, err)
	} else {
		writeResource(filepath.Join(releaseDir, "values.yaml"), vals)
	}

	getValuesAll := action.NewGetValues(nsCfg)
	getValuesAll.AllValues = true
	allVals, err := getValuesAll.Run(name)
	if err != nil {
		writeCollectError(filepath.Join(releaseDir, "all-values.yaml"), "helm get values --all "+name, err)
	} else {
		writeResource(filepath.Join(releaseDir, "all-values.yaml"), allVals)
	}

	// Get release (manifest, hooks, notes)
	getRel := action.NewGet(nsCfg)
	rel, err := getRel.Run(name)
	if err != nil {
		writeCollectError(filepath.Join(releaseDir, "manifest.yaml"), "helm get "+name, err)
		writeCollectError(filepath.Join(releaseDir, "hooks.yaml"), "helm get "+name, err)
		writeCollectError(filepath.Join(releaseDir, "notes.txt"), "helm get "+name, err)
	} else {
		relAcc, err := release.NewAccessor(rel)
		if err != nil {
			writeCollectError(filepath.Join(releaseDir, "manifest.yaml"), "access release "+name, err)
			return
		}

		manifest := relAcc.Manifest()
		if !cfg.WithSecrets {
			manifest = filterSecretsFromYAML(manifest)
		}
		_ = os.WriteFile(filepath.Join(releaseDir, "manifest.yaml"), []byte(manifest), 0o644)

		hooks := relAcc.Hooks()
		var hookManifests []string
		for _, hook := range hooks {
			hookAcc, err := release.NewHookAccessor(hook)
			if err != nil {
				continue
			}
			hookManifests = append(hookManifests, hookAcc.Manifest())
		}
		hooksContent := strings.Join(hookManifests, "\n---\n")
		if !cfg.WithSecrets {
			hooksContent = filterSecretsFromYAML(hooksContent)
		}
		_ = os.WriteFile(filepath.Join(releaseDir, "hooks.yaml"), []byte(hooksContent), 0o644)

		notes := relAcc.Notes()
		_ = os.WriteFile(filepath.Join(releaseDir, "notes.txt"), []byte(notes), 0o644)

		// Extract workload names from manifest for collection
		deployName, stsName := extractWorkloadNames(manifest)
		if deployName != "" {
			ref := WorkloadRef{Namespace: ns, Name: deployName, Kind: KindDeployment, InstanceName: name}
			if err := CollectWorkload(ctx, cfg, ref, filepath.Join(releaseDir, "deployment")); err != nil {
				log.Warn("Failed to collect workload %s/%s: %v", ns, deployName, err)
			}
			processedWorkloads[ns+"/"+deployName] = true
		}
		if stsName != "" {
			if err := CollectDBStatefulSet(ctx, cfg, ns, stsName, releaseDir); err != nil {
				log.Warn("Failed to collect DB statefulset: %v", err)
			}
			processedWorkloads[ns+"/"+stsName] = true
		}
	}

	// History
	histAction := action.NewHistory(nsCfg)
	histReleases, err := histAction.Run(name)
	if err != nil {
		writeCollectError(filepath.Join(releaseDir, "history.txt"), "helm history "+name, err)
	} else {
		h.writeHistoryText(filepath.Join(releaseDir, "history.txt"), histReleases)
		writeResource(filepath.Join(releaseDir, "history.yaml"), h.historyToMap(histReleases))
	}

	// Status
	statusAction := action.NewStatus(nsCfg)
	statusRel, err := statusAction.Run(name)
	if err != nil {
		writeCollectError(filepath.Join(releaseDir, "status.txt"), "helm status "+name, err)
	} else {
		statusAcc, err := release.NewAccessor(statusRel)
		if err == nil {
			_ = os.WriteFile(filepath.Join(releaseDir, "status.txt"),
				[]byte(formatReleaseStatus(statusAcc)), 0o644)
		}
	}
}

func (h *Helm) gatherStandaloneDeployments(ctx context.Context, cfg *Config, helmDir string, processedNS map[string]bool, processedWorkloads map[string]bool) int {
	standaloneDir := filepath.Join(helmDir, "standalone")
	client := cfg.Client.Clientset

	selector := "app.kubernetes.io/managed-by=Helm"
	namespaces := cfg.Namespaces()

	var allDeps []appsv1.Deployment
	var allSTS []appsv1.StatefulSet

	if namespaces == nil {
		deps, err := client.AppsV1().Deployments("").List(ctx, metav1.ListOptions{LabelSelector: selector})
		if err == nil {
			allDeps = deps.Items
		}
		sts, err := client.AppsV1().StatefulSets("").List(ctx, metav1.ListOptions{LabelSelector: selector})
		if err == nil {
			allSTS = sts.Items
		}
	} else {
		for _, ns := range namespaces {
			deps, err := client.AppsV1().Deployments(ns).List(ctx, metav1.ListOptions{LabelSelector: selector})
			if err == nil {
				allDeps = append(allDeps, deps.Items...)
			}
			sts, err := client.AppsV1().StatefulSets(ns).List(ctx, metav1.ListOptions{LabelSelector: selector})
			if err == nil {
				allSTS = append(allSTS, sts.Items...)
			}
		}
	}

	type standaloneWorkload struct {
		namespace string
		name      string
		kind      WorkloadKind
	}

	var workloads []standaloneWorkload
	for _, dep := range allDeps {
		if isRHDHHelmWorkload(dep.Labels, dep.Spec.Template.Spec) && !mustGatherRE.MatchString(dep.Name) {
			key := dep.Namespace + "/" + dep.Name
			if !processedWorkloads[key] && cfg.ShouldInclude(dep.Namespace) {
				workloads = append(workloads, standaloneWorkload{dep.Namespace, dep.Name, KindDeployment})
			}
		}
	}
	for _, sts := range allSTS {
		if isRHDHHelmWorkload(sts.Labels, sts.Spec.Template.Spec) && !mustGatherRE.MatchString(sts.Name) {
			key := sts.Namespace + "/" + sts.Name
			if !processedWorkloads[key] && cfg.ShouldInclude(sts.Namespace) {
				workloads = append(workloads, standaloneWorkload{sts.Namespace, sts.Name, KindStatefulSet})
			}
		}
	}

	if len(workloads) == 0 {
		log.Debug("No additional RHDH workloads found via standalone deployment detection")
		return 0
	}

	log.Info("Found potential standalone RHDH deployments, filtering out already-processed ones...")
	count := 0

	for _, wl := range workloads {
		log.Info("--> Processing standalone Helm deployment: %s in namespace %s", wl.name, wl.namespace)
		count++

		wlDir := filepath.Join(standaloneDir, "ns="+wl.namespace, wl.name)
		_ = os.MkdirAll(wlDir, 0o755)

		if !processedNS[wl.namespace] {
			nsDir := filepath.Join(standaloneDir, "ns="+wl.namespace)
			_ = os.MkdirAll(nsDir, 0o755)
			CollectNamespaceData(ctx, cfg, wl.namespace, nsDir, cfg.WithSecrets)
			processedNS[wl.namespace] = true
		}

		h.writeStandaloneNote(filepath.Join(wlDir, "standalone-note.txt"), wl.namespace, wl.name)
		h.writeHelmMetadata(ctx, cfg, wl.namespace, wl.name, wl.kind, filepath.Join(wlDir, "helm-metadata.txt"))

		ref := WorkloadRef{Namespace: wl.namespace, Name: wl.name, Kind: wl.kind, InstanceName: wl.name}
		subDir := "deployment"
		if wl.kind == KindStatefulSet {
			subDir = "statefulset"
		}
		if err := CollectWorkload(ctx, cfg, ref, filepath.Join(wlDir, subDir)); err != nil {
			log.Warn("Failed to collect workload %s/%s: %v", wl.namespace, wl.name, err)
		}

		h.collectDependentServices(ctx, cfg, wl.namespace, wl.name, wl.kind, wlDir, processedWorkloads)
		processedWorkloads[wl.namespace+"/"+wl.name] = true
	}

	if count > 0 {
		log.Info("Standalone Helm deployments were found and collected in: %s", standaloneDir)
	}

	return count
}

func (h *Helm) collectDependentServices(ctx context.Context, cfg *Config, ns, mainName string, mainKind WorkloadKind, wlDir string, processedWorkloads map[string]bool) {
	client := cfg.Client.Clientset

	var instanceLabel string
	switch mainKind {
	case KindDeployment:
		dep, err := client.AppsV1().Deployments(ns).Get(ctx, mainName, metav1.GetOptions{})
		if err == nil {
			instanceLabel = dep.Labels["app.kubernetes.io/instance"]
		}
	case KindStatefulSet:
		sts, err := client.AppsV1().StatefulSets(ns).Get(ctx, mainName, metav1.GetOptions{})
		if err == nil {
			instanceLabel = sts.Labels["app.kubernetes.io/instance"]
		}
	}

	if instanceLabel == "" {
		return
	}

	log.Debug("Looking for dependent services with instance=%s in namespace %s", instanceLabel, ns)
	selector := "app.kubernetes.io/managed-by=Helm,app.kubernetes.io/instance=" + instanceLabel

	deps, _ := client.AppsV1().Deployments(ns).List(ctx, metav1.ListOptions{LabelSelector: selector})
	if deps != nil {
		for _, dep := range deps.Items {
			if dep.Name == mainName || processedWorkloads[ns+"/"+dep.Name] {
				continue
			}
			log.Info("    --> Collecting dependent service: %s (Deployment)", dep.Name)
			depDir := filepath.Join(wlDir, "dependencies", dep.Name)
			_ = os.MkdirAll(depDir, 0o755)

			writeResource(filepath.Join(depDir, "deployment.yaml"), &dep)
			describeResource(ctx, filepath.Join(depDir, "deployment.describe.txt"), "deployment", ns, dep.Name)
			h.collectDependentLogs(ctx, cfg, ns, dep.Name, instanceLabel, &dep.Spec.Selector.MatchLabels, depDir)
			processedWorkloads[ns+"/"+dep.Name] = true
		}
	}

	stsList, _ := client.AppsV1().StatefulSets(ns).List(ctx, metav1.ListOptions{LabelSelector: selector})
	if stsList != nil {
		for _, sts := range stsList.Items {
			if sts.Name == mainName || processedWorkloads[ns+"/"+sts.Name] {
				continue
			}
			log.Info("    --> Collecting dependent service: %s (StatefulSet)", sts.Name)
			depDir := filepath.Join(wlDir, "dependencies", sts.Name)
			_ = os.MkdirAll(depDir, 0o755)

			writeResource(filepath.Join(depDir, "statefulset.yaml"), &sts)
			describeResource(ctx, filepath.Join(depDir, "statefulset.describe.txt"), "statefulset", ns, sts.Name)
			h.collectDependentLogs(ctx, cfg, ns, sts.Name, instanceLabel, &sts.Spec.Selector.MatchLabels, depDir)
			processedWorkloads[ns+"/"+sts.Name] = true
		}
	}
}

func (h *Helm) collectDependentLogs(ctx context.Context, cfg *Config, ns, depName, instanceLabel string, matchLabels *map[string]string, depDir string) {
	client := cfg.Client.Clientset

	sel := fmt.Sprintf("app.kubernetes.io/instance=%s,app.kubernetes.io/name=%s", instanceLabel, depName)
	pods, err := client.CoreV1().Pods(ns).List(ctx, metav1.ListOptions{LabelSelector: sel})
	if err != nil || len(pods.Items) == 0 {
		if matchLabels != nil {
			sel = labels.Set(*matchLabels).String()
			pods, err = client.CoreV1().Pods(ns).List(ctx, metav1.ListOptions{LabelSelector: sel})
		}
	}

	if err != nil || pods == nil || len(pods.Items) == 0 {
		return
	}

	for i := range pods.Items {
		pod := &pods.Items[i]
		CollectPodLogs(ctx, cfg, ns, pod, filepath.Join(depDir, "logs", "pod="+pod.Name))
	}
}

// --- Helpers ---

func newHelmActionConfig(cfg *Config, namespace string) (*action.Configuration, error) {
	actionCfg := new(action.Configuration)
	getter := newRESTClientGetter(cfg.Client.Config, namespace)
	if err := actionCfg.Init(getter, namespace, os.Getenv("HELM_DRIVER")); err != nil {
		return nil, err
	}
	return actionCfg, nil
}

func isRHDHHelmWorkload(workloadLabels map[string]string, podSpec corev1.PodSpec) bool {
	for _, key := range []string{"helm.sh/chart", "app.kubernetes.io/name", "app.kubernetes.io/instance"} {
		if val, ok := workloadLabels[key]; ok && rhdhPatternRE.MatchString(val) {
			return true
		}
	}

	for _, c := range podSpec.Containers {
		if rhdhImagePatternRE.MatchString(c.Image) {
			return true
		}
	}
	for _, c := range podSpec.InitContainers {
		if rhdhImagePatternRE.MatchString(c.Image) {
			return true
		}
	}
	return false
}

func filterSecretsFromYAML(input string) string {
	decoder := yaml.NewDecoder(strings.NewReader(input))
	var buf bytes.Buffer
	encoder := yaml.NewEncoder(&buf)
	encoder.SetIndent(2)

	first := true
	for {
		var node yaml.Node
		err := decoder.Decode(&node)
		if err == io.EOF {
			break
		}
		if err != nil {
			return input
		}
		if isSecretDocument(&node) {
			continue
		}
		if !first {
			buf.WriteString("---\n")
		}
		if err := encoder.Encode(&node); err != nil {
			return input
		}
		first = false
	}
	encoder.Close()
	return buf.String()
}

func isSecretDocument(node *yaml.Node) bool {
	if node.Kind != yaml.DocumentNode || len(node.Content) == 0 {
		return false
	}
	mapping := node.Content[0]
	if mapping.Kind != yaml.MappingNode {
		return false
	}
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		key := mapping.Content[i]
		val := mapping.Content[i+1]
		if key.Value == "kind" && val.Value == "Secret" {
			return true
		}
	}
	return false
}

func extractWorkloadNames(manifest string) (deployName, stsName string) {
	decoder := yaml.NewDecoder(strings.NewReader(manifest))
	for {
		var doc struct {
			Kind     string `yaml:"kind"`
			Metadata struct {
				Name string `yaml:"name"`
			} `yaml:"metadata"`
		}
		if err := decoder.Decode(&doc); err != nil {
			break
		}
		switch doc.Kind {
		case "Deployment":
			if deployName == "" {
				deployName = doc.Metadata.Name
			}
		case "StatefulSet":
			if stsName == "" {
				stsName = doc.Metadata.Name
			}
		}
	}
	return
}

func chartNameFromAccessor(acc release.Accessor) string {
	chartAcc, err := chart.NewAccessor(acc.Chart())
	if err != nil {
		return ""
	}
	return chartAcc.Name()
}

func chartLabel(acc release.Accessor) string {
	chartAcc, err := chart.NewAccessor(acc.Chart())
	if err != nil {
		return "unknown"
	}
	meta := chartAcc.MetadataAsMap()
	appVersion, _ := meta["appVersion"].(string)
	return fmt.Sprintf("%s-%s", chartAcc.Name(), appVersion)
}

func chartAppVersion(acc release.Accessor) string {
	chartAcc, err := chart.NewAccessor(acc.Chart())
	if err != nil {
		return ""
	}
	meta := chartAcc.MetadataAsMap()
	v, _ := meta["appVersion"].(string)
	return v
}

func (h *Helm) writeReleasesTable(path string, releases []release.Accessor) {
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	var sb strings.Builder
	fmt.Fprintf(&sb, "%-30s  %-30s  %-10s  %-30s  %-12s  %s\n",
		"NAMESPACE", "NAME", "REVISION", "CHART", "STATUS", "UPDATED")
	for _, acc := range releases {
		fmt.Fprintf(&sb, "%-30s  %-30s  %-10d  %-30s  %-12s  %s\n",
			acc.Namespace(), acc.Name(), acc.Version(),
			chartLabel(acc),
			acc.Status(), acc.DeployedAt().Format(time.RFC3339))
	}
	if len(releases) == 0 {
		sb.WriteString("No RHDH-related Helm releases found\n")
	}
	_ = os.WriteFile(path, []byte(sb.String()), 0o644)
}

func (h *Helm) writeHistoryText(path string, releases []release.Releaser) {
	var sb strings.Builder
	fmt.Fprintf(&sb, "%-10s  %-30s  %-12s  %-30s  %s\n",
		"REVISION", "UPDATED", "STATUS", "CHART", "DESCRIPTION")
	for _, rel := range releases {
		acc, err := release.NewAccessor(rel)
		if err != nil {
			continue
		}
		fmt.Fprintf(&sb, "%-10d  %-30s  %-12s  %-30s  %s\n",
			acc.Version(), acc.DeployedAt().Format(time.RFC3339),
			acc.Status(), chartLabel(acc), acc.Notes())
	}
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	_ = os.WriteFile(path, []byte(sb.String()), 0o644)
}

func (h *Helm) historyToMap(releases []release.Releaser) []map[string]any {
	var result []map[string]any
	for _, rel := range releases {
		acc, err := release.NewAccessor(rel)
		if err != nil {
			continue
		}
		result = append(result, map[string]any{
			"revision":    acc.Version(),
			"updated":     acc.DeployedAt().Format(time.RFC3339),
			"status":      acc.Status(),
			"chart":       chartLabel(acc),
			"app_version": chartAppVersion(acc),
			"description": acc.Notes(),
		})
	}
	return result
}

func formatReleaseStatus(acc release.Accessor) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "NAME: %s\n", acc.Name())
	fmt.Fprintf(&sb, "LAST DEPLOYED: %s\n", acc.DeployedAt().Format(time.RFC3339))
	fmt.Fprintf(&sb, "NAMESPACE: %s\n", acc.Namespace())
	fmt.Fprintf(&sb, "STATUS: %s\n", acc.Status())
	fmt.Fprintf(&sb, "REVISION: %d\n", acc.Version())
	fmt.Fprintf(&sb, "CHART: %s\n", chartLabel(acc))
	fmt.Fprintf(&sb, "APP VERSION: %s\n", chartAppVersion(acc))
	if notes := acc.Notes(); notes != "" {
		fmt.Fprintf(&sb, "\nNOTES:\n%s\n", notes)
	}
	return sb.String()
}

func (h *Helm) writeStandaloneNote(path, ns, name string) {
	var sb strings.Builder
	sb.WriteString("# Standalone RHDH Helm Deployment\n")
	sb.WriteString("# ================================\n")
	sb.WriteString("# This RHDH instance was detected via workload labels/annotations rather than\n")
	sb.WriteString("# native Helm release tracking. This typically means it was deployed by rendering\n")
	sb.WriteString("# the Helm chart using 'helm template' and applying the manifests directly with\n")
	sb.WriteString("# kubectl/oc apply.\n")
	sb.WriteString("#\n")
	sb.WriteString("# As a result, Helm-specific data (values, history, hooks) is not available.\n")
	sb.WriteString("# However, we've collected the workload configuration and runtime data.\n")
	sb.WriteString("#\n")
	sb.WriteString("# Detection method: Matched labels/images indicating RHDH deployment\n")
	fmt.Fprintf(&sb, "# Namespace: %s\n", ns)
	fmt.Fprintf(&sb, "# Workload: %s\n", name)
	fmt.Fprintf(&sb, "# Collected at: %s\n\n", time.Now().UTC().Format("2006-01-02T15:04:05Z"))
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	_ = os.WriteFile(path, []byte(sb.String()), 0o644)
}

func (h *Helm) writeHelmMetadata(ctx context.Context, cfg *Config, ns, name string, kind WorkloadKind, path string) {
	client := cfg.Client.Clientset
	var workloadLabels map[string]string

	switch kind {
	case KindDeployment:
		dep, err := client.AppsV1().Deployments(ns).Get(ctx, name, metav1.GetOptions{})
		if err == nil {
			workloadLabels = dep.Labels
		}
	case KindStatefulSet:
		sts, err := client.AppsV1().StatefulSets(ns).Get(ctx, name, metav1.GetOptions{})
		if err == nil {
			workloadLabels = sts.Labels
		}
	}

	if workloadLabels == nil {
		_ = os.WriteFile(path, []byte("Could not extract metadata\n"), 0o644)
		return
	}

	getLabel := func(key string) string {
		if v, ok := workloadLabels[key]; ok {
			return v
		}
		return "N/A"
	}

	var sb strings.Builder
	sb.WriteString("# Helm Metadata\n")
	sb.WriteString("# =============\n")
	fmt.Fprintf(&sb, "Helm Chart: %s\n", getLabel("helm.sh/chart"))
	fmt.Fprintf(&sb, "App Name: %s\n", getLabel("app.kubernetes.io/name"))
	fmt.Fprintf(&sb, "App Instance: %s\n", getLabel("app.kubernetes.io/instance"))
	fmt.Fprintf(&sb, "App Version: %s\n", getLabel("app.kubernetes.io/version"))
	fmt.Fprintf(&sb, "Managed By: %s\n", getLabel("app.kubernetes.io/managed-by"))
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	_ = os.WriteFile(path, []byte(sb.String()), 0o644)
}
