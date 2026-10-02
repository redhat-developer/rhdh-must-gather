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

func TestRoute_Name(t *testing.T) {
	r := &Route{}
	if got := r.Name(); got != "route" {
		t.Errorf("Name() = %q, want %q", got, "route")
	}
}

func TestGetString(t *testing.T) {
	tests := []struct {
		name   string
		obj    map[string]any
		fields []string
		want   string
	}{
		{
			name:   "nested value",
			obj:    map[string]any{"metadata": map[string]any{"name": "my-route"}},
			fields: []string{"metadata", "name"},
			want:   "my-route",
		},
		{
			name:   "missing field",
			obj:    map[string]any{"metadata": map[string]any{}},
			fields: []string{"metadata", "name"},
			want:   "",
		},
		{
			name:   "missing path",
			obj:    map[string]any{},
			fields: []string{"spec", "host"},
			want:   "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			u := unstructured.Unstructured{Object: tt.obj}
			if got := getString(u, tt.fields...); got != tt.want {
				t.Errorf("getString() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestRoute_Run_NoRouteAPI(t *testing.T) {
	dir := t.TempDir()
	cfg := newTestConfig(t, dir, withAPIGroups("apps/v1"))

	r := &Route{}
	if err := r.Run(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(filepath.Join(dir, "all-routes.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "not an OpenShift cluster") {
		t.Error("expected message about Route API not available")
	}
}

func TestRoute_Run_WithRoutes(t *testing.T) {
	dir := t.TempDir()

	route := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "route.openshift.io/v1",
			"kind":       "Route",
			"metadata": map[string]any{
				"name":      "backstage",
				"namespace": "rhdh",
			},
			"spec": map[string]any{
				"host": "backstage.apps.example.com",
			},
		},
	}

	cfg := newTestConfig(t, dir,
		withAPIGroups("route.openshift.io/v1"),
		withDynamicObjs(
			map[schema.GroupVersionResource]string{routeGVR: "RouteList"},
			route,
		),
	)

	r := &Route{}
	if err := r.Run(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(filepath.Join(dir, "all-routes.txt"))
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	if !strings.Contains(content, "backstage") {
		t.Error("expected route name in output")
	}
	if !strings.Contains(content, "backstage.apps.example.com") {
		t.Error("expected route host in output")
	}
}

func TestRoute_Run_NoRoutes(t *testing.T) {
	dir := t.TempDir()

	cfg := newTestConfig(t, dir,
		withAPIGroups("route.openshift.io/v1"),
		withDynamicObjs(
			map[schema.GroupVersionResource]string{routeGVR: "RouteList"},
		),
	)

	r := &Route{}
	if err := r.Run(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(filepath.Join(dir, "all-routes.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "No resources found") {
		t.Error("expected 'No resources found' for empty route list")
	}
}
