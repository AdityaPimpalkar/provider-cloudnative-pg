package provider

import (
	"context"
	"testing"

	"github.com/AlekSi/pointer"
	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	backupv1alpha1 "github.com/openeverest/openeverest/v2/api/backup/v1alpha1"
	commonv1alpha1 "github.com/openeverest/openeverest/v2/api/common/v1alpha1"
	corev1alpha1 "github.com/openeverest/openeverest/v2/api/core/v1alpha1"
	"github.com/openeverest/openeverest/v2/provider-runtime/controller"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/adityapimpalkar/provider-cloudnative-pg/internal/cnpg/barman"
)

const testNamespace = "ns"

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

func TestSyncBackupStorageChecks(t *testing.T) {
	scheme := runtime.NewScheme()
	for _, add := range []func(*runtime.Scheme) error{
		cnpgv1.AddToScheme, corev1alpha1.AddToScheme, backupv1alpha1.AddToScheme,
	} {
		if err := add(scheme); err != nil {
			t.Fatal(err)
		}
	}

	tests := []struct {
		name             string
		instanceStorage  string
		clusterArchiveTo string
		backupStorage    string
		cnpgBackupExists bool
		wantState        backupv1alpha1.BackupState
		wantCNPGBackup   bool
	}{
		{
			name:             "storage not configured on the instance",
			instanceStorage:  "s3",
			clusterArchiveTo: "s3",
			backupStorage:    "other",
			wantState:        backupv1alpha1.BackupStateFailed,
		},
		{
			name:             "cluster not yet archiving to the storage",
			instanceStorage:  "new",
			clusterArchiveTo: "old",
			backupStorage:    "new",
			wantState:        backupv1alpha1.BackupStatePending,
		},
		{
			name:             "storage matches",
			instanceStorage:  "s3",
			clusterArchiveTo: "s3",
			backupStorage:    "s3",
			wantState:        backupv1alpha1.BackupStatePending,
			wantCNPGBackup:   true,
		},
		{
			name:             "existing backup is not re-checked after the storage changes",
			instanceStorage:  "new",
			clusterArchiveTo: "new",
			backupStorage:    "old",
			cnpgBackupExists: true,
			wantState:        backupv1alpha1.BackupStateSucceeded,
			wantCNPGBackup:   true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			instance := instanceWithStorage(true, tt.instanceStorage)
			instance.Namespace = testNamespace

			cluster := clusterArchivingTo(tt.clusterArchiveTo, true)
			cluster.Name = instance.Name
			cluster.Namespace = testNamespace

			backup := &backupv1alpha1.Backup{
				ObjectMeta: metav1.ObjectMeta{Name: "backup-1", Namespace: testNamespace},
				Spec: backupv1alpha1.BackupSpec{
					StorageRef: commonv1alpha1.ObjectRef{Name: tt.backupStorage},
				},
			}

			objects := []client.Object{instance, cluster, backup}
			if tt.cnpgBackupExists {
				objects = append(objects, &cnpgv1.Backup{
					ObjectMeta: metav1.ObjectMeta{Name: backup.Name, Namespace: testNamespace},
					Status:     cnpgv1.BackupStatus{Phase: cnpgv1.BackupPhaseCompleted},
				})
			}
			cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).Build()
			c := controller.NewContext(context.Background(), cl, instance, "provider-cloudnative-pg")

			got, err := (&Provider{}).SyncBackup(c, backup)
			if err != nil {
				t.Fatalf("SyncBackup: %v", err)
			}
			if got.State != tt.wantState {
				t.Errorf("state = %q, want %q (message: %s)", got.State, tt.wantState, got.Message)
			}

			cnpgBackup := &cnpgv1.Backup{}
			err = cl.Get(context.Background(), client.ObjectKey{Namespace: testNamespace, Name: backup.Name}, cnpgBackup)
			if gotCNPGBackup := err == nil; gotCNPGBackup != tt.wantCNPGBackup {
				t.Fatalf("CNPG Backup exists = %t, want %t (get err: %v)", gotCNPGBackup, tt.wantCNPGBackup, err)
			}
			if tt.wantCNPGBackup && !tt.cnpgBackupExists {
				store := cnpgBackup.Spec.PluginConfiguration.Parameters[barman.PluginParameterObjectStore]
				if store != tt.backupStorage {
					t.Errorf("CNPG Backup object store = %q, want %q", store, tt.backupStorage)
				}
			}
		})
	}
}
