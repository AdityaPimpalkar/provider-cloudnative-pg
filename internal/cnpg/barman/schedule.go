package barman

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/AlekSi/pointer"
	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	corev1alpha1 "github.com/openeverest/openeverest/v2/api/core/v1alpha1"
	"github.com/openeverest/openeverest/v2/provider-runtime/controller"
)

const (
	// ParentScheduledBackupLabel is stamped by CloudNativePG on Backup CRs
	// produced by a ScheduledBackup. Value is the ScheduledBackup name.
	ParentScheduledBackupLabel = "cnpg.io/scheduled-backup"
)

// SyncScheduledBackups translates Instance.spec.backup.storages[].schedules
// into CloudNativePG ScheduledBackup CRs (one per schedule) and deletes
// provider-managed ScheduledBackups that are no longer desired.
func SyncScheduledBackups(c *controller.Context) error {
	backupCfg := c.Instance().Spec.Backup
	if backupCfg != nil && backupCfg.Enabled {
		for _, st := range backupCfg.Storages {
			for _, s := range st.Schedules {
				sb, err := buildScheduledBackup(c, s, st)
				if err != nil {
					return err
				}
				if err := c.Apply(sb); err != nil {
					return fmt.Errorf("apply ScheduledBackup %q: %w", s.Name, err)
				}
			}
		}
	}
	return nil
}

func buildScheduledBackup(
	c *controller.Context,
	s corev1alpha1.InstanceBackupSchedule,
	st corev1alpha1.InstanceBackupStorage,
) (*cnpgv1.ScheduledBackup, error) {
	cfg, err := DecodeBackupParameters(s.Parameters)
	if err != nil {
		return nil, fmt.Errorf("schedule %q parameters: %w", s.Name, err)
	}

	sb := &cnpgv1.ScheduledBackup{
		ObjectMeta: c.ObjectMeta(s.Name),
		Spec: cnpgv1.ScheduledBackupSpec{
			Schedule:             toCNPGSchedule(s.Cron),
			Suspend:              pointer.To(!s.Enabled),
			BackupOwnerReference: "none",
			Cluster:              cnpgv1.LocalObjectReference{Name: c.Name()},
			Method:               cnpgv1.BackupMethodPlugin,
			PluginConfiguration:  PluginConfiguration(st.StorageRef.Name),
		},
	}
	if cfg.Target != "" {
		sb.Spec.Target = cnpgv1.BackupTarget(cfg.Target)
	}
	return sb, nil
}

// toCNPGSchedule converts an OpenEverest 5-field cron expression into the
// 6-field (seconds-prefixed) form CloudNativePG expects. Expressions that
// already have 6 fields are returned unchanged.
func toCNPGSchedule(cron string) string {
	fields := strings.Fields(strings.TrimSpace(cron))
	switch len(fields) {
	case 6:
		return strings.Join(fields, " ")
	case 5:
		return "0 " + strings.Join(fields, " ")
	default:
		return cron
	}
}

// retentionPolicyFromSchedules approximates OpenEverest per-schedule
// retentionCopies onto a single Barman recovery-window string (e.g. "7d").
// Zero/unset retentionCopies means "keep all" for that schedule. The most
// permissive (longest) window among schedules on the storage wins. Returns
// "" when no schedule requests retention.
func retentionPolicyFromSchedules(schedules []corev1alpha1.InstanceBackupSchedule) string {
	maxDays := 0
	for _, s := range schedules {
		if s.RetentionCopies <= 0 {
			continue
		}
		days := int(math.Ceil(float64(s.RetentionCopies) * schedulePeriodDays(s.Cron)))
		if days < 1 {
			days = 1
		}
		if days > maxDays {
			maxDays = days
		}
	}
	if maxDays == 0 {
		return ""
	}
	return fmt.Sprintf("%dd", maxDays)
}

// schedulePeriodDays estimates how many days elapse between two firings of a
// 5-field cron. Used only to approximate retentionCopies → Barman days.
func schedulePeriodDays(cron string) float64 {
	fields := strings.Fields(strings.TrimSpace(cron))
	if len(fields) == 6 {
		fields = fields[1:]
	}
	if len(fields) != 5 {
		return 1
	}
	minute, hour, dom, _, dow := fields[0], fields[1], fields[2], fields[3], fields[4]

	if dow != "*" && dow != "?" {
		return 7
	}
	if dom != "*" && dom != "?" {
		return 30
	}

	if step, ok := cronStep(hour); ok {
		return float64(step) / 24.0
	}
	if hour == "*" {
		if step, ok := cronStep(minute); ok {
			return float64(step) / (60.0 * 24.0)
		}
		// every minute → treat as sub-daily; one "copy" ≈ 1 minute
		if minute == "*" {
			return 1.0 / (60.0 * 24.0)
		}
		// specific minute every hour
		return 1.0 / 24.0
	}
	// specific hour(s), daily-ish
	return 1
}

func cronStep(field string) (int, bool) {
	if !strings.HasPrefix(field, "*/") {
		return 0, false
	}
	n, err := strconv.Atoi(field[2:])
	if err != nil || n <= 0 {
		return 0, false
	}
	return n, true
}
