package collector

import (
	"testing"

	networkingv1 "k8s.io/api/networking/v1"
)

func TestIngressClass(t *testing.T) {
	className := "nginx"
	tests := []struct {
		name string
		ing  *networkingv1.Ingress
		want string
	}{
		{
			name: "spec field",
			ing: &networkingv1.Ingress{
				Spec: networkingv1.IngressSpec{IngressClassName: &className},
			},
			want: "nginx",
		},
		{
			name: "no class",
			ing:  &networkingv1.Ingress{},
			want: "<none>",
		},
		{
			name: "annotation fallback",
			ing: func() *networkingv1.Ingress {
				ing := &networkingv1.Ingress{}
				ing.Annotations = map[string]string{"kubernetes.io/ingress.class": "haproxy"}
				return ing
			}(),
			want: "haproxy",
		},
		{
			name: "spec takes precedence over annotation",
			ing: func() *networkingv1.Ingress {
				ing := &networkingv1.Ingress{
					Spec: networkingv1.IngressSpec{IngressClassName: &className},
				}
				ing.Annotations = map[string]string{"kubernetes.io/ingress.class": "haproxy"}
				return ing
			}(),
			want: "nginx",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ingressClass(tt.ing)
			if got != tt.want {
				t.Errorf("ingressClass() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestIngressPorts(t *testing.T) {
	defaultBackend := &networkingv1.IngressBackend{
		Service: &networkingv1.IngressServiceBackend{Name: "svc", Port: networkingv1.ServiceBackendPort{Number: 8080}},
	}
	tests := []struct {
		name           string
		rules          []networkingv1.IngressRule
		tls            []networkingv1.IngressTLS
		defaultBackend *networkingv1.IngressBackend
		want           string
	}{
		{
			name:  "rules only",
			rules: []networkingv1.IngressRule{{Host: "example.com"}},
			want:  "80",
		},
		{
			name:           "tls and default backend",
			tls:            []networkingv1.IngressTLS{{Hosts: []string{"example.com"}}},
			defaultBackend: defaultBackend,
			want:           "80, 443",
		},
		{
			name:  "rules and tls",
			rules: []networkingv1.IngressRule{{Host: "example.com"}},
			tls:   []networkingv1.IngressTLS{{Hosts: []string{"example.com"}}},
			want:  "80, 443",
		},
		{
			name:           "default backend only",
			defaultBackend: defaultBackend,
			want:           "80",
		},
		{
			name: "tls only no backend",
			tls:  []networkingv1.IngressTLS{{Hosts: []string{"example.com"}}},
			want: "443",
		},
		{
			name: "neither",
			want: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ingressPorts(tt.rules, tt.tls, tt.defaultBackend)
			if got != tt.want {
				t.Errorf("ingressPorts() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestIngressHosts(t *testing.T) {
	tests := []struct {
		name  string
		rules []networkingv1.IngressRule
		want  string
	}{
		{
			name: "multiple hosts",
			rules: []networkingv1.IngressRule{
				{Host: "a.example.com"},
				{Host: "b.example.com"},
			},
			want: "a.example.com,b.example.com",
		},
		{
			name: "no hosts",
			want: "*",
		},
		{
			name:  "empty host string",
			rules: []networkingv1.IngressRule{{}},
			want:  "*",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ingressHosts(tt.rules)
			if got != tt.want {
				t.Errorf("ingressHosts() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestIngressAddress(t *testing.T) {
	tests := []struct {
		name      string
		lbIngress []networkingv1.IngressLoadBalancerIngress
		want      string
	}{
		{
			name:      "ip",
			lbIngress: []networkingv1.IngressLoadBalancerIngress{{IP: "10.0.0.1"}},
			want:      "10.0.0.1",
		},
		{
			name:      "hostname",
			lbIngress: []networkingv1.IngressLoadBalancerIngress{{Hostname: "lb.example.com"}},
			want:      "lb.example.com",
		},
		{
			name: "empty",
			want: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ingressAddress(tt.lbIngress)
			if got != tt.want {
				t.Errorf("ingressAddress() = %q, want %q", got, tt.want)
			}
		})
	}
}
