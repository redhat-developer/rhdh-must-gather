package collector

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/duration"

	"github.com/redhat-developer/rhdh-must-gather/internal/log"
)

type NetworkPolicies struct {
}

type nsDump struct {
	policies []networkingv1.NetworkPolicy
	listErr  error
}

func (n *NetworkPolicies) Name() string { return "network-policies" }

func (n *NetworkPolicies) Run(ctx context.Context, cfg *Config) error {
	log.Info("Starting Network Policies collection...")

	outDir := filepath.Join(cfg.BasePath, "network-policies")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return err
	}

	n.collectClusterPolicies(ctx, cfg, outDir)

	namespaces := (&NamespaceInspect{}).resolveNamespaces(ctx, cfg)
	if len(namespaces) == 0 {
		log.Warn("No RHDH deployments or orchestrator components found in cluster")
		_ = os.WriteFile(filepath.Join(outDir, "no-namespaces.txt"), []byte("No namespaces detected\n"), 0o644)
		return nil
	}
	log.Info("Found %d namespace(s) for NetworkPolicy collection: %s", len(namespaces), strings.Join(namespaces, " "))
	_ = os.WriteFile(filepath.Join(outDir, "detected-namespaces.txt"), []byte(strings.Join(namespaces, "\n")+"\n"), 0o644)
	policiesByNS := map[string]nsDump{}
	for _, ns := range namespaces {
		policiesByNS[ns] = n.collectNamespace(ctx, cfg, outDir, ns)
	}

	n.collectPeerNamespaces(ctx, cfg, outDir)
	n.writeSummary(outDir, namespaces, policiesByNS)

	log.Success("NetworkPolicy collection completed for %d namespace(s).", len(namespaces))
	return nil
}

func (n *NetworkPolicies) collectNamespace(ctx context.Context, cfg *Config, outDir, ns string) nsDump {
	log.Info("Collecting NetworkPolicies in namespace %s", ns)
	nsDir := filepath.Join(outDir, "ns="+ns)
	_ = os.MkdirAll(nsDir, 0o755)
	client := cfg.Client.Clientset

	dump := nsDump{}

	list, err := client.NetworkingV1().NetworkPolicies(ns).List(ctx, metav1.ListOptions{})
	if err != nil {
		writeCollectError(filepath.Join(nsDir, "networkpolicies.yaml"), "NetworkPolicy list in "+ns, err)
		dump.listErr = err
	} else {
		setGVK(list, "NetworkPolicyList", "networking.k8s.io/v1")
		for i := range list.Items {
			setGVK(&list.Items[i], "NetworkPolicy", "networking.k8s.io/v1")
		}
		writeResource(filepath.Join(nsDir, "networkpolicies.yaml"), list)

		if data, jerr := json.MarshalIndent(list, " ", " "); jerr == nil {
			_ = os.WriteFile(filepath.Join(nsDir, "networkpolicies.json"), data, 0o644)
		}

		n.writePolicyTable(filepath.Join(nsDir, "networkpolicies.txt"), list.Items)
		n.writePolicyLabels(filepath.Join(nsDir, "networkpolicies.labels.txt"), list.Items)
		n.writePolicyDescribe(ctx, cfg, nsDir, ns)

		dump.policies = list.Items
	}

	nsObj, err := client.CoreV1().Namespaces().Get(ctx, ns, metav1.GetOptions{})
	if err != nil {
		writeCollectError(filepath.Join(nsDir, "namespace.yaml"), "Namespace labels for "+ns, err)
	} else {
		setGVK(nsObj, "Namespace", "v1")
		writeResource(filepath.Join(nsDir, "namespace.yaml"), nsObj)
	}

	pods, err := client.CoreV1().Pods(ns).List(ctx, metav1.ListOptions{})
	if err != nil {
		writeCollectError(filepath.Join(nsDir, "pods-show-labels.txt"), "Pod labels in "+ns, err)
	} else {
		n.writePodLabels(filepath.Join(nsDir, "pods-show-labels.txt"), pods.Items)
	}
	svcs, err := client.CoreV1().Services(ns).List(ctx, metav1.ListOptions{})
	if err != nil {
		writeCollectError(filepath.Join(nsDir, "services.yaml"), "Services (ports) in "+ns, err)
	} else {
		setGVK(svcs, "ServiceList", "v1")
		writeResource(filepath.Join(nsDir, "services.yaml"), svcs)
	}

	return dump
}

func (n *NetworkPolicies) writePolicyTable(path string, items []networkingv1.NetworkPolicy) {
	var sb strings.Builder
	fmt.Fprintf(&sb, "%-50s %-40s %s\n", "NAME", "POD-SELECTOR", "AGE")
	for _, np := range items {
		age := duration.ShortHumanDuration(time.Since(np.CreationTimestamp.Time))
		fmt.Fprintf(&sb, "%-50s %-40s %s\n", np.Name, formatSelector(&np.Spec.PodSelector), age)
	}
	if len(items) == 0 {
		sb.WriteString("No resources found\n")
	}
	_ = os.WriteFile(path, []byte(sb.String()), 0o644)
}

func (n *NetworkPolicies) writePolicyLabels(path string, items []networkingv1.NetworkPolicy) {
	var sb strings.Builder
	fmt.Fprintf(&sb, "%-50s %s\n", "NAME", "LABELS")
	for _, np := range items {
		fmt.Fprintf(&sb, "%-50s %s\n", np.Name, labels.FormatLabels(np.Labels))
	}
	_ = os.WriteFile(path, []byte(sb.String()), 0o644)
}

func (n *NetworkPolicies) writePodLabels(path string, pods []corev1.Pod) {
	var sb strings.Builder
	fmt.Fprintf(&sb, "%-50s %-12s %-16s %s\n", "NAME", "STATUS", "IP", "LABELS")
	for _, p := range pods {
		fmt.Fprintf(&sb, "%-50s %-12s %-16s %s\n", p.Name, string(p.Status.Phase), p.Status.PodIP, labels.FormatLabels(p.Labels))
	}
	_ = os.WriteFile(path, []byte(sb.String()), 0o644)
}

func (n *NetworkPolicies) writePolicyDescribe(ctx context.Context, cfg *Config, nsDir, ns string) {
	path := filepath.Join(nsDir, "networkpolicies.describe.txt")
	if cfg.Client.Config == nil {
		_ = os.WriteFile(path, []byte("describe skipped (no REST config)\n"), 0o644)
		return
	}
	describeResource(ctx, cfg, path, "networkpolicy", ns)
}

func (n *NetworkPolicies) collectPeerNamespaces(ctx context.Context, cfg *Config, outDir string) {
	peerDir := filepath.Join(outDir, "peer-namespaces")
	_ = os.MkdirAll(peerDir, 0o755)

	peers := []string{
		"openshift-ingress",
		"openshift-monitoring",
		"openshift-user-workload-monitoring",
		"knative-serving",
		"knative-eventing",
		"openshift-serverless-logic",
		"monitoring",
		"gmp-system",
		"gke-gmp-system",
	}

	for _, peer := range peers {
		nsObj, err := cfg.Client.Clientset.CoreV1().Namespaces().Get(ctx, peer, metav1.GetOptions{})
		if err != nil {
			if apierrors.IsNotFound(err) {
				continue
			}
			writeCollectError(filepath.Join(peerDir, peer+".yaml"), "Peer namespaces "+peer, err)
			continue
		}
		setGVK(nsObj, "Namespace", "v1")
		writeResource(filepath.Join(peerDir, peer+".yaml"), nsObj)
	}
}

func (n *NetworkPolicies) collectClusterPolicies(ctx context.Context, cfg *Config, outDir string) {
	if cfg.Client == nil || cfg.Client.Dynamic == nil {
		return
	}

	dir := filepath.Join(outDir, "cluster")
	_ = os.MkdirAll(dir, 0o755)

	resources := []string{"adminnetworkpolicies", "clusternetworkpolicies"}
	for _, resource := range resources {
		gvr := schema.GroupVersionResource{
			Group:    "policy.networking.k8s.io",
			Version:  "v1alpha1",
			Resource: resource,
		}
		list, err := cfg.Client.Dynamic.Resource(gvr).List(ctx, metav1.ListOptions{})
		if err != nil {
			if apierrors.IsNotFound(err) || meta.IsNoMatchError(err) {
				continue
			}
			writeCollectError(filepath.Join(dir, resource+".yaml"), "list "+resource, err)
			continue
		}
		writeResource(filepath.Join(dir, resource+".yaml"), list)
	}
}

func (n *NetworkPolicies) writeSummary(outDir string, namespaces []string, policiesByNS map[string]nsDump) {
	var sb strings.Builder
	fmt.Fprintf(&sb, "NetworkPolicy diagnostic summary\n")
	fmt.Fprintf(&sb, "=======================\n")
	fmt.Fprintf(&sb, "Collected at: %s\n\n", time.Now().UTC().Format(time.RFC3339))
	sb.WriteString("This dump is for connectivity failures caused by NetworkPolicies\n")
	sb.WriteString("(default-deny, wrong pod/namespace labels, missing user additive policies).\n\n")
	sb.WriteString("RHDH namespaces collected:\n")
	for _, ns := range namespaces {
		fmt.Fprintf(&sb, "  - %s\n", ns)
	}
	sb.WriteString("\n")

	for _, ns := range namespaces {
		fmt.Fprintf(&sb, "Namespace: %s\n-----------------------\n", ns)
		dump := policiesByNS[ns]
		if dump.listErr != nil {
			fmt.Fprintf(&sb, " NetworkPolicy count: unavailable (list failed; see ns=%s/networkpolicies.yaml)\n", ns)
		} else {
			fmt.Fprintf(&sb, " NetworkPolicy count: %d\n", len(dump.policies))
			for _, np := range dump.policies {
				fmt.Fprintf(&sb, "  - %s\n", np.Name)
				fmt.Fprintf(&sb, "    policyTypes: %s\n", np.Spec.PolicyTypes)
				fmt.Fprintf(&sb, "    podSelector: %s\n", formatSelector(&np.Spec.PodSelector))
			}
			sb.WriteString(" namespaceSelectors (peers these policies allow from/to):\n")
			found := false
			for _, np := range dump.policies {
				for _, rule := range np.Spec.Ingress {
					for _, from := range rule.From {
						if from.NamespaceSelector != nil {
							found = true
							fmt.Fprintf(&sb, "    %s: %s\n", np.Name, formatSelector(from.NamespaceSelector))
						}
					}
				}
				for _, rule := range np.Spec.Egress {
					for _, to := range rule.To {
						if to.NamespaceSelector != nil {
							found = true
							fmt.Fprintf(&sb, "    %s: %s\n", np.Name, formatSelector(to.NamespaceSelector))
						}
					}
				}
			}
			if !found {
				sb.WriteString("    (none found)\n")
			}
		}
		fmt.Fprintf(&sb, "  Pod labels: ns=%s/pods-show-labels.txt\n", ns)
		fmt.Fprintf(&sb, "  Namespace labels: ns=%s/namespace.yaml\n", ns)
		fmt.Fprintf(&sb, "  Services/ports: ns=%s/services.yaml\n\n", ns)
	}

	sb.WriteString("Peer namespaces (for namespaceSelector matching):\n")
	sb.WriteString("  Directory: peer-namespaces/\n")
	sb.WriteString("  Compare labels in those YAML files to namespaceSelector in the policies.\n\n")
	sb.WriteString("How to troubleshoot with this dump:\n")
	sb.WriteString("  1. Find default-deny and allow policies in ns=*/networkpolicies.yaml\n")
	sb.WriteString("  2. Match spec.podSelector in ns=*/networkpolicies.yaml to pods-show-labels.txt\n")
	sb.WriteString("  (Helm chart default: app.kubernetes.io/name=redhat-developer-hub\n")
	sb.WriteString("  (nameOverride may be developer-hub. Also match instance/component labels.\n")
	sb.WriteString("     Operator: rhdh.redhat.com/app=backstage-<cr> / backstage-psql-<cr>\n")
	sb.WriteString("  3. For UI (7007) or metrics (9464), match namespaceSelector to \n")
	sb.WriteString("  peer-namespaces/*.yaml (ingress / openshift-monitoring labels)\n")
	sb.WriteString("  4. If a plugin uses a port other than 443, look for a user-created\n")
	sb.WriteString("  additive NetworkPolicy; base policies only allow DNS + HTTPS 443\n")
	sb.WriteString("  (+ Redis 6379, Postgres 5432, etc.)\n")
	sb.WriteString("  5. Empty namespaced policy list + pods still isolated: check\n")
	sb.WriteString("  cluster/adminnetworkpolicies.yaml and cluster/clusternetworkpolicies.yaml\n")
	sb.WriteString("  (or CNI not enforcing NetworkPolicies)\n")

	_ = os.WriteFile(filepath.Join(outDir, "summary.txt"), []byte(sb.String()), 0o644)
	log.Info("Summary written to %s", filepath.Join(outDir, "summary.txt"))
}

func formatSelector(sel *metav1.LabelSelector) string {
	if sel == nil {
		return "<none>"
	}
	if len(sel.MatchLabels) == 0 && len(sel.MatchExpressions) == 0 {
		return "<empty, selects all>"
	}
	return metav1.FormatLabelSelector(sel)
}
