package collector

import (
	"testing"
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
