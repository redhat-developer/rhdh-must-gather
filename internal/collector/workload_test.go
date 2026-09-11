package collector

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestFilterPodsByOwner_Deployment(t *testing.T) {
	pods := []corev1.Pod{
		{
			ObjectMeta: metav1.ObjectMeta{
				Name: "pod-from-rs",
				OwnerReferences: []metav1.OwnerReference{
					{Kind: "ReplicaSet", Name: "my-dep-abc123"},
				},
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{
				Name: "pod-from-sts",
				OwnerReferences: []metav1.OwnerReference{
					{Kind: "StatefulSet", Name: "my-sts"},
				},
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{
				Name: "orphan-pod",
			},
		},
	}

	filtered := filterPodsByOwner(pods, KindDeployment)
	if len(filtered) != 1 {
		t.Fatalf("got %d pods, want 1", len(filtered))
	}
	if filtered[0].Name != "pod-from-rs" {
		t.Errorf("got %q, want pod-from-rs", filtered[0].Name)
	}
}

func TestFilterPodsByOwner_StatefulSet(t *testing.T) {
	pods := []corev1.Pod{
		{
			ObjectMeta: metav1.ObjectMeta{
				Name: "pod-from-rs",
				OwnerReferences: []metav1.OwnerReference{
					{Kind: "ReplicaSet", Name: "my-dep-abc123"},
				},
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{
				Name: "pod-from-sts",
				OwnerReferences: []metav1.OwnerReference{
					{Kind: "StatefulSet", Name: "my-sts"},
				},
			},
		},
	}

	filtered := filterPodsByOwner(pods, KindStatefulSet)
	if len(filtered) != 1 {
		t.Fatalf("got %d pods, want 1", len(filtered))
	}
	if filtered[0].Name != "pod-from-sts" {
		t.Errorf("got %q, want pod-from-sts", filtered[0].Name)
	}
}

func TestFilterPodsByOwner_Empty(t *testing.T) {
	filtered := filterPodsByOwner(nil, KindDeployment)
	if len(filtered) != 0 {
		t.Errorf("got %d pods, want 0", len(filtered))
	}
}

func TestOwnerRefKind(t *testing.T) {
	tests := []struct {
		kind WorkloadKind
		want string
	}{
		{KindDeployment, "ReplicaSet"},
		{KindStatefulSet, "StatefulSet"},
		{"unknown", ""},
	}
	for _, tt := range tests {
		got := ownerRefKind(tt.kind)
		if got != tt.want {
			t.Errorf("ownerRefKind(%q) = %q, want %q", tt.kind, got, tt.want)
		}
	}
}
