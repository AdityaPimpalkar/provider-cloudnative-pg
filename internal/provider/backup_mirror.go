package provider

import (
	"context"
	"fmt"

	"github.com/adityapimpalkar/provider-cloudnative-pg/internal/cnpg/barman"
	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	cnpgutils "github.com/cloudnative-pg/cloudnative-pg/pkg/utils"
	backupv1alpha1 "github.com/openeverest/openeverest/v2/api/backup/v1alpha1"
	commonv1alpha1 "github.com/openeverest/openeverest/v2/api/common/v1alpha1"
	corev1alpha1 "github.com/openeverest/openeverest/v2/api/core/v1alpha1"
	"github.com/openeverest/openeverest/v2/provider-runtime/controller"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var _ controller.BackupMirror = (*Provider)(nil)

// Mirror implements controller.BackupMirror. It mirrors CNPG Backups produced
// by the ScheduledBackups this provider manages into OpenEverest Backup CRs;
// SyncBackup then adopts them and reports their status. On-demand CNPG Backups
// already originate from a Backup CR and are skipped.
func (p *Provider) Mirror(ctx context.Context, c client.Client, obj client.Object) (*backupv1alpha1.Backup, error) {
	cnpgBackup, ok := obj.(*cnpgv1.Backup)
	if !ok {
		return nil, nil
	}
	sbName := cnpgBackup.Labels[cnpgutils.ParentScheduledBackupLabelName]
	if sbName == "" {
		return nil, nil
	}
	pluginCfg := cnpgBackup.Spec.PluginConfiguration
	if pluginCfg == nil || pluginCfg.Name != barman.PluginName {
		return nil, nil
	}
	storageName := pluginCfg.Parameters[barman.PluginParameterObjectStore]
	if storageName == "" {
		return nil, nil
	}

	instanceName := cnpgBackup.Spec.Cluster.Name
	instance := &corev1alpha1.Instance{}
	if err := c.Get(ctx, client.ObjectKey{Namespace: cnpgBackup.Namespace, Name: instanceName}, instance); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("get parent Instance %q: %w", instanceName, err)
	}
	if instance.Spec.ProviderRef.Name != p.Name() {
		return nil, nil
	}
	if instance.Spec.Backup == nil || instance.Spec.Backup.ClassRef.Name == "" {
		return nil, nil
	}
	schedule, ok := barman.ScheduleForScheduledBackup(instance.Name, instance.Spec.Backup, sbName)
	if !ok {
		return nil, nil
	}

	return &backupv1alpha1.Backup{
		ObjectMeta: metav1.ObjectMeta{
			Name:      cnpgBackup.Name,
			Namespace: cnpgBackup.Namespace,
		},
		Spec: backupv1alpha1.BackupSpec{
			Origin: backupv1alpha1.BackupOrigin{
				Type:        backupv1alpha1.BackupOriginTypeInstance,
				InstanceRef: &commonv1alpha1.ObjectRef{Name: instance.Name},
			},
			ClassRef:     commonv1alpha1.ObjectRef{Name: instance.Spec.Backup.ClassRef.Name},
			StorageRef:   commonv1alpha1.ObjectRef{Name: storageName},
			ScheduleName: schedule.Name,
			Parameters:   schedule.Parameters,
		},
	}, nil
}

// OperatorBackupType implements controller.BackupMirror.
func (p *Provider) OperatorBackupType() client.Object {
	return &cnpgv1.Backup{}
}
