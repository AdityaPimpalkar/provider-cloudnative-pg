package barman

import (
	"fmt"
	"time"

	"github.com/AlekSi/pointer"
	"github.com/adityapimpalkar/provider-cloudnative-pg/definition/components"
	backupv1alpha1 "github.com/openeverest/openeverest/v2/api/backup/v1alpha1"
	"github.com/openeverest/openeverest/v2/provider-runtime/controller"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
)

const recoveryExternalClusterName = "origin"

func ValidateDataSource(ds *backupv1alpha1.DataSource) error {
	if ds == nil {
		return nil
	}

	switch ds.Type {
	case backupv1alpha1.DataSourceTypeBackup:
		if ds.Backup == nil || ds.Backup.BackupRef.Name == "" {
			return fmt.Errorf("spec.dataSource.backup.backupRef.name is required")
		}
	case backupv1alpha1.DataSourceTypePointInTime:
		if ds.PointInTime == nil || ds.PointInTime.Source.StorageRef.Name == "" {
			return fmt.Errorf("spec.dataSource.pointInTime.source.storageRef.name is required")
		}
		if ds.PointInTime.RecoveryTarget == backupv1alpha1.RecoveryTargetDate && ds.PointInTime.Date == nil {
			return fmt.Errorf("spec.dataSource.pointInTime.date is required when recoveryTarget is date")
		}
	default:
		return fmt.Errorf("unsupported dataSource.type %q", ds.Type)
	}

	return nil
}

func BuildRecoveryConfig(c *controller.Context, custom components.CNPGCustomSpec) (*cnpgv1.BootstrapRecovery, cnpgv1.ExternalCluster, error) {
	ds := c.Instance().Spec.DataSource

	var objectStoreName, serverName string
	var recoveryTarget *cnpgv1.RecoveryTarget

	if ds.Type == backupv1alpha1.DataSourceTypePointInTime {
		pitr := ds.PointInTime
		objectStoreName = pitr.Source.StorageRef.Name
		serverName = c.Instance().Name
		if ref := pitr.Source.InstanceRef; ref != nil && ref.Name != "" {
			serverName = ref.Name
		}
		if pitr.RecoveryTarget == backupv1alpha1.RecoveryTargetDate {
			recoveryTarget = &cnpgv1.RecoveryTarget{
				TargetTime: pitr.Date.UTC().Format(time.RFC3339),
			}
		}
	} else {
		cnpgBackup, storeName, err := resolveSourceBackup(c, ds)
		if err != nil {
			return nil, cnpgv1.ExternalCluster{}, err
		}
		objectStoreName = storeName

		serverName = cnpgBackup.Status.ServerName
		if serverName == "" {
			serverName = cnpgBackup.Spec.Cluster.Name
		}
		if serverName == "" {
			return nil, cnpgv1.ExternalCluster{}, fmt.Errorf(
				"CloudNativePG Backup %q has no server name (status.serverName / spec.cluster.name)",
				cnpgBackup.Name)
		}

		recoveryTarget = &cnpgv1.RecoveryTarget {
			BackupID:        cnpgBackup.Status.BackupID,
			TargetImmediate: pointer.To(true),
		}
	}

	recovery := &cnpgv1.BootstrapRecovery{
		Source:         recoveryExternalClusterName,
		RecoveryTarget: recoveryTarget,
	}

	if custom.Bootstrap != nil && custom.Bootstrap.InitDB != nil {
		initdb := custom.Bootstrap.InitDB
		recovery.Database = initdb.Database
		recovery.Owner = initdb.Owner
		recovery.Secret = initdb.Secret
	}

	externalCluster := cnpgv1.ExternalCluster{
		Name: recoveryExternalClusterName,
		PluginConfiguration: &cnpgv1.PluginConfiguration{
			Name: PluginName,
			Parameters: map[string]string{
				PluginParameterObjectStore: objectStoreName,
				"serverName":               serverName,
			},
		},
	}

	return recovery, externalCluster, nil
}

func resolveSourceBackup(c *controller.Context, ds *backupv1alpha1.DataSource) (*cnpgv1.Backup, string, error) {
	backupName := ds.Backup.BackupRef.Name

	oeBackup := &backupv1alpha1.Backup{}
	if err := c.Get(oeBackup, backupName); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, "", controller.WaitFor(fmt.Sprintf(
				"source Backup %q not yet present", backupName))
		}
		return nil, "", &controller.BackupConfigError{
			Reason:  "BackupResolutionFailed",
			Message: err.Error(),
		}
	}

	if oeBackup.Status.State != backupv1alpha1.BackupStateSucceeded {
		return nil, "", controller.WaitFor(fmt.Sprintf(
			"source Backup %q is in state %q, waiting for Succeeded",
			backupName, oeBackup.Status.State))
	}

	objectStoreName := oeBackup.Spec.StorageRef.Name
	if objectStoreName == "" {
		return nil, "", &controller.BackupConfigError{
			Reason:  "StorageRefMissing",
			Message: fmt.Sprintf("source Backup %q has no storageRef.name", backupName),
		}
	}

	cnpgBackupName := backupName
	if ref := oeBackup.Status.OperatorBackupRef; ref != nil && ref.Name != "" {
		cnpgBackupName = ref.Name
	}

	cnpgBackup := &cnpgv1.Backup{}
	if err := c.Get(cnpgBackup, cnpgBackupName); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, "", controller.WaitFor(fmt.Sprintf(
				"CloudNativePG Backup %q not yet present", cnpgBackupName))
		}
		return nil, "", fmt.Errorf("get CloudNativePG Backup %q: %w", cnpgBackupName, err)
	}

	if cnpgBackup.Status.Phase != cnpgv1.BackupPhaseCompleted {
		return nil, "", controller.WaitFor(fmt.Sprintf(
			"CloudNativePG Backup %q is not completed (phase=%q)",
			cnpgBackupName, cnpgBackup.Status.Phase))
	}

	if cnpgBackup.Status.BackupID == "" {
		return nil, "", fmt.Errorf("CloudNativePG Backup %q has no Barman backup ID", cnpgBackup.Name)
	}

	return cnpgBackup, objectStoreName, nil
}
