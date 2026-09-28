package provider

import (
	"testing"

	"github.com/AlekSi/pointer"
	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	backupv1alpha1 "github.com/openeverest/openeverest/v2/api/backup/v1alpha1"
	commonv1alpha1 "github.com/openeverest/openeverest/v2/api/common/v1alpha1"
	corev1alpha1 "github.com/openeverest/openeverest/v2/api/core/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/adityapimpalkar/provider-cloudnative-pg/internal/cnpg/barman"
)

func instanceWithStorage(enabled bool, storages ...string) *corev1alpha1.Instance {
	backup := &corev1alpha1.InstanceBackupSpec{Enabled: enabled}
	for _, name := range storages {
		backup.Storages = append(backup.Storages, corev1alpha1.InstanceBackupStorage{
			StorageRef: commonv1alpha1.ObjectRef{Name: name},
		})
	}
	return &corev1alpha1.Instance{
		ObjectMeta: metav1.ObjectMeta{Name: "pg"},
		Spec:       corev1alpha1.InstanceSpec{Backup: backup},
	}
}

func clusterArchivingTo(storage string, enabled bool) *cnpgv1.Cluster {
	return &cnpgv1.Cluster{Spec: cnpgv1.ClusterSpec{Plugins: []cnpgv1.PluginConfiguration{{
		Name:       barman.PluginName,
		Enabled:    pointer.To(enabled),
		Parameters: map[string]string{barman.PluginParameterObjectStore: storage},
	}}}}
}

func TestBackupStorageBlocker(t *testing.T) {
	tests := []struct {
		name      string
		instance  *corev1alpha1.Instance
		cluster   *cnpgv1.Cluster
		storage   string
		wantState backupv1alpha1.BackupState
	}{
		{
			name:     "configured storage that the cluster archives to",
			instance: instanceWithStorage(true, "s3"),
			cluster:  clusterArchivingTo("s3", true),
			storage:  "s3",
		},
		{
			name:      "storage not configured on the instance",
			instance:  instanceWithStorage(true, "s3"),
			cluster:   clusterArchivingTo("s3", true),
			storage:   "other",
			wantState: backupv1alpha1.BackupStateFailed,
		},
		{
			name:      "backups disabled on the instance",
			instance:  instanceWithStorage(false, "s3"),
			cluster:   clusterArchivingTo("s3", true),
			storage:   "s3",
			wantState: backupv1alpha1.BackupStateFailed,
		},
		{
			name:      "cluster not yet archiving to the configured storage",
			instance:  instanceWithStorage(true, "new"),
			cluster:   clusterArchivingTo("old", true),
			storage:   "new",
			wantState: backupv1alpha1.BackupStatePending,
		},
		{
			name:      "archiver plugin disabled on the cluster",
			instance:  instanceWithStorage(true, "s3"),
			cluster:   clusterArchivingTo("s3", false),
			storage:   "s3",
			wantState: backupv1alpha1.BackupStatePending,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := backupStorageBlocker(tt.instance, tt.cluster, tt.storage)
			if tt.wantState == "" {
				if got != nil {
					t.Fatalf("expected backup to proceed, got %+v", *got)
				}
				return
			}
			if got == nil {
				t.Fatalf("expected state %q, backup was allowed to proceed", tt.wantState)
			}
			if got.State != tt.wantState {
				t.Fatalf("state = %q, want %q (message: %s)", got.State, tt.wantState, got.Message)
			}
		})
	}
}
