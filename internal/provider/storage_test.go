package provider

import (
	"reflect"
	"testing"

	"github.com/AlekSi/pointer"
	"github.com/adityapimpalkar/provider-cloudnative-pg/definition/components"
	"github.com/adityapimpalkar/provider-cloudnative-pg/internal/cnpg"
	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	corev1alpha1 "github.com/openeverest/openeverest/v2/api/core/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
)

func TestWALStorage(t *testing.T) {
	engine := corev1alpha1.ComponentSpec{
		Replicas: pointer.To(int32(1)),
		Storage:  &corev1alpha1.Storage{Size: resource.MustParse("5Gi"), StorageClass: pointer.To("data")},
		Resources: &corev1.ResourceRequirements{
			Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("1"), corev1.ResourceMemory: resource.MustParse("1Gi")},
			Limits:   corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("1"), corev1.ResourceMemory: resource.MustParse("1Gi")},
		},
	}
	baseline := buildClusterSpec(engine, components.CNPGCustomSpec{})
	if baseline.WalStorage != nil {
		t.Fatal("unset WAL storage must preserve the existing single-volume behavior")
	}
	wal := &cnpgv1.StorageConfiguration{
		Size: "1Gi", StorageClass: pointer.To("wal"), ResizeInUseVolumes: pointer.To(true),
	}
	for _, size := range []string{"1Gi", "2Gi"} {
		wal.Size = size
		custom := components.CNPGCustomSpec{WALStorage: wal}
		if err := cnpg.ValidateCustomSpec(&custom); err != nil {
			t.Fatal(err)
		}
		got := buildClusterSpec(engine, custom)
		if !reflect.DeepEqual(got.WalStorage, wal) {
			t.Fatalf("WAL configuration was not passed through: %+v", got.WalStorage)
		}
		if !reflect.DeepEqual(got.StorageConfiguration, baseline.StorageConfiguration) {
			t.Fatal("WAL configuration changed data storage")
		}
	}
	if err := cnpg.ValidateCustomSpec(&components.CNPGCustomSpec{}); err != nil {
		t.Fatal(err)
	}
	templateOnly := components.CNPGCustomSpec{WALStorage: &cnpgv1.StorageConfiguration{
		PersistentVolumeClaimTemplate: &corev1.PersistentVolumeClaimSpec{
			Resources: corev1.VolumeResourceRequirements{
				Requests: corev1.ResourceList{corev1.ResourceStorage: resource.MustParse("1Gi")},
			},
		},
	}}
	if err := cnpg.ValidateCustomSpec(&templateOnly); err != nil {
		t.Fatalf("pvcTemplate storage request must be accepted like CNPG does: %v", err)
	}
	for _, size := range []string{"", "invalid", "0", "-1Gi"} {
		custom := components.CNPGCustomSpec{WALStorage: &cnpgv1.StorageConfiguration{Size: size}}
		if err := cnpg.ValidateCustomSpec(&custom); err == nil {
			t.Errorf("walStorage.size %q must be rejected", size)
		}
	}
}
