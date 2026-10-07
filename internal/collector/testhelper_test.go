package collector

import (
	"context"
	"io"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	fakediscovery "k8s.io/client-go/discovery/fake"
	fakedynamic "k8s.io/client-go/dynamic/fake"
	fakeclientset "k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/rest"

	"github.com/redhat-developer/rhdh-must-gather/internal/kube"
)

// fakePodOps is a test double for the PodOps interface.
type fakePodOps struct {
	execOutput string
	execErr    error
	logOutput  string
	logErr     error
}

func (f *fakePodOps) Exec(_ context.Context, _, _, _, _ string) (string, error) {
	return f.execOutput, f.execErr
}

func (f *fakePodOps) ExecToFile(_ context.Context, _, _, _, script, destPath string) error {
	if f.execErr != nil {
		return f.execErr
	}
	return os.WriteFile(destPath, []byte(f.execOutput), 0o644)
}

func (f *fakePodOps) GetLogStream(_ context.Context, _, _ string, _ *corev1.PodLogOptions) (io.ReadCloser, error) {
	if f.logErr != nil {
		return nil, f.logErr
	}
	return io.NopCloser(strings.NewReader(f.logOutput)), nil
}

func newTestConfig(t *testing.T, basePath string, opts ...testConfigOption) *Config {
	t.Helper()

	o := &testConfigOpts{}
	for _, fn := range opts {
		fn(o)
	}

	fakeClient := fakeclientset.NewSimpleClientset(o.typedObjs...)

	fd := fakeClient.Discovery().(*fakediscovery.FakeDiscovery)
	resources := make([]*metav1.APIResourceList, len(o.apiGroupVersions))
	for i, gv := range o.apiGroupVersions {
		resources[i] = &metav1.APIResourceList{GroupVersion: gv}
	}
	fd.Resources = resources

	scheme := runtime.NewScheme()
	dynClient := fakedynamic.NewSimpleDynamicClientWithCustomListKinds(scheme, o.dynamicListKinds, o.dynamicObjs...)

	interrupted := new(atomic.Bool)
	if o.interrupted {
		interrupted.Store(true)
	}

	return &Config{
		BasePath:    basePath,
		Interrupted: interrupted,
		PodOps:      o.podOps,
		Client: &kube.Client{
			Clientset: fakeClient,
			Discovery: fd,
			Dynamic:   dynClient,
			Config:    &rest.Config{Host: "https://fake-server:6443"},
		},
	}
}

type testConfigOpts struct {
	apiGroupVersions []string
	typedObjs        []runtime.Object
	dynamicObjs      []runtime.Object
	dynamicListKinds map[schema.GroupVersionResource]string
	interrupted      bool
	podOps           PodOps
}

type testConfigOption func(*testConfigOpts)

func withAPIGroups(groups ...string) testConfigOption {
	return func(o *testConfigOpts) { o.apiGroupVersions = groups }
}

func withTypedObjs(objs ...runtime.Object) testConfigOption {
	return func(o *testConfigOpts) { o.typedObjs = objs }
}

func withDynamicObjs(listKinds map[schema.GroupVersionResource]string, objs ...runtime.Object) testConfigOption {
	return func(o *testConfigOpts) {
		o.dynamicListKinds = listKinds
		o.dynamicObjs = objs
	}
}

func withInterrupted() testConfigOption {
	return func(o *testConfigOpts) { o.interrupted = true }
}

func withPodOps(ops PodOps) testConfigOption {
	return func(o *testConfigOpts) { o.podOps = ops }
}

type execResult struct {
	output string
	err    error
}

type scriptablePodOps struct {
	mu          sync.Mutex
	execResults []execResult
	callIndex   int
	logOutput   string
	logErr      error
}

func (s *scriptablePodOps) Exec(_ context.Context, _, _, _, _ string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.callIndex >= len(s.execResults) {
		return "", nil
	}
	r := s.execResults[s.callIndex]
	s.callIndex++
	return r.output, r.err
}

func (s *scriptablePodOps) ExecToFile(_ context.Context, _, _, _, _ string, destPath string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.callIndex >= len(s.execResults) {
		return nil
	}
	r := s.execResults[s.callIndex]
	s.callIndex++
	if r.err != nil {
		return r.err
	}
	return os.WriteFile(destPath, []byte(r.output), 0o644)
}

func (s *scriptablePodOps) GetLogStream(_ context.Context, _, _ string, _ *corev1.PodLogOptions) (io.ReadCloser, error) {
	if s.logErr != nil {
		return nil, s.logErr
	}
	return io.NopCloser(strings.NewReader(s.logOutput)), nil
}

func testNode(name, providerID string, labels map[string]string) *corev1.Node {
	if labels == nil {
		labels = map[string]string{}
	}
	return &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:   name,
			Labels: labels,
		},
		Spec: corev1.NodeSpec{
			ProviderID: providerID,
		},
	}
}
