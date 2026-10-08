package collector

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	fakedynamic "k8s.io/client-go/dynamic/fake"
	k8stesting "k8s.io/client-go/testing"
)

func TestGetConditionStatus(t *testing.T) {
	obj := map[string]any{
		"status": map[string]any{
			"conditions": []any{
				map[string]any{
					"type":   "Ready",
					"status": "True",
				},
				map[string]any{
					"type":   "Reconciled",
					"status": "False",
				},
			},
		},
	}

	if got := getConditionStatus(obj, "Ready"); got != "True" {
		t.Errorf("getConditionStatus(Ready) = %q, want True", got)
	}
	if got := getConditionStatus(obj, "Reconciled"); got != "False" {
		t.Errorf("getConditionStatus(Reconciled) = %q, want False", got)
	}
	if got := getConditionStatus(obj, "Missing"); got != "" {
		t.Errorf("getConditionStatus(Missing) = %q, want empty", got)
	}
}

func TestGetConditionStatus_NoConditions(t *testing.T) {
	obj := map[string]any{
		"status": map[string]any{},
	}
	if got := getConditionStatus(obj, "Ready"); got != "" {
		t.Errorf("getConditionStatus = %q, want empty", got)
	}
}

func TestGetConditionStatus_NoStatus(t *testing.T) {
	obj := map[string]any{}
	if got := getConditionStatus(obj, "Ready"); got != "" {
		t.Errorf("getConditionStatus = %q, want empty", got)
	}
}

func TestOrchestratorName(t *testing.T) {
	o := &Orchestrator{}
	if got := o.Name(); got != "orchestrator" {
		t.Errorf("Name() = %q, want orchestrator", got)
	}
}

func TestListFilteredNamespaces(t *testing.T) {
	sfp1 := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "sonataflow.org/v1alpha08",
			"kind":       "SonataFlowPlatform",
			"metadata": map[string]any{
				"name":      "platform-1",
				"namespace": "ns1",
			},
		},
	}
	sfp2 := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "sonataflow.org/v1alpha08",
			"kind":       "SonataFlowPlatform",
			"metadata": map[string]any{
				"name":      "platform-2",
				"namespace": "ns2",
			},
		},
	}

	t.Run("no filter returns all", func(t *testing.T) {
		cfg := newTestConfig(t, "",
			withDynamicObjs(
				map[schema.GroupVersionResource]string{sonataFlowPlatformGVR: "SonataFlowPlatformList"},
				sfp1, sfp2,
			),
		)

		items, err := listFilteredNamespaces(context.Background(), cfg, sonataFlowPlatformGVR)
		if err != nil {
			t.Fatal(err)
		}
		if len(items) != 2 {
			t.Errorf("got %d items, want 2", len(items))
		}
	})

	t.Run("with filter", func(t *testing.T) {
		cfg := newTestConfig(t, "",
			withDynamicObjs(
				map[schema.GroupVersionResource]string{sonataFlowPlatformGVR: "SonataFlowPlatformList"},
				sfp1, sfp2,
			),
		)
		cfg.TargetNamespaces = []string{"ns1"}

		items, err := listFilteredNamespaces(context.Background(), cfg, sonataFlowPlatformGVR)
		if err != nil {
			t.Fatal(err)
		}
		if len(items) != 1 {
			t.Fatalf("got %d items, want 1", len(items))
		}
		if items[0].GetName() != "platform-1" {
			t.Errorf("got %q, want platform-1", items[0].GetName())
		}
	})
}

func TestGatherOrchestratorCRDs_NoCRDs(t *testing.T) {
	dir := t.TempDir()
	cfg := newTestConfig(t, dir,
		withDynamicObjs(
			map[schema.GroupVersionResource]string{crdGVR: "CustomResourceDefinitionList"},
		),
	)

	o := &Orchestrator{}
	outDir := filepath.Join(dir, "orchestrator")
	detected := o.gatherOrchestratorCRDs(context.Background(), cfg, outDir)

	if detected {
		t.Error("expected no orchestrator CRDs detected")
	}
	noCRDsFile := filepath.Join(outDir, "crds", "no-crds.txt")
	if _, err := os.Stat(noCRDsFile); err != nil {
		t.Error("expected no-crds.txt file")
	}
}

func TestGatherOrchestratorCRDs_WithCRDs(t *testing.T) {
	dir := t.TempDir()
	crd := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "apiextensions.k8s.io/v1",
			"kind":       "CustomResourceDefinition",
			"metadata":   map[string]any{"name": "sonataflowplatforms.sonataflow.org"},
		},
	}
	cfg := newTestConfig(t, dir,
		withDynamicObjs(
			map[schema.GroupVersionResource]string{crdGVR: "CustomResourceDefinitionList"},
			crd,
		),
	)

	o := &Orchestrator{}
	outDir := filepath.Join(dir, "orchestrator")
	detected := o.gatherOrchestratorCRDs(context.Background(), cfg, outDir)

	if !detected {
		t.Error("expected orchestrator CRDs detected")
	}
	foundFile := filepath.Join(outDir, "crds", "found-crds.txt")
	if _, err := os.Stat(foundFile); err != nil {
		t.Error("expected found-crds.txt file")
	}
}

func TestGenerateSummary(t *testing.T) {
	dir := t.TempDir()
	sfp := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "sonataflow.org/v1alpha08",
			"kind":       "SonataFlowPlatform",
			"metadata": map[string]any{
				"name":      "platform-1",
				"namespace": "sonata-ns",
			},
			"status": map[string]any{
				"phase": "Succeeded",
			},
		},
	}

	cfg := newTestConfig(t, dir,
		withAPIGroups("apps/v1"),
		withDynamicObjs(
			map[schema.GroupVersionResource]string{
				sonataFlowPlatformGVR: "SonataFlowPlatformList",
				sonataFlowGVR:         "SonataFlowList",
				knativeServingGVR:     "KnativeServingList",
				knativeEventingGVR:    "KnativeEventingList",
			},
			sfp,
		),
	)

	o := &Orchestrator{}
	outDir := filepath.Join(dir, "orchestrator")
	_ = os.MkdirAll(outDir, 0o755)
	o.generateSummary(context.Background(), cfg, outDir, true)

	data, err := os.ReadFile(filepath.Join(outDir, "summary.txt"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "RHDH Orchestrator Components Summary") {
		t.Error("expected summary header")
	}
	if !strings.Contains(content, "platform-1") {
		t.Error("expected SonataFlowPlatform name")
	}
	if !strings.Contains(content, "Orchestrator components detected: YES") {
		t.Error("expected detected=YES")
	}
}

func TestGenerateSummary_NotDetected(t *testing.T) {
	dir := t.TempDir()
	cfg := newTestConfig(t, dir,
		withAPIGroups("apps/v1"),
		withDynamicObjs(
			map[schema.GroupVersionResource]string{
				sonataFlowPlatformGVR: "SonataFlowPlatformList",
				sonataFlowGVR:         "SonataFlowList",
				knativeServingGVR:     "KnativeServingList",
				knativeEventingGVR:    "KnativeEventingList",
			},
		),
	)

	o := &Orchestrator{}
	outDir := filepath.Join(dir, "orchestrator")
	_ = os.MkdirAll(outDir, 0o755)
	o.generateSummary(context.Background(), cfg, outDir, false)

	data, err := os.ReadFile(filepath.Join(outDir, "summary.txt"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !strings.Contains(string(data), "Orchestrator components detected: NO") {
		t.Error("expected detected=NO")
	}
}

func TestOrchestratorCRDsList(t *testing.T) {
	expected := []string{
		"sonataflowplatforms.sonataflow.org",
		"sonataflows.sonataflow.org",
		"sonataflowclusterplatforms.sonataflow.org",
		"sonataflowbuilds.sonataflow.org",
		"knativeservings.operator.knative.dev",
		"knativeeventings.operator.knative.dev",
		"knativekafkas.operator.serverless.openshift.io",
	}

	if len(orchestratorCRDs) != len(expected) {
		t.Fatalf("orchestratorCRDs has %d items, want %d", len(orchestratorCRDs), len(expected))
	}

	for i, crd := range orchestratorCRDs {
		if crd != expected[i] {
			t.Errorf("orchestratorCRDs[%d] = %q, want %q", i, crd, expected[i])
		}
	}
}

func TestGatherServerlessOperators_NotFound(t *testing.T) {
	dir := t.TempDir()
	cfg := newTestConfig(t, dir)

	o := &Orchestrator{}
	outDir := filepath.Join(dir, "orchestrator")
	_ = os.MkdirAll(outDir, 0o755)

	var addedNS []string
	addNS := func(ns string) { addedNS = append(addedNS, ns) }
	detected := o.gatherServerlessOperators(context.Background(), cfg, outDir, addNS)

	if detected {
		t.Error("expected detected=false when namespaces don't exist")
	}
	if len(addedNS) != 0 {
		t.Errorf("expected no namespaces added, got %v", addedNS)
	}

	serverlessDir := filepath.Join(outDir, "serverless-operators")
	if _, err := os.Stat(filepath.Join(serverlessDir, "serverless-not-installed.txt")); err != nil {
		t.Error("expected serverless-not-installed.txt")
	}
	if _, err := os.Stat(filepath.Join(serverlessDir, "serverless-logic-not-installed.txt")); err != nil {
		t.Error("expected serverless-logic-not-installed.txt")
	}
}

func TestGatherServerlessOperators_Found(t *testing.T) {
	dir := t.TempDir()

	serverlessNS := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: "openshift-serverless"},
	}
	logicNS := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: "openshift-serverless-logic"},
	}

	cfg := newTestConfig(t, dir,
		withTypedObjs(serverlessNS, logicNS),
		withAPIGroups("apps/v1"),
		withDynamicObjs(
			map[schema.GroupVersionResource]string{
				csvGVR:          "ClusterServiceVersionList",
				subscriptionGVR: "SubscriptionList",
			},
		),
	)

	o := &Orchestrator{}
	outDir := filepath.Join(dir, "orchestrator")
	_ = os.MkdirAll(outDir, 0o755)

	var addedNS []string
	addNS := func(ns string) { addedNS = append(addedNS, ns) }
	detected := o.gatherServerlessOperators(context.Background(), cfg, outDir, addNS)

	if !detected {
		t.Error("expected detected=true when serverless namespaces exist")
	}
	if len(addedNS) != 2 {
		t.Errorf("expected 2 namespaces added, got %v", addedNS)
	}
}

func TestGatherServerlessOperators_Targeted(t *testing.T) {
	dir := t.TempDir()

	serverlessNS := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: "openshift-serverless"},
	}

	cfg := newTestConfig(t, dir,
		withTypedObjs(serverlessNS),
		withAPIGroups("apps/v1"),
		withDynamicObjs(
			map[schema.GroupVersionResource]string{
				csvGVR:          "ClusterServiceVersionList",
				subscriptionGVR: "SubscriptionList",
			},
		),
	)
	cfg.TargetNamespaces = []string{"other-ns"}

	o := &Orchestrator{}
	outDir := filepath.Join(dir, "orchestrator")
	_ = os.MkdirAll(outDir, 0o755)

	var addedNS []string
	addNS := func(ns string) { addedNS = append(addedNS, ns) }
	detected := o.gatherServerlessOperators(context.Background(), cfg, outDir, addNS)

	if !detected {
		t.Error("expected detected=true (namespace exists even if filtered)")
	}
	if len(addedNS) != 0 {
		t.Errorf("expected no namespaces added when not in target list, got %v", addedNS)
	}
}

func TestCollectServerlessNamespace(t *testing.T) {
	dir := t.TempDir()

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "knative-operator-abc",
			Namespace: "openshift-serverless",
			Labels:    map[string]string{"name": "knative-openshift"},
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{Name: "knative-openshift"}},
		},
		Status: corev1.PodStatus{Phase: corev1.PodRunning},
	}

	cfg := newTestConfig(t, dir,
		withTypedObjs(pod),
		withAPIGroups("apps/v1"),
		withDynamicObjs(
			map[schema.GroupVersionResource]string{
				csvGVR:          "ClusterServiceVersionList",
				subscriptionGVR: "SubscriptionList",
			},
		),
	)

	o := &Orchestrator{}
	nsDir := filepath.Join(dir, "ns")
	_ = os.MkdirAll(nsDir, 0o755)
	o.collectServerlessNamespace(context.Background(), cfg, "openshift-serverless", nsDir,
		[]logSelector{{"logs-knative-openshift", "name=knative-openshift"}})

	if _, err := os.Stat(filepath.Join(nsDir, "pods.txt")); err != nil {
		t.Error("expected pods.txt")
	}
	if _, err := os.Stat(filepath.Join(nsDir, "logs-knative-openshift.txt")); err != nil {
		t.Error("expected logs file")
	}
	if _, err := os.Stat(filepath.Join(nsDir, "logs-knative-openshift-previous.txt")); err != nil {
		t.Error("expected previous logs file")
	}
}

func TestCollectServerlessNamespace_NoMatchingPods(t *testing.T) {
	dir := t.TempDir()

	cfg := newTestConfig(t, dir,
		withAPIGroups("apps/v1"),
		withDynamicObjs(
			map[schema.GroupVersionResource]string{
				csvGVR:          "ClusterServiceVersionList",
				subscriptionGVR: "SubscriptionList",
			},
		),
	)

	o := &Orchestrator{}
	nsDir := filepath.Join(dir, "ns")
	_ = os.MkdirAll(nsDir, 0o755)
	o.collectServerlessNamespace(context.Background(), cfg, "openshift-serverless", nsDir,
		[]logSelector{{"logs-knative-openshift", "name=knative-openshift"}})

	data, err := os.ReadFile(filepath.Join(nsDir, "logs-knative-openshift.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if len(data) != 0 {
		t.Error("expected empty log file when no matching pods")
	}
}

func TestGatherSonataFlowPlatforms_NoAPI(t *testing.T) {
	dir := t.TempDir()
	cfg := newTestConfig(t, dir, withAPIGroups("apps/v1"))

	o := &Orchestrator{}
	outDir := filepath.Join(dir, "orchestrator")
	_ = os.MkdirAll(outDir, 0o755)

	var addedNS []string
	addNS := func(ns string) { addedNS = append(addedNS, ns) }
	detected := o.gatherSonataFlowPlatforms(context.Background(), cfg, outDir, addNS)

	if detected {
		t.Error("expected not detected when SonataFlow API not available")
	}
	if _, err := os.Stat(filepath.Join(outDir, "sonataflow-platforms", "no-platforms.txt")); err != nil {
		t.Error("expected no-platforms.txt")
	}
}

func TestGatherSonataFlowPlatforms_WithPlatforms(t *testing.T) {
	dir := t.TempDir()

	sfp := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "sonataflow.org/v1alpha08",
			"kind":       "SonataFlowPlatform",
			"metadata": map[string]any{
				"name":      "my-platform",
				"namespace": "sonata-ns",
			},
			"status": map[string]any{
				"phase": "Ready",
			},
		},
	}

	cfg := newTestConfig(t, dir,
		withAPIGroups("sonataflow.org/v1alpha08"),
		withDynamicObjs(
			map[schema.GroupVersionResource]string{
				sonataFlowPlatformGVR: "SonataFlowPlatformList",
			},
			sfp,
		),
	)

	o := &Orchestrator{}
	outDir := filepath.Join(dir, "orchestrator")
	_ = os.MkdirAll(outDir, 0o755)

	var addedNS []string
	addNS := func(ns string) { addedNS = append(addedNS, ns) }
	detected := o.gatherSonataFlowPlatforms(context.Background(), cfg, outDir, addNS)

	if !detected {
		t.Error("expected detected=true")
	}
	if len(addedNS) != 1 || addedNS[0] != "sonata-ns" {
		t.Errorf("expected [sonata-ns], got %v", addedNS)
	}

	sfpDir := filepath.Join(outDir, "sonataflow-platforms")
	if _, err := os.Stat(filepath.Join(sfpDir, "all-sonataflow-platforms.txt")); err != nil {
		t.Error("expected all-sonataflow-platforms.txt")
	}
	crDir := filepath.Join(sfpDir, "ns=sonata-ns", "my-platform")
	if _, err := os.Stat(filepath.Join(crDir, "my-platform.yaml")); err != nil {
		t.Error("expected platform YAML file")
	}
}

func TestGatherSonataFlowPlatforms_NoPlatforms(t *testing.T) {
	dir := t.TempDir()

	cfg := newTestConfig(t, dir,
		withAPIGroups("sonataflow.org/v1alpha08"),
		withDynamicObjs(
			map[schema.GroupVersionResource]string{
				sonataFlowPlatformGVR: "SonataFlowPlatformList",
			},
		),
	)

	o := &Orchestrator{}
	outDir := filepath.Join(dir, "orchestrator")
	_ = os.MkdirAll(outDir, 0o755)

	var addedNS []string
	addNS := func(ns string) { addedNS = append(addedNS, ns) }
	detected := o.gatherSonataFlowPlatforms(context.Background(), cfg, outDir, addNS)

	if detected {
		t.Error("expected not detected when no platforms exist")
	}
	if _, err := os.Stat(filepath.Join(outDir, "sonataflow-platforms", "no-platforms.txt")); err != nil {
		t.Error("expected no-platforms.txt")
	}
}

func TestGatherSonataFlowWorkflows_NoAPI(t *testing.T) {
	dir := t.TempDir()
	cfg := newTestConfig(t, dir, withAPIGroups("apps/v1"))

	o := &Orchestrator{}
	outDir := filepath.Join(dir, "orchestrator")
	_ = os.MkdirAll(outDir, 0o755)

	var addedNS []string
	addNS := func(ns string) { addedNS = append(addedNS, ns) }
	detected := o.gatherSonataFlowWorkflows(context.Background(), cfg, outDir, addNS)

	if detected {
		t.Error("expected not detected when SonataFlow API not available")
	}
	if _, err := os.Stat(filepath.Join(outDir, "sonataflow-workflows", "no-workflows.txt")); err != nil {
		t.Error("expected no-workflows.txt")
	}
}

func TestGatherSonataFlowWorkflows_WithWorkflows(t *testing.T) {
	dir := t.TempDir()

	wf := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "sonataflow.org/v1alpha08",
			"kind":       "SonataFlow",
			"metadata": map[string]any{
				"name":      "my-workflow",
				"namespace": "wf-ns",
			},
			"status": map[string]any{
				"phase": "Running",
			},
		},
	}

	cfg := newTestConfig(t, dir,
		withAPIGroups("sonataflow.org/v1alpha08"),
		withDynamicObjs(
			map[schema.GroupVersionResource]string{
				sonataFlowGVR: "SonataFlowList",
			},
			wf,
		),
	)

	o := &Orchestrator{}
	outDir := filepath.Join(dir, "orchestrator")
	_ = os.MkdirAll(outDir, 0o755)

	var addedNS []string
	addNS := func(ns string) { addedNS = append(addedNS, ns) }
	detected := o.gatherSonataFlowWorkflows(context.Background(), cfg, outDir, addNS)

	if !detected {
		t.Error("expected detected=true")
	}
	if len(addedNS) != 1 || addedNS[0] != "wf-ns" {
		t.Errorf("expected [wf-ns], got %v", addedNS)
	}

	sfwDir := filepath.Join(outDir, "sonataflow-workflows")
	if _, err := os.Stat(filepath.Join(sfwDir, "all-sonataflow-workflows.txt")); err != nil {
		t.Error("expected all-sonataflow-workflows.txt")
	}
	wfDir := filepath.Join(sfwDir, "ns=wf-ns", "my-workflow")
	if _, err := os.Stat(filepath.Join(wfDir, "workflow.yaml")); err != nil {
		t.Error("expected workflow YAML file")
	}
}

func TestGatherSonataFlowWorkflows_NoWorkflows(t *testing.T) {
	dir := t.TempDir()

	cfg := newTestConfig(t, dir,
		withAPIGroups("sonataflow.org/v1alpha08"),
		withDynamicObjs(
			map[schema.GroupVersionResource]string{
				sonataFlowGVR: "SonataFlowList",
			},
		),
	)

	o := &Orchestrator{}
	outDir := filepath.Join(dir, "orchestrator")
	_ = os.MkdirAll(outDir, 0o755)

	var addedNS []string
	addNS := func(ns string) { addedNS = append(addedNS, ns) }
	detected := o.gatherSonataFlowWorkflows(context.Background(), cfg, outDir, addNS)

	if detected {
		t.Error("expected not detected when no workflows")
	}
}

func TestGatherKnativeResources_NoAPI(t *testing.T) {
	dir := t.TempDir()
	cfg := newTestConfig(t, dir, withAPIGroups("apps/v1"))

	o := &Orchestrator{}
	outDir := filepath.Join(dir, "orchestrator")
	_ = os.MkdirAll(outDir, 0o755)

	var addedNS []string
	addNS := func(ns string) { addedNS = append(addedNS, ns) }
	detected := o.gatherKnativeResources(context.Background(), cfg, outDir, addNS)

	if detected {
		t.Error("expected not detected when no Knative API and no namespaces")
	}
}

func TestGatherKnativeResources_WithNamespace(t *testing.T) {
	dir := t.TempDir()

	servingNS := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: "knative-serving"},
	}

	cfg := newTestConfig(t, dir,
		withTypedObjs(servingNS),
		withAPIGroups("apps/v1"),
	)

	o := &Orchestrator{}
	outDir := filepath.Join(dir, "orchestrator")
	_ = os.MkdirAll(outDir, 0o755)

	var addedNS []string
	addNS := func(ns string) { addedNS = append(addedNS, ns) }
	detected := o.gatherKnativeResources(context.Background(), cfg, outDir, addNS)

	if !detected {
		t.Error("expected detected=true when knative-serving namespace exists")
	}
	if len(addedNS) != 1 || addedNS[0] != "knative-serving" {
		t.Errorf("expected [knative-serving], got %v", addedNS)
	}
}

func TestGatherKnativeResources_WithCRs(t *testing.T) {
	dir := t.TempDir()

	serving := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "operator.knative.dev/v1beta1",
			"kind":       "KnativeServing",
			"metadata": map[string]any{
				"name":      "knative-serving",
				"namespace": "knative-serving",
			},
			"status": map[string]any{
				"version": "1.14.0",
			},
		},
	}

	cfg := newTestConfig(t, dir,
		withAPIGroups("operator.knative.dev/v1beta1"),
		withDynamicObjs(
			map[schema.GroupVersionResource]string{
				knativeServingGVR:  "KnativeServingList",
				knativeEventingGVR: "KnativeEventingList",
			},
			serving,
		),
	)

	o := &Orchestrator{}
	outDir := filepath.Join(dir, "orchestrator")
	_ = os.MkdirAll(outDir, 0o755)

	var addedNS []string
	addNS := func(ns string) { addedNS = append(addedNS, ns) }
	detected := o.gatherKnativeResources(context.Background(), cfg, outDir, addNS)

	if !detected {
		t.Error("expected detected=true when KnativeServing CR exists")
	}

	knativeDir := filepath.Join(outDir, "knative")
	if _, err := os.Stat(filepath.Join(knativeDir, "knative-serving-list.txt")); err != nil {
		t.Error("expected knative-serving-list.txt")
	}
	if _, err := os.Stat(filepath.Join(knativeDir, "knative-serving.yaml")); err != nil {
		t.Error("expected knative-serving.yaml")
	}
}

func TestCollectKnativeCRs(t *testing.T) {
	dir := t.TempDir()

	serving := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "operator.knative.dev/v1beta1",
			"kind":       "KnativeServing",
			"metadata": map[string]any{
				"name":      "knative-serving",
				"namespace": "knative-serving",
			},
		},
	}

	cfg := newTestConfig(t, dir,
		withDynamicObjs(
			map[schema.GroupVersionResource]string{
				knativeServingGVR: "KnativeServingList",
			},
			serving,
		),
	)

	o := &Orchestrator{}
	listPath := filepath.Join(dir, "list.txt")
	yamlPath := filepath.Join(dir, "crs.yaml")
	items := o.collectKnativeCRs(context.Background(), cfg, knativeServingGVR, listPath, yamlPath, knativeServingColumns)

	if len(items) != 1 {
		t.Fatalf("got %d items, want 1", len(items))
	}
	if _, err := os.Stat(listPath); err != nil {
		t.Error("expected list file")
	}
	if _, err := os.Stat(yamlPath); err != nil {
		t.Error("expected YAML file")
	}
}

func TestCollectKnativeCRs_Empty(t *testing.T) {
	dir := t.TempDir()

	cfg := newTestConfig(t, dir,
		withDynamicObjs(
			map[schema.GroupVersionResource]string{
				knativeServingGVR: "KnativeServingList",
			},
		),
	)

	o := &Orchestrator{}
	listPath := filepath.Join(dir, "list.txt")
	yamlPath := filepath.Join(dir, "crs.yaml")
	items := o.collectKnativeCRs(context.Background(), cfg, knativeServingGVR, listPath, yamlPath, knativeServingColumns)

	if len(items) != 0 {
		t.Errorf("got %d items, want 0", len(items))
	}
}

func TestCollectKnativeNamespace(t *testing.T) {
	dir := t.TempDir()

	replicas := int32(1)
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "activator",
			Namespace: "knative-serving",
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"app": "activator"},
			},
		},
		Status: appsv1.DeploymentStatus{ReadyReplicas: 1},
	}
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "activator-service", Namespace: "knative-serving"},
		Spec: corev1.ServiceSpec{
			Type:      corev1.ServiceTypeClusterIP,
			ClusterIP: "10.0.0.5",
		},
	}
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "activator-abc", Namespace: "knative-serving"},
		Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "activator"}}},
		Status:     corev1.PodStatus{Phase: corev1.PodRunning},
	}

	cfg := newTestConfig(t, dir, withTypedObjs(dep, svc, pod))

	o := &Orchestrator{}
	nsDir := filepath.Join(dir, "knative-serving")
	_ = os.MkdirAll(nsDir, 0o755)
	o.collectKnativeNamespace(context.Background(), cfg, "knative-serving", nsDir)

	if _, err := os.Stat(filepath.Join(nsDir, "deployments.txt")); err != nil {
		t.Error("expected deployments.txt")
	}
	if _, err := os.Stat(filepath.Join(nsDir, "pods.txt")); err != nil {
		t.Error("expected pods.txt")
	}
	data, err := os.ReadFile(filepath.Join(nsDir, "services.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "activator-service") {
		t.Error("expected service name in services.txt")
	}
}

func TestCollectKnativeNamespace_Empty(t *testing.T) {
	dir := t.TempDir()

	cfg := newTestConfig(t, dir)

	o := &Orchestrator{}
	nsDir := filepath.Join(dir, "knative-serving")
	_ = os.MkdirAll(nsDir, 0o755)
	o.collectKnativeNamespace(context.Background(), cfg, "knative-serving", nsDir)

	data, err := os.ReadFile(filepath.Join(nsDir, "services.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "No services found") {
		t.Error("expected 'No services found'")
	}
}

func TestOrchestrator_Run_NothingDetected(t *testing.T) {
	dir := t.TempDir()

	cfg := newTestConfig(t, dir,
		withAPIGroups("apps/v1"),
		withDynamicObjs(
			map[schema.GroupVersionResource]string{
				crdGVR:                "CustomResourceDefinitionList",
				sonataFlowPlatformGVR: "SonataFlowPlatformList",
				sonataFlowGVR:         "SonataFlowList",
				knativeServingGVR:     "KnativeServingList",
				knativeEventingGVR:    "KnativeEventingList",
			},
		),
	)

	o := &Orchestrator{}
	err := o.Run(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	outDir := filepath.Join(dir, "orchestrator")
	if _, err := os.Stat(outDir); err != nil {
		t.Error("expected orchestrator directory")
	}
	data, err := os.ReadFile(filepath.Join(outDir, "summary.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "Orchestrator components detected: NO") {
		t.Error("expected detected=NO in summary")
	}
}

func TestOrchestrator_Run_WithDetectedComponents(t *testing.T) {
	dir := t.TempDir()

	serverlessNS := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: "openshift-serverless"},
	}

	cfg := newTestConfig(t, dir,
		withTypedObjs(serverlessNS),
		withAPIGroups("apps/v1"),
		withDynamicObjs(
			map[schema.GroupVersionResource]string{
				crdGVR:                "CustomResourceDefinitionList",
				csvGVR:                "ClusterServiceVersionList",
				subscriptionGVR:       "SubscriptionList",
				sonataFlowPlatformGVR: "SonataFlowPlatformList",
				sonataFlowGVR:         "SonataFlowList",
				knativeServingGVR:     "KnativeServingList",
				knativeEventingGVR:    "KnativeEventingList",
			},
		),
	)

	o := &Orchestrator{}
	err := o.Run(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	outDir := filepath.Join(dir, "orchestrator")
	data, err := os.ReadFile(filepath.Join(outDir, "summary.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "Orchestrator components detected: YES") {
		t.Error("expected detected=YES in summary")
	}

	nsFile := filepath.Join(outDir, "detected-namespaces.txt")
	nsData, err := os.ReadFile(nsFile)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(nsData), "openshift-serverless") {
		t.Error("expected openshift-serverless in detected namespaces")
	}
}

func TestCollectServerlessNamespace_WithCSVsAndDeployments(t *testing.T) {
	dir := t.TempDir()

	replicas := int32(1)
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "knative-operator",
			Namespace: "openshift-serverless",
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"app": "knative-operator"},
			},
		},
		Status: appsv1.DeploymentStatus{ReadyReplicas: 1},
	}

	csv := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "operators.coreos.com/v1alpha1",
			"kind":       "ClusterServiceVersion",
			"metadata": map[string]any{
				"name":      "serverless-operator.v1.34.0",
				"namespace": "openshift-serverless",
			},
			"spec": map[string]any{
				"version": "1.34.0",
			},
			"status": map[string]any{
				"phase": "Succeeded",
			},
		},
	}

	sub := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "operators.coreos.com/v1alpha1",
			"kind":       "Subscription",
			"metadata": map[string]any{
				"name":      "serverless-operator",
				"namespace": "openshift-serverless",
			},
			"spec": map[string]any{
				"channel": "stable",
			},
		},
	}

	cfg := newTestConfig(t, dir,
		withTypedObjs(dep),
		withAPIGroups("operators.coreos.com/v1alpha1"),
		withDynamicObjs(
			map[schema.GroupVersionResource]string{
				csvGVR:          "ClusterServiceVersionList",
				subscriptionGVR: "SubscriptionList",
			},
			csv, sub,
		),
	)

	o := &Orchestrator{}
	nsDir := filepath.Join(dir, "ns")
	_ = os.MkdirAll(nsDir, 0o755)
	o.collectServerlessNamespace(context.Background(), cfg, "openshift-serverless", nsDir,
		[]logSelector{})

	if _, err := os.Stat(filepath.Join(nsDir, "csv-list.txt")); err != nil {
		t.Error("expected csv-list.txt")
	}
	if _, err := os.Stat(filepath.Join(nsDir, "csv-all.yaml")); err != nil {
		t.Error("expected csv-all.yaml")
	}
	if _, err := os.Stat(filepath.Join(nsDir, "subscriptions.txt")); err != nil {
		t.Error("expected subscriptions.txt")
	}
	if _, err := os.Stat(filepath.Join(nsDir, "subscriptions.yaml")); err != nil {
		t.Error("expected subscriptions.yaml")
	}
	if _, err := os.Stat(filepath.Join(nsDir, "deployments.txt")); err != nil {
		t.Error("expected deployments.txt")
	}
	if _, err := os.Stat(filepath.Join(nsDir, "deployments.yaml")); err != nil {
		t.Error("expected deployments.yaml")
	}
}

func TestGenerateSummary_WithOLMCSVs(t *testing.T) {
	dir := t.TempDir()

	serverlessCSV := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "operators.coreos.com/v1alpha1",
			"kind":       "ClusterServiceVersion",
			"metadata": map[string]any{
				"name":      "serverless-operator.v1.34.0",
				"namespace": "openshift-serverless",
			},
			"spec":   map[string]any{"version": "1.34.0"},
			"status": map[string]any{"phase": "Succeeded"},
		},
	}
	logicCSV := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "operators.coreos.com/v1alpha1",
			"kind":       "ClusterServiceVersion",
			"metadata": map[string]any{
				"name":      "logic-operator-rhel8.v1.34.0",
				"namespace": "openshift-serverless-logic",
			},
			"spec":   map[string]any{"version": "1.34.0"},
			"status": map[string]any{"phase": "Succeeded"},
		},
	}

	cfg := newTestConfig(t, dir,
		withAPIGroups("operators.coreos.com/v1alpha1"),
		withDynamicObjs(
			map[schema.GroupVersionResource]string{
				csvGVR:                "ClusterServiceVersionList",
				sonataFlowPlatformGVR: "SonataFlowPlatformList",
				sonataFlowGVR:         "SonataFlowList",
				knativeServingGVR:     "KnativeServingList",
				knativeEventingGVR:    "KnativeEventingList",
			},
			serverlessCSV, logicCSV,
		),
	)

	o := &Orchestrator{}
	outDir := filepath.Join(dir, "orchestrator")
	_ = os.MkdirAll(outDir, 0o755)
	o.generateSummary(context.Background(), cfg, outDir, true)

	data, err := os.ReadFile(filepath.Join(outDir, "summary.txt"))
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	if !strings.Contains(content, "serverless-operator.v1.34.0") {
		t.Error("expected serverless CSV name in summary")
	}
	if !strings.Contains(content, "logic-operator-rhel8.v1.34.0") {
		t.Error("expected logic CSV name in summary")
	}
	if !strings.Contains(content, "1.34.0") {
		t.Error("expected version in summary")
	}
	if !strings.Contains(content, "Succeeded") {
		t.Error("expected phase in summary")
	}
}

func TestGenerateSummary_WithAllComponents(t *testing.T) {
	dir := t.TempDir()

	sfp := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "sonataflow.org/v1alpha08",
			"kind":       "SonataFlowPlatform",
			"metadata":   map[string]any{"name": "platform-1", "namespace": "sonata-ns"},
			"status":     map[string]any{"phase": "Ready"},
		},
	}
	wf := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "sonataflow.org/v1alpha08",
			"kind":       "SonataFlow",
			"metadata":   map[string]any{"name": "my-workflow", "namespace": "wf-ns"},
			"status":     map[string]any{"phase": "Running"},
		},
	}
	serving := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "operator.knative.dev/v1beta1",
			"kind":       "KnativeServing",
			"metadata":   map[string]any{"name": "knative-serving", "namespace": "knative-serving"},
			"status": map[string]any{
				"version": "1.14.0",
				"conditions": []any{
					map[string]any{"type": "Ready", "status": "True"},
				},
			},
		},
	}
	eventing := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "operator.knative.dev/v1beta1",
			"kind":       "KnativeEventing",
			"metadata":   map[string]any{"name": "knative-eventing", "namespace": "knative-eventing"},
			"status": map[string]any{
				"version": "1.14.0",
				"conditions": []any{
					map[string]any{"type": "Ready", "status": "True"},
				},
			},
		},
	}

	cfg := newTestConfig(t, dir,
		withAPIGroups("apps/v1"),
		withDynamicObjs(
			map[schema.GroupVersionResource]string{
				sonataFlowPlatformGVR: "SonataFlowPlatformList",
				sonataFlowGVR:         "SonataFlowList",
				knativeServingGVR:     "KnativeServingList",
				knativeEventingGVR:    "KnativeEventingList",
			},
			sfp, wf, serving, eventing,
		),
	)

	o := &Orchestrator{}
	outDir := filepath.Join(dir, "orchestrator")
	_ = os.MkdirAll(outDir, 0o755)
	o.generateSummary(context.Background(), cfg, outDir, true)

	data, err := os.ReadFile(filepath.Join(outDir, "summary.txt"))
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	if !strings.Contains(content, "platform-1") {
		t.Error("expected SonataFlowPlatform name")
	}
	if !strings.Contains(content, "my-workflow") {
		t.Error("expected SonataFlow workflow name")
	}
	if !strings.Contains(content, "knative-serving") {
		t.Error("expected KnativeServing name")
	}
	if !strings.Contains(content, "knative-eventing") {
		t.Error("expected KnativeEventing name")
	}
	if !strings.Contains(content, "1.14.0") {
		t.Error("expected version in Knative section")
	}
}

func TestGenerateSummary_OLMSkippedNamespace(t *testing.T) {
	dir := t.TempDir()

	cfg := newTestConfig(t, dir,
		withAPIGroups("operators.coreos.com/v1alpha1"),
		withDynamicObjs(
			map[schema.GroupVersionResource]string{
				csvGVR:                "ClusterServiceVersionList",
				sonataFlowPlatformGVR: "SonataFlowPlatformList",
				sonataFlowGVR:         "SonataFlowList",
				knativeServingGVR:     "KnativeServingList",
				knativeEventingGVR:    "KnativeEventingList",
			},
		),
	)
	cfg.TargetNamespaces = []string{"other-ns"}

	o := &Orchestrator{}
	outDir := filepath.Join(dir, "orchestrator")
	_ = os.MkdirAll(outDir, 0o755)
	o.generateSummary(context.Background(), cfg, outDir, false)

	data, err := os.ReadFile(filepath.Join(outDir, "summary.txt"))
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	if !strings.Contains(content, "Skipped (not in target namespaces)") {
		t.Error("expected skipped message for filtered OLM namespaces")
	}
}

func TestGatherKnativeResources_WithEventingNamespace(t *testing.T) {
	dir := t.TempDir()

	eventingNS := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: "knative-eventing"},
	}

	cfg := newTestConfig(t, dir,
		withTypedObjs(eventingNS),
		withAPIGroups("apps/v1"),
	)

	o := &Orchestrator{}
	outDir := filepath.Join(dir, "orchestrator")
	_ = os.MkdirAll(outDir, 0o755)

	var addedNS []string
	addNS := func(ns string) { addedNS = append(addedNS, ns) }
	detected := o.gatherKnativeResources(context.Background(), cfg, outDir, addNS)

	if !detected {
		t.Error("expected detected=true when knative-eventing namespace exists")
	}
	if len(addedNS) != 1 || addedNS[0] != "knative-eventing" {
		t.Errorf("expected [knative-eventing], got %v", addedNS)
	}
}

func TestGatherKnativeResources_WithKafkaCRs(t *testing.T) {
	dir := t.TempDir()

	kafka := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "operator.serverless.openshift.io/v1alpha1",
			"kind":       "KnativeKafka",
			"metadata":   map[string]any{"name": "knative-kafka", "namespace": "knative-eventing"},
		},
	}

	cfg := newTestConfig(t, dir,
		withAPIGroups("operator.knative.dev/v1beta1", "operator.serverless.openshift.io/v1alpha1"),
		withDynamicObjs(
			map[schema.GroupVersionResource]string{
				knativeServingGVR:  "KnativeServingList",
				knativeEventingGVR: "KnativeEventingList",
				knativeKafkaGVR:    "KnativeKafkaList",
			},
			kafka,
		),
	)

	o := &Orchestrator{}
	outDir := filepath.Join(dir, "orchestrator")
	_ = os.MkdirAll(outDir, 0o755)

	var addedNS []string
	addNS := func(ns string) { addedNS = append(addedNS, ns) }
	o.gatherKnativeResources(context.Background(), cfg, outDir, addNS)

	knativeDir := filepath.Join(outDir, "knative")
	if _, err := os.Stat(filepath.Join(knativeDir, "knative-kafka-list.txt")); err != nil {
		t.Error("expected knative-kafka-list.txt")
	}
}

func TestGatherKnativeResources_NamespaceFiltered(t *testing.T) {
	dir := t.TempDir()

	servingNS := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: "knative-serving"},
	}

	cfg := newTestConfig(t, dir,
		withTypedObjs(servingNS),
		withAPIGroups("apps/v1"),
	)
	cfg.TargetNamespaces = []string{"other-ns"}

	o := &Orchestrator{}
	outDir := filepath.Join(dir, "orchestrator")
	_ = os.MkdirAll(outDir, 0o755)

	var addedNS []string
	addNS := func(ns string) { addedNS = append(addedNS, ns) }
	detected := o.gatherKnativeResources(context.Background(), cfg, outDir, addNS)

	if !detected {
		t.Error("expected detected=true (namespace exists even if filtered)")
	}
	if len(addedNS) != 0 {
		t.Errorf("expected no namespaces added when filtered, got %v", addedNS)
	}
}

func TestCollectKnativeCRs_Error(t *testing.T) {
	dir := t.TempDir()

	scheme := runtime.NewScheme()
	dynClient := fakedynamic.NewSimpleDynamicClientWithCustomListKinds(scheme,
		map[schema.GroupVersionResource]string{
			knativeServingGVR: "KnativeServingList",
		},
	)
	dynClient.PrependReactor("list", "*", func(_ k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, fmt.Errorf("API unavailable")
	})

	cfg := newTestConfig(t, dir)
	cfg.Client.Dynamic = dynClient

	o := &Orchestrator{}
	listPath := filepath.Join(dir, "serving-list.txt")
	yamlPath := filepath.Join(dir, "serving.yaml")
	items := o.collectKnativeCRs(context.Background(), cfg, knativeServingGVR, listPath, yamlPath, knativeServingColumns)

	if len(items) != 0 {
		t.Errorf("got %d items, want 0", len(items))
	}

	listData, err := os.ReadFile(listPath)
	if err != nil {
		t.Fatalf("expected list error file: %v", err)
	}
	if !strings.Contains(string(listData), "API unavailable") {
		t.Error("expected error message in list file")
	}

	yamlData, err := os.ReadFile(yamlPath)
	if err != nil {
		t.Fatalf("expected yaml error file: %v", err)
	}
	if !strings.Contains(string(yamlData), "API unavailable") {
		t.Error("expected error message in yaml file")
	}
}
