package provider

import (
	"context"
	"fmt"

	"github.com/adityapimpalkar/provider-cloudnative-pg/internal/cnpg/barman"
	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	backupv1alpha1 "github.com/openeverest/openeverest/v2/api/backup/v1alpha1"
	commonv1alpha1 "github.com/openeverest/openeverest/v2/api/common/v1alpha1"
	corev1alpha1 "github.com/openeverest/openeverest/v2/api/core/v1alpha1"
	"github.com/openeverest/openeverest/v2/provider-runtime/controller"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Compile-time interface checks.
var _ controller.BackupMirror = (*Provider)(nil)

// Mirror implements controller.BackupMirror. CloudNativePG ScheduledBackup
// creates Backup CRs labeled with cnpg.io/scheduled-backup; those are mirrored
// into OpenEverest Backup CRs. On-demand backups (no parent schedule label)
// are skipped because SyncBackup already owns them.
func (p *Provider) Mirror(ctx context.Context, c client.Client, obj client.Object) (*backupv1alpha1.Backup, error) {
	ub, ok := obj.(*cnpgv1.Backup)
	if !ok {
		return nil, nil
	}
	scheduleName := ub.Labels[barman.ParentScheduledBackupLabel]
	if scheduleName == "" {
		return nil, nil
	}

	instance := &corev1alpha1.Instance{}
	if err := c.Get(ctx, client.ObjectKey{Namespace: ub.Namespace, Name: ub.Spec.Cluster.Name}, instance); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("get parent Instance %q: %w", ub.Spec.Cluster.Name, err)
	}
	if instance.Spec.ProviderRef.Name != p.Name() {
		return nil, nil
	}
	if instance.Spec.Backup == nil || instance.Spec.Backup.ClassRef.Name == "" {
		return nil, nil
	}

	storageName := storageNameForSchedule(instance, scheduleName)

	return &backupv1alpha1.Backup{
		ObjectMeta: metav1.ObjectMeta{
			Name:      ub.Name,
			Namespace: ub.Namespace,
		},
		Spec: backupv1alpha1.BackupSpec{
			InstanceRef:  commonv1alpha1.ObjectRef{Name: ub.Spec.Cluster.Name},
			ClassRef:     instance.Spec.Backup.ClassRef,
			StorageRef:   commonv1alpha1.ObjectRef{Name: storageName},
			ScheduleName: scheduleName,
		},
	}, nil
}

// OperatorBackupType implements controller.BackupMirror.
func (p *Provider) OperatorBackupType() client.Object {
	return &cnpgv1.Backup{}
}

func storageNameForSchedule(instance *corev1alpha1.Instance, scheduleName string) string {
	if instance.Spec.Backup == nil {
		return ""
	}
	for _, st := range instance.Spec.Backup.Storages {
		for _, s := range st.Schedules {
			if s.Name == scheduleName {
				return st.StorageRef.Name
			}
		}
	}
	return ""
}
