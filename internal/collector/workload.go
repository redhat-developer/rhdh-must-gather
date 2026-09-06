package collector

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/kubernetes"

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
		describeResource(ctx, filepath.Join(outDir, "deployment.describe.txt"), "deployment", ns, ref.Name)
		labelSelector = labels.Set(dep.Spec.Selector.MatchLabels).String()
		collectRolloutHistory(ctx, cfg, ns, ref.Kind, dep.Spec.Selector.MatchLabels, outDir)

	case KindStatefulSet:
		sts, err := client.AppsV1().StatefulSets(ns).Get(ctx, ref.Name, metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("getting statefulset %s/%s: %w", ns, ref.Name, err)
		}
		writeResource(filepath.Join(outDir, "statefulset.yaml"), sts)
		describeResource(ctx, filepath.Join(outDir, "statefulset.describe.txt"), "statefulset", ns, ref.Name)
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
		_ = os.WriteFile(filepath.Join(podsDir, "pods.txt"), []byte(msg), 0o644)
		_ = os.WriteFile(filepath.Join(podsDir, "pods.describe.txt"), []byte(msg), 0o644)
		log.Warn("\tNo pods found for %s %s/%s with owner %s", ref.Kind, ns, ref.Name, ownerKind)
	} else {
		podList := &corev1.PodList{Items: pods}
		writeResource(filepath.Join(podsDir, "pods.yaml"), podList)
		describeResource(ctx, filepath.Join(podsDir, "pods.describe.txt"), "pods", ns, "-l", labelSelector)
		writePodTable(filepath.Join(podsDir, "pods.txt"), pods)
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

	collectHeapDumps(cfg, ns, labelSelector, outDir, ref.Name, ref.InstanceName, string(ref.Kind))

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
	describeResource(ctx, filepath.Join(stsDir, "db-statefulset.describe.txt"), "statefulset", ns, name)

	sel := labels.Set(sts.Spec.Selector.MatchLabels).String()
	writeAggregatedStatefulSetLogs(ctx, client, ns, name, sel, stsDir)

	if sel == "" {
		return nil
	}

	podList, err := client.CoreV1().Pods(ns).List(ctx, metav1.ListOptions{LabelSelector: sel})
	if err == nil {
		podsDir := filepath.Join(stsDir, "pods")
		_ = os.MkdirAll(podsDir, 0o755)
		writeResource(filepath.Join(podsDir, "pods.yaml"), podList)
		describeResource(ctx, filepath.Join(podsDir, "pods.describe.txt"), "pods", ns, "-l", sel)
		writePodTable(filepath.Join(podsDir, "pods.txt"), podList.Items)

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
			setGVK(rsList, "ReplicaSetList", "apps/v1")
			for i := range rsList.Items {
				setGVK(&rsList.Items[i], "ReplicaSet", "apps/v1")
			}
			writeResource(filepath.Join(rsDir, "replicasets.yaml"), rsList)
			describeResource(ctx, filepath.Join(rsDir, "replicasets.describe.txt"), "replicasets", ns, "-l", sel)
			writeRolloutHistoryText(filepath.Join(histDir, "history.txt"), "deployment", rsList.Items)
		}

	case KindStatefulSet:
		crDir := filepath.Join(histDir, "controllerrevisions")
		_ = os.MkdirAll(crDir, 0o755)
		crList, err := client.AppsV1().ControllerRevisions(ns).List(ctx, metav1.ListOptions{LabelSelector: sel})
		if err == nil {
			setGVK(crList, "ControllerRevisionList", "apps/v1")
			for i := range crList.Items {
				setGVK(&crList.Items[i], "ControllerRevision", "apps/v1")
			}
			writeResource(filepath.Join(crDir, "controllerrevisions.yaml"), crList)
			describeResource(ctx, filepath.Join(crDir, "controllerrevisions.describe.txt"), "controllerrevisions", ns, "-l", sel)
			writeRolloutHistoryText(filepath.Join(histDir, "history.txt"), "statefulset", crList.Items)
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

func writePodTable(path string, pods []corev1.Pod) {
	var sb strings.Builder
	fmt.Fprintf(&sb, "%-60s  %-10s  %s\n", "NAME", "READY", "STATUS")
	for _, pod := range pods {
		ready := 0
		for _, cs := range pod.Status.ContainerStatuses {
			if cs.Ready {
				ready++
			}
		}
		total := len(pod.Spec.Containers)
		fmt.Fprintf(&sb, "%-60s  %d/%d        %s\n", pod.Name, ready, total, pod.Status.Phase)
	}
	_ = os.WriteFile(path, []byte(sb.String()), 0o644)
}

func writeRolloutHistoryText(path string, kind string, items any) {
	var sb strings.Builder
	fmt.Fprintf(&sb, "%s rollout history:\n", kind)
	sb.WriteString("REVISION  CHANGE-CAUSE\n")

	switch v := items.(type) {
	case []appsv1.ReplicaSet:
		for _, rs := range v {
			rev := rs.Annotations["deployment.kubernetes.io/revision"]
			cause := rs.Annotations["kubernetes.io/change-cause"]
			if cause == "" {
				cause = "<none>"
			}
			fmt.Fprintf(&sb, "%-10s%s\n", rev, cause)
		}
	case []appsv1.ControllerRevision:
		for _, cr := range v {
			fmt.Fprintf(&sb, "%-10d%s\n", cr.Revision, "<none>")
		}
	}

	_ = os.WriteFile(path, []byte(sb.String()), 0o644)
}

func writeAggregatedStatefulSetLogs(ctx context.Context, client kubernetes.Interface, ns, stsName, sel, stsDir string) {
	pods, err := client.CoreV1().Pods(ns).List(ctx, metav1.ListOptions{LabelSelector: sel})
	if err != nil || len(pods.Items) == 0 {
		return
	}
	writeAggregatedLogs(ctx, client, ns, pods.Items, false, filepath.Join(stsDir, "logs-db.txt"))
	writeAggregatedLogs(ctx, client, ns, pods.Items, true, filepath.Join(stsDir, "logs-db-previous.txt"))
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
			describeResource(ctx, filepath.Join(cmDir, cm.Name+".describe.txt"), "configmap", ns, cm.Name)
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
