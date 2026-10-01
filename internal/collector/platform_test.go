package collector

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	fakediscovery "k8s.io/client-go/discovery/fake"
	fakedynamic "k8s.io/client-go/dynamic/fake"
	fakeclientset "k8s.io/client-go/kubernetes/fake"

	"github.com/redhat-developer/rhdh-must-gather/internal/kube"
)

func TestPlatform_VanillaK8s(t *testing.T) {
	dir := t.TempDir()
	cfg := newTestConfig(t, dir,
		[]string{"apps/v1"},
		[]*corev1.Node{testNode("node1", "", nil)},
		nil,
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
		[]string{"apps/v1"},
		[]*corev1.Node{testNode("node1", "aws://us-east-1/i-123", map[string]string{
			"eks.amazonaws.com/nodegroup": "my-nodegroup",
		})},
		nil,
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
		[]string{"apps/v1"},
		[]*corev1.Node{testNode("node1", "gce://project/zone/instance", map[string]string{
			"cloud.google.com/gke-nodepool": "default-pool",
		})},
		nil,
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
		[]string{"config.openshift.io/v1"},
		nil,
		[]runtime.Object{cv, infra},
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

func TestPlatform_OutputFiles(t *testing.T) {
	dir := t.TempDir()
	cfg := newTestConfig(t, dir,
		[]string{"apps/v1"},
		[]*corev1.Node{testNode("node1", "", nil)},
		nil,
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

func newTestConfig(t *testing.T, basePath string, apiGroupVersions []string, nodes []*corev1.Node, dynamicObjs []runtime.Object) *Config {
	t.Helper()

	var typedObjs []runtime.Object
	for _, n := range nodes {
		typedObjs = append(typedObjs, n)
	}
	fakeClient := fakeclientset.NewSimpleClientset(typedObjs...)

	fd := fakeClient.Discovery().(*fakediscovery.FakeDiscovery)
	resources := make([]*metav1.APIResourceList, len(apiGroupVersions))
	for i, gv := range apiGroupVersions {
		resources[i] = &metav1.APIResourceList{GroupVersion: gv}
	}
	fd.Resources = resources

	scheme := runtime.NewScheme()
	dynClient := fakedynamic.NewSimpleDynamicClientWithCustomListKinds(scheme,
		map[schema.GroupVersionResource]string{
			clusterVersionGVR: "ClusterVersionList",
			infrastructureGVR: "InfrastructureList",
		},
		dynamicObjs...)

	return &Config{
		BasePath:    basePath,
		Interrupted: new(atomic.Bool),
		Client: &kube.Client{
			Clientset: fakeClient,
			Discovery: fd,
			Dynamic:   dynClient,
		},
	}
}

func testNode(name, providerID string, labels map[string]string) *corev1.Node {
	if labels == nil {
		labels = map[string]string{}
	}
	return &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:   name,
			Labels: labels,
		},
		Spec: corev1.NodeSpec{
			ProviderID: providerID,
		},
	}
}
