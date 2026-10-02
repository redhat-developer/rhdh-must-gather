package kube

import (
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/discovery/fake"
	fakeclientset "k8s.io/client-go/kubernetes/fake"
)

func newTestClient(groupVersions ...string) *Client {
	fakeClient := fakeclientset.NewSimpleClientset()
	fakeDiscovery := fakeClient.Discovery().(*fake.FakeDiscovery)

	resources := make([]*metav1.APIResourceList, len(groupVersions))
	for i, gv := range groupVersions {
		resources[i] = &metav1.APIResourceList{GroupVersion: gv}
	}
	fakeDiscovery.Resources = resources

	return &Client{
		Clientset: fakeClient,
		Discovery: fakeDiscovery,
	}
}

func TestHasAPIGroup(t *testing.T) {
	tests := []struct {
		name   string
		groups []string
		query  string
		want   bool
	}{
		{
			name:   "found",
			groups: []string{"route.openshift.io/v1", "apps/v1", "config.openshift.io/v1"},
			query:  "route.openshift.io",
			want:   true,
		},
		{
			name:   "not found",
			groups: []string{"apps/v1"},
			query:  "route.openshift.io",
			want:   false,
		},
		{
			name:   "empty groups",
			groups: nil,
			query:  "anything",
			want:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := newTestClient(tt.groups...)
			got, err := c.HasAPIGroup(tt.query)
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Errorf("HasAPIGroup(%q) = %v, want %v", tt.query, got, tt.want)
			}
		})
	}
}

func TestPreferredVersion(t *testing.T) {
	t.Run("found", func(t *testing.T) {
		c := newTestClient("rhdh.redhat.com/v1alpha3", "operators.coreos.com/v1alpha1")
		ver, err := c.PreferredVersion("rhdh.redhat.com")
		if err != nil {
			t.Fatal(err)
		}
		if ver != "v1alpha3" {
			t.Errorf("version = %q, want v1alpha3", ver)
		}
	})

	t.Run("not found", func(t *testing.T) {
		c := newTestClient("apps/v1")
		_, err := c.PreferredVersion("rhdh.redhat.com")
		if err == nil {
			t.Error("expected error for missing group")
		}
	})
}
