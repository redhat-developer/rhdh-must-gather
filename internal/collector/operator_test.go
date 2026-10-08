package collector

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
)

func TestOperator_Name(t *testing.T) {
	o := &Operator{}
	if got := o.Name(); got != "operator" {
		t.Errorf("Name() = %q, want %q", got, "operator")
	}
}

func TestDeploymentControlledByBackstageCR(t *testing.T) {
	controller := true
	crUID := types.UID("test-uid")

	tests := []struct {
		name   string
		dep    *appsv1.Deployment
		crName string
		crUID  types.UID
		want   bool
	}{
		{
			name: "matching owner",
			dep: &appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					OwnerReferences: []metav1.OwnerReference{{
						APIVersion: "rhdh.redhat.com/v1alpha5",
						Kind:       "Backstage",
						Name:       "my-backstage",
						UID:        crUID,
						Controller: &controller,
					}},
				},
			},
			crName: "my-backstage",
			crUID:  crUID,
			want:   true,
		},
		{
			name: "empty UID matches any",
			dep: &appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					OwnerReferences: []metav1.OwnerReference{{
						APIVersion: "rhdh.redhat.com/v1alpha5",
						Kind:       "Backstage",
						Name:       "my-backstage",
						UID:        crUID,
						Controller: &controller,
					}},
				},
			},
			crName: "my-backstage",
			crUID:  "",
			want:   true,
		},
		{
			name: "wrong kind",
			dep: &appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					OwnerReferences: []metav1.OwnerReference{{
						APIVersion: "rhdh.redhat.com/v1alpha5",
						Kind:       "OtherKind",
						Name:       "my-backstage",
						UID:        crUID,
						Controller: &controller,
					}},
				},
			},
			crName: "my-backstage",
			crUID:  crUID,
			want:   false,
		},
		{
			name: "wrong API group",
			dep: &appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					OwnerReferences: []metav1.OwnerReference{{
						APIVersion: "other.group/v1",
						Kind:       "Backstage",
						Name:       "my-backstage",
						UID:        crUID,
						Controller: &controller,
					}},
				},
			},
			crName: "my-backstage",
			crUID:  crUID,
			want:   false,
		},
		{
			name: "not a controller",
			dep: &appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					OwnerReferences: []metav1.OwnerReference{{
						APIVersion: "rhdh.redhat.com/v1alpha5",
						Kind:       "Backstage",
						Name:       "my-backstage",
						UID:        crUID,
					}},
				},
			},
			crName: "my-backstage",
			crUID:  crUID,
			want:   false,
		},
		{
			name:   "no owner refs",
			dep:    &appsv1.Deployment{},
			crName: "my-backstage",
			crUID:  crUID,
			want:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := deploymentControlledByBackstageCR(tt.dep, tt.crName, tt.crUID)
			if got != tt.want {
				t.Errorf("deploymentControlledByBackstageCR() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestOwnedOKPDeployments(t *testing.T) {
	controller := true
	notController := false
	crUID := types.UID("developer-hub-uid")

	deployment := func(name, container string, owner metav1.OwnerReference) appsv1.Deployment {
		return appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{Name: name, OwnerReferences: []metav1.OwnerReference{owner}},
			Spec: appsv1.DeploymentSpec{Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{
				Containers: []corev1.Container{{Name: container}},
			}}},
		}
	}
	owner := func(name string, uid types.UID, controller *bool) metav1.OwnerReference {
		return metav1.OwnerReference{
			APIVersion: "rhdh.redhat.com/v1alpha5",
			Kind:       "Backstage",
			Name:       name,
			UID:        uid,
			Controller: controller,
		}
	}

	deployments := []appsv1.Deployment{
		deployment("z-okp", "okp", owner("developer-hub", crUID, &controller)),
		deployment("a-okp", "okp", owner("developer-hub", crUID, &controller)),
		deployment("backstage-developer-hub", "okp", owner("developer-hub", crUID, &controller)),
		deployment("wrong-container", "worker", owner("developer-hub", crUID, &controller)),
		deployment("wrong-cr", "okp", owner("another-hub", crUID, &controller)),
		deployment("wrong-uid", "okp", owner("developer-hub", "old-uid", &controller)),
		deployment("not-controller", "okp", owner("developer-hub", crUID, &notController)),
	}

	got := ownedOKPDeployments(deployments, "developer-hub", crUID, "backstage-developer-hub")
	var gotNames []string
	for _, item := range got {
		gotNames = append(gotNames, item.Name)
	}
	want := []string{"a-okp", "z-okp"}
	if !reflect.DeepEqual(gotNames, want) {
		t.Errorf("ownedOKPDeployments() = %q, want %q", gotNames, want)
	}
}

func TestOwnedOKPDeployments_IAOnly(t *testing.T) {
	controller := true
	deployments := []appsv1.Deployment{{
		ObjectMeta: metav1.ObjectMeta{
			Name: "backstage-developer-hub",
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: "rhdh.redhat.com/v1alpha5",
				Kind:       "Backstage",
				Name:       "developer-hub",
				UID:        "developer-hub-uid",
				Controller: &controller,
			}},
		},
		Spec: appsv1.DeploymentSpec{Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{
			Containers: []corev1.Container{{Name: "backstage-backend"}, {Name: "lightspeed-core"}},
		}}},
	}}

	got := ownedOKPDeployments(deployments, "developer-hub", "developer-hub-uid", "backstage-developer-hub")
	if len(got) != 0 {
		t.Errorf("ownedOKPDeployments() returned %d workloads for an IA-only deployment", len(got))
	}
}

func TestIsRHDHRelated(t *testing.T) {
	tests := []struct {
		label string
		name  string
		want  bool
	}{
		{"rhdh operator", "rhdh-operator.v1.5.0", true},
		{"backstage operator", "backstage-operator.v1.0.0", true},
		{"developer hub operator", "developer-hub-operator.v1.0.0", true},
		{"case insensitive", "RHDH-Operator", true},
		{"backstage in name", "my-Backstage-app", true},
		{"unrelated operator", "some-other-operator", false},
		{"cert manager", "cert-manager.v1.0.0", false},
		{"empty string", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.label, func(t *testing.T) {
			got := isRHDHRelated(tt.name)
			if got != tt.want {
				t.Errorf("isRHDHRelated(%q) = %v, want %v", tt.name, got, tt.want)
			}
		})
	}
}

func TestFilterRHDHResources(t *testing.T) {
	items := []unstructured.Unstructured{
		{Object: map[string]any{"metadata": map[string]any{"name": "rhdh-operator.v1.5.0"}}},
		{Object: map[string]any{"metadata": map[string]any{"name": "cert-manager.v1.0.0"}}},
		{Object: map[string]any{"metadata": map[string]any{"name": "backstage-operator.v1.0.0"}}},
	}

	filtered := filterRHDHResources(items)
	if len(filtered) != 2 {
		t.Fatalf("got %d items, want 2", len(filtered))
	}
	if filtered[0].GetName() != "rhdh-operator.v1.5.0" {
		t.Errorf("first item = %q, want rhdh-operator.v1.5.0", filtered[0].GetName())
	}
	if filtered[1].GetName() != "backstage-operator.v1.0.0" {
		t.Errorf("second item = %q, want backstage-operator.v1.0.0", filtered[1].GetName())
	}
}

func TestFilterRHDHResources_Empty(t *testing.T) {
	items := []unstructured.Unstructured{
		{Object: map[string]any{"metadata": map[string]any{"name": "cert-manager"}}},
	}
	filtered := filterRHDHResources(items)
	if len(filtered) != 0 {
		t.Errorf("got %d items, want 0", len(filtered))
	}
}

func TestWriteDynamicTable(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test-table.txt")

	items := []unstructured.Unstructured{
		{Object: map[string]any{
			"metadata": map[string]any{
				"namespace": "ns1",
				"name":      "rhdh-operator.v1.5.0",
			},
			"spec": map[string]any{
				"displayName": "RHDH Operator",
				"version":     "1.5.0",
			},
			"status": map[string]any{
				"phase": "Succeeded",
			},
		}},
	}

	writeDynamicTable(path, items, csvColumns)

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	if !strings.Contains(content, "NAMESPACE") {
		t.Error("missing NAMESPACE header")
	}
	if !strings.Contains(content, "rhdh-operator.v1.5.0") {
		t.Error("missing operator name")
	}
	if !strings.Contains(content, "Succeeded") {
		t.Error("missing phase")
	}
}

func TestWriteDynamicTable_Empty(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "empty.txt")

	writeDynamicTable(path, nil, operatorGroupColumns)

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "No resources found") {
		t.Error("expected 'No resources found' message")
	}
}

func TestGetFieldAsString(t *testing.T) {
	obj := map[string]any{
		"metadata": map[string]any{
			"name": "test",
		},
		"spec": map[string]any{
			"approved": true,
			"replicas": float64(3),
			"approval": "Automatic",
		},
	}

	tests := []struct {
		fields []string
		want   string
	}{
		{[]string{"metadata", "name"}, "test"},
		{[]string{"spec", "approved"}, "true"},
		{[]string{"spec", "replicas"}, "3"},
		{[]string{"spec", "approval"}, "Automatic"},
		{[]string{"spec", "missing"}, ""},
		{[]string{"nonexistent"}, ""},
	}

	for _, tt := range tests {
		got := getFieldAsString(obj, tt.fields...)
		if got != tt.want {
			t.Errorf("getFieldAsString(%v) = %q, want %q", tt.fields, got, tt.want)
		}
	}
}

func TestWriteDualWorkloadWarning(t *testing.T) {
	warning := writeDualWorkloadWarning("rhdh-ns", "backstage-my-rhdh", "StatefulSet", "Deployment")

	if !strings.Contains(warning, "WARNING: Duplicate RHDH workloads detected") {
		t.Error("missing warning header")
	}
	if !strings.Contains(warning, "backstage-my-rhdh") {
		t.Error("missing deploy name")
	}
	if !strings.Contains(warning, "rhdh-ns") {
		t.Error("missing namespace")
	}
	if !strings.Contains(warning, "StatefulSet") {
		t.Error("missing intended kind")
	}
	if !strings.Contains(warning, "delete deployment backstage-my-rhdh") {
		t.Error("missing recommended action")
	}
}

func TestWriteDualWorkloadWarning_NoIntendedKind(t *testing.T) {
	warning := writeDualWorkloadWarning("ns", "backstage-app", "", "StatefulSet")

	if !strings.Contains(warning, "defaults to Deployment") {
		t.Error("expected defaults-to-Deployment message")
	}
}

func TestPodReadyContainers(t *testing.T) {
	pod := &corev1.Pod{
		Status: corev1.PodStatus{
			ContainerStatuses: []corev1.ContainerStatus{
				{Name: "main", Ready: true},
				{Name: "sidecar", Ready: false},
				{Name: "init", Ready: true},
			},
		},
	}

	if got := podReadyContainers(pod); got != 2 {
		t.Errorf("podReadyContainers = %d, want 2", got)
	}
}

func TestPodReadyContainers_Empty(t *testing.T) {
	pod := &corev1.Pod{}
	if got := podReadyContainers(pod); got != 0 {
		t.Errorf("podReadyContainers = %d, want 0", got)
	}
}

func TestPtrVal(t *testing.T) {
	v := int32(3)
	if got := ptrVal(&v); got != 3 {
		t.Errorf("ptrVal(&3) = %d, want 3", got)
	}
	if got := ptrVal(nil); got != 0 {
		t.Errorf("ptrVal(nil) = %d, want 0", got)
	}
}

func TestWriteDeploymentSummaryTable(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "deps.txt")

	deps := []deploymentSummary{
		{Namespace: "ns1", Name: "rhdh-operator", Ready: 1, Desired: 1},
		{Namespace: "ns2", Name: "rhdh-operator", Ready: 0, Desired: 1},
	}

	writeDeploymentSummaryTable(path, deps)

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	if !strings.Contains(content, "NAMESPACE") {
		t.Error("expected NAMESPACE header when namespaces present")
	}
	if !strings.Contains(content, "1/1") {
		t.Error("missing ready count")
	}
	if !strings.Contains(content, "0/1") {
		t.Error("missing not-ready count")
	}
}

func TestWriteDeploymentSummaryTable_NoNamespace(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "deps.txt")

	deps := []deploymentSummary{
		{Name: "rhdh-operator", Ready: 1, Desired: 1},
	}

	writeDeploymentSummaryTable(path, deps)

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	if strings.Contains(content, "NAMESPACE") {
		t.Error("unexpected NAMESPACE header when no namespaces")
	}
}

func TestWriteDeploymentSummaryTable_Empty(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "deps.txt")

	writeDeploymentSummaryTable(path, nil)

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "No resources found") {
		t.Error("expected 'No resources found'")
	}
}

func TestOperator_GatherOLM_NoOLM(t *testing.T) {
	dir := t.TempDir()
	cfg := newTestConfig(t, dir, withAPIGroups("apps/v1"))

	o := &Operator{}
	outDir := filepath.Join(dir, "operator")
	_ = os.MkdirAll(outDir, 0o755)
	o.gatherOLM(context.Background(), cfg, outDir)

	data, err := os.ReadFile(filepath.Join(outDir, "olm", "rhdh-csv-all.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "not available") {
		t.Error("expected OLM not available message")
	}
}

func TestOperator_GatherOLM_WithOLM(t *testing.T) {
	dir := t.TempDir()

	csv := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "operators.coreos.com/v1alpha1",
			"kind":       "ClusterServiceVersion",
			"metadata": map[string]any{
				"name":      "rhdh-operator.v1.5.0",
				"namespace": "rhdh-operator",
			},
			"spec": map[string]any{
				"displayName": "RHDH Operator",
				"version":     "1.5.0",
			},
			"status": map[string]any{
				"phase": "Succeeded",
			},
		},
	}

	cfg := newTestConfig(t, dir,
		withAPIGroups("operators.coreos.com/v1alpha1"),
		withDynamicObjs(
			map[schema.GroupVersionResource]string{
				csvGVR:            "ClusterServiceVersionList",
				subscriptionGVR:   "SubscriptionList",
				installPlanGVR:    "InstallPlanList",
				operatorGroupGVR:  "OperatorGroupList",
				catalogSourceGVR:  "CatalogSourceList",
			},
			csv,
		),
	)

	o := &Operator{}
	outDir := filepath.Join(dir, "operator")
	_ = os.MkdirAll(outDir, 0o755)
	o.gatherOLM(context.Background(), cfg, outDir)

	data, err := os.ReadFile(filepath.Join(outDir, "olm", "rhdh-csv-all.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "rhdh-operator.v1.5.0") {
		t.Error("expected CSV name in output")
	}
}

func TestOperator_GatherCRDs(t *testing.T) {
	dir := t.TempDir()

	crd := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "apiextensions.k8s.io/v1",
			"kind":       "CustomResourceDefinition",
			"metadata":   map[string]any{"name": "backstages.rhdh.redhat.com"},
		},
	}

	cfg := newTestConfig(t, dir,
		withDynamicObjs(
			map[schema.GroupVersionResource]string{crdGVR: "CustomResourceDefinitionList"},
			crd,
		),
	)

	o := &Operator{}
	outDir := filepath.Join(dir, "operator")
	_ = os.MkdirAll(outDir, 0o755)
	o.gatherCRDs(context.Background(), cfg, outDir)

	if _, err := os.Stat(filepath.Join(outDir, "crds", "all-crds.txt")); err != nil {
		t.Error("all-crds.txt not created")
	}
	if _, err := os.Stat(filepath.Join(outDir, "crds", "backstages.rhdh.redhat.com.yaml")); err != nil {
		t.Error("backstage CRD YAML not created")
	}
}

func TestOperator_GatherCRDs_NoCRDs(t *testing.T) {
	dir := t.TempDir()
	cfg := newTestConfig(t, dir,
		withDynamicObjs(
			map[schema.GroupVersionResource]string{crdGVR: "CustomResourceDefinitionList"},
		),
	)

	o := &Operator{}
	outDir := filepath.Join(dir, "operator")
	_ = os.MkdirAll(outDir, 0o755)
	o.gatherCRDs(context.Background(), cfg, outDir)

	data, err := os.ReadFile(filepath.Join(outDir, "crds", "backstages.rhdh.redhat.com--not-found.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "not found") {
		t.Error("expected not found message")
	}
}

func TestOperator_GatherNamespaceResources(t *testing.T) {
	dir := t.TempDir()

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "rhdh-pod", Namespace: "rhdh-operator"},
		Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "manager"}}},
		Status:     corev1.PodStatus{Phase: corev1.PodRunning},
	}
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "rhdh-svc", Namespace: "rhdh-operator"},
		Spec: corev1.ServiceSpec{
			Type:      corev1.ServiceTypeClusterIP,
			ClusterIP: "10.0.0.1",
		},
	}
	replicas := int32(1)
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "rhdh-dep", Namespace: "rhdh-operator"},
		Spec:       appsv1.DeploymentSpec{Replicas: &replicas},
		Status:     appsv1.DeploymentStatus{ReadyReplicas: 1},
	}

	cfg := newTestConfig(t, dir, withTypedObjs(pod, svc, dep))

	o := &Operator{}
	nsDir := filepath.Join(dir, "ns-resources")
	_ = os.MkdirAll(nsDir, 0o755)
	o.gatherNamespaceResources(context.Background(), cfg, "rhdh-operator", nsDir)

	data, err := os.ReadFile(filepath.Join(nsDir, "all-resources.txt"))
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	if !strings.Contains(content, "rhdh-pod") {
		t.Error("expected pod name in output")
	}
	if !strings.Contains(content, "rhdh-svc") {
		t.Error("expected service name in output")
	}
	if !strings.Contains(content, "rhdh-dep") {
		t.Error("expected deployment name in output")
	}
}

func TestOperator_GatherOperatorConfig(t *testing.T) {
	dir := t.TempDir()

	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "rhdh-default-config", Namespace: "rhdh-operator"},
		Data:       map[string]string{"key": "value"},
	}

	cfg := newTestConfig(t, dir, withTypedObjs(cm))

	o := &Operator{}
	nsDir := filepath.Join(dir, "ns")
	_ = os.MkdirAll(nsDir, 0o755)
	o.gatherOperatorConfig(context.Background(), cfg, "rhdh-operator", nsDir)

	configsDir := filepath.Join(nsDir, "configs")
	if _, err := os.Stat(filepath.Join(configsDir, "all-configmaps.txt")); err != nil {
		t.Error("all-configmaps.txt not created")
	}
	if _, err := os.Stat(filepath.Join(configsDir, "rhdh-default-config.yaml")); err != nil {
		t.Error("rhdh-default-config.yaml not created")
	}
	if _, err := os.Stat(filepath.Join(configsDir, "rhdh-plugin-deps--not-found.txt")); err != nil {
		t.Error("rhdh-plugin-deps--not-found.txt not created")
	}
}

func TestOperator_GatherOperatorConfig_BothCMs(t *testing.T) {
	dir := t.TempDir()

	cm1 := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "rhdh-default-config", Namespace: "rhdh-operator"},
		Data:       map[string]string{"key": "value"},
	}
	cm2 := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "rhdh-plugin-deps", Namespace: "rhdh-operator"},
		Data:       map[string]string{"plugin": "data"},
	}

	cfg := newTestConfig(t, dir, withTypedObjs(cm1, cm2))

	o := &Operator{}
	nsDir := filepath.Join(dir, "ns")
	_ = os.MkdirAll(nsDir, 0o755)
	o.gatherOperatorConfig(context.Background(), cfg, "rhdh-operator", nsDir)

	configsDir := filepath.Join(nsDir, "configs")

	data, err := os.ReadFile(filepath.Join(configsDir, "all-configmaps.txt"))
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	if !strings.Contains(content, "rhdh-default-config") {
		t.Error("expected rhdh-default-config in listing")
	}
	if !strings.Contains(content, "rhdh-plugin-deps") {
		t.Error("expected rhdh-plugin-deps in listing")
	}

	if _, err := os.Stat(filepath.Join(configsDir, "rhdh-default-config.yaml")); err != nil {
		t.Error("rhdh-default-config.yaml not created")
	}
	if _, err := os.Stat(filepath.Join(configsDir, "rhdh-plugin-deps.yaml")); err != nil {
		t.Error("rhdh-plugin-deps.yaml not created")
	}
}

func TestOperator_GatherNamespaceResources_Empty(t *testing.T) {
	dir := t.TempDir()
	cfg := newTestConfig(t, dir)

	o := &Operator{}
	nsDir := filepath.Join(dir, "ns-resources")
	_ = os.MkdirAll(nsDir, 0o755)
	o.gatherNamespaceResources(context.Background(), cfg, "empty-ns", nsDir)

	data, err := os.ReadFile(filepath.Join(nsDir, "all-resources.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "No resources found") {
		t.Error("expected 'No resources found' for empty namespace")
	}
}

func TestOperator_GatherOperatorDeployments(t *testing.T) {
	dir := t.TempDir()

	replicas := int32(1)
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "rhdh-operator-controller",
			Namespace: "rhdh-operator",
			Labels:    map[string]string{"app": "rhdh-operator"},
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"app": "rhdh-operator"},
			},
		},
		Status: appsv1.DeploymentStatus{ReadyReplicas: 1},
	}

	cfg := newTestConfig(t, dir, withTypedObjs(dep))

	o := &Operator{}
	nsDir := filepath.Join(dir, "ns")
	_ = os.MkdirAll(nsDir, 0o755)
	o.gatherOperatorDeployments(context.Background(), cfg, "rhdh-operator", nsDir)

	depsDir := filepath.Join(nsDir, "deployments")
	data, err := os.ReadFile(filepath.Join(depsDir, "all-deployments.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "rhdh-operator-controller") {
		t.Error("expected operator deployment in all-deployments.txt")
	}
	if _, err := os.Stat(filepath.Join(depsDir, "app=rhdh-operator.yaml")); err != nil {
		t.Error("expected operator deployment YAML file")
	}
}

func TestOperator_GatherOperatorDeployments_NoOperator(t *testing.T) {
	dir := t.TempDir()

	replicas := int32(1)
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "other-deploy",
			Namespace: "rhdh-operator",
			Labels:    map[string]string{"app": "other"},
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"app": "other"},
			},
		},
	}

	cfg := newTestConfig(t, dir, withTypedObjs(dep))

	o := &Operator{}
	nsDir := filepath.Join(dir, "ns")
	_ = os.MkdirAll(nsDir, 0o755)
	o.gatherOperatorDeployments(context.Background(), cfg, "rhdh-operator", nsDir)

	depsDir := filepath.Join(nsDir, "deployments")
	if _, err := os.Stat(filepath.Join(depsDir, "app=rhdh-operator.yaml")); !os.IsNotExist(err) {
		t.Error("expected no operator YAML when no operator deployments exist")
	}
}

func TestOperator_GatherOperatorLogs_NoPods(t *testing.T) {
	dir := t.TempDir()
	cfg := newTestConfig(t, dir)

	o := &Operator{}
	nsDir := filepath.Join(dir, "ns")
	_ = os.MkdirAll(nsDir, 0o755)
	o.gatherOperatorLogs(context.Background(), cfg, "rhdh-operator", nsDir)

	if _, err := os.Stat(filepath.Join(nsDir, "logs.txt")); !os.IsNotExist(err) {
		t.Error("expected no logs.txt when no pods found")
	}
}

func TestOperator_GatherOperatorLogs_WithPods(t *testing.T) {
	dir := t.TempDir()

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "rhdh-operator-abc",
			Namespace: "rhdh-operator",
			Labels:    map[string]string{"app": "rhdh-operator"},
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{Name: "manager"}},
		},
	}

	cfg := newTestConfig(t, dir, withTypedObjs(pod))

	o := &Operator{}
	nsDir := filepath.Join(dir, "ns")
	_ = os.MkdirAll(nsDir, 0o755)
	o.gatherOperatorLogs(context.Background(), cfg, "rhdh-operator", nsDir)

	if _, err := os.Stat(filepath.Join(nsDir, "logs.txt")); err != nil {
		t.Error("expected logs.txt to be created when pods exist")
	}
	if _, err := os.Stat(filepath.Join(nsDir, "logs-previous.txt")); err != nil {
		t.Error("expected logs-previous.txt to be created when pods exist")
	}
}

func TestOperator_GatherOperatorNamespaces_Found(t *testing.T) {
	dir := t.TempDir()

	replicas := int32(1)
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "rhdh-operator-controller",
			Namespace: "rhdh-operator",
			Labels:    map[string]string{"app": "rhdh-operator"},
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"app": "rhdh-operator"},
			},
		},
		Status: appsv1.DeploymentStatus{ReadyReplicas: 1},
	}

	cfg := newTestConfig(t, dir, withTypedObjs(dep))

	o := &Operator{}
	outDir := filepath.Join(dir, "operator")
	_ = os.MkdirAll(outDir, 0o755)
	o.gatherOperatorNamespaces(context.Background(), cfg, outDir)

	if _, err := os.Stat(filepath.Join(outDir, "all-deployments.txt")); err != nil {
		t.Error("expected all-deployments.txt")
	}
	nsDir := filepath.Join(outDir, "ns=rhdh-operator")
	if _, err := os.Stat(nsDir); err != nil {
		t.Error("expected ns=rhdh-operator directory")
	}
	if _, err := os.Stat(filepath.Join(nsDir, "all-resources.txt")); err != nil {
		t.Error("expected all-resources.txt in namespace dir")
	}
}

func TestOperator_GatherOperatorNamespaces_NoDeployments(t *testing.T) {
	dir := t.TempDir()
	cfg := newTestConfig(t, dir)

	o := &Operator{}
	outDir := filepath.Join(dir, "operator")
	_ = os.MkdirAll(outDir, 0o755)
	o.gatherOperatorNamespaces(context.Background(), cfg, outDir)

	data, err := os.ReadFile(filepath.Join(outDir, "all-deployments.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "No resources found") {
		t.Error("expected 'No resources found' when no operator deployments")
	}
}

func TestOperator_GatherOperatorNamespaces_Targeted(t *testing.T) {
	dir := t.TempDir()

	replicas := int32(1)
	dep1 := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "rhdh-operator-1",
			Namespace: "ns1",
			Labels:    map[string]string{"app": "rhdh-operator"},
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"app": "rhdh-operator"},
			},
		},
	}
	dep2 := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "rhdh-operator-2",
			Namespace: "ns2",
			Labels:    map[string]string{"app": "rhdh-operator"},
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"app": "rhdh-operator"},
			},
		},
	}

	cfg := newTestConfig(t, dir, withTypedObjs(dep1, dep2))
	cfg.TargetNamespaces = []string{"ns1"}

	o := &Operator{}
	outDir := filepath.Join(dir, "operator")
	_ = os.MkdirAll(outDir, 0o755)
	o.gatherOperatorNamespaces(context.Background(), cfg, outDir)

	if _, err := os.Stat(filepath.Join(outDir, "ns=ns1")); err != nil {
		t.Error("expected ns=ns1 directory for targeted namespace")
	}
	if _, err := os.Stat(filepath.Join(outDir, "ns=ns2")); !os.IsNotExist(err) {
		t.Error("expected ns=ns2 to be skipped (not in target list)")
	}
}

func TestOperator_GatherBackstageCRs_NoAPI(t *testing.T) {
	dir := t.TempDir()
	cfg := newTestConfig(t, dir, withAPIGroups("apps/v1"))

	o := &Operator{}
	outDir := filepath.Join(dir, "operator")
	_ = os.MkdirAll(outDir, 0o755)
	o.gatherBackstageCRs(context.Background(), cfg, outDir)

	data, err := os.ReadFile(filepath.Join(outDir, "backstage-crs", "no-crs.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "not available") {
		t.Error("expected CRD not available message")
	}
}

func TestOperator_GatherBackstageCRs_NoCRs(t *testing.T) {
	dir := t.TempDir()

	backstageGVR := schema.GroupVersionResource{
		Group: "rhdh.redhat.com", Version: "v1alpha5", Resource: "backstages",
	}

	cfg := newTestConfig(t, dir,
		withAPIGroups("rhdh.redhat.com/v1alpha5"),
		withDynamicObjs(
			map[schema.GroupVersionResource]string{backstageGVR: "BackstageList"},
		),
	)

	o := &Operator{}
	outDir := filepath.Join(dir, "operator")
	_ = os.MkdirAll(outDir, 0o755)
	o.gatherBackstageCRs(context.Background(), cfg, outDir)

	data, err := os.ReadFile(filepath.Join(outDir, "backstage-crs", "no-crs.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "No Backstage CR found") {
		t.Error("expected no CR found message")
	}
}

func TestOperator_GatherBackstageCRs_WithCRs(t *testing.T) {
	dir := t.TempDir()

	backstageGVR := schema.GroupVersionResource{
		Group: "rhdh.redhat.com", Version: "v1alpha5", Resource: "backstages",
	}
	cr := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "rhdh.redhat.com/v1alpha5",
			"kind":       "Backstage",
			"metadata": map[string]any{
				"name":      "my-backstage",
				"namespace": "backstage-ns",
			},
		},
	}

	cfg := newTestConfig(t, dir,
		withAPIGroups("rhdh.redhat.com/v1alpha5"),
		withDynamicObjs(
			map[schema.GroupVersionResource]string{backstageGVR: "BackstageList"},
			cr,
		),
	)

	o := &Operator{}
	outDir := filepath.Join(dir, "operator")
	_ = os.MkdirAll(outDir, 0o755)
	o.gatherBackstageCRs(context.Background(), cfg, outDir)

	if _, err := os.Stat(filepath.Join(outDir, "backstage-crs", "all-backstage-crs.txt")); err != nil {
		t.Error("expected all-backstage-crs.txt")
	}
	crDir := filepath.Join(outDir, "backstage-crs", "ns=backstage-ns", "my-backstage")
	if _, err := os.Stat(filepath.Join(crDir, "my-backstage.yaml")); err != nil {
		t.Error("expected CR YAML file")
	}
}

func TestOperator_CollectCRWorkloads_DeploymentOnly(t *testing.T) {
	dir := t.TempDir()

	replicas := int32(1)
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "backstage-my-backstage",
			Namespace: "backstage-ns",
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"app": "backstage"},
			},
		},
	}

	backstageGVR := schema.GroupVersionResource{
		Group: "rhdh.redhat.com", Version: "v1alpha5", Resource: "backstages",
	}

	cfg := newTestConfig(t, dir,
		withTypedObjs(dep),
		withDynamicObjs(
			map[schema.GroupVersionResource]string{backstageGVR: "BackstageList"},
		),
	)

	o := &Operator{}
	crDir := filepath.Join(dir, "cr")
	_ = os.MkdirAll(crDir, 0o755)
	o.collectCRWorkloads(context.Background(), cfg, "backstage-ns", "my-backstage", "uid-1", crDir, backstageGVR)

	if _, err := os.Stat(filepath.Join(crDir, "deployment")); err != nil {
		t.Error("expected deployment directory")
	}
}

func TestOperator_CollectCRWorkloads_NeitherExists(t *testing.T) {
	dir := t.TempDir()

	backstageGVR := schema.GroupVersionResource{
		Group: "rhdh.redhat.com", Version: "v1alpha5", Resource: "backstages",
	}

	cfg := newTestConfig(t, dir,
		withDynamicObjs(
			map[schema.GroupVersionResource]string{backstageGVR: "BackstageList"},
		),
	)

	o := &Operator{}
	crDir := filepath.Join(dir, "cr")
	_ = os.MkdirAll(crDir, 0o755)
	o.collectCRWorkloads(context.Background(), cfg, "ns", "missing", "", crDir, backstageGVR)

	if _, err := os.Stat(filepath.Join(crDir, "deployment")); err != nil {
		t.Error("expected deployment directory even when deployment doesn't exist (error is logged)")
	}
}

func TestOperator_CollectOKPWorkload_NoOKP(t *testing.T) {
	dir := t.TempDir()

	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "backstage-my-cr",
			Namespace: "ns",
		},
		Spec: appsv1.DeploymentSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{Name: "backstage-backend"}},
				},
			},
		},
	}

	cfg := newTestConfig(t, dir, withTypedObjs(dep))

	o := &Operator{}
	crDir := filepath.Join(dir, "cr")
	_ = os.MkdirAll(crDir, 0o755)
	o.collectOKPWorkload(context.Background(), cfg, "ns", "my-cr", "uid-1", "backstage-my-cr", crDir)

	if _, err := os.Stat(filepath.Join(crDir, "okp-deployment")); !os.IsNotExist(err) {
		t.Error("expected no okp-deployment directory when no OKP deployments")
	}
}

func TestOperator_CollectOKPWorkload_WithOKP(t *testing.T) {
	dir := t.TempDir()

	controller := true
	replicas := int32(1)
	okpDep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-cr-okp",
			Namespace: "ns",
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: "rhdh.redhat.com/v1alpha5",
				Kind:       "Backstage",
				Name:       "my-cr",
				UID:        "uid-1",
				Controller: &controller,
			}},
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"app": "okp"},
			},
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{Name: "okp"}},
				},
			},
		},
	}
	primaryDep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "backstage-my-cr",
			Namespace: "ns",
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"app": "backstage"},
			},
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{Name: "backstage-backend"}},
				},
			},
		},
	}

	cfg := newTestConfig(t, dir, withTypedObjs(okpDep, primaryDep))

	o := &Operator{}
	crDir := filepath.Join(dir, "cr")
	_ = os.MkdirAll(crDir, 0o755)
	o.collectOKPWorkload(context.Background(), cfg, "ns", "my-cr", "uid-1", "backstage-my-cr", crDir)

	if _, err := os.Stat(filepath.Join(crDir, "okp-deployment")); err != nil {
		t.Error("expected okp-deployment directory when OKP deployment exists")
	}
}

func TestOperator_HandleDualWorkload(t *testing.T) {
	dir := t.TempDir()

	backstageGVR := schema.GroupVersionResource{
		Group: "rhdh.redhat.com", Version: "v1alpha5", Resource: "backstages",
	}
	cr := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "rhdh.redhat.com/v1alpha5",
			"kind":       "Backstage",
			"metadata": map[string]any{
				"name":      "my-cr",
				"namespace": "ns",
			},
			"spec": map[string]any{
				"deployment": map[string]any{
					"kind": "StatefulSet",
				},
			},
		},
	}

	replicas := int32(1)
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "backstage-my-cr", Namespace: "ns"},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"app": "backstage"},
			},
		},
	}
	sts := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{Name: "backstage-my-cr", Namespace: "ns"},
		Spec: appsv1.StatefulSetSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"app": "backstage"},
			},
		},
	}

	cfg := newTestConfig(t, dir,
		withTypedObjs(dep, sts),
		withDynamicObjs(
			map[schema.GroupVersionResource]string{backstageGVR: "BackstageList"},
			cr,
		),
	)

	o := &Operator{}
	crDir := filepath.Join(dir, "cr")
	_ = os.MkdirAll(crDir, 0o755)
	o.handleDualWorkload(context.Background(), cfg, "ns", "my-cr", "backstage-my-cr", crDir, backstageGVR)

	data, err := os.ReadFile(filepath.Join(crDir, "warning-dual-workload.txt"))
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	if !strings.Contains(content, "WARNING: Duplicate RHDH workloads detected") {
		t.Error("expected warning header")
	}
	if !strings.Contains(content, "StatefulSet") {
		t.Error("expected StatefulSet mentioned in warning")
	}
	if _, err := os.Stat(filepath.Join(crDir, "deployment")); err != nil {
		t.Error("expected deployment directory")
	}
	if _, err := os.Stat(filepath.Join(crDir, "rhdh-statefulset")); err != nil {
		t.Error("expected rhdh-statefulset directory")
	}
}

func TestOperator_Run(t *testing.T) {
	dir := t.TempDir()
	cfg := newTestConfig(t, dir,
		withAPIGroups("apps/v1"),
		withDynamicObjs(
			map[schema.GroupVersionResource]string{crdGVR: "CustomResourceDefinitionList"},
		),
	)

	o := &Operator{}
	err := o.Run(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	outDir := filepath.Join(dir, "operator")
	if _, err := os.Stat(outDir); err != nil {
		t.Error("expected operator directory")
	}
	if _, err := os.Stat(filepath.Join(outDir, "olm")); err != nil {
		t.Error("expected olm subdirectory")
	}
	if _, err := os.Stat(filepath.Join(outDir, "crds")); err != nil {
		t.Error("expected crds subdirectory")
	}
	if _, err := os.Stat(filepath.Join(outDir, "backstage-crs")); err != nil {
		t.Error("expected backstage-crs subdirectory")
	}
}

func TestOperator_WriteAggregatedLogs(t *testing.T) {
	dir := t.TempDir()

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "rhdh-pod", Namespace: "ns"},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{Name: "manager"}},
		},
	}

	cfg := newTestConfig(t, dir, withTypedObjs(pod))

	outPath := filepath.Join(dir, "logs.txt")
	writeAggregatedLogs(context.Background(), cfg, "ns", []corev1.Pod{*pod}, false, outPath)

	if _, err := os.Stat(outPath); err != nil {
		t.Error("expected logs file to be created")
	}
}

func TestOperator_CollectOKPWorkload_MultipleOKP(t *testing.T) {
	dir := t.TempDir()

	controller := true
	replicas := int32(1)
	okp1 := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-cr-okp-alpha",
			Namespace: "ns",
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: "rhdh.redhat.com/v1alpha5",
				Kind:       "Backstage",
				Name:       "my-cr",
				UID:        "uid-1",
				Controller: &controller,
			}},
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "okp"}},
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "okp"}}},
			},
		},
	}
	okp2 := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-cr-okp-beta",
			Namespace: "ns",
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: "rhdh.redhat.com/v1alpha5",
				Kind:       "Backstage",
				Name:       "my-cr",
				UID:        "uid-1",
				Controller: &controller,
			}},
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "okp2"}},
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "okp"}}},
			},
		},
	}
	primaryDep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "backstage-my-cr", Namespace: "ns"},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "backstage"}},
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "backstage-backend"}}},
			},
		},
	}

	cfg := newTestConfig(t, dir, withTypedObjs(okp1, okp2, primaryDep))

	o := &Operator{}
	crDir := filepath.Join(dir, "cr")
	_ = os.MkdirAll(crDir, 0o755)
	o.collectOKPWorkload(context.Background(), cfg, "ns", "my-cr", "uid-1", "backstage-my-cr", crDir)

	if _, err := os.Stat(filepath.Join(crDir, "okp-deployment")); err != nil {
		t.Error("expected okp-deployment directory (first alphabetically)")
	}
}

func TestGetFieldAsString_NonString(t *testing.T) {
	obj := map[string]any{
		"spec": map[string]any{
			"replicas": int64(3),
		},
	}
	got := getFieldAsString(obj, "spec", "replicas")
	if got != "3" {
		t.Errorf("getFieldAsString(int) = %q, want '3'", got)
	}
}

func TestGetFieldAsString_MissingIntermediate(t *testing.T) {
	obj := map[string]any{}
	got := getFieldAsString(obj, "spec", "version")
	if got != "" {
		t.Errorf("getFieldAsString(missing) = %q, want empty", got)
	}
}

func TestGetFieldAsString_NonMapIntermediate(t *testing.T) {
	obj := map[string]any{
		"spec": "not-a-map",
	}
	got := getFieldAsString(obj, "spec", "version")
	if got != "" {
		t.Errorf("getFieldAsString(non-map) = %q, want empty", got)
	}
}
