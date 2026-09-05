package kube

import (
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/discovery/fake"
	fakeclientset "k8s.io/client-go/kubernetes/fake"
)

func newTestClient(groups ...string) *Client {
	fakeClient := fakeclientset.NewSimpleClientset()
	fakeDiscovery := fakeClient.Discovery().(*fake.FakeDiscovery)

	resources := make([]*metav1.APIResourceList, len(groups))
	for i, g := range groups {
		resources[i] = &metav1.APIResourceList{
			GroupVersion: g + "/v1",
		}
	}
	fakeDiscovery.Resources = resources

	return &Client{
		Clientset: fakeClient,
		Discovery: fakeDiscovery,
	}
}

func TestHasAPIGroup_Found(t *testing.T) {
	c := newTestClient("route.openshift.io", "apps", "config.openshift.io")
	ok, err := c.HasAPIGroup("route.openshift.io")
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Error("expected route.openshift.io to be found")
	}
}

func TestHasAPIGroup_NotFound(t *testing.T) {
	c := newTestClient("apps")
	ok, err := c.HasAPIGroup("route.openshift.io")
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Error("expected route.openshift.io not to be found")
	}
}

func TestHasAPIGroup_Empty(t *testing.T) {
	c := newTestClient()
	ok, err := c.HasAPIGroup("anything")
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Error("expected no API groups to be found")
	}
}
