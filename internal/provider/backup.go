package provider

import (
	"context"
	"fmt"
	"strings"

	"github.com/adityapimpalkar/provider-cloudnative-pg/internal/cnpg/barman"
	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	backupv1alpha1 "github.com/openeverest/openeverest/v2/api/backup/v1alpha1"
	commonv1alpha1 "github.com/openeverest/openeverest/v2/api/common/v1alpha1"
	corev1alpha1 "github.com/openeverest/openeverest/v2/api/core/v1alpha1"
	"github.com/openeverest/openeverest/v2/provider-runtime/controller"
	batchv1 "k8s.io/api/batch/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

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

	exists, err := c.Exists(&cnpgv1.Backup{}, backup.Name)
	if err != nil {
		return controller.BackupExecutionStatus{}, fmt.Errorf("get CloudNativePG backup: %w", err)
	}
	if !exists {
		if backup.Status.OperatorBackupRef != nil {
			return controller.BackupExecutionStatus{
				State:   backupv1alpha1.BackupStateFailed,
				Message: "CloudNativePG backup is no longer available; it may have expired under Barman retention",
			}, nil
		}
		if err := barman.EnsureObjectStoreAvailable(c); err != nil {
			if bce := controller.AsBackupConfigError(err); bce != nil {
				return controller.BackupExecutionStatus{
					State:   backupv1alpha1.BackupStateFailed,
					Message: bce.Message,
				}, nil
			}
			return controller.BackupExecutionStatus{}, err
		}
		if blocked := backupStorageBlocker(c.Instance(), cluster, backup.Spec.StorageRef.Name); blocked != nil {
			return *blocked, nil
		}
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
		exec.Message = backupDetailsMessage(cnpgBackup.Status)
	case cnpgv1.BackupPhaseRunning:
		exec.State = backupv1alpha1.BackupStateRunning
		exec.StartedAt = cnpgBackup.Status.StartedAt
		exec.CompletedAt = cnpgBackup.Status.StoppedAt
	case cnpgv1.BackupPhaseStarted, cnpgv1.BackupPhasePending:
		exec.State = backupv1alpha1.BackupStatePending
		exec.StartedAt = cnpgBackup.Status.StartedAt
		exec.CompletedAt = cnpgBackup.Status.StoppedAt
	case cnpgv1.BackupPhaseFailed, cnpgv1.BackupPhaseWalArchivingFailing:
		exec.State = backupv1alpha1.BackupStateFailed
		exec.CompletedAt = cnpgBackup.Status.StoppedAt
		exec.StartedAt = cnpgBackup.Status.StartedAt
		if cnpgBackup.Status.Error != "" {
			exec.Message = cnpgBackup.Status.Error
		} else {
			exec.Message = backupDetailsMessage(cnpgBackup.Status)
		}
	default:
		exec.State = backupv1alpha1.BackupStatePending
	}

	return exec, nil
}

// backupDetailsMessage surfaces Barman ID and LSN/WAL range for observability.
// OpenEverest has no dedicated fields for these, so they go in Message.
func backupDetailsMessage(status cnpgv1.BackupStatus) string {
	var parts []string
	if status.BackupID != "" {
		parts = append(parts, "backupId="+status.BackupID)
	}
	if status.BeginLSN != "" || status.EndLSN != "" {
		parts = append(parts, "lsn="+status.BeginLSN+".."+status.EndLSN)
	}
	if status.BeginWal != "" || status.EndWal != "" {
		parts = append(parts, "wal="+status.BeginWal+".."+status.EndWal)
	}
	return strings.Join(parts, " ")
}

// backupStorageBlocker returns the status to report instead of creating the CNPG
// Backup, or nil when the backup can start.
func backupStorageBlocker(instance *corev1alpha1.Instance, cluster *cnpgv1.Cluster, storageName string) *controller.BackupExecutionStatus {
	if !instanceHasBackupStorage(instance, storageName) {
		return &controller.BackupExecutionStatus{
			State: backupv1alpha1.BackupStateFailed,
			Message: fmt.Sprintf("storage %q is not configured in spec.backup.storages of Instance %q",
				storageName, instance.Name),
		}
	}
	// The plugin writes base backups to the Cluster's WAL archive whatever storage the Backup names.
	if barman.ArchiveObjectStoreName(cluster) != storageName {
		return &controller.BackupExecutionStatus{
			State:   backupv1alpha1.BackupStatePending,
			Message: fmt.Sprintf("waiting for CloudNativePG cluster to archive to storage %q", storageName),
		}
	}
	return nil
}

func instanceHasBackupStorage(instance *corev1alpha1.Instance, storageName string) bool {
	backup := instance.Spec.Backup
	if backup == nil || !backup.Enabled {
		return false
	}
	for _, storage := range backup.Storages {
		if storage.StorageRef.Name == storageName {
			return true
		}
	}
	return false
}

// SyncRestore tracks CloudNativePG recovery bootstrap for Instance.spec.dataSource
// seeding. In-place Restore CRs against an existing Instance are unsupported:
// CNPG recovery only bootstraps a new Cluster (see CNPG recovery docs).
func (p *Provider) SyncRestore(c *controller.Context, restore *backupv1alpha1.Restore) (controller.RestoreExecutionStatus, error) {
	l := log.FromContext(c.Context())
	l.Info("Syncing restore", "name", restore.Name)

	if !isSeedingRestore(c, restore) {
		return controller.RestoreExecutionStatus{
			State: backupv1alpha1.RestoreStateFailed,
			Message: "Unsupported: CloudNativePG does not support in-place restore; " +
				"seed a new Instance from a Backup via spec.dataSource instead",
		}, nil
	}

	return seedingRestoreStatus(c, restore)
}

// isSeedingRestore reports whether the Restore is the one the runtime creates
// to seed the Instance from its spec.dataSource.
func isSeedingRestore(c *controller.Context, restore *backupv1alpha1.Restore) bool {
	return c.Instance().Spec.DataSource != nil &&
		restore.Name == c.Name()+controller.DataSourceRestoreNameSuffix
}

// seedingRestoreStatus maps Cluster readiness onto the datasource Restore CR.
// Sync() configures bootstrap.recovery; there is no separate CNPG Restore object.
func seedingRestoreStatus(c *controller.Context, _ *backupv1alpha1.Restore) (controller.RestoreExecutionStatus, error) {
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

// CleanupBackup removes Barman backup data first for DeletionPolicy: Delete, then
// deletes the CNPG Backup resource. Retain only removes that resource.
// Return true only when cleanup is complete, false to requeue.
func (p *Provider) CleanupBackup(c *controller.Context, backup *backupv1alpha1.Backup) (bool, error) {
	l := log.FromContext(c.Context())
	l.Info("Cleaning up backup", "name", backup.Name)

	cnpgBackup := &cnpgv1.Backup{}
	if err := c.Get(cnpgBackup, backup.Name); err != nil {
		if apierrors.IsNotFound(err) {
			if backup.Spec.DeletionPolicy == backupv1alpha1.BackupDeletionPolicyRetain {
				return true, nil
			}
			job := &batchv1.Job{}
			if jobErr := c.Get(job, backupDeletionJobName(backup.Name)); jobErr != nil {
				if !apierrors.IsNotFound(jobErr) {
					return false, fmt.Errorf("get backup data deletion Job: %w", jobErr)
				}
				// Without the CNPG Backup there is no Barman backup ID to delete:
				// it never started, or native retention already removed it.
				l.Info("CloudNativePG backup is absent; no Barman backup to delete",
					"name", backup.Name, "observed", backup.Status.OperatorBackupRef != nil)
				return true, nil
			}
			return deletionJobDone(job)
		}
		return false, fmt.Errorf("get CloudNativePG backup: %w", err)
	}

	if backup.Spec.DeletionPolicy != backupv1alpha1.BackupDeletionPolicyRetain {
		done, err := p.deleteBackupData(c, backup, cnpgBackup)
		if err != nil {
			return false, err
		}
		if !done {
			return false, nil
		}
	}

	if cnpgBackup.DeletionTimestamp.IsZero() {
		if err := c.Delete(cnpgBackup); err != nil {
			return false, fmt.Errorf("delete CloudNativePG backup: %w", err)
		}
	}
	return false, nil
}

// CleanupRestore has nothing to delete: CNPG has no Restore CR, and seeding
// recovery lives on the Instance-owned Cluster. Return true so the Restore
// finalizer can be removed.
func (p *Provider) CleanupRestore(c *controller.Context, restore *backupv1alpha1.Restore) (bool, error) {
	l := log.FromContext(c.Context())
	l.Info("Cleaning up restore", "name", restore.Name)
	return true, nil
}

// BackupWatches implements controller.BackupWatcher. Register watches so operator
// backup status changes trigger reconciliation. Use WatchOwned for resources with
// controller references set by SyncBackup.
func (p *Provider) BackupWatches() []controller.WatchConfig {
	return []controller.WatchConfig{
		controller.WatchOwned(&cnpgv1.Backup{}),
		controller.WatchOwned(&batchv1.Job{}),
	}
}

// RestoreWatches maps Cluster status changes onto the datasource seeding Restore
// CR ({instance}-datasource). In-place restores create no watched resources.
func (p *Provider) RestoreWatches() []controller.WatchConfig {
	return []controller.WatchConfig{
		controller.WatchExternal(
			&cnpgv1.Cluster{},
			handler.EnqueueRequestsFromMapFunc(func(_ context.Context, obj client.Object) []reconcile.Request {
				return []reconcile.Request{{
					NamespacedName: types.NamespacedName{
						Namespace: obj.GetNamespace(),
						Name:      obj.GetName() + controller.DataSourceRestoreNameSuffix,
					},
				}}
			}),
			controller.ResourceVersionChangedPredicate,
		),
	}
}
