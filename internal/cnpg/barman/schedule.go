package barman

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	corev1alpha1 "github.com/openeverest/openeverest/v2/api/core/v1alpha1"
	"github.com/openeverest/openeverest/v2/provider-runtime/controller"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/validation"
)

// CNPG copies the ScheduledBackup name into a label on every produced Backup,
// so it must fit a label value.
const maxScheduledBackupNameLength = validation.LabelValueMaxLength

const scheduledBackupNameHashLength = 8

// Descriptors accepted by both standard cron and CNPG's robfig/cron parser.
var cronDescriptors = map[string]bool{
	"@yearly":   true,
	"@annually": true,
	"@monthly":  true,
	"@weekly":   true,
	"@daily":    true,
	"@midnight": true,
	"@hourly":   true,
}

// ValidateSchedules checks the parts of the backup schedules that CNPG cannot
// validate for us before the ScheduledBackup is created.
func ValidateSchedules(backup *corev1alpha1.InstanceBackupSpec) error {
	if backup == nil {
		return nil
	}
	for _, storage := range backup.Storages {
		for _, schedule := range storage.Schedules {
			if errs := validation.IsDNS1123Label(schedule.Name); len(errs) > 0 {
				return fmt.Errorf("backup schedule name %q is invalid: %s", schedule.Name, strings.Join(errs, "; "))
			}
			if _, err := toCNPGSchedule(schedule.Cron); err != nil {
				return fmt.Errorf("backup schedule %q: %w", schedule.Name, err)
			}
			if _, err := decodeBackupParameters(schedule.Parameters); err != nil {
				return fmt.Errorf("backup schedule %q: %w", schedule.Name, err)
			}
		}
	}
	return nil
}

// SyncScheduledBackups reconciles one CNPG ScheduledBackup per enabled
// schedule and deletes the ones owned by the Instance that are no longer desired.
func SyncScheduledBackups(c *controller.Context) error {
	desired, err := buildScheduledBackups(c.Name(), c.Instance().Spec.Backup)
	if err != nil {
		return &controller.BackupConfigError{
			Reason:  "InvalidBackupSchedule",
			Message: err.Error(),
		}
	}

	keep := make(map[string]bool, len(desired))
	for i := range desired {
		sb := &desired[i]
		sb.ObjectMeta = c.ObjectMeta(sb.Name)
		if err := c.Apply(sb); err != nil {
			return fmt.Errorf("apply ScheduledBackup %q: %w", sb.Name, err)
		}
		keep[sb.Name] = true
	}

	existing := &cnpgv1.ScheduledBackupList{}
	if err := c.List(existing); err != nil {
		return fmt.Errorf("list ScheduledBackups: %w", err)
	}
	for i := range existing.Items {
		sb := &existing.Items[i]
		if keep[sb.Name] || !metav1.IsControlledBy(sb, c.Instance()) {
			continue
		}
		if err := c.Delete(sb); err != nil {
			return fmt.Errorf("delete ScheduledBackup %q: %w", sb.Name, err)
		}
	}
	return nil
}

func buildScheduledBackups(clusterName string, backup *corev1alpha1.InstanceBackupSpec) ([]cnpgv1.ScheduledBackup, error) {
	if backup == nil || !backup.Enabled {
		return nil, nil
	}

	var scheduledBackups []cnpgv1.ScheduledBackup
	for _, storage := range backup.Storages {
		for _, schedule := range storage.Schedules {
			if !schedule.Enabled {
				continue
			}
			cnpgSchedule, err := toCNPGSchedule(schedule.Cron)
			if err != nil {
				return nil, fmt.Errorf("backup schedule %q: %w", schedule.Name, err)
			}
			params, err := decodeBackupParameters(schedule.Parameters)
			if err != nil {
				return nil, fmt.Errorf("backup schedule %q: %w", schedule.Name, err)
			}

			scheduledBackups = append(scheduledBackups, cnpgv1.ScheduledBackup{
				ObjectMeta: metav1.ObjectMeta{Name: scheduledBackupName(clusterName, schedule.Name)},
				Spec: cnpgv1.ScheduledBackupSpec{
					Schedule: cnpgSchedule,
					Cluster:  cnpgv1.LocalObjectReference{Name: clusterName},
					// Produced Backups stay unowned so mirrored OpenEverest Backups can take controller ownership.
					BackupOwnerReference: "none",
					Method:               cnpgv1.BackupMethodPlugin,
					PluginConfiguration:  PluginConfiguration(storage.StorageRef.Name),
					Target:               cnpgv1.BackupTarget(params.Target),
				},
			})
		}
	}
	return scheduledBackups, nil
}

// ScheduleForScheduledBackup returns the Instance backup schedule whose CNPG
// ScheduledBackup is named sbName.
func ScheduleForScheduledBackup(
	instanceName string,
	backup *corev1alpha1.InstanceBackupSpec,
	sbName string,
) (corev1alpha1.InstanceBackupSchedule, bool) {
	if backup == nil {
		return corev1alpha1.InstanceBackupSchedule{}, false
	}
	for _, storage := range backup.Storages {
		for _, schedule := range storage.Schedules {
			if scheduledBackupName(instanceName, schedule.Name) == sbName {
				return schedule, true
			}
		}
	}
	return corev1alpha1.InstanceBackupSchedule{}, false
}

// toCNPGSchedule converts a standard 5-field cron expression into CNPG's
// format, which has a leading seconds field.
func toCNPGSchedule(cron string) (string, error) {
	cron = strings.TrimSpace(cron)
	if cronDescriptors[cron] {
		return cron, nil
	}
	fields := strings.Fields(cron)
	if len(fields) != 5 {
		return "", fmt.Errorf("cron %q must have 5 fields: minute hour day-of-month month day-of-week", cron)
	}
	return "0 " + strings.Join(fields, " "), nil
}

func scheduledBackupName(instanceName, scheduleName string) string {
	name := instanceName + "-" + scheduleName
	if len(name) <= maxScheduledBackupNameLength {
		return name
	}
	sum := sha256.Sum256([]byte(name))
	hash := hex.EncodeToString(sum[:])[:scheduledBackupNameHashLength]
	prefix := strings.TrimRight(name[:maxScheduledBackupNameLength-scheduledBackupNameHashLength-1], "-.")
	return prefix + "-" + hash
}
