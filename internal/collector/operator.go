package collector

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes"

	"github.com/redhat-developer/rhdh-must-gather/internal/log"
)

type Operator struct{}

func (o *Operator) Name() string { return "operator" }

var (
	csvGVR = schema.GroupVersionResource{
		Group: "operators.coreos.com", Version: "v1alpha1", Resource: "clusterserviceversions",
	}
	subscriptionGVR = schema.GroupVersionResource{
		Group: "operators.coreos.com", Version: "v1alpha1", Resource: "subscriptions",
	}
	installPlanGVR = schema.GroupVersionResource{
		Group: "operators.coreos.com", Version: "v1alpha1", Resource: "installplans",
	}
	operatorGroupGVR = schema.GroupVersionResource{
		Group: "operators.coreos.com", Version: "v1", Resource: "operatorgroups",
	}
	catalogSourceGVR = schema.GroupVersionResource{
		Group: "operators.coreos.com", Version: "v1alpha1", Resource: "catalogsources",
	}
	crdGVR = schema.GroupVersionResource{
		Group: "apiextensions.k8s.io", Version: "v1", Resource: "customresourcedefinitions",
	}
)

var rhdhNamePatterns = []string{"rhdh", "backstage", "developer-hub"}

func (o *Operator) Run(ctx context.Context, cfg *Config) error {
	log.Info("Starting Operator data collection...")
	outDir := filepath.Join(cfg.BasePath, "operator")
	_ = os.MkdirAll(outDir, 0o755)

	o.gatherOLM(ctx, cfg, outDir)
	o.gatherCRDs(ctx, cfg, outDir)
	o.gatherOperatorNamespaces(ctx, cfg, outDir)
	o.gatherBackstageCRs(ctx, cfg, outDir)

	log.Success("... Operator data collection done.")
	return nil
}

func (o *Operator) gatherOLM(ctx context.Context, cfg *Config, outDir string) {
	hasOLM, err := cfg.Client.HasAPIGroup("operators.coreos.com")
	if err != nil {
		log.Warn("Failed to check OLM API: %v", err)
		return
	}
	if !hasOLM {
		log.Info("OLM not available, skipping OLM collection")
		return
	}

	log.Info("Collecting OLM (Operator Lifecycle Manager) information...")
	olmDir := filepath.Join(outDir, "olm")
	_ = os.MkdirAll(olmDir, 0o755)

	type olmResource struct {
		gvr     schema.GroupVersionResource
		path    string
		desc    string
		columns []tableColumn
		filter  bool
	}

	resources := []olmResource{
		{csvGVR, "rhdh-csv-all.txt", "CSVs", csvColumns, true},
		{subscriptionGVR, "rhdh-subscriptions-all.txt", "Subscriptions", subscriptionColumns, true},
		{installPlanGVR, "installplans-all.txt", "InstallPlans", installPlanColumns, false},
		{operatorGroupGVR, "operatorgroups-all.txt", "OperatorGroups", operatorGroupColumns, false},
		{catalogSourceGVR, "catalogsources-all.txt", "CatalogSources", catalogSourceColumns, false},
	}

	for _, r := range resources {
		items, err := listDynamic(ctx, cfg, r.gvr)
		if err != nil {
			writeCollectError(filepath.Join(olmDir, r.path), "list "+r.desc, err)
			continue
		}
		if r.filter {
			items = filterRHDHResources(items)
		}
		writeDynamicTable(filepath.Join(olmDir, r.path), items, r.columns)
	}
}

func (o *Operator) gatherCRDs(ctx context.Context, cfg *Config, outDir string) {
	log.Info("\tCollecting RHDH-related Custom Resource Definitions...")
	crdsDir := filepath.Join(outDir, "crds")
	_ = os.MkdirAll(crdsDir, 0o755)

	allCRDs, err := cfg.Client.Dynamic.Resource(crdGVR).List(ctx, metav1.ListOptions{})
	if err != nil {
		writeCollectError(filepath.Join(crdsDir, "all-crds.txt"), "list CRDs", err)
	} else {
		var sb strings.Builder
		for _, crd := range allCRDs.Items {
			fmt.Fprintln(&sb, crd.GetName())
		}
		_ = os.WriteFile(filepath.Join(crdsDir, "all-crds.txt"), []byte(sb.String()), 0o644)
	}

	rhdhCRDs := []string{"backstages.rhdh.redhat.com"}
	for _, name := range rhdhCRDs {
		crd, err := cfg.Client.Dynamic.Resource(crdGVR).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			_ = os.WriteFile(filepath.Join(crdsDir, name+"--not-found.txt"),
				[]byte(fmt.Sprintf("CRD %s not found\n", name)), 0o644)
			continue
		}
		writeResource(filepath.Join(crdsDir, name+".yaml"), crd)
	}
}

func (o *Operator) gatherOperatorNamespaces(ctx context.Context, cfg *Config, outDir string) {
	client := cfg.Client.Clientset
	selector := "app=rhdh-operator"

	var allDeps []deploymentSummary
	namespaces := cfg.Namespaces()
	if namespaces == nil {
		list, err := client.AppsV1().Deployments("").List(ctx, metav1.ListOptions{LabelSelector: selector})
		if err == nil {
			for _, dep := range list.Items {
				allDeps = append(allDeps, deploymentSummary{
					Namespace: dep.Namespace, Name: dep.Name,
					Ready: dep.Status.ReadyReplicas, Desired: ptrVal(dep.Spec.Replicas),
					MatchLabels: dep.Spec.Selector.MatchLabels,
				})
			}
		}
	} else {
		for _, ns := range namespaces {
			list, err := client.AppsV1().Deployments(ns).List(ctx, metav1.ListOptions{LabelSelector: selector})
			if err == nil {
				for _, dep := range list.Items {
					allDeps = append(allDeps, deploymentSummary{
						Namespace: dep.Namespace, Name: dep.Name,
						Ready: dep.Status.ReadyReplicas, Desired: ptrVal(dep.Spec.Replicas),
						MatchLabels: dep.Spec.Selector.MatchLabels,
					})
				}
			}
		}
	}

	writeDeploymentSummaryTable(filepath.Join(outDir, "all-deployments.txt"), allDeps)

	nsSet := make(map[string]struct{})
	for _, dep := range allDeps {
		nsSet[dep.Namespace] = struct{}{}
	}

	if len(nsSet) == 0 {
		log.Info("No RHDH Operator namespaces found")
		return
	}

	for ns := range nsSet {
		if !cfg.ShouldInclude(ns) {
			log.Debug("Skipping operator namespace %s (not in target list)", ns)
			continue
		}

		log.Info("Processing operator namespace %s", ns)
		nsDir := filepath.Join(outDir, "ns="+ns)
		_ = os.MkdirAll(nsDir, 0o755)

		o.gatherNamespaceResources(ctx, cfg, ns, nsDir)
		o.gatherOperatorConfig(ctx, cfg, ns, nsDir)
		o.gatherOperatorDeployments(ctx, cfg, ns, nsDir)
		o.gatherOperatorLogs(ctx, cfg, ns, nsDir)
	}
}

func (o *Operator) gatherNamespaceResources(ctx context.Context, cfg *Config, ns, nsDir string) {
	client := cfg.Client.Clientset
	var sb strings.Builder

	pods, err := client.CoreV1().Pods(ns).List(ctx, metav1.ListOptions{})
	if err == nil {
		for _, pod := range pods.Items {
			ready := podReadyContainers(&pod)
			total := len(pod.Spec.Containers)
			fmt.Fprintf(&sb, "pod/%s   %d/%d   %s\n", pod.Name, ready, total, pod.Status.Phase)
		}
	}

	svcs, err := client.CoreV1().Services(ns).List(ctx, metav1.ListOptions{})
	if err == nil {
		for _, svc := range svcs.Items {
			fmt.Fprintf(&sb, "service/%s   %s   %s\n", svc.Name, svc.Spec.Type, svc.Spec.ClusterIP)
		}
	}

	deps, err := client.AppsV1().Deployments(ns).List(ctx, metav1.ListOptions{})
	if err == nil {
		for _, dep := range deps.Items {
			fmt.Fprintf(&sb, "deployment.apps/%s   %d/%d\n",
				dep.Name, dep.Status.ReadyReplicas, ptrVal(dep.Spec.Replicas))
		}
	}

	rsList, err := client.AppsV1().ReplicaSets(ns).List(ctx, metav1.ListOptions{})
	if err == nil {
		for _, rs := range rsList.Items {
			fmt.Fprintf(&sb, "replicaset.apps/%s   %d   %d\n",
				rs.Name, rs.Status.Replicas, rs.Status.ReadyReplicas)
		}
	}

	if sb.Len() == 0 {
		sb.WriteString("No resources found\n")
	}
	_ = os.WriteFile(filepath.Join(nsDir, "all-resources.txt"), []byte(sb.String()), 0o644)
}

func (o *Operator) gatherOperatorConfig(ctx context.Context, cfg *Config, ns, nsDir string) {
	log.Info("\tCollecting all Operator config ConfigMaps in %s...", ns)
	configsDir := filepath.Join(nsDir, "configs")
	_ = os.MkdirAll(configsDir, 0o755)

	client := cfg.Client.Clientset

	cmList, err := client.CoreV1().ConfigMaps(ns).List(ctx, metav1.ListOptions{})
	if err != nil {
		writeCollectError(filepath.Join(configsDir, "all-configmaps.txt"), "list configmaps", err)
	} else {
		var sb strings.Builder
		for _, cm := range cmList.Items {
			fmt.Fprintln(&sb, cm.Name)
		}
		_ = os.WriteFile(filepath.Join(configsDir, "all-configmaps.txt"), []byte(sb.String()), 0o644)
	}

	rhdhCMs := []string{"rhdh-default-config", "rhdh-plugin-deps"}
	for _, name := range rhdhCMs {
		cm, err := client.CoreV1().ConfigMaps(ns).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			_ = os.WriteFile(filepath.Join(configsDir, name+"--not-found.txt"),
				[]byte(fmt.Sprintf("CM %s not found\n", name)), 0o644)
			continue
		}
		writeResource(filepath.Join(configsDir, name+".yaml"), cm)
	}
}

func (o *Operator) gatherOperatorDeployments(ctx context.Context, cfg *Config, ns, nsDir string) {
	log.Info("\tCollecting Operator Deployments in %s...", ns)
	depsDir := filepath.Join(nsDir, "deployments")
	_ = os.MkdirAll(depsDir, 0o755)

	client := cfg.Client.Clientset
	selector := "app=rhdh-operator"

	allDeps, err := client.AppsV1().Deployments(ns).List(ctx, metav1.ListOptions{})
	if err == nil {
		var summaries []deploymentSummary
		for _, dep := range allDeps.Items {
			summaries = append(summaries, deploymentSummary{
				Name: dep.Name, Ready: dep.Status.ReadyReplicas, Desired: ptrVal(dep.Spec.Replicas),
			})
		}
		writeDeploymentSummaryTable(filepath.Join(depsDir, "all-deployments.txt"), summaries)
	}

	opDeps, err := client.AppsV1().Deployments(ns).List(ctx, metav1.ListOptions{LabelSelector: selector})
	if err != nil {
		writeCollectError(filepath.Join(depsDir, "app=rhdh-operator.yaml"), "list operator deployments", err)
		return
	}
	if len(opDeps.Items) > 0 {
		writeResource(filepath.Join(depsDir, "app=rhdh-operator.yaml"), opDeps)
	}

	for i := range opDeps.Items {
		dep := &opDeps.Items[i]
		collectRolloutHistory(ctx, cfg, ns, KindDeployment, dep.Spec.Selector.MatchLabels,
			filepath.Join(depsDir, dep.Name))
	}
}

func (o *Operator) gatherOperatorLogs(ctx context.Context, cfg *Config, ns, nsDir string) {
	log.Info("\tCollecting Operator Logs in %s...", ns)

	client := cfg.Client.Clientset
	pods, err := client.CoreV1().Pods(ns).List(ctx, metav1.ListOptions{LabelSelector: "app=rhdh-operator"})
	if err != nil {
		writeCollectError(filepath.Join(nsDir, "logs.txt"), "list operator pods for logs", err)
		return
	}
	if len(pods.Items) == 0 {
		return
	}

	writeAggregatedLogs(ctx, client, ns, pods.Items, false, filepath.Join(nsDir, "logs.txt"))
	writeAggregatedLogs(ctx, client, ns, pods.Items, true, filepath.Join(nsDir, "logs-previous.txt"))
}

func (o *Operator) gatherBackstageCRs(ctx context.Context, cfg *Config, outDir string) {
	log.Info("Searching for Backstage Custom Resources (CRs)...")
	crsDir := filepath.Join(outDir, "backstage-crs")
	_ = os.MkdirAll(crsDir, 0o755)

	version, err := cfg.Client.PreferredVersion("rhdh.redhat.com")
	if err != nil {
		log.Warn("Backstage CRD not available: %v", err)
		_ = os.WriteFile(filepath.Join(crsDir, "no-crs.txt"),
			[]byte("Backstage CRD not available\n"), 0o644)
		return
	}

	backstageGVR := schema.GroupVersionResource{
		Group: "rhdh.redhat.com", Version: version, Resource: "backstages",
	}

	items, err := listDynamic(ctx, cfg, backstageGVR)
	if err != nil {
		writeCollectError(filepath.Join(crsDir, "all-backstage-crs.txt"), "list Backstage CRs", err)
		return
	}

	writeDynamicTable(filepath.Join(crsDir, "all-backstage-crs.txt"), items, backstageCRColumns)

	if len(items) == 0 {
		log.Warn("No Backstage CR found")
		_ = os.WriteFile(filepath.Join(crsDir, "no-crs.txt"), []byte("No Backstage CR found\n"), 0o644)
		return
	}

	crsByNS := make(map[string][]unstructured.Unstructured)
	for _, item := range items {
		ns := item.GetNamespace()
		crsByNS[ns] = append(crsByNS[ns], item)
	}

	for ns, crs := range crsByNS {
		log.Info("--> Processing namespace %s containing at least one Backstage CR", ns)
		nsDir := filepath.Join(crsDir, "ns="+ns)
		_ = os.MkdirAll(nsDir, 0o755)

		CollectNamespaceData(ctx, cfg, ns, nsDir, cfg.WithSecrets)

		for _, cr := range crs {
			crName := cr.GetName()
			log.Info("--> Processing Backstage CR %s in namespace %s", crName, ns)

			crDir := filepath.Join(nsDir, crName)
			_ = os.MkdirAll(crDir, 0o755)

			writeResource(filepath.Join(crDir, crName+".yaml"), &cr)
			o.collectCRWorkloads(ctx, cfg, ns, crName, crDir, backstageGVR)
		}
	}
}

func (o *Operator) collectCRWorkloads(ctx context.Context, cfg *Config, ns, crName, crDir string, backstageGVR schema.GroupVersionResource) {
	client := cfg.Client.Clientset
	deployName := "backstage-" + crName
	stsName := "backstage-psql-" + crName

	_, depErr := client.AppsV1().Deployments(ns).Get(ctx, deployName, metav1.GetOptions{})
	_, stsErr := client.AppsV1().StatefulSets(ns).Get(ctx, deployName, metav1.GetOptions{})
	hasDeploy := depErr == nil
	hasSTS := stsErr == nil

	if hasDeploy && hasSTS {
		o.handleDualWorkload(ctx, cfg, ns, crName, deployName, crDir, backstageGVR)
	} else {
		kind := KindDeployment
		if !hasDeploy && hasSTS {
			kind = KindStatefulSet
		}
		ref := WorkloadRef{Namespace: ns, Name: deployName, Kind: kind, InstanceName: crName}
		if err := CollectWorkload(ctx, cfg, ref, filepath.Join(crDir, "deployment")); err != nil {
			log.Warn("Failed to collect workload %s/%s: %v", ns, deployName, err)
		}
	}

	if err := CollectDBStatefulSet(ctx, cfg, ns, stsName, crDir); err != nil {
		log.Warn("Failed to collect DB statefulset: %v", err)
	}
}

func (o *Operator) handleDualWorkload(ctx context.Context, cfg *Config, ns, crName, deployName, crDir string, backstageGVR schema.GroupVersionResource) {
	intendedKind := ""
	cr, err := cfg.Client.Dynamic.Resource(backstageGVR).Namespace(ns).Get(ctx, crName, metav1.GetOptions{})
	if err == nil {
		intendedKind, _, _ = nestedString(cr.Object, "spec", "deployment", "kind")
	}

	leftoverKind := "StatefulSet"
	if intendedKind == "StatefulSet" {
		leftoverKind = "Deployment"
	}

	log.Warn("Both a Deployment and a StatefulSet named '%s' exist in namespace '%s'.", deployName, ns)
	log.Warn("This may indicate a workload type migration where the previous %s was not cleaned up.", leftoverKind)
	if intendedKind != "" {
		log.Warn("The Backstage CR spec.deployment.kind is set to '%s'.", intendedKind)
	}

	warning := writeDualWorkloadWarning(ns, deployName, intendedKind, leftoverKind)
	_ = os.WriteFile(filepath.Join(crDir, "warning-dual-workload.txt"), []byte(warning), 0o644)

	depRef := WorkloadRef{Namespace: ns, Name: deployName, Kind: KindDeployment, InstanceName: crName}
	if err := CollectWorkload(ctx, cfg, depRef, filepath.Join(crDir, "deployment")); err != nil {
		log.Warn("Failed to collect deployment workload: %v", err)
	}

	stsRef := WorkloadRef{Namespace: ns, Name: deployName, Kind: KindStatefulSet, InstanceName: crName}
	if err := CollectWorkload(ctx, cfg, stsRef, filepath.Join(crDir, "rhdh-statefulset")); err != nil {
		log.Warn("Failed to collect statefulset workload: %v", err)
	}
}

// --- Helpers ---

func filterRHDHResources(items []unstructured.Unstructured) []unstructured.Unstructured {
	var filtered []unstructured.Unstructured
	for _, item := range items {
		if isRHDHRelated(item.GetName()) {
			filtered = append(filtered, item)
		}
	}
	return filtered
}

func isRHDHRelated(name string) bool {
	lower := strings.ToLower(name)
	for _, pattern := range rhdhNamePatterns {
		if strings.Contains(lower, pattern) {
			return true
		}
	}
	return false
}

type tableColumn struct {
	header string
	width  int
	fields []string
}

func writeDynamicTable(path string, items []unstructured.Unstructured, columns []tableColumn) {
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	var sb strings.Builder
	for _, col := range columns {
		fmt.Fprintf(&sb, "%-*s  ", col.width, col.header)
	}
	sb.WriteString("\n")
	for _, item := range items {
		for _, col := range columns {
			fmt.Fprintf(&sb, "%-*s  ", col.width, getFieldAsString(item.Object, col.fields...))
		}
		sb.WriteString("\n")
	}
	if len(items) == 0 {
		sb.WriteString("No resources found\n")
	}
	_ = os.WriteFile(path, []byte(sb.String()), 0o644)
}

func getFieldAsString(obj map[string]any, fields ...string) string {
	current := obj
	for i, field := range fields {
		val, ok := current[field]
		if !ok {
			return ""
		}
		if i == len(fields)-1 {
			switch v := val.(type) {
			case string:
				return v
			default:
				return fmt.Sprintf("%v", v)
			}
		}
		next, ok := val.(map[string]any)
		if !ok {
			return ""
		}
		current = next
	}
	return ""
}

var csvColumns = []tableColumn{
	{"NAMESPACE", 30, []string{"metadata", "namespace"}},
	{"NAME", 50, []string{"metadata", "name"}},
	{"DISPLAY", 30, []string{"spec", "displayName"}},
	{"VERSION", 12, []string{"spec", "version"}},
	{"PHASE", 12, []string{"status", "phase"}},
}

var subscriptionColumns = []tableColumn{
	{"NAMESPACE", 30, []string{"metadata", "namespace"}},
	{"NAME", 50, []string{"metadata", "name"}},
	{"PACKAGE", 40, []string{"spec", "name"}},
	{"SOURCE", 30, []string{"spec", "source"}},
	{"CHANNEL", 20, []string{"spec", "channel"}},
}

var installPlanColumns = []tableColumn{
	{"NAMESPACE", 30, []string{"metadata", "namespace"}},
	{"NAME", 50, []string{"metadata", "name"}},
	{"APPROVAL", 12, []string{"spec", "approval"}},
	{"APPROVED", 10, []string{"spec", "approved"}},
}

var operatorGroupColumns = []tableColumn{
	{"NAMESPACE", 30, []string{"metadata", "namespace"}},
	{"NAME", 50, []string{"metadata", "name"}},
}

var catalogSourceColumns = []tableColumn{
	{"NAMESPACE", 30, []string{"metadata", "namespace"}},
	{"NAME", 50, []string{"metadata", "name"}},
	{"DISPLAY", 40, []string{"spec", "displayName"}},
	{"TYPE", 15, []string{"spec", "sourceType"}},
}

var backstageCRColumns = []tableColumn{
	{"NAMESPACE", 30, []string{"metadata", "namespace"}},
	{"NAME", 50, []string{"metadata", "name"}},
}

type deploymentSummary struct {
	Namespace   string
	Name        string
	Ready       int32
	Desired     int32
	MatchLabels map[string]string
}

func writeDeploymentSummaryTable(path string, deps []deploymentSummary) {
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	var sb strings.Builder
	hasNS := false
	for _, d := range deps {
		if d.Namespace != "" {
			hasNS = true
			break
		}
	}
	if hasNS {
		fmt.Fprintf(&sb, "%-30s  %-50s  %s\n", "NAMESPACE", "NAME", "READY")
	} else {
		fmt.Fprintf(&sb, "%-50s  %s\n", "NAME", "READY")
	}
	for _, d := range deps {
		ready := fmt.Sprintf("%d/%d", d.Ready, d.Desired)
		if hasNS {
			fmt.Fprintf(&sb, "%-30s  %-50s  %s\n", d.Namespace, d.Name, ready)
		} else {
			fmt.Fprintf(&sb, "%-50s  %s\n", d.Name, ready)
		}
	}
	if len(deps) == 0 {
		sb.WriteString("No resources found\n")
	}
	_ = os.WriteFile(path, []byte(sb.String()), 0o644)
}

func writeAggregatedLogs(ctx context.Context, client kubernetes.Interface, ns string, pods []corev1.Pod, previous bool, path string) {
	var sb strings.Builder
	for i := range pods {
		pod := &pods[i]
		allContainers := make([]corev1.Container, 0, len(pod.Spec.InitContainers)+len(pod.Spec.Containers))
		allContainers = append(allContainers, pod.Spec.InitContainers...)
		allContainers = append(allContainers, pod.Spec.Containers...)

		for _, c := range allContainers {
			opts := &corev1.PodLogOptions{Container: c.Name, Previous: previous}
			stream, err := client.CoreV1().Pods(ns).GetLogs(pod.Name, opts).Stream(ctx)
			if err != nil {
				continue
			}
			data, _ := io.ReadAll(stream)
			_ = stream.Close()
			prefix := fmt.Sprintf("[pod/%s/%s] ", pod.Name, c.Name)
			for _, line := range strings.Split(strings.TrimRight(string(data), "\n"), "\n") {
				if line != "" {
					sb.WriteString(prefix)
					sb.WriteString(line)
					sb.WriteByte('\n')
				}
			}
		}
	}
	if sb.Len() > 0 {
		_ = os.WriteFile(path, []byte(sb.String()), 0o644)
	}
}

func podReadyContainers(pod *corev1.Pod) int {
	count := 0
	for _, cs := range pod.Status.ContainerStatuses {
		if cs.Ready {
			count++
		}
	}
	return count
}

func ptrVal(p *int32) int32 {
	if p == nil {
		return 0
	}
	return *p
}

func writeDualWorkloadWarning(ns, deployName, intendedKind, leftoverKind string) string {
	var sb strings.Builder
	sb.WriteString("WARNING: Duplicate RHDH workloads detected\n\n")
	fmt.Fprintf(&sb, "Both a Deployment and a StatefulSet named '%s' were found in namespace '%s'.\n\n", deployName, ns)
	sb.WriteString("This typically happens when the Backstage CR's spec.deployment.kind field is changed\n")
	sb.WriteString("(e.g., from Deployment to StatefulSet), causing the RHDH Operator to create a new\n")
	sb.WriteString("workload of the desired type while the previous one remains running, as it is no\n")
	sb.WriteString("longer managed by the Operator.\n\n")

	if intendedKind != "" {
		fmt.Fprintf(&sb, "The Backstage CR spec.deployment.kind is currently set to: %s\n", intendedKind)
		fmt.Fprintf(&sb, "This means the %s is likely a leftover that should be manually deleted.\n\n", leftoverKind)
	} else {
		sb.WriteString("The Backstage CR spec.deployment.kind is not set (defaults to Deployment).\n")
		sb.WriteString("This means the StatefulSet may be a leftover that should be manually deleted.\n\n")
	}

	fmt.Fprintf(&sb, "Recommended action:\n")
	fmt.Fprintf(&sb, "  Delete the leftover %s that is no longer managed by the Operator:\n",
		strings.ToLower(leftoverKind))
	fmt.Fprintf(&sb, "    kubectl -n %s delete %s %s\n\n",
		ns, strings.ToLower(leftoverKind), deployName)
	sb.WriteString("Refer to the RHDH Operator documentation for more details on workload type migration.\n\n")
	sb.WriteString("Collected data:\n")
	sb.WriteString("  - deployment/          contains the Deployment workload data\n")
	sb.WriteString("  - rhdh-statefulset/    contains the StatefulSet workload data\n")
	sb.WriteString("  - db-statefulset/      contains the database StatefulSet data (if applicable)\n")

	return sb.String()
}
