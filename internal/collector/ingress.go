package collector

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/duration"

	"github.com/redhat-developer/rhdh-must-gather/internal/log"
)

type Ingress struct{}

func (i *Ingress) Name() string { return "ingress" }

func (i *Ingress) Run(ctx context.Context, cfg *Config) error {
	log.Info("Starting Ingress data collection...")

	outPath := filepath.Join(cfg.BasePath, "all-ingresses.txt")

	namespaces := cfg.Namespaces()

	var ingresses []networkingv1.Ingress
	if namespaces == nil {
		list, err := cfg.Client.Clientset.NetworkingV1().Ingresses("").List(ctx, metav1.ListOptions{})
		if err != nil {
			writeCollectError(outPath, "list ingresses", err)
			return nil
		}
		ingresses = list.Items
	} else {
		for _, ns := range namespaces {
			list, err := cfg.Client.Clientset.NetworkingV1().Ingresses(ns).List(ctx, metav1.ListOptions{})
			if err != nil {
				log.Warn("Failed to list ingresses in namespace %s: %v", ns, err)
				continue
			}
			ingresses = append(ingresses, list.Items...)
		}
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "%-15s %-45s %-15s %-50s %-50s %-10s %s\n",
		"NAMESPACE", "NAME", "CLASS", "HOSTS", "ADDRESS", "PORTS", "AGE")
	for _, ing := range ingresses {
		class := ingressClass(&ing)
		hosts := ingressHosts(ing.Spec.Rules)
		address := ingressAddress(ing.Status.LoadBalancer.Ingress)
		ports := ingressPorts(ing.Spec.Rules, ing.Spec.TLS, ing.Spec.DefaultBackend)
		age := duration.ShortHumanDuration(time.Since(ing.CreationTimestamp.Time))
		fmt.Fprintf(&sb, "%-15s %-45s %-15s %-50s %-50s %-10s %s\n",
			ing.Namespace, ing.Name, class, hosts, address, ports, age)
	}

	if len(ingresses) == 0 {
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

func ingressAddress(lbIngress []networkingv1.IngressLoadBalancerIngress) string {
	var addrs []string
	for _, lb := range lbIngress {
		if lb.IP != "" {
			addrs = append(addrs, lb.IP)
		} else if lb.Hostname != "" {
			addrs = append(addrs, lb.Hostname)
		}
	}
	return strings.Join(addrs, ",")
}

func ingressClass(ing *networkingv1.Ingress) string {
	if ing.Spec.IngressClassName != nil {
		return *ing.Spec.IngressClassName
	}
	if v, ok := ing.Annotations["kubernetes.io/ingress.class"]; ok {
		return v
	}
	return "<none>"
}

func ingressPorts(rules []networkingv1.IngressRule, tls []networkingv1.IngressTLS, defaultBackend *networkingv1.IngressBackend) string {
	var ports []string
	if len(rules) > 0 || defaultBackend != nil {
		ports = append(ports, "80")
	}
	if len(tls) > 0 {
		ports = append(ports, "443")
	}
	if len(ports) == 0 {
		return ""
	}
	return strings.Join(ports, ", ")
}

