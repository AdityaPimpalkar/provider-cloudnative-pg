package cnpg

import (
	"context"
	"strings"
	"testing"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	corev1alpha1 "github.com/openeverest/openeverest/v2/api/core/v1alpha1"
	"github.com/openeverest/openeverest/v2/provider-runtime/controller"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/adityapimpalkar/provider-cloudnative-pg/definition/components"
)

func TestVolumeSnapshotRecovery(t *testing.T) {
	recovery := VolumeSnapshotRecovery(&components.BootstrapConfiguration{
		InitDB:         &cnpgv1.BootstrapInitDB{Database: "orders", Owner: "orders_owner"},
		VolumeSnapshot: &components.VolumeSnapshotSource{Name: "snap"},
	})
	storage := recovery.VolumeSnapshots.Storage
	if storage.Name != "snap" || storage.Kind != "VolumeSnapshot" || storage.APIGroup == nil || *storage.APIGroup != "snapshot.storage.k8s.io" {
		t.Errorf("storage %+v", storage)
	}
	if recovery.Source != "" || recovery.VolumeSnapshots.WalStorage != nil {
		t.Errorf("recovery needs no WAL source: %+v", recovery)
	}
	if recovery.Database != "orders" || recovery.Owner != "orders_owner" {
		t.Errorf("application database %q owner %q", recovery.Database, recovery.Owner)
	}
}

func TestValidateVolumeSnapshotBootstrap(t *testing.T) {
	snapshot := func(name string) *components.CNPGCustomSpec {
		return &components.CNPGCustomSpec{Bootstrap: &components.BootstrapConfiguration{VolumeSnapshot: &components.VolumeSnapshotSource{Name: name}}}
	}
	withWAL := snapshot("snap")
	withWAL.WALStorage = &cnpgv1.StorageConfiguration{Size: "1Gi"}
	tests := []struct {
		name          string
		custom        *components.CNPGCustomSpec
		hasDataSource bool
		wantErr       string
	}{
		{name: "none", custom: &components.CNPGCustomSpec{}},
		{name: "valid", custom: snapshot("orders-branch-1")},
		{name: "empty name", custom: snapshot(""), wantErr: "invalid"},
		{name: "invalid name", custom: snapshot("Snap_1"), wantErr: "invalid"},
		{name: "with a data source", custom: snapshot("snap"), hasDataSource: true, wantErr: "mutually exclusive"},
		{name: "with walStorage", custom: withWAL, wantErr: "walStorage"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateVolumeSnapshotBootstrap(tt.custom, tt.hasDataSource)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error = %v, want %q", err, tt.wantErr)
			}
		})
	}
}

func TestValidateVolumeSnapshot(t *testing.T) {
	snapshot := func(annotation string) *unstructured.Unstructured {
		snap := &unstructured.Unstructured{Object: map[string]any{"spec": map[string]any{}}}
		snap.SetGroupVersionKind(volumeSnapshotGVK)
		snap.SetNamespace("ns")
		snap.SetName("snap")
		if annotation != "" {
			snap.SetAnnotations(map[string]string{BootstrapInstanceAnnotation: annotation})
		}
		return snap
	}
	custom := &components.CNPGCustomSpec{Bootstrap: &components.BootstrapConfiguration{VolumeSnapshot: &components.VolumeSnapshotSource{Name: "snap"}}}
	tests := []struct {
		name    string
		objects []client.Object
		custom  *components.CNPGCustomSpec
		wantErr string
	}{
		{name: "no snapshot bootstrap", custom: &components.CNPGCustomSpec{}},
		{name: "authorized for this instance", objects: []client.Object{snapshot("pg")}, custom: custom},
		{name: "missing", custom: custom, wantErr: "not found"},
		{name: "not annotated", objects: []client.Object{snapshot("")}, custom: custom, wantErr: "isn't authorized"},
		{name: "authorized for another instance", objects: []client.Object{snapshot("other")}, custom: custom, wantErr: "isn't authorized"},
		// Only read at creation: once the cluster exists, the snapshot may be
		// gone, and changing it would have no effect.
		{name: "cluster started from it", objects: []client.Object{startedFrom("snap")}, custom: custom},
		{name: "cluster started from another snapshot", objects: []client.Object{startedFrom("other")}, custom: custom, wantErr: "immutable"},
		{name: "added to an existing cluster", objects: []client.Object{startedFrom("")}, custom: custom, wantErr: "immutable"},
		{name: "removed from an existing cluster", objects: []client.Object{startedFrom("snap")}, custom: &components.CNPGCustomSpec{}, wantErr: "immutable"},
		{name: "existing cluster without one", objects: []client.Object{startedFrom("")}, custom: &components.CNPGCustomSpec{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scheme := runtime.NewScheme()
			for _, add := range []func(*runtime.Scheme) error{cnpgv1.AddToScheme, corev1alpha1.AddToScheme} {
				if err := add(scheme); err != nil {
					t.Fatal(err)
				}
			}
			scheme.AddKnownTypeWithName(volumeSnapshotGVK, &unstructured.Unstructured{})
			instance := &corev1alpha1.Instance{ObjectMeta: metav1.ObjectMeta{Name: "pg", Namespace: "ns"}}
			cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(tt.objects...).Build()
			err := ValidateVolumeSnapshot(controller.NewContext(context.Background(), cl, instance, "provider-cloudnative-pg"), tt.custom)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error = %v, want %q", err, tt.wantErr)
			}
		})
	}
}

// startedFrom is a cluster rendered to start from the snapshot, or from initdb.
func startedFrom(snapshot string) *cnpgv1.Cluster {
	pg := &cnpgv1.Cluster{ObjectMeta: metav1.ObjectMeta{Name: "pg", Namespace: "ns"}}
	if snapshot != "" {
		pg.Spec.Bootstrap = &cnpgv1.BootstrapConfiguration{Recovery: VolumeSnapshotRecovery(&components.BootstrapConfiguration{
			VolumeSnapshot: &components.VolumeSnapshotSource{Name: snapshot},
		})}
	}
	return pg
}
