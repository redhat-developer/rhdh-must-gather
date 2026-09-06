package collector

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/redhat-developer/rhdh-must-gather/internal/log"
)

type Orchestrator struct{}

func (o *Orchestrator) Name() string { return "orchestrator" }

var (
	sonataFlowPlatformGVR = schema.GroupVersionResource{
		Group: "sonataflow.org", Version: "v1alpha08", Resource: "sonataflowplatforms",
	}
	sonataFlowGVR = schema.GroupVersionResource{
		Group: "sonataflow.org", Version: "v1alpha08", Resource: "sonataflows",
	}
	knativeServingGVR = schema.GroupVersionResource{
		Group: "operator.knative.dev", Version: "v1beta1", Resource: "knativeservings",
	}
	knativeEventingGVR = schema.GroupVersionResource{
		Group: "operator.knative.dev", Version: "v1beta1", Resource: "knativeeventings",
	}
	knativeKafkaGVR = schema.GroupVersionResource{
		Group: "operator.serverless.openshift.io", Version: "v1alpha1", Resource: "knativekafkas",
	}
)

var orchestratorCRDs = []string{
	"sonataflowplatforms.sonataflow.org",
	"sonataflows.sonataflow.org",
	"sonataflowclusterplatforms.sonataflow.org",
	"sonataflowbuilds.sonataflow.org",
	"knativeservings.operator.knative.dev",
	"knativeeventings.operator.knative.dev",
	"knativekafkas.operator.serverless.openshift.io",
}

func (o *Orchestrator) Run(ctx context.Context, cfg *Config) error {
	log.Info("Starting Orchestrator data collection...")
	outDir := filepath.Join(cfg.BasePath, "orchestrator")
	_ = os.MkdirAll(outDir, 0o755)

	detected := false
	namespacesFile := filepath.Join(outDir, "detected-namespaces.txt")
	nsSet := make(map[string]struct{})

	addNS := func(ns string) {
		if ns != "" {
			nsSet[ns] = struct{}{}
		}
	}

	if d := o.gatherServerlessOperators(ctx, cfg, outDir, addNS); d {
		detected = true
	}
	if d := o.gatherOrchestratorCRDs(ctx, cfg, outDir); d {
		detected = true
	}
	if d := o.gatherSonataFlowPlatforms(ctx, cfg, outDir, addNS); d {
		detected = true
	}
	if d := o.gatherSonataFlowWorkflows(ctx, cfg, outDir, addNS); d {
		detected = true
	}
	if d := o.gatherKnativeResources(ctx, cfg, outDir, addNS); d {
		detected = true
	}

	if len(nsSet) > 0 {
		nsList := make([]string, 0, len(nsSet))
		for ns := range nsSet {
			nsList = append(nsList, ns)
		}
		sort.Strings(nsList)
		_ = os.WriteFile(namespacesFile, []byte(strings.Join(nsList, "\n")+"\n"), 0o644)
		log.Info("Orchestrator namespaces for namespace-inspect: %s", strings.Join(nsList, ","))
	}

	o.generateSummary(ctx, cfg, outDir, detected)

	if detected {
		log.Success("... Orchestrator data collection done (Orchestrator components detected).")
	} else {
		log.Info("... Orchestrator data collection done (no Orchestrator components detected).")
	}
	return nil
}

func (o *Orchestrator) gatherServerlessOperators(ctx context.Context, cfg *Config, outDir string, addNS func(string)) bool {
	log.Info("Checking for OpenShift Serverless operators...")
	serverlessDir := filepath.Join(outDir, "serverless-operators")
	_ = os.MkdirAll(serverlessDir, 0o755)

	detected := false
	client := cfg.Client.Clientset

	// OpenShift Serverless operator
	serverlessNS := "openshift-serverless"
	if _, err := client.CoreV1().Namespaces().Get(ctx, serverlessNS, metav1.GetOptions{}); err == nil {
		log.Info("\tFound OpenShift Serverless namespace: %s", serverlessNS)
		detected = true
		addNS(serverlessNS)

		nsDir := filepath.Join(serverlessDir, "ns="+serverlessNS)
		_ = os.MkdirAll(nsDir, 0o755)
		o.collectServerlessNamespace(ctx, cfg, serverlessNS, nsDir,
			[]logSelector{
				{"logs-knative-openshift", "name=knative-openshift"},
				{"logs-knative-openshift-ingress", "name=knative-openshift-ingress"},
			})
	} else {
		log.Info("\tOpenShift Serverless namespace not found (namespace: %s)", serverlessNS)
		_ = os.WriteFile(filepath.Join(serverlessDir, "serverless-not-installed.txt"),
			[]byte(fmt.Sprintf("OpenShift Serverless namespace (%s) not found\n", serverlessNS)), 0o644)
	}

	// OpenShift Serverless Logic operator
	logicNS := "openshift-serverless-logic"
	if _, err := client.CoreV1().Namespaces().Get(ctx, logicNS, metav1.GetOptions{}); err == nil {
		log.Info("\tFound OpenShift Serverless Logic namespace: %s", logicNS)
		detected = true
		addNS(logicNS)

		nsDir := filepath.Join(serverlessDir, "ns="+logicNS)
		_ = os.MkdirAll(nsDir, 0o755)
		o.collectServerlessNamespace(ctx, cfg, logicNS, nsDir,
			[]logSelector{
				{"logs-logic-operator", "app.kubernetes.io/name=logic-operator-rhel8"},
			})
	} else {
		log.Info("\tOpenShift Serverless Logic namespace not found (namespace: %s)", logicNS)
		_ = os.WriteFile(filepath.Join(serverlessDir, "serverless-logic-not-installed.txt"),
			[]byte(fmt.Sprintf("OpenShift Serverless Logic namespace (%s) not found\n", logicNS)), 0o644)
	}

	return detected
}

type logSelector struct {
	prefix   string
	selector string
}

func (o *Orchestrator) collectServerlessNamespace(ctx context.Context, cfg *Config, ns, nsDir string, logSelectors []logSelector) {
	client := cfg.Client.Clientset

	// CSVs (OLM)
	hasOLM, _ := cfg.Client.HasAPIGroup("operators.coreos.com")
	if hasOLM {
		csvs, err := cfg.Client.Dynamic.Resource(csvGVR).Namespace(ns).List(ctx, metav1.ListOptions{})
		if err == nil {
			writeDynamicTable(filepath.Join(nsDir, "csv-list.txt"), csvs.Items, csvColumns)
			writeResource(filepath.Join(nsDir, "csv-all.yaml"), csvs)
		} else {
			writeCollectError(filepath.Join(nsDir, "csv-list.txt"), "list CSVs in "+ns, err)
		}

		subs, err := cfg.Client.Dynamic.Resource(subscriptionGVR).Namespace(ns).List(ctx, metav1.ListOptions{})
		if err == nil {
			writeDynamicTable(filepath.Join(nsDir, "subscriptions.txt"), subs.Items, subscriptionColumns)
			writeResource(filepath.Join(nsDir, "subscriptions.yaml"), subs)
		} else {
			writeCollectError(filepath.Join(nsDir, "subscriptions.txt"), "list Subscriptions in "+ns, err)
		}
	}

	// Deployments
	deps, err := client.AppsV1().Deployments(ns).List(ctx, metav1.ListOptions{})
	if err == nil {
		var summaries []deploymentSummary
		for _, dep := range deps.Items {
			summaries = append(summaries, deploymentSummary{
				Name: dep.Name, Ready: dep.Status.ReadyReplicas, Desired: ptrVal(dep.Spec.Replicas),
			})
		}
		writeDeploymentSummaryTable(filepath.Join(nsDir, "deployments.txt"), summaries)
		writeResource(filepath.Join(nsDir, "deployments.yaml"), deps)

		for i := range deps.Items {
			dep := &deps.Items[i]
			collectRolloutHistory(ctx, cfg, ns, KindDeployment, dep.Spec.Selector.MatchLabels,
				filepath.Join(nsDir, "deployments", dep.Name))
		}
	}

	// Pods
	pods, err := client.CoreV1().Pods(ns).List(ctx, metav1.ListOptions{})
	if err == nil {
		writePodTable(filepath.Join(nsDir, "pods.txt"), pods.Items)
		describeResource(ctx, cfg, filepath.Join(nsDir, "pods.describe.txt"), "pods", ns)
	}

	// Logs by selector
	for _, ls := range logSelectors {
		labeledPods, err := client.CoreV1().Pods(ns).List(ctx, metav1.ListOptions{LabelSelector: ls.selector})
		if err == nil && len(labeledPods.Items) > 0 {
			writeAggregatedLogs(ctx, client, ns, labeledPods.Items, false, filepath.Join(nsDir, ls.prefix+".txt"))
			writeAggregatedLogs(ctx, client, ns, labeledPods.Items, true, filepath.Join(nsDir, ls.prefix+"-previous.txt"))
		} else {
			_ = os.WriteFile(filepath.Join(nsDir, ls.prefix+".txt"), []byte(""), 0o644)
			_ = os.WriteFile(filepath.Join(nsDir, ls.prefix+"-previous.txt"), []byte(""), 0o644)
		}
	}
}

func (o *Orchestrator) gatherOrchestratorCRDs(ctx context.Context, cfg *Config, outDir string) bool {
	log.Info("Collecting Orchestrator-related Custom Resource Definitions...")
	crdsDir := filepath.Join(outDir, "crds")
	_ = os.MkdirAll(crdsDir, 0o755)

	detected := false
	var foundCRDs []string

	for _, crdName := range orchestratorCRDs {
		crd, err := cfg.Client.Dynamic.Resource(crdGVR).Get(ctx, crdName, metav1.GetOptions{})
		if err != nil {
			log.Debug("\tCRD not found: %s", crdName)
			continue
		}
		log.Info("\tFound CRD: %s", crdName)
		detected = true
		foundCRDs = append(foundCRDs, crdName)
		writeResource(filepath.Join(crdsDir, crdName+".yaml"), crd)
		describeResource(ctx, cfg, filepath.Join(crdsDir, crdName+".describe.txt"), "crd", "", crdName)
	}

	if len(foundCRDs) == 0 {
		_ = os.WriteFile(filepath.Join(crdsDir, "no-crds.txt"),
			[]byte("No Orchestrator-related CRDs found\n"), 0o644)
	} else {
		_ = os.WriteFile(filepath.Join(crdsDir, "found-crds.txt"),
			[]byte(strings.Join(foundCRDs, "\n")+"\n"), 0o644)
	}

	return detected
}

func (o *Orchestrator) gatherSonataFlowPlatforms(ctx context.Context, cfg *Config, outDir string, addNS func(string)) bool {
	log.Info("Searching for SonataFlowPlatform Custom Resources...")
	sfpDir := filepath.Join(outDir, "sonataflow-platforms")
	_ = os.MkdirAll(sfpDir, 0o755)

	items, err := listDynamic(ctx, cfg, sonataFlowPlatformGVR)
	if err != nil {
		writeCollectError(filepath.Join(sfpDir, "all-sonataflow-platforms.txt"),
			"list SonataFlowPlatform CRs", err)
	}
	writeDynamicTable(filepath.Join(sfpDir, "all-sonataflow-platforms.txt"), items,
		sonataFlowPlatformColumns)

	if len(items) == 0 {
		log.Info("\tNo SonataFlowPlatform CRs found")
		_ = os.WriteFile(filepath.Join(sfpDir, "no-platforms.txt"),
			[]byte("No SonataFlowPlatform CRs found\n"), 0o644)
		return false
	}

	client := cfg.Client.Clientset

	for _, item := range items {
		ns := item.GetNamespace()
		name := item.GetName()
		addNS(ns)

		log.Info("--> Processing SonataFlowPlatform %s in namespace %s", name, ns)

		crDir := filepath.Join(sfpDir, "ns="+ns, name)
		_ = os.MkdirAll(crDir, 0o755)

		writeResource(filepath.Join(crDir, name+".yaml"), &item)
		describeResource(ctx, cfg, filepath.Join(crDir, "describe.txt"),
			"sonataflowplatforms.sonataflow.org", ns, name)

		// Related deployments
		sfpDeps, err := client.AppsV1().Deployments(ns).List(ctx, metav1.ListOptions{
			LabelSelector: "app.kubernetes.io/component=sonataflow-platform",
		})
		if err == nil {
			var summaries []deploymentSummary
			for _, dep := range sfpDeps.Items {
				summaries = append(summaries, deploymentSummary{
					Name: dep.Name, Ready: dep.Status.ReadyReplicas, Desired: ptrVal(dep.Spec.Replicas),
				})
			}
			writeDeploymentSummaryTable(filepath.Join(crDir, "related-deployments.txt"), summaries)
			for i := range sfpDeps.Items {
				dep := &sfpDeps.Items[i]
				collectRolloutHistory(ctx, cfg, ns, KindDeployment, dep.Spec.Selector.MatchLabels,
					filepath.Join(crDir, "deployments", dep.Name))
			}
		}

		// Related services
		sfpSvcs, err := client.CoreV1().Services(ns).List(ctx, metav1.ListOptions{
			LabelSelector: "sonataflow.org/platform=" + name,
		})
		if err == nil {
			var sb strings.Builder
			for _, svc := range sfpSvcs.Items {
				fmt.Fprintf(&sb, "service/%s   %s   %s\n", svc.Name, svc.Spec.Type, svc.Spec.ClusterIP)
			}
			if sb.Len() == 0 {
				sb.WriteString("No related services found\n")
			}
			_ = os.WriteFile(filepath.Join(crDir, "related-services.txt"), []byte(sb.String()), 0o644)
		}

		// Logs
		sfpPods, err := client.CoreV1().Pods(ns).List(ctx, metav1.ListOptions{
			LabelSelector: "sonataflow.org/platform=" + name,
		})
		if err == nil && len(sfpPods.Items) > 0 {
			writeAggregatedLogs(ctx, client, ns, sfpPods.Items, false, filepath.Join(crDir, "logs.txt"))
			writeAggregatedLogs(ctx, client, ns, sfpPods.Items, true, filepath.Join(crDir, "logs-previous.txt"))
		} else {
			_ = os.WriteFile(filepath.Join(crDir, "logs.txt"), []byte(""), 0o644)
			_ = os.WriteFile(filepath.Join(crDir, "logs-previous.txt"), []byte(""), 0o644)
		}
	}

	return true
}

func (o *Orchestrator) gatherSonataFlowWorkflows(ctx context.Context, cfg *Config, outDir string, addNS func(string)) bool {
	log.Info("Searching for SonataFlow workflows...")
	sfwDir := filepath.Join(outDir, "sonataflow-workflows")
	_ = os.MkdirAll(sfwDir, 0o755)

	items, err := listDynamic(ctx, cfg, sonataFlowGVR)
	if err != nil {
		writeCollectError(filepath.Join(sfwDir, "all-sonataflow-workflows.txt"),
			"list SonataFlow workflows", err)
	}
	writeDynamicTable(filepath.Join(sfwDir, "all-sonataflow-workflows.txt"), items,
		sonataFlowWorkflowColumns)

	if len(items) == 0 {
		log.Info("\tNo SonataFlow workflows found")
		_ = os.WriteFile(filepath.Join(sfwDir, "no-workflows.txt"),
			[]byte("No SonataFlow workflows found\n"), 0o644)
		return false
	}

	client := cfg.Client.Clientset
	seenNS := make(map[string]bool)

	for _, item := range items {
		ns := item.GetNamespace()
		name := item.GetName()
		addNS(ns)

		nsDir := filepath.Join(sfwDir, "ns="+ns)
		if !seenNS[ns] {
			seenNS[ns] = true
			_ = os.MkdirAll(nsDir, 0o755)
			nsItems, err := cfg.Client.Dynamic.Resource(sonataFlowGVR).Namespace(ns).List(ctx, metav1.ListOptions{})
			if err == nil {
				writeDynamicTable(filepath.Join(nsDir, "all-workflows.txt"), nsItems.Items,
					sonataFlowWorkflowColumns)
			}
		}

		log.Info("\tProcessing SonataFlow workflow: %s in namespace %s", name, ns)

		wfDir := filepath.Join(nsDir, name)
		_ = os.MkdirAll(wfDir, 0o755)

		writeResource(filepath.Join(wfDir, "workflow.yaml"), &item)
		describeResource(ctx, cfg, filepath.Join(wfDir, "describe.txt"),
			"sonataflows.sonataflow.org", ns, name)

		// Pods
		wfPods, err := client.CoreV1().Pods(ns).List(ctx, metav1.ListOptions{
			LabelSelector: "sonataflow.org/workflow-app=" + name,
		})
		if err == nil {
			writePodTable(filepath.Join(wfDir, "pods.txt"), wfPods.Items)
			if len(wfPods.Items) > 0 {
				writeAggregatedLogs(ctx, client, ns, wfPods.Items, false, filepath.Join(wfDir, "logs.txt"))
				writeAggregatedLogs(ctx, client, ns, wfPods.Items, true, filepath.Join(wfDir, "logs-previous.txt"))
			} else {
				_ = os.WriteFile(filepath.Join(wfDir, "logs.txt"), []byte(""), 0o644)
				_ = os.WriteFile(filepath.Join(wfDir, "logs-previous.txt"), []byte(""), 0o644)
			}
		}
	}

	return true
}

func (o *Orchestrator) gatherKnativeResources(ctx context.Context, cfg *Config, outDir string, addNS func(string)) bool {
	log.Info("Collecting Knative resources...")
	knativeDir := filepath.Join(outDir, "knative")
	_ = os.MkdirAll(knativeDir, 0o755)

	detected := false
	client := cfg.Client.Clientset

	// KnativeServing CRs
	servingItems := o.collectKnativeCRs(ctx, cfg, knativeServingGVR,
		filepath.Join(knativeDir, "knative-serving-list.txt"),
		filepath.Join(knativeDir, "knative-serving.yaml"),
		knativeServingColumns)

	// knative-serving namespace resources
	if _, err := client.CoreV1().Namespaces().Get(ctx, "knative-serving", metav1.GetOptions{}); err == nil {
		detected = true
		addNS("knative-serving")
		servingDir := filepath.Join(knativeDir, "knative-serving")
		_ = os.MkdirAll(servingDir, 0o755)
		o.collectKnativeNamespace(ctx, cfg, "knative-serving", servingDir)
	}
	if len(servingItems) > 0 {
		detected = true
	}

	// KnativeEventing CRs
	eventingItems := o.collectKnativeCRs(ctx, cfg, knativeEventingGVR,
		filepath.Join(knativeDir, "knative-eventing-list.txt"),
		filepath.Join(knativeDir, "knative-eventing.yaml"),
		knativeEventingColumns)

	// knative-eventing namespace resources
	if _, err := client.CoreV1().Namespaces().Get(ctx, "knative-eventing", metav1.GetOptions{}); err == nil {
		detected = true
		addNS("knative-eventing")
		eventingDir := filepath.Join(knativeDir, "knative-eventing")
		_ = os.MkdirAll(eventingDir, 0o755)
		o.collectKnativeNamespace(ctx, cfg, "knative-eventing", eventingDir)
	}
	if len(eventingItems) > 0 {
		detected = true
	}

	// KnativeKafka CRs (optional)
	o.collectKnativeCRs(ctx, cfg, knativeKafkaGVR,
		filepath.Join(knativeDir, "knative-kafka-list.txt"),
		filepath.Join(knativeDir, "knative-kafka.yaml"),
		knativeKafkaColumns)

	return detected
}

func (o *Orchestrator) collectKnativeCRs(ctx context.Context, cfg *Config, gvr schema.GroupVersionResource, listPath, yamlPath string, columns []tableColumn) []unstructured.Unstructured {
	list, err := cfg.Client.Dynamic.Resource(gvr).Namespace("").List(ctx, metav1.ListOptions{})
	if err != nil {
		writeCollectError(listPath, "list "+gvr.Resource, err)
		writeCollectError(yamlPath, "list "+gvr.Resource, err)
		return nil
	}
	writeDynamicTable(listPath, list.Items, columns)
	writeResource(yamlPath, list)
	return list.Items
}

func (o *Orchestrator) collectKnativeNamespace(ctx context.Context, cfg *Config, ns, nsDir string) {
	client := cfg.Client.Clientset

	deps, err := client.AppsV1().Deployments(ns).List(ctx, metav1.ListOptions{})
	if err == nil {
		var summaries []deploymentSummary
		for _, dep := range deps.Items {
			summaries = append(summaries, deploymentSummary{
				Name: dep.Name, Ready: dep.Status.ReadyReplicas, Desired: ptrVal(dep.Spec.Replicas),
			})
		}
		writeDeploymentSummaryTable(filepath.Join(nsDir, "deployments.txt"), summaries)
		for i := range deps.Items {
			dep := &deps.Items[i]
			collectRolloutHistory(ctx, cfg, ns, KindDeployment, dep.Spec.Selector.MatchLabels,
				filepath.Join(nsDir, "deployments", dep.Name))
		}
	}

	pods, err := client.CoreV1().Pods(ns).List(ctx, metav1.ListOptions{})
	if err == nil {
		writePodTable(filepath.Join(nsDir, "pods.txt"), pods.Items)
	}

	svcs, err := client.CoreV1().Services(ns).List(ctx, metav1.ListOptions{})
	if err == nil {
		var sb strings.Builder
		for _, svc := range svcs.Items {
			fmt.Fprintf(&sb, "service/%s   %s   %s\n", svc.Name, svc.Spec.Type, svc.Spec.ClusterIP)
		}
		if sb.Len() == 0 {
			sb.WriteString("No services found\n")
		}
		_ = os.WriteFile(filepath.Join(nsDir, "services.txt"), []byte(sb.String()), 0o644)
	}
}

func (o *Orchestrator) generateSummary(ctx context.Context, cfg *Config, outDir string, detected bool) {
	log.Info("Generating Orchestrator components summary...")

	hasOLM, _ := cfg.Client.HasAPIGroup("operators.coreos.com")

	var sb strings.Builder
	sb.WriteString("==============================================\n")
	sb.WriteString("RHDH Orchestrator Components Summary\n")
	fmt.Fprintf(&sb, "Generated: %s\n", time.Now().UTC().Format("2006-01-02T15:04:05Z"))
	sb.WriteString("==============================================\n\n")

	sb.WriteString("=== OpenShift Serverless Operator ===\n")
	if hasOLM {
		csvs, err := cfg.Client.Dynamic.Resource(csvGVR).Namespace("openshift-serverless").List(ctx, metav1.ListOptions{})
		if err == nil {
			found := false
			for _, csv := range csvs.Items {
				if strings.Contains(strings.ToLower(csv.GetName()), "serverless") {
					name := csv.GetName()
					version := getFieldAsString(csv.Object, "spec", "version")
					phase := getFieldAsString(csv.Object, "status", "phase")
					fmt.Fprintf(&sb, "%-50s  %-12s  %s\n", name, version, phase)
					found = true
				}
			}
			if !found {
				sb.WriteString("Not installed\n")
			}
		} else {
			sb.WriteString("Not installed\n")
		}
	} else {
		sb.WriteString("Not installed\n")
	}
	sb.WriteString("\n")

	sb.WriteString("=== OpenShift Serverless Logic Operator ===\n")
	if hasOLM {
		csvs, err := cfg.Client.Dynamic.Resource(csvGVR).Namespace("openshift-serverless-logic").List(ctx, metav1.ListOptions{})
		if err == nil {
			found := false
			for _, csv := range csvs.Items {
				if strings.Contains(strings.ToLower(csv.GetName()), "logic") {
					name := csv.GetName()
					version := getFieldAsString(csv.Object, "spec", "version")
					phase := getFieldAsString(csv.Object, "status", "phase")
					fmt.Fprintf(&sb, "%-50s  %-12s  %s\n", name, version, phase)
					found = true
				}
			}
			if !found {
				sb.WriteString("Not installed\n")
			}
		} else {
			sb.WriteString("Not installed\n")
		}
	} else {
		sb.WriteString("Not installed\n")
	}
	sb.WriteString("\n")

	sb.WriteString("=== SonataFlowPlatform CRs ===\n")
	sfps, err := listAllNamespaces(ctx, cfg, sonataFlowPlatformGVR)
	if err == nil && len(sfps) > 0 {
		fmt.Fprintf(&sb, "%-30s  %-50s  %s\n", "NAMESPACE", "NAME", "PHASE")
		for _, sfp := range sfps {
			phase := getFieldAsString(sfp.Object, "status", "phase")
			fmt.Fprintf(&sb, "%-30s  %-50s  %s\n", sfp.GetNamespace(), sfp.GetName(), phase)
		}
	} else {
		sb.WriteString("No SonataFlowPlatform CRs found or no permission\n")
	}
	sb.WriteString("\n")

	sb.WriteString("=== SonataFlow Workflows ===\n")
	sflows, err := listAllNamespaces(ctx, cfg, sonataFlowGVR)
	if err == nil && len(sflows) > 0 {
		fmt.Fprintf(&sb, "%-30s  %-50s  %s\n", "NAMESPACE", "NAME", "PHASE")
		for _, sf := range sflows {
			phase := getFieldAsString(sf.Object, "status", "phase")
			fmt.Fprintf(&sb, "%-30s  %-50s  %s\n", sf.GetNamespace(), sf.GetName(), phase)
		}
	} else {
		sb.WriteString("No SonataFlow workflows found or no permission\n")
	}
	sb.WriteString("\n")

	sb.WriteString("=== Knative Serving ===\n")
	kservings, err := listAllNamespaces(ctx, cfg, knativeServingGVR)
	if err == nil && len(kservings) > 0 {
		fmt.Fprintf(&sb, "%-30s  %-50s  %-12s  %s\n", "NAMESPACE", "NAME", "VERSION", "READY")
		for _, ks := range kservings {
			version := getFieldAsString(ks.Object, "status", "version")
			ready := getConditionStatus(ks.Object, "Ready")
			fmt.Fprintf(&sb, "%-30s  %-50s  %-12s  %s\n", ks.GetNamespace(), ks.GetName(), version, ready)
		}
	} else {
		sb.WriteString("Not found or no permission\n")
	}
	sb.WriteString("\n")

	sb.WriteString("=== Knative Eventing ===\n")
	keventings, err := listAllNamespaces(ctx, cfg, knativeEventingGVR)
	if err == nil && len(keventings) > 0 {
		fmt.Fprintf(&sb, "%-30s  %-50s  %-12s  %s\n", "NAMESPACE", "NAME", "VERSION", "READY")
		for _, ke := range keventings {
			version := getFieldAsString(ke.Object, "status", "version")
			ready := getConditionStatus(ke.Object, "Ready")
			fmt.Fprintf(&sb, "%-30s  %-50s  %-12s  %s\n", ke.GetNamespace(), ke.GetName(), version, ready)
		}
	} else {
		sb.WriteString("Not found or no permission\n")
	}
	sb.WriteString("\n")

	sb.WriteString("==============================================\n")
	if detected {
		sb.WriteString("Orchestrator components detected: YES\n")
	} else {
		sb.WriteString("Orchestrator components detected: NO\n")
	}
	sb.WriteString("==============================================\n")

	summaryFile := filepath.Join(outDir, "summary.txt")
	_ = os.WriteFile(summaryFile, []byte(sb.String()), 0o644)
	log.Info("\tSummary written to: %s", summaryFile)

}

func listAllNamespaces(ctx context.Context, cfg *Config, gvr schema.GroupVersionResource) ([]unstructured.Unstructured, error) {
	list, err := cfg.Client.Dynamic.Resource(gvr).Namespace("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	return list.Items, nil
}

func getConditionStatus(obj map[string]any, condType string) string {
	conditions, ok := obj["status"].(map[string]any)
	if !ok {
		return ""
	}
	condList, ok := conditions["conditions"].([]any)
	if !ok {
		return ""
	}
	for _, c := range condList {
		cond, ok := c.(map[string]any)
		if !ok {
			continue
		}
		if t, _ := cond["type"].(string); t == condType {
			if s, ok := cond["status"].(string); ok {
				return s
			}
		}
	}
	return ""
}

var sonataFlowPlatformColumns = []tableColumn{
	{"NAMESPACE", 30, []string{"metadata", "namespace"}},
	{"NAME", 50, []string{"metadata", "name"}},
	{"PHASE", 12, []string{"status", "phase"}},
}

var sonataFlowWorkflowColumns = []tableColumn{
	{"NAMESPACE", 30, []string{"metadata", "namespace"}},
	{"NAME", 50, []string{"metadata", "name"}},
	{"PHASE", 12, []string{"status", "phase"}},
}

var knativeServingColumns = []tableColumn{
	{"NAMESPACE", 30, []string{"metadata", "namespace"}},
	{"NAME", 50, []string{"metadata", "name"}},
	{"VERSION", 12, []string{"status", "version"}},
}

var knativeEventingColumns = []tableColumn{
	{"NAMESPACE", 30, []string{"metadata", "namespace"}},
	{"NAME", 50, []string{"metadata", "name"}},
	{"VERSION", 12, []string{"status", "version"}},
}

var knativeKafkaColumns = []tableColumn{
	{"NAMESPACE", 30, []string{"metadata", "namespace"}},
	{"NAME", 50, []string{"metadata", "name"}},
}
