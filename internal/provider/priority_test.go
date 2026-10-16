package provider

import (
	"testing"

	"github.com/AlekSi/pointer"
	corev1alpha1 "github.com/openeverest/openeverest/v2/api/core/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"

	"github.com/adityapimpalkar/provider-cloudnative-pg/definition/components"
	"github.com/adityapimpalkar/provider-cloudnative-pg/internal/cnpg"
)

func TestPriorityClassName(t *testing.T) {
	engine := corev1alpha1.ComponentSpec{
		Replicas: pointer.To(int32(1)),
		Storage:  &corev1alpha1.Storage{Size: resource.MustParse("1Gi")},
		Resources: &corev1.ResourceRequirements{
			Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("1"), corev1.ResourceMemory: resource.MustParse("1Gi")},
			Limits:   corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("1"), corev1.ResourceMemory: resource.MustParse("1Gi")},
		},
	}
	if got := buildClusterSpec(engine, components.CNPGCustomSpec{}).PriorityClassName; got != "" {
		t.Fatalf("unset priority class must keep the cluster default, got %q", got)
	}
	custom := components.CNPGCustomSpec{PriorityClassName: "database-critical"}
	if err := cnpg.ValidateCustomSpec(&custom); err != nil {
		t.Fatal(err)
	}
	if got := buildClusterSpec(engine, custom).PriorityClassName; got != "database-critical" {
		t.Fatalf("PriorityClassName = %q, want database-critical", got)
	}
	for _, name := range []string{"Database", "has space", "-leading-dash", "under_score"} {
		if err := cnpg.ValidateCustomSpec(&components.CNPGCustomSpec{PriorityClassName: name}); err == nil {
			t.Errorf("priorityClassName %q must be rejected", name)
		}
	}
}
