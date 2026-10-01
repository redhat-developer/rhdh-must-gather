package collector

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/redhat-developer/rhdh-must-gather/internal/log"
)

type Route struct{}

func (r *Route) Name() string { return "route" }

var routeGVR = schema.GroupVersionResource{
	Group:    "route.openshift.io",
	Version:  "v1",
	Resource: "routes",
}

func (r *Route) Run(ctx context.Context, cfg *Config) error {
	log.Info("Starting OCP Route data collection...")

	outPath := filepath.Join(cfg.BasePath, "all-routes.txt")

	hasRoutes, err := cfg.Client.HasAPIGroup("route.openshift.io")
	if err != nil {
		writeCollectError(outPath, "check route API group", err)
		return nil
	}
	if !hasRoutes {
		log.Info("Route API not available (not an OpenShift cluster)")
		return os.WriteFile(outPath, []byte("Route API not available (not an OpenShift cluster)\n"), 0o644)
	}

	items, err := listDynamic(ctx, cfg, routeGVR)
	if err != nil {
		writeCollectError(outPath, "list routes", err)
		return nil
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "%-30s %-40s %-50s\n", "NAMESPACE", "NAME", "HOST")
	for _, item := range items {
		ns := getString(item, "metadata", "namespace")
		name := getString(item, "metadata", "name")
		host := getString(item, "spec", "host")
		fmt.Fprintf(&sb, "%-30s %-40s %-50s\n", ns, name, host)
	}
	if len(items) == 0 {
		sb.WriteString("No resources found\n")
	}

	return os.WriteFile(outPath, []byte(sb.String()), 0o644)
}

func listDynamic(ctx context.Context, cfg *Config, gvr schema.GroupVersionResource) ([]unstructured.Unstructured, error) {
	namespaces := cfg.Namespaces()
	if namespaces == nil {
		list, err := cfg.Client.Dynamic.Resource(gvr).Namespace("").List(ctx, metav1.ListOptions{})
		if err != nil {
			return nil, err
		}
		return list.Items, nil
	}

	var all []unstructured.Unstructured
	for _, ns := range namespaces {
		list, err := cfg.Client.Dynamic.Resource(gvr).Namespace(ns).List(ctx, metav1.ListOptions{})
		if err != nil {
			log.Warn("Failed to list %s in namespace %s: %v", gvr.Resource, ns, err)
			continue
		}
		all = append(all, list.Items...)
	}
	return all, nil
}

func getString(obj unstructured.Unstructured, fields ...string) string {
	val, _, _ := nestedString(obj.Object, fields...)
	return val
}

