package cnpg

import (
	"fmt"
	"strings"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	"github.com/openeverest/openeverest/v2/provider-runtime/controller"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/utils/ptr"

	"github.com/adityapimpalkar/provider-cloudnative-pg/definition/components"
)

const volumeSnapshotAPIGroup = "snapshot.storage.k8s.io"

// BootstrapInstanceAnnotation authorizes a VolumeSnapshot to start the
// Instance it names. Everest checks that a user may read an Instance's data
// sources, not engine parameters, and its users can't set annotations on
// VolumeSnapshots: only cluster administrators and trusted controllers, such
// as the Branching plugin, can authorize a snapshot.
const BootstrapInstanceAnnotation = "provider-cloudnative-pg.openeverest.io/bootstrap-instance"

var volumeSnapshotGVK = schema.GroupVersionKind{Group: volumeSnapshotAPIGroup, Version: "v1", Kind: "VolumeSnapshot"}

// VolumeSnapshotRecovery is the CloudNativePG recovery that starts the
// cluster from the bootstrap's VolumeSnapshot. It has no WAL source: the
// snapshot is recovered from its own pg_wal, so a crash-consistent snapshot
// of a running instance is enough.
func VolumeSnapshotRecovery(bootstrap *components.BootstrapConfiguration) *cnpgv1.BootstrapRecovery {
	recovery := &cnpgv1.BootstrapRecovery{
		VolumeSnapshots: &cnpgv1.DataSource{
			Storage: corev1.TypedLocalObjectReference{
				APIGroup: ptr.To(volumeSnapshotAPIGroup),
				Kind:     "VolumeSnapshot",
				Name:     bootstrap.VolumeSnapshot.Name,
			},
		},
	}
	if initdb := bootstrap.InitDB; initdb != nil {
		recovery.Database = initdb.Database
		recovery.Owner = initdb.Owner
		recovery.Secret = initdb.Secret
	}
	return recovery
}

// ValidateVolumeSnapshotBootstrap checks a bootstrap from a VolumeSnapshot
// against the rest of the instance.
func ValidateVolumeSnapshotBootstrap(custom *components.CNPGCustomSpec, hasDataSource bool) error {
	if custom.Bootstrap == nil || custom.Bootstrap.VolumeSnapshot == nil {
		return nil
	}
	name := custom.Bootstrap.VolumeSnapshot.Name
	if errs := validation.IsDNS1123Subdomain(name); len(errs) > 0 {
		return fmt.Errorf("bootstrap volumeSnapshot name %q is invalid: %s", name, strings.Join(errs, "; "))
	}
	if hasDataSource {
		return fmt.Errorf("bootstrap volumeSnapshot and spec.dataSource are mutually exclusive")
	}
	if custom.WALStorage != nil {
		return fmt.Errorf("bootstrap volumeSnapshot needs PGDATA and WAL on one volume: it can't be combined with walStorage")
	}
	return nil
}

func snapshotName(custom *components.CNPGCustomSpec) string {
	if custom == nil || custom.Bootstrap == nil || custom.Bootstrap.VolumeSnapshot == nil {
		return ""
	}
	return custom.Bootstrap.VolumeSnapshot.Name
}

// ValidateVolumeSnapshot checks the bootstrap VolumeSnapshot against the
// cluster. Before the cluster exists, the snapshot must exist and be
// authorized for this instance by BootstrapInstanceAnnotation. Once it
// exists, the snapshot was read and can't change.
func ValidateVolumeSnapshot(c *controller.Context, custom *components.CNPGCustomSpec) error {
	name := snapshotName(custom)
	pg := &cnpgv1.Cluster{}
	err := c.Get(pg, c.Name())
	if err == nil {
		if bootstrapSnapshot(pg) != name {
			return fmt.Errorf("bootstrap volumeSnapshot is immutable")
		}
		return nil
	}
	if !apierrors.IsNotFound(err) {
		return err
	}
	if name == "" {
		return nil
	}
	snap := &unstructured.Unstructured{}
	snap.SetGroupVersionKind(volumeSnapshotGVK)
	if err := c.Get(snap, name); err != nil {
		if apierrors.IsNotFound(err) {
			return fmt.Errorf("VolumeSnapshot %q not found", name)
		}
		return err
	}
	if got := snap.GetAnnotations()[BootstrapInstanceAnnotation]; got != c.Name() {
		return fmt.Errorf("VolumeSnapshot %q isn't authorized to start instance %q: it needs the annotation %s=%s", name, c.Name(), BootstrapInstanceAnnotation, c.Name())
	}
	return nil
}

// bootstrapSnapshot is the VolumeSnapshot a cluster was rendered to start from.
func bootstrapSnapshot(pg *cnpgv1.Cluster) string {
	if pg.Spec.Bootstrap == nil || pg.Spec.Bootstrap.Recovery == nil || pg.Spec.Bootstrap.Recovery.VolumeSnapshots == nil ||
		pg.Spec.Bootstrap.Recovery.Source != "" {
		return ""
	}
	return pg.Spec.Bootstrap.Recovery.VolumeSnapshots.Storage.Name
}
