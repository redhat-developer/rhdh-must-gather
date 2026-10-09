package collector

import (
	"testing"

	"k8s.io/client-go/rest"
)

func TestNewRESTClientGetter(t *testing.T) {
	cfg := &rest.Config{Host: "https://test:6443"}
	getter := newRESTClientGetter(cfg, "test-ns")
	if getter == nil {
		t.Fatal("expected non-nil getter")
	}
}

func TestRESTClientGetter_ToRESTConfig(t *testing.T) {
	cfg := &rest.Config{Host: "https://test:6443"}
	getter := newRESTClientGetter(cfg, "test-ns")

	got, err := getter.ToRESTConfig()
	if err != nil {
		t.Fatalf("ToRESTConfig: %v", err)
	}
	if got != cfg {
		t.Error("expected same config object")
	}
}

func TestRESTClientGetter_ToRawKubeConfigLoader(t *testing.T) {
	cfg := &rest.Config{Host: "https://test:6443"}
	getter := newRESTClientGetter(cfg, "test-ns")

	loader := getter.ToRawKubeConfigLoader()
	if loader == nil {
		t.Fatal("expected non-nil loader")
	}
}

func TestStaticClientConfig_RawConfig(t *testing.T) {
	cfg := &rest.Config{Host: "https://test:6443"}
	scc := &staticClientConfig{config: cfg, namespace: "test-ns"}

	raw, err := scc.RawConfig()
	if err != nil {
		t.Fatalf("RawConfig: %v", err)
	}
	if len(raw.Clusters) != 0 {
		t.Errorf("expected empty config, got %d clusters", len(raw.Clusters))
	}
}

func TestStaticClientConfig_ClientConfig(t *testing.T) {
	cfg := &rest.Config{Host: "https://test:6443"}
	scc := &staticClientConfig{config: cfg, namespace: "test-ns"}

	got, err := scc.ClientConfig()
	if err != nil {
		t.Fatalf("ClientConfig: %v", err)
	}
	if got != cfg {
		t.Error("expected same config object")
	}
}

func TestStaticClientConfig_Namespace(t *testing.T) {
	t.Run("with namespace", func(t *testing.T) {
		scc := &staticClientConfig{config: &rest.Config{}, namespace: "test-ns"}
		ns, overridden, err := scc.Namespace()
		if err != nil {
			t.Fatalf("Namespace: %v", err)
		}
		if ns != "test-ns" {
			t.Errorf("ns = %q, want test-ns", ns)
		}
		if !overridden {
			t.Error("expected overridden=true")
		}
	})

	t.Run("empty namespace", func(t *testing.T) {
		scc := &staticClientConfig{config: &rest.Config{}, namespace: ""}
		ns, overridden, err := scc.Namespace()
		if err != nil {
			t.Fatalf("Namespace: %v", err)
		}
		if ns != "" {
			t.Errorf("ns = %q, want empty", ns)
		}
		if overridden {
			t.Error("expected overridden=false")
		}
	})
}

func TestStaticClientConfig_ConfigAccess(t *testing.T) {
	scc := &staticClientConfig{config: &rest.Config{}, namespace: "test-ns"}
	if got := scc.ConfigAccess(); got != nil {
		t.Errorf("ConfigAccess() = %v, want nil", got)
	}
}

func TestRESTClientGetter_ToDiscoveryClient(t *testing.T) {
	cfg := &rest.Config{Host: "https://fake-server:6443"}
	getter := newRESTClientGetter(cfg, "test-ns")

	dc, err := getter.ToDiscoveryClient()
	if err != nil {
		t.Fatalf("ToDiscoveryClient: %v", err)
	}
	if dc == nil {
		t.Error("expected non-nil discovery client")
	}
}

func TestRESTClientGetter_ToDiscoveryClient_Error(t *testing.T) {
	cfg := &rest.Config{
		Host: "https://fake-server:6443",
		TLSClientConfig: rest.TLSClientConfig{
			CertFile: "/nonexistent/cert.pem",
			KeyFile:  "/nonexistent/key.pem",
		},
	}
	getter := newRESTClientGetter(cfg, "test-ns")

	_, err := getter.ToDiscoveryClient()
	if err == nil {
		t.Fatal("expected error with invalid TLS config")
	}
}

func TestRESTClientGetter_ToRESTMapper(t *testing.T) {
	cfg := &rest.Config{Host: "https://fake-server:6443"}
	getter := newRESTClientGetter(cfg, "test-ns")

	mapper, err := getter.ToRESTMapper()
	if err != nil {
		t.Fatalf("ToRESTMapper: %v", err)
	}
	if mapper == nil {
		t.Error("expected non-nil REST mapper")
	}
}

func TestRESTClientGetter_ToRESTMapper_Error(t *testing.T) {
	cfg := &rest.Config{
		Host: "https://fake-server:6443",
		TLSClientConfig: rest.TLSClientConfig{
			CertFile: "/nonexistent/cert.pem",
			KeyFile:  "/nonexistent/key.pem",
		},
	}
	getter := newRESTClientGetter(cfg, "test-ns")

	_, err := getter.ToRESTMapper()
	if err == nil {
		t.Fatal("expected error with invalid TLS config")
	}
}
