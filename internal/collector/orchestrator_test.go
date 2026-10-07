package collector

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
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
