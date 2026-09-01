package barman

import (
	"testing"

	corev1alpha1 "github.com/openeverest/openeverest/v2/api/core/v1alpha1"
)

func TestToCNPGSchedule(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in, want string
	}{
		{"0 2 * * *", "0 0 2 * * *"},
		{"0 0 2 * * *", "0 0 2 * * *"},
		{"*/15 * * * *", "0 */15 * * * *"},
	}
	for _, tc := range cases {
		if got := toCNPGSchedule(tc.in); got != tc.want {
			t.Errorf("toCNPGSchedule(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestRetentionPolicyFromSchedules(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name      string
		schedules []corev1alpha1.InstanceBackupSchedule
		want      string
	}{
		{
			name: "keep all",
			schedules: []corev1alpha1.InstanceBackupSchedule{
				{Name: "a", Cron: "0 2 * * *", RetentionCopies: 0},
			},
			want: "",
		},
		{
			name: "daily copies",
			schedules: []corev1alpha1.InstanceBackupSchedule{
				{Name: "daily", Cron: "0 2 * * *", RetentionCopies: 7},
			},
			want: "7d",
		},
		{
			name: "weekly copies",
			schedules: []corev1alpha1.InstanceBackupSchedule{
				{Name: "weekly", Cron: "0 3 * * 0", RetentionCopies: 4},
			},
			want: "28d",
		},
		{
			name: "max across schedules",
			schedules: []corev1alpha1.InstanceBackupSchedule{
				{Name: "daily", Cron: "0 2 * * *", RetentionCopies: 7},
				{Name: "weekly", Cron: "0 3 * * 0", RetentionCopies: 4},
			},
			want: "28d",
		},
		{
			name: "hourly",
			schedules: []corev1alpha1.InstanceBackupSchedule{
				{Name: "hourly", Cron: "0 */6 * * *", RetentionCopies: 8},
			},
			want: "2d", // 8 * (6/24) = 2
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := retentionPolicyFromSchedules(tc.schedules); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestSchedulePeriodDays(t *testing.T) {
	t.Parallel()
	cases := []struct {
		cron string
		want float64
	}{
		{"0 2 * * *", 1},
		{"0 2 * * 1", 7},
		{"0 2 1 * *", 30},
		{"0 */12 * * *", 0.5},
	}
	for _, tc := range cases {
		if got := schedulePeriodDays(tc.cron); got != tc.want {
			t.Errorf("schedulePeriodDays(%q) = %v, want %v", tc.cron, got, tc.want)
		}
	}
}
