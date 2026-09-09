package collector

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/rest"
	"k8s.io/kubectl/pkg/describe"
	"sigs.k8s.io/yaml"

	"github.com/redhat-developer/rhdh-must-gather/internal/log"
)

// setGVK populates kind/apiVersion on typed client-go objects, which
// leave these fields empty after Get/List calls.
func setGVK(obj interface{ GetObjectKind() schema.ObjectKind }, kind, apiVersion string) {
	obj.GetObjectKind().SetGroupVersionKind(schema.FromAPIVersionAndKind(apiVersion, kind))
}

func writeResource(path string, obj any) {
	data, err := yaml.Marshal(obj)
	if err != nil {
		return
	}
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	_ = os.WriteFile(path, data, 0o644)
}

var knownGroupKinds = map[string]schema.GroupKind{
	"pod":                       {Group: "", Kind: "Pod"},
	"pods":                      {Group: "", Kind: "Pod"},
	"configmap":                 {Group: "", Kind: "ConfigMap"},
	"configmaps":                {Group: "", Kind: "ConfigMap"},
	"deployment":                {Group: "apps", Kind: "Deployment"},
	"deployments":               {Group: "apps", Kind: "Deployment"},
	"statefulset":               {Group: "apps", Kind: "StatefulSet"},
	"statefulsets":               {Group: "apps", Kind: "StatefulSet"},
	"replicaset":                {Group: "apps", Kind: "ReplicaSet"},
	"replicasets":                {Group: "apps", Kind: "ReplicaSet"},
	"controllerrevision":        {Group: "apps", Kind: "ControllerRevision"},
	"controllerrevisions":       {Group: "apps", Kind: "ControllerRevision"},
	"crd":                       {Group: "apiextensions.k8s.io", Kind: "CustomResourceDefinition"},
	"customresourcedefinition":  {Group: "apiextensions.k8s.io", Kind: "CustomResourceDefinition"},
	"customresourcedefinitions": {Group: "apiextensions.k8s.io", Kind: "CustomResourceDefinition"},
}

// describeResource uses the kubectl describe library to produce
// human-readable resource descriptions, replacing the kubectl binary call.
// For CRD instances (resource types not in knownGroupKinds), it fetches
// the resource via the dynamic client and writes YAML instead.
func describeResource(ctx context.Context, cfg *Config, path, resourceType, namespace string, args ...string) {
	var name, labelSelector string
	for i := 0; i < len(args); i++ {
		if args[i] == "-l" && i+1 < len(args) {
			labelSelector = args[i+1]
			i++
		} else if args[i] != "" {
			name = args[i]
		}
	}

	_ = os.MkdirAll(filepath.Dir(path), 0o755)

	gk, ok := knownGroupKinds[strings.ToLower(resourceType)]
	if !ok {
		describeCRD(ctx, cfg, path, resourceType, namespace, name)
		return
	}

	if name != "" {
		out := describeBuiltin(cfg.Client.Config, gk, namespace, name)
		_ = os.WriteFile(path, []byte(out), 0o644)
		return
	}

	names, err := listResourceNames(ctx, cfg, gk, namespace, labelSelector)
	if err != nil {
		content := fmt.Sprintf("describe %s failed: %v\n", resourceType, err)
		_ = os.WriteFile(path, []byte(content), 0o644)
		return
	}

	var sb strings.Builder
	for _, n := range names {
		out := describeBuiltin(cfg.Client.Config, gk, namespace, n)
		sb.WriteString(out)
		sb.WriteString("\n\n")
	}
	_ = os.WriteFile(path, []byte(sb.String()), 0o644)
}

func describeBuiltin(config *rest.Config, gk schema.GroupKind, namespace, name string) string {
	settings := describe.DescriberSettings{ShowEvents: true}
	d, ok := describe.DescriberFor(gk, config)
	if !ok {
		return fmt.Sprintf("no describer available for %s", gk.String())
	}
	out, err := d.Describe(namespace, name, settings)
	if err != nil {
		return fmt.Sprintf("describe failed for %s/%s: %v", namespace, name, err)
	}
	return out
}

// describeCRD handles CRD instance types by fetching the resource via the
// dynamic client and writing a YAML representation with metadata.
func describeCRD(ctx context.Context, cfg *Config, path, resourceType, namespace, name string) {
	if name == "" {
		_ = os.WriteFile(path, []byte(fmt.Sprintf("describe %s: name required for CRD resources\n", resourceType)), 0o644)
		return
	}

	gvr, err := resolveCRDType(cfg, resourceType)
	if err != nil {
		_ = os.WriteFile(path, []byte(fmt.Sprintf("describe %s failed: %v\n", resourceType, err)), 0o644)
		return
	}

	var obj interface{}
	if namespace != "" {
		obj, err = cfg.Client.Dynamic.Resource(gvr).Namespace(namespace).Get(ctx, name, metav1.GetOptions{})
	} else {
		obj, err = cfg.Client.Dynamic.Resource(gvr).Get(ctx, name, metav1.GetOptions{})
	}
	if err != nil {
		_ = os.WriteFile(path, []byte(fmt.Sprintf("describe %s %s failed: %v\n", resourceType, name, err)), 0o644)
		return
	}

	data, err := yaml.Marshal(obj)
	if err != nil {
		_ = os.WriteFile(path, []byte(fmt.Sprintf("describe %s %s: marshal failed: %v\n", resourceType, name, err)), 0o644)
		return
	}
	_ = os.WriteFile(path, data, 0o644)
}

// resolveCRDType maps a resource type string to a GVR.
// Handles forms like "backstage", "sonataflows.sonataflow.org".
func resolveCRDType(cfg *Config, resourceType string) (schema.GroupVersionResource, error) {
	parts := strings.SplitN(resourceType, ".", 2)
	resource := strings.ToLower(parts[0])
	group := ""
	if len(parts) > 1 {
		group = parts[1]
	}

	if group != "" {
		version, err := cfg.Client.PreferredVersion(group)
		if err != nil {
			return schema.GroupVersionResource{}, fmt.Errorf("resolving API group for %s: %w", resourceType, err)
		}
		if !strings.HasSuffix(resource, "s") {
			resource += "s"
		}
		return schema.GroupVersionResource{Group: group, Version: version, Resource: resource}, nil
	}

	// Short name without group — try known CRD mappings
	switch resource {
	case "backstage", "backstages":
		version, err := cfg.Client.PreferredVersion("rhdh.redhat.com")
		if err != nil {
			return schema.GroupVersionResource{}, err
		}
		return schema.GroupVersionResource{Group: "rhdh.redhat.com", Version: version, Resource: "backstages"}, nil
	default:
		return schema.GroupVersionResource{}, fmt.Errorf("unknown CRD resource type: %s", resourceType)
	}
}

func listResourceNames(ctx context.Context, cfg *Config, gk schema.GroupKind, namespace, labelSelector string) ([]string, error) {
	client := cfg.Client.Clientset

	opts := metav1.ListOptions{}
	if labelSelector != "" {
		opts.LabelSelector = labelSelector
	}

	switch gk.Kind {
	case "Pod":
		list, err := client.CoreV1().Pods(namespace).List(ctx, opts)
		if err != nil {
			return nil, err
		}
		names := make([]string, len(list.Items))
		for i, item := range list.Items {
			names[i] = item.Name
		}
		return names, nil
	case "Deployment":
		list, err := client.AppsV1().Deployments(namespace).List(ctx, opts)
		if err != nil {
			return nil, err
		}
		names := make([]string, len(list.Items))
		for i, item := range list.Items {
			names[i] = item.Name
		}
		return names, nil
	case "StatefulSet":
		list, err := client.AppsV1().StatefulSets(namespace).List(ctx, opts)
		if err != nil {
			return nil, err
		}
		names := make([]string, len(list.Items))
		for i, item := range list.Items {
			names[i] = item.Name
		}
		return names, nil
	case "ReplicaSet":
		list, err := client.AppsV1().ReplicaSets(namespace).List(ctx, opts)
		if err != nil {
			return nil, err
		}
		names := make([]string, len(list.Items))
		for i, item := range list.Items {
			names[i] = item.Name
		}
		return names, nil
	case "ControllerRevision":
		list, err := client.AppsV1().ControllerRevisions(namespace).List(ctx, opts)
		if err != nil {
			return nil, err
		}
		names := make([]string, len(list.Items))
		for i, item := range list.Items {
			names[i] = item.Name
		}
		return names, nil
	case "ConfigMap":
		list, err := client.CoreV1().ConfigMaps(namespace).List(ctx, opts)
		if err != nil {
			return nil, err
		}
		names := make([]string, len(list.Items))
		for i, item := range list.Items {
			names[i] = item.Name
		}
		return names, nil
	default:
		return nil, fmt.Errorf("listing not implemented for %s", gk.Kind)
	}
}

func writeCollectError(path, description string, err error) {
	content := fmt.Sprintf("Command failed: %s\n\n=== Error Details ===\n%s\n", description, err)
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	_ = os.WriteFile(path, []byte(content), 0o644)
	log.Warn("\tFailed: %s — %v", description, err)
}
