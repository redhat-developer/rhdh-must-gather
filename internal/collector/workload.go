package collector

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"

	"github.com/redhat-developer/rhdh-must-gather/internal/log"
)

type WorkloadKind string

const (
	KindDeployment  WorkloadKind = "deployment"
	KindStatefulSet WorkloadKind = "statefulset"
)

type WorkloadRef struct {
	Namespace    string
	Name         string
	Kind         WorkloadKind
	InstanceName string
}

func CollectWorkload(ctx context.Context, cfg *Config, ref WorkloadRef, outDir string) error {
	log.Debug("Collecting %s %s in %s", ref.Kind, ref.Name, ref.Namespace)
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return err
	}

	client := cfg.Client.Clientset
	ns := ref.Namespace

	var labelSelector string
	switch ref.Kind {
	case KindDeployment:
		dep, err := client.AppsV1().Deployments(ns).Get(ctx, ref.Name, metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("getting deployment %s/%s: %w", ns, ref.Name, err)
		}
		writeResource(filepath.Join(outDir, "deployment.yaml"), dep)
		labelSelector = labels.Set(dep.Spec.Selector.MatchLabels).String()
		collectRolloutHistory(ctx, cfg, ns, ref.Kind, dep.Spec.Selector.MatchLabels, outDir)

	case KindStatefulSet:
		sts, err := client.AppsV1().StatefulSets(ns).Get(ctx, ref.Name, metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("getting statefulset %s/%s: %w", ns, ref.Name, err)
		}
		writeResource(filepath.Join(outDir, "statefulset.yaml"), sts)
		labelSelector = labels.Set(sts.Spec.Selector.MatchLabels).String()
		collectRolloutHistory(ctx, cfg, ns, ref.Kind, sts.Spec.Selector.MatchLabels, outDir)
	}

	if labelSelector == "" {
		return nil
	}

	allPods, err := client.CoreV1().Pods(ns).List(ctx, metav1.ListOptions{LabelSelector: labelSelector})
	if err != nil {
		return fmt.Errorf("listing pods: %w", err)
	}

	pods := filterPodsByOwner(allPods.Items, ref.Kind)

	podsDir := filepath.Join(outDir, "pods")
	_ = os.MkdirAll(podsDir, 0o755)
	if len(pods) == 0 {
		ownerKind := ownerRefKind(ref.Kind)
		msg := fmt.Sprintf("No pods found with owner kind %s\n", ownerKind)
		_ = os.WriteFile(filepath.Join(podsDir, "pods.yaml"), []byte(msg), 0o644)
		log.Warn("\tNo pods found for %s %s/%s with owner %s", ref.Kind, ns, ref.Name, ownerKind)
	} else {
		writeResource(filepath.Join(podsDir, "pods.yaml"), &corev1.PodList{Items: pods})
	}

	var wg sync.WaitGroup
	for i := range pods {
		pod := &pods[i]

		wg.Add(1)
		go func() {
			defer wg.Done()
			if cfg.IsInterrupted() {
				return
			}
			CollectPodLogs(ctx, cfg, ns, pod, filepath.Join(outDir, "logs", "pod="+pod.Name))
		}()

		if pod.Status.Phase == corev1.PodRunning {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if cfg.IsInterrupted() {
					return
				}
				log.Info("\tCollecting app data and processes from pod %s", pod.Name)
				CollectPodData(ctx, cfg, ns, pod, filepath.Join(outDir, "data", "pod="+pod.Name, "container=backstage-backend"))
				CollectProcesses(ctx, cfg, ns, pod, filepath.Join(outDir, "processes", "pod="+pod.Name))
			}()
		}
	}
	wg.Wait()

	return nil
}

func CollectDBStatefulSet(ctx context.Context, cfg *Config, ns, name, outDir string) error {
	if name == "" {
		return nil
	}
	log.Debug("db-statefulset=%s", name)

	stsDir := filepath.Join(outDir, "db-statefulset")
	_ = os.MkdirAll(stsDir, 0o755)

	client := cfg.Client.Clientset

	sts, err := client.AppsV1().StatefulSets(ns).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		writeCollectError(filepath.Join(stsDir, "db-statefulset.yaml"), "get DB statefulset", err)
		return nil
	}
	writeResource(filepath.Join(stsDir, "db-statefulset.yaml"), sts)

	sel := labels.Set(sts.Spec.Selector.MatchLabels).String()
	if sel == "" {
		return nil
	}

	podList, err := client.CoreV1().Pods(ns).List(ctx, metav1.ListOptions{LabelSelector: sel})
	if err == nil {
		podsDir := filepath.Join(stsDir, "pods")
		_ = os.MkdirAll(podsDir, 0o755)
		writeResource(filepath.Join(podsDir, "pods.yaml"), podList)

		for i := range podList.Items {
			pod := &podList.Items[i]
			CollectPodLogs(ctx, cfg, ns, pod, filepath.Join(stsDir, "logs", "pod="+pod.Name))
		}
	}

	collectRolloutHistory(ctx, cfg, ns, KindStatefulSet, sts.Spec.Selector.MatchLabels, stsDir)
	return nil
}

func collectRolloutHistory(ctx context.Context, cfg *Config, ns string, kind WorkloadKind, matchLabels map[string]string, outDir string) {
	histDir := filepath.Join(outDir, "rollout-history")
	_ = os.MkdirAll(histDir, 0o755)

	sel := labels.Set(matchLabels).String()
	client := cfg.Client.Clientset

	switch kind {
	case KindDeployment:
		rsDir := filepath.Join(histDir, "replicasets")
		_ = os.MkdirAll(rsDir, 0o755)
		rsList, err := client.AppsV1().ReplicaSets(ns).List(ctx, metav1.ListOptions{LabelSelector: sel})
		if err == nil {
			writeResource(filepath.Join(rsDir, "replicasets.yaml"), rsList)
		}

	case KindStatefulSet:
		crDir := filepath.Join(histDir, "controllerrevisions")
		_ = os.MkdirAll(crDir, 0o755)
		crList, err := client.AppsV1().ControllerRevisions(ns).List(ctx, metav1.ListOptions{LabelSelector: sel})
		if err == nil {
			writeResource(filepath.Join(crDir, "controllerrevisions.yaml"), crList)
		}
	}
}

func filterPodsByOwner(pods []corev1.Pod, kind WorkloadKind) []corev1.Pod {
	ownerKind := ownerRefKind(kind)
	var filtered []corev1.Pod
	for _, pod := range pods {
		for _, ref := range pod.OwnerReferences {
			if ref.Kind == ownerKind {
				filtered = append(filtered, pod)
				break
			}
		}
	}
	return filtered
}

func ownerRefKind(kind WorkloadKind) string {
	switch kind {
	case KindDeployment:
		return "ReplicaSet"
	case KindStatefulSet:
		return "StatefulSet"
	default:
		return ""
	}
}

func CollectNamespaceData(ctx context.Context, cfg *Config, ns, outDir string, withSecrets bool) {
	_ = os.MkdirAll(outDir, 0o755)
	client := cfg.Client.Clientset

	cmDir := filepath.Join(outDir, "_configmaps")
	_ = os.MkdirAll(cmDir, 0o755)
	cmList, err := client.CoreV1().ConfigMaps(ns).List(ctx, metav1.ListOptions{})
	if err == nil {
		for i := range cmList.Items {
			cm := &cmList.Items[i]
			writeResource(filepath.Join(cmDir, cm.Name+".yaml"), cm)
		}
	}

	if withSecrets {
		secDir := filepath.Join(outDir, "_secrets")
		_ = os.MkdirAll(secDir, 0o755)
		secList, err := client.CoreV1().Secrets(ns).List(ctx, metav1.ListOptions{})
		if err == nil {
			for i := range secList.Items {
				sec := &secList.Items[i]
				writeResource(filepath.Join(secDir, sec.Name+".yaml"), sec)
			}
		}
	} else {
		log.Debug("Skipping secret collection for namespace %s (use --with-secrets to collect)", ns)
	}
}
