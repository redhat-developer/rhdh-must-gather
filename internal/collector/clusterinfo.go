package collector

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/yaml"

	"github.com/redhat-developer/rhdh-must-gather/internal/log"
)

type ClusterInfo struct{}

func (c *ClusterInfo) Name() string { return "cluster-info" }

func (c *ClusterInfo) Run(ctx context.Context, cfg *Config) error {
	outDir := filepath.Join(cfg.BasePath, "cluster-info")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return fmt.Errorf("creating cluster-info output directory: %w", err)
	}

	client := cfg.Client.Clientset
	log.Info("Collecting cluster-info dump...")

	// Nodes
	nodes, err := client.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err == nil {
		writeYAML(filepath.Join(outDir, "nodes.yaml"), nodes)
	}

	// Namespaces
	namespaces, err := client.CoreV1().Namespaces().List(ctx, metav1.ListOptions{})
	if err != nil {
		log.Warn("cluster-info: failed to list namespaces: %v", err)
		return nil
	}

	for _, ns := range namespaces.Items {
		if cfg.IsInterrupted() {
			break
		}
		nsDir := filepath.Join(outDir, ns.Name)
		_ = os.MkdirAll(nsDir, 0o755)

		events, err := client.CoreV1().Events(ns.Name).List(ctx, metav1.ListOptions{})
		if err == nil {
			writeYAML(filepath.Join(nsDir, "events.yaml"), events)
		}

		pods, err := client.CoreV1().Pods(ns.Name).List(ctx, metav1.ListOptions{})
		if err == nil {
			writeYAML(filepath.Join(nsDir, "pods.yaml"), pods)
		}

		rcs, err := client.CoreV1().ReplicationControllers(ns.Name).List(ctx, metav1.ListOptions{})
		if err == nil && len(rcs.Items) > 0 {
			writeYAML(filepath.Join(nsDir, "replication-controllers.yaml"), rcs)
		}

		svcs, err := client.CoreV1().Services(ns.Name).List(ctx, metav1.ListOptions{})
		if err == nil {
			writeYAML(filepath.Join(nsDir, "services.yaml"), svcs)
		}

		dss, err := client.AppsV1().DaemonSets(ns.Name).List(ctx, metav1.ListOptions{})
		if err == nil && len(dss.Items) > 0 {
			writeYAML(filepath.Join(nsDir, "daemonsets.yaml"), dss)
		}

		deps, err := client.AppsV1().Deployments(ns.Name).List(ctx, metav1.ListOptions{})
		if err == nil {
			writeYAML(filepath.Join(nsDir, "deployments.yaml"), deps)
		}

		rss, err := client.AppsV1().ReplicaSets(ns.Name).List(ctx, metav1.ListOptions{})
		if err == nil && len(rss.Items) > 0 {
			writeYAML(filepath.Join(nsDir, "replicasets.yaml"), rss)
		}
	}

	log.Info("cluster-info dump completed")
	return nil
}

func writeYAML(path string, obj any) {
	data, err := yaml.Marshal(obj)
	if err != nil {
		return
	}
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	_ = os.WriteFile(path, data, 0o644)
}
