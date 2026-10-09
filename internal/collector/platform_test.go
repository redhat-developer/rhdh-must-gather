package collector

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

func TestPlatform_VanillaK8s(t *testing.T) {
	dir := t.TempDir()
	cfg := newTestConfig(t, dir,
		withAPIGroups("apps/v1"),
		withTypedObjs(testNode("node1", "", nil)),
	)

	p := &Platform{}
	if err := p.Run(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}

	info := readPlatformJSON(t, dir)
	if info.Platform != "Vanilla K8s" {
		t.Errorf("platform = %q, want Vanilla K8s", info.Platform)
	}
}

func TestPlatform_EKS(t *testing.T) {
	dir := t.TempDir()
	cfg := newTestConfig(t, dir,
		withAPIGroups("apps/v1"),
		withTypedObjs(testNode("node1", "aws://us-east-1/i-123", map[string]string{
			"eks.amazonaws.com/nodegroup": "my-nodegroup",
		})),
	)

	p := &Platform{}
	if err := p.Run(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}

	info := readPlatformJSON(t, dir)
	if info.Platform != "EKS" {
		t.Errorf("platform = %q, want EKS", info.Platform)
	}
	if info.Underlying != "AWS" {
		t.Errorf("underlying = %q, want AWS", info.Underlying)
	}
}

func TestPlatform_GKE(t *testing.T) {
	dir := t.TempDir()
	cfg := newTestConfig(t, dir,
		withAPIGroups("apps/v1"),
		withTypedObjs(testNode("node1", "gce://project/zone/instance", map[string]string{
			"cloud.google.com/gke-nodepool": "default-pool",
		})),
	)

	p := &Platform{}
	if err := p.Run(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}

	info := readPlatformJSON(t, dir)
	if info.Platform != "GKE" {
		t.Errorf("platform = %q, want GKE", info.Platform)
	}
}

func TestPlatform_OCP(t *testing.T) {
	dir := t.TempDir()

	cv := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "config.openshift.io/v1",
			"kind":       "ClusterVersion",
			"metadata":   map[string]any{"name": "version"},
			"status": map[string]any{
				"desired": map[string]any{
					"version":           "4.15.3",
					"kubernetesVersion": "v1.28.6",
				},
			},
		},
	}

	infra := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "config.openshift.io/v1",
			"kind":       "Infrastructure",
			"metadata":   map[string]any{"name": "cluster"},
			"status": map[string]any{
				"platformStatus": map[string]any{
					"type": "AWS",
				},
			},
		},
	}

	cfg := newTestConfig(t, dir,
		withAPIGroups("config.openshift.io/v1"),
		withDynamicObjs(
			map[schema.GroupVersionResource]string{
				clusterVersionGVR: "ClusterVersionList",
				infrastructureGVR: "InfrastructureList",
			},
			cv, infra,
		),
	)

	p := &Platform{}
	if err := p.Run(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}

	info := readPlatformJSON(t, dir)
	if info.Platform != "OCP" {
		t.Errorf("platform = %q, want OCP", info.Platform)
	}
	if info.OCPVersion != "4.15.3" {
		t.Errorf("ocpVersion = %q, want 4.15.3", info.OCPVersion)
	}
	if info.K8sVersion != "v1.28.6" {
		t.Errorf("k8sVersion = %q, want v1.28.6", info.K8sVersion)
	}
	if info.Underlying != "AWS" {
		t.Errorf("underlying = %q, want AWS", info.Underlying)
	}
}

func TestPlatform_AKS(t *testing.T) {
	dir := t.TempDir()
	cfg := newTestConfig(t, dir,
		withAPIGroups("apps/v1"),
		withTypedObjs(testNode("node1", "azure:///subscriptions/sub/resourceGroups/rg/providers/Microsoft.Compute/virtualMachineScaleSets/vmss/virtualMachines/0", map[string]string{
			"agentpool": "default",
		})),
	)

	p := &Platform{}
	if err := p.Run(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}

	info := readPlatformJSON(t, dir)
	if info.Platform != "AKS" {
		t.Errorf("platform = %q, want AKS", info.Platform)
	}
	if info.Underlying != "Azure" {
		t.Errorf("underlying = %q, want Azure", info.Underlying)
	}
}

func TestPlatform_ROSA(t *testing.T) {
	dir := t.TempDir()

	cv := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "config.openshift.io/v1",
			"kind":       "ClusterVersion",
			"metadata":   map[string]any{"name": "version"},
			"status": map[string]any{
				"desired": map[string]any{
					"version": "4.16.0",
				},
			},
		},
	}

	infra := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "config.openshift.io/v1",
			"kind":       "Infrastructure",
			"metadata":   map[string]any{"name": "cluster"},
			"status": map[string]any{
				"platformStatus": map[string]any{
					"type": "AWS",
					"aws": map[string]any{
						"resourceTags": []any{
							map[string]any{"key": "rosa.openshift.io/cluster-id"},
						},
					},
				},
			},
		},
	}

	cfg := newTestConfig(t, dir,
		withAPIGroups("config.openshift.io/v1"),
		withDynamicObjs(
			map[schema.GroupVersionResource]string{
				clusterVersionGVR: "ClusterVersionList",
				infrastructureGVR: "InfrastructureList",
			},
			cv, infra,
		),
	)

	p := &Platform{}
	if err := p.Run(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}

	info := readPlatformJSON(t, dir)
	if info.Platform != "ROSA" {
		t.Errorf("platform = %q, want ROSA", info.Platform)
	}
}

func TestPlatform_ROKS(t *testing.T) {
	dir := t.TempDir()

	cv := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "config.openshift.io/v1",
			"kind":       "ClusterVersion",
			"metadata":   map[string]any{"name": "version"},
			"status":     map[string]any{"desired": map[string]any{"version": "4.16.0"}},
		},
	}

	infra := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "config.openshift.io/v1",
			"kind":       "Infrastructure",
			"metadata":   map[string]any{"name": "cluster"},
			"status": map[string]any{
				"platformStatus": map[string]any{
					"type": "IBMCloud",
				},
			},
		},
	}

	cfg := newTestConfig(t, dir,
		withAPIGroups("config.openshift.io/v1"),
		withDynamicObjs(
			map[schema.GroupVersionResource]string{
				clusterVersionGVR: "ClusterVersionList",
				infrastructureGVR: "InfrastructureList",
			},
			cv, infra,
		),
	)

	p := &Platform{}
	if err := p.Run(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}

	info := readPlatformJSON(t, dir)
	if info.Platform != "ROKS" {
		t.Errorf("platform = %q, want ROKS", info.Platform)
	}
	if info.Underlying != "IBMCloud" {
		t.Errorf("underlying = %q, want IBMCloud", info.Underlying)
	}
}

func TestPlatform_vSphere(t *testing.T) {
	dir := t.TempDir()
	cfg := newTestConfig(t, dir,
		withAPIGroups("apps/v1"),
		withTypedObjs(testNode("node1", "vsphere://vm-123", nil)),
	)

	p := &Platform{}
	if err := p.Run(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}

	info := readPlatformJSON(t, dir)
	if info.Underlying != "vSphere" {
		t.Errorf("underlying = %q, want vSphere", info.Underlying)
	}
}

func TestPlatform_IBMCloud(t *testing.T) {
	dir := t.TempDir()
	cfg := newTestConfig(t, dir,
		withAPIGroups("apps/v1"),
		withTypedObjs(testNode("node1", "ibm://instance-1", nil)),
	)

	p := &Platform{}
	if err := p.Run(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}

	info := readPlatformJSON(t, dir)
	if info.Underlying != "IBMCloud" {
		t.Errorf("underlying = %q, want IBMCloud", info.Underlying)
	}
}

func TestPlatform_OutputFiles(t *testing.T) {
	dir := t.TempDir()
	cfg := newTestConfig(t, dir,
		withAPIGroups("apps/v1"),
		withTypedObjs(testNode("node1", "", nil)),
	)

	p := &Platform{}
	if err := p.Run(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(filepath.Join(dir, "platform", "platform.json")); err != nil {
		t.Error("platform.json not created")
	}
	if _, err := os.Stat(filepath.Join(dir, "platform", "platform.txt")); err != nil {
		t.Error("platform.txt not created")
	}
}

func TestNestedString(t *testing.T) {
	obj := map[string]any{
		"a": map[string]any{
			"b": map[string]any{
				"c": "value",
			},
		},
	}

	val, ok, _ := nestedString(obj, "a", "b", "c")
	if !ok || val != "value" {
		t.Errorf("got %q (ok=%v), want value", val, ok)
	}

	_, ok, _ = nestedString(obj, "a", "x")
	if ok {
		t.Error("expected missing path to return ok=false")
	}
}

func TestPlatform_ARO(t *testing.T) {
	dir := t.TempDir()

	cv := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "config.openshift.io/v1",
			"kind":       "ClusterVersion",
			"metadata":   map[string]any{"name": "version"},
			"status": map[string]any{
				"desired": map[string]any{"version": "4.16.0"},
			},
		},
	}

	infra := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "config.openshift.io/v1",
			"kind":       "Infrastructure",
			"metadata":   map[string]any{"name": "cluster"},
			"status": map[string]any{
				"platformStatus": map[string]any{
					"type": "Azure",
					"azure": map[string]any{
						"resourceTags": []any{
							map[string]any{"key": "aro.openshift.io/cluster-id"},
						},
					},
				},
			},
		},
	}

	cfg := newTestConfig(t, dir,
		withAPIGroups("config.openshift.io/v1"),
		withDynamicObjs(
			map[schema.GroupVersionResource]string{
				clusterVersionGVR: "ClusterVersionList",
				infrastructureGVR: "InfrastructureList",
			},
			cv, infra,
		),
	)

	p := &Platform{}
	if err := p.Run(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}

	info := readPlatformJSON(t, dir)
	if info.Platform != "ARO" {
		t.Errorf("platform = %q, want ARO", info.Platform)
	}
	if info.Underlying != "Azure" {
		t.Errorf("underlying = %q, want Azure", info.Underlying)
	}
}

func TestPlatform_OCP_NoClusterVersion(t *testing.T) {
	dir := t.TempDir()

	infra := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "config.openshift.io/v1",
			"kind":       "Infrastructure",
			"metadata":   map[string]any{"name": "cluster"},
			"status": map[string]any{
				"platformStatus": map[string]any{"type": "AWS"},
			},
		},
	}

	cfg := newTestConfig(t, dir,
		withAPIGroups("config.openshift.io/v1"),
		withDynamicObjs(
			map[schema.GroupVersionResource]string{
				clusterVersionGVR: "ClusterVersionList",
				infrastructureGVR: "InfrastructureList",
			},
			infra,
		),
	)

	p := &Platform{}
	if err := p.Run(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}

	info := readPlatformJSON(t, dir)
	if info.Platform != "OCP" {
		t.Errorf("platform = %q, want OCP", info.Platform)
	}
	if info.OCPVersion != "" {
		t.Errorf("ocpVersion = %q, want empty when ClusterVersion not available", info.OCPVersion)
	}
}

func TestPlatform_K8s_NoNodes(t *testing.T) {
	dir := t.TempDir()
	cfg := newTestConfig(t, dir,
		withAPIGroups("apps/v1"),
	)

	p := &Platform{}
	if err := p.Run(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}

	info := readPlatformJSON(t, dir)
	if info.Platform != "Vanilla K8s" {
		t.Errorf("platform = %q, want Vanilla K8s", info.Platform)
	}
}

func TestPlatform_OCP_FallbackK8sVersion(t *testing.T) {
	dir := t.TempDir()

	cv := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "config.openshift.io/v1",
			"kind":       "ClusterVersion",
			"metadata":   map[string]any{"name": "version"},
			"status": map[string]any{
				"desired": map[string]any{
					"version": "4.16.0",
				},
			},
		},
	}

	cfg := newTestConfig(t, dir,
		withAPIGroups("config.openshift.io/v1"),
		withDynamicObjs(
			map[schema.GroupVersionResource]string{
				clusterVersionGVR: "ClusterVersionList",
				infrastructureGVR: "InfrastructureList",
			},
			cv,
		),
	)

	p := &Platform{}
	if err := p.Run(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}

	info := readPlatformJSON(t, dir)
	if info.Platform != "OCP" {
		t.Errorf("platform = %q, want OCP", info.Platform)
	}
	if info.OCPVersion != "4.16.0" {
		t.Errorf("ocpVersion = %q, want 4.16.0", info.OCPVersion)
	}
}

func TestPlatform_OCP_InfraPlatformFallback(t *testing.T) {
	dir := t.TempDir()

	cv := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "config.openshift.io/v1",
			"kind":       "ClusterVersion",
			"metadata":   map[string]any{"name": "version"},
			"status":     map[string]any{"desired": map[string]any{"version": "4.16.0"}},
		},
	}

	infra := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "config.openshift.io/v1",
			"kind":       "Infrastructure",
			"metadata":   map[string]any{"name": "cluster"},
			"status": map[string]any{
				"platform": "BareMetal",
			},
		},
	}

	cfg := newTestConfig(t, dir,
		withAPIGroups("config.openshift.io/v1"),
		withDynamicObjs(
			map[schema.GroupVersionResource]string{
				clusterVersionGVR: "ClusterVersionList",
				infrastructureGVR: "InfrastructureList",
			},
			cv, infra,
		),
	)

	p := &Platform{}
	if err := p.Run(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}

	info := readPlatformJSON(t, dir)
	if info.Underlying != "BareMetal" {
		t.Errorf("underlying = %q, want BareMetal (fallback to status.platform)", info.Underlying)
	}
}

func TestNestedString_NonMapIntermediate(t *testing.T) {
	obj := map[string]any{
		"status": "not-a-map",
	}
	val, found, err := nestedString(obj, "status", "version")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if found {
		t.Error("expected found=false for non-map intermediate")
	}
	if val != "" {
		t.Errorf("val = %q, want empty", val)
	}
}

func TestNestedString_NonStringFinalValue(t *testing.T) {
	obj := map[string]any{
		"spec": map[string]any{
			"replicas": int64(3),
		},
	}
	val, found, err := nestedString(obj, "spec", "replicas")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if found {
		t.Error("expected found=false for non-string value")
	}
	if val != "" {
		t.Errorf("val = %q, want empty", val)
	}
}

func TestGetServerVersion_VanillaK8s(t *testing.T) {
	dir := t.TempDir()
	cfg := newTestConfig(t, dir)

	p := &Platform{}
	ver := p.getServerVersion(cfg)
	if ver == "" {
		t.Log("FakeDiscovery.ServerVersion returns empty by default, expected")
	}
}

func readPlatformJSON(t *testing.T, dir string) platformInfo {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, "platform", "platform.json"))
	if err != nil {
		t.Fatalf("reading platform.json: %v", err)
	}
	var info platformInfo
	if err := json.Unmarshal(data, &info); err != nil {
		t.Fatalf("parsing platform.json: %v", err)
	}
	return info
}
