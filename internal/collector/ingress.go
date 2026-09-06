package collector

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	networkingv1 "k8s.io/api/networking/v1"

	"github.com/redhat-developer/rhdh-must-gather/internal/log"
)

type Ingress struct{}

func (i *Ingress) Name() string { return "ingress" }

func (i *Ingress) Run(ctx context.Context, cfg *Config) error {
	log.Info("Starting Ingress data collection...")

	outPath := filepath.Join(cfg.BasePath, "all-ingresses.txt")

	namespaces := cfg.Namespaces()

	var sb strings.Builder
	fmt.Fprintf(&sb, "%-30s %-40s %-15s %-50s\n", "NAMESPACE", "NAME", "CLASS", "HOSTS")

	count := 0
	if namespaces == nil {
		list, err := cfg.Client.Clientset.NetworkingV1().Ingresses("").List(ctx, metav1.ListOptions{})
		if err != nil {
			writeCollectError(outPath, "list ingresses", err)
			return nil
		}
		for _, ing := range list.Items {
			class := ""
			if ing.Spec.IngressClassName != nil {
				class = *ing.Spec.IngressClassName
			}
			hosts := ingressHosts(ing.Spec.Rules)
			fmt.Fprintf(&sb, "%-30s %-40s %-15s %-50s\n", ing.Namespace, ing.Name, class, hosts)
			count++
		}
	} else {
		for _, ns := range namespaces {
			list, err := cfg.Client.Clientset.NetworkingV1().Ingresses(ns).List(ctx, metav1.ListOptions{})
			if err != nil {
				log.Warn("Failed to list ingresses in namespace %s: %v", ns, err)
				continue
			}
			for _, ing := range list.Items {
				class := ""
				if ing.Spec.IngressClassName != nil {
					class = *ing.Spec.IngressClassName
				}
				hosts := ingressHosts(ing.Spec.Rules)
				fmt.Fprintf(&sb, "%-30s %-40s %-15s %-50s\n", ing.Namespace, ing.Name, class, hosts)
				count++
			}
		}
	}

	if count == 0 {
		sb.WriteString("No resources found\n")
	}

	return os.WriteFile(outPath, []byte(sb.String()), 0o644)
}

func ingressHosts(rules []networkingv1.IngressRule) string {
	var hosts []string
	for _, r := range rules {
		if r.Host != "" {
			hosts = append(hosts, r.Host)
		}
	}
	if len(hosts) == 0 {
		return "*"
	}
	return strings.Join(hosts, ",")
}
