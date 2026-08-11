package provider

import (
	"fmt"

	"github.com/adityapimpalkar/provider-cloudnative-pg/internal/cnpg/barman"
	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	backupv1alpha1 "github.com/openeverest/openeverest/v2/api/backup/v1alpha1"
	commonv1alpha1 "github.com/openeverest/openeverest/v2/api/common/v1alpha1"
	"github.com/openeverest/openeverest/v2/provider-runtime/controller"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// TODO: If your operator supports backups and restores, implement the backup interfaces
// to enable OpenEverest's backup management. All backup interfaces are optional.
//
// Compile-time interface checks.
var _ controller.BackupProvider = (*Provider)(nil)
var _ controller.BackupWatcher = (*Provider)(nil)
var _ controller.RestoreWatcher = (*Provider)(nil)

// SyncBackup creates or updates the operator's backup resource, sets a controller
// reference from the Backup CR to enable owner-based watches, and maps operator
// status to OpenEverest states.
func (p *Provider) SyncBackup(c *controller.Context, backup *backupv1alpha1.Backup) (controller.BackupExecutionStatus, error) {
	l := log.FromContext(c.Context())
	l.Info("Syncing backup", "name", backup.Name)

	cluster := &cnpgv1.Cluster{}
	if err := c.Get(cluster, c.Name()); err != nil {
		if apierrors.IsNotFound(err) {
			return controller.BackupExecutionStatus{
				State:   backupv1alpha1.BackupStatePending,
				Message: "Waiting for CloudnativePG cluster to exist",
			}, nil
		}
		return controller.BackupExecutionStatus{}, fmt.Errorf("get CloudnativePG: %w", err)
	}

	backupCfg, err := barman.DecodeBackupConfig(backup)
	if err != nil {
		return controller.BackupExecutionStatus{}, err
	}

	cnpgBackup := &cnpgv1.Backup{
		ObjectMeta: metav1.ObjectMeta{
			Name:      backup.Name,
			Namespace: backup.Namespace,
		},
	}
	if _, err := controllerutil.CreateOrUpdate(c.Context(), c.Client(), cnpgBackup, func() error {
		// CNPG BackupSpec is immutable once set only populate on create.
		if cnpgBackup.CreationTimestamp.IsZero() {
			cnpgBackup.Spec = cnpgv1.BackupSpec{
				Cluster:             cnpgv1.LocalObjectReference{Name: c.Name()},
				Method:              cnpgv1.BackupMethodPlugin,
				PluginConfiguration: barman.PluginConfiguration(backup.Spec.StorageRef.Name),
				Target:              cnpgv1.BackupTarget(backupCfg.Target),
			}
		}
		return controllerutil.SetControllerReference(backup, cnpgBackup, c.Client().Scheme())
	}); err != nil {
		return controller.BackupExecutionStatus{}, err
	}

	exec := controller.BackupExecutionStatus{
		OperatorBackupRef: &commonv1alpha1.TypedObjectRef{
			Group: cnpgv1.SchemeGroupVersion.Group,
			Kind:  barman.KindBackup,
			Name:  cnpgBackup.Name,
		},
		State: backupv1alpha1.BackupStatePending,
	}

	switch cnpgBackup.Status.Phase {
	case cnpgv1.BackupPhaseCompleted:
		exec.State = backupv1alpha1.BackupStateSucceeded
		exec.CompletedAt = cnpgBackup.Status.StoppedAt
		exec.StartedAt = cnpgBackup.Status.StartedAt
	case cnpgv1.BackupPhaseRunning:
		exec.State = backupv1alpha1.BackupStateRunning
	case cnpgv1.BackupPhaseStarted, cnpgv1.BackupPhasePending:
		exec.State = backupv1alpha1.BackupStatePending
	case cnpgv1.BackupPhaseFailed, cnpgv1.BackupPhaseWalArchivingFailing:
		exec.State = backupv1alpha1.BackupStateFailed
		exec.Message = cnpgBackup.Status.Error
	default:
		exec.State = backupv1alpha1.BackupStatePending
	}

	return exec, nil
}

// SyncRestore resolves the source Backup CR, creates or updates the operator's
// restore resource with a controller reference, and maps operator status to
// OpenEverest states.
func (p *Provider) SyncRestore(c *controller.Context, restore *backupv1alpha1.Restore) (controller.RestoreExecutionStatus, error) {
	l := log.FromContext(c.Context())
	l.Info("Syncing restore", "name", restore.Name)

	cluster := &cnpgv1.Cluster{}
	if err := c.Get(cluster, c.Name()); err != nil {
		if apierrors.IsNotFound(err) {
			return controller.RestoreExecutionStatus{
				State:   backupv1alpha1.RestoreStatePending,
				Message: "Waiting for recovery Cluster to be created",
			}, nil
		}
		return controller.RestoreExecutionStatus{}, err
	}

	exec := controller.RestoreExecutionStatus{
		State: backupv1alpha1.RestoreStateRunning,
		OperatorRestoreRef: &commonv1alpha1.TypedObjectRef{
			Group: cnpgv1.SchemeGroupVersion.Group,
			Kind:  "Cluster",
			Name:  cluster.Name,
		},
		StartedAt: &cluster.CreationTimestamp,
		Message:   cluster.Status.PhaseReason,
	}

	ready := meta.FindStatusCondition(
		cluster.Status.Conditions,
		string(cnpgv1.ConditionClusterReady),
	)

	if ready != nil &&
		ready.Status == metav1.ConditionTrue &&
		cluster.Status.CurrentPrimary != "" &&
		cluster.Status.ReadyInstances == cluster.Status.Instances {
		exec.State = backupv1alpha1.RestoreStateSucceeded
		exec.CompletedAt = &ready.LastTransitionTime
		exec.Message = "CloudNativePG recovery completed"
		return exec, nil
	}

	switch cluster.Status.Phase {
	case cnpgv1.PhaseUnrecoverable,
		cnpgv1.PhaseFailurePlugin,
		cnpgv1.PhaseCannotCreateClusterObjects,
		cnpgv1.PhaseImageCatalogError:
		exec.State = backupv1alpha1.RestoreStateFailed
		exec.Message = cluster.Status.Phase
		if cluster.Status.PhaseReason != "" {
			exec.Message += ": " + cluster.Status.PhaseReason
		}
	}
	return exec, nil
}

// CleanupBackup deletes the operator backup resource. For DeletionPolicy: Retain,
// remove storage-protection finalizers before deletion to preserve backup data.
// Return true only when fully deleted, false to requeue.
func (p *Provider) CleanupBackup(c *controller.Context, backup *backupv1alpha1.Backup) (bool, error) {
	l := log.FromContext(c.Context())
	l.Info("Cleaning up backup", "name", backup.Name)

	cnpgBackup := &cnpgv1.Backup{}
	if err := c.Get(cnpgBackup, backup.Name); err != nil {
		if apierrors.IsNotFound(err) {
			return true, nil
		}
		return false, fmt.Errorf("get CloudNativePG backup: %w", err)
	}

	if cnpgBackup.DeletionTimestamp.IsZero() {
		if err := c.Delete(cnpgBackup); err != nil {
			return false, fmt.Errorf("delete CloudNativePG backup: %w", err)
		}
	}
	return false, nil
}

// CleanupRestore deletes the operator restore resource. Return true when fully
// deleted, false to requeue.
func (p *Provider) CleanupRestore(c *controller.Context, restore *backupv1alpha1.Restore) (bool, error) {
	l := log.FromContext(c.Context())
	l.Info("Cleaning up restore", "name", restore.Name)

	// TODO: Implement restore cleanup logic.
	// Typical pattern:
	//   1. Get the operator restore CR
	//   2. Delete the operator restore CR
	//   3. Return true when fully deleted, false to requeue
	//
	// Example:
	//   or := &operatorv1.MyDatabaseRestore{}
	//   err := c.Get(or, restore.Name)
	//   if apierrors.IsNotFound(err) {
	//       return true, nil
	//   }
	//   if err != nil {
	//       return false, err
	//   }
	//   if or.DeletionTimestamp.IsZero() {
	//       return false, c.Delete(or)
	//   }
	//   return false, nil

	return true, nil
}

// BackupWatches implements controller.BackupWatcher. Register watches so operator
// backup status changes trigger reconciliation. Use WatchOwned for resources with
// controller references set by SyncBackup.
func (p *Provider) BackupWatches() []controller.WatchConfig {
	return []controller.WatchConfig{
		controller.WatchOwned(&cnpgv1.Backup{}),
	}
}

// RestoreWatches implements controller.RestoreWatcher. Register watches so operator
// restore status changes trigger reconciliation. Use WatchOwned for resources with
// controller references set by SyncRestore.
func (p *Provider) RestoreWatches() []controller.WatchConfig {
	// TODO: Register watches for your operator restore resource.
	// Example:
	//   return []controller.WatchConfig{
	//       controller.WatchOwned(&operatorv1.MyDatabaseRestore{}),
	//   }
	return []controller.WatchConfig{}
}
