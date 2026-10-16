package provider

import (
	"context"
	"strings"
	"testing"

	"github.com/AlekSi/pointer"
	barmanapi "github.com/cloudnative-pg/barman-cloud/pkg/api"
	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	machineryapi "github.com/cloudnative-pg/machinery/pkg/api"
	barmancloudv1 "github.com/cloudnative-pg/plugin-barman-cloud/api/v1"
	backupv1alpha1 "github.com/openeverest/openeverest/v2/api/backup/v1alpha1"
	commonv1alpha1 "github.com/openeverest/openeverest/v2/api/common/v1alpha1"
	corev1alpha1 "github.com/openeverest/openeverest/v2/api/core/v1alpha1"
	"github.com/openeverest/openeverest/v2/provider-runtime/controller"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/adityapimpalkar/provider-cloudnative-pg/internal/cnpg/barman"
)

const testNamespace = "ns"

func cleanupScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	scheme := runtime.NewScheme()
	barmancloudv1.AddKnownTypes(scheme)
	for _, add := range []func(*runtime.Scheme) error{
		cnpgv1.AddToScheme, corev1alpha1.AddToScheme, backupv1alpha1.AddToScheme, batchv1.AddToScheme,
	} {
		if err := add(scheme); err != nil {
			t.Fatal(err)
		}
	}
	return scheme
}

func TestCleanupBackupWithoutCNPGBackup(t *testing.T) {
	jobWith := func(condition batchv1.JobConditionType) *batchv1.Job {
		job := &batchv1.Job{ObjectMeta: metav1.ObjectMeta{Name: backupDeletionJobName("gone"), Namespace: testNamespace}}
		if condition != "" {
			job.Status.Conditions = []batchv1.JobCondition{{Type: condition, Status: corev1.ConditionTrue}}
		}
		return job
	}
	tests := []struct {
		name     string
		observed bool
		job      *batchv1.Job
		wantDone bool
		wantErr  string
	}{
		{name: "never started", wantDone: true},
		{name: "expired under native retention", observed: true, wantDone: true},
		{name: "deletion job running", observed: true, job: jobWith("")},
		{name: "deletion job complete", observed: true, job: jobWith(batchv1.JobComplete), wantDone: true},
		{name: "deletion job failed", observed: true, job: jobWith(batchv1.JobFailed), wantErr: "delete the Job to retry"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			instance := instanceWithStorage(true, "s3")
			instance.Namespace = testNamespace
			backup := &backupv1alpha1.Backup{
				ObjectMeta: metav1.ObjectMeta{Name: "gone", Namespace: testNamespace},
				Spec:       backupv1alpha1.BackupSpec{DeletionPolicy: backupv1alpha1.BackupDeletionPolicyDelete},
			}
			if tt.observed {
				backup.Status.OperatorBackupRef = &commonv1alpha1.TypedObjectRef{
					Group: cnpgv1.SchemeGroupVersion.Group, Kind: "Backup", Name: backup.Name,
				}
			}
			objects := []client.Object{instance, backup}
			if tt.job != nil {
				objects = append(objects, tt.job)
			}
			cl := fake.NewClientBuilder().WithScheme(cleanupScheme(t)).WithObjects(objects...).Build()
			c := controller.NewContext(context.Background(), cl, instance, "provider-cloudnative-pg")
			done, err := (&Provider{}).CleanupBackup(c, backup)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("error = %v, want %q", err, tt.wantErr)
				}
				return
			}
			if err != nil || done != tt.wantDone {
				t.Fatalf("done=%t err=%v, want done=%t", done, err, tt.wantDone)
			}
		})
	}
}

func TestCleanupBackupByCNPGPhase(t *testing.T) {
	tests := []struct {
		name         string
		phase        cnpgv1.BackupPhase
		backupID     string
		wantJob      bool
		wantCNPGGone bool
	}{
		{name: "not yet picked up", wantCNPGGone: true},
		{name: "pending", phase: cnpgv1.BackupPhasePending},
		{name: "running", phase: cnpgv1.BackupPhaseRunning, backupID: "20260101T000000"},
		{name: "finalizing", phase: cnpgv1.BackupPhaseFinalizing, backupID: "20260101T000000"},
		{name: "failed before an ID was assigned", phase: cnpgv1.BackupPhaseFailed, wantCNPGGone: true},
		{name: "invalid definition", phase: cnpgv1.BackupPhaseDefinitionInvalid, wantCNPGGone: true},
		{name: "failed with an ID", phase: cnpgv1.BackupPhaseFailed, backupID: "20260101T000000", wantJob: true},
		{name: "completed", phase: cnpgv1.BackupPhaseCompleted, backupID: "20260101T000000", wantJob: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			instance := instanceWithStorage(true, "s3")
			instance.Namespace = testNamespace
			backup := &backupv1alpha1.Backup{
				ObjectMeta: metav1.ObjectMeta{Name: "b", Namespace: testNamespace, UID: "backup-uid"},
				Spec: backupv1alpha1.BackupSpec{
					StorageRef:     commonv1alpha1.ObjectRef{Name: "s3"},
					DeletionPolicy: backupv1alpha1.BackupDeletionPolicyDelete,
				},
			}
			cnpgBackup := &cnpgv1.Backup{
				ObjectMeta: metav1.ObjectMeta{Name: "b", Namespace: testNamespace},
				Spec:       cnpgv1.BackupSpec{Cluster: cnpgv1.LocalObjectReference{Name: "pg"}},
				Status: cnpgv1.BackupStatus{
					Phase: tt.phase, BackupID: tt.backupID, DestinationPath: "s3://backups/", ServerName: "pg",
				},
			}
			objectStore := &barmancloudv1.ObjectStore{
				ObjectMeta: metav1.ObjectMeta{Name: "s3", Namespace: testNamespace},
				Spec: barmancloudv1.ObjectStoreSpec{Configuration: barmanapi.BarmanObjectStoreConfiguration{
					DestinationPath: "s3://backups/",
					BarmanCredentials: barmanapi.BarmanCredentials{AWS: &barmanapi.S3Credentials{
						AccessKeyIDReference:     &machineryapi.SecretKeySelector{LocalObjectReference: machineryapi.LocalObjectReference{Name: "creds"}, Key: "AWS_ACCESS_KEY_ID"},
						SecretAccessKeyReference: &machineryapi.SecretKeySelector{LocalObjectReference: machineryapi.LocalObjectReference{Name: "creds"}, Key: "AWS_SECRET_ACCESS_KEY"},
					}},
				}},
			}
			storage := &backupv1alpha1.BackupStorage{
				ObjectMeta: metav1.ObjectMeta{Name: "s3", Namespace: testNamespace},
				Spec:       backupv1alpha1.BackupStorageSpec{S3: &backupv1alpha1.BackupStorageS3Spec{Bucket: "backups"}},
			}
			cl := fake.NewClientBuilder().WithScheme(cleanupScheme(t)).
				WithObjects(instance, backup, cnpgBackup, objectStore, storage).Build()
			c := controller.NewContext(context.Background(), cl, instance, "provider-cloudnative-pg")
			done, err := (&Provider{}).CleanupBackup(c, backup)
			if err != nil || done {
				t.Fatalf("done=%t err=%v; cleanup must requeue until the CNPG Backup is gone", done, err)
			}
			jobErr := cl.Get(context.Background(), client.ObjectKey{Namespace: testNamespace, Name: backupDeletionJobName("b")}, &batchv1.Job{})
			if gotJob := jobErr == nil; gotJob != tt.wantJob {
				t.Fatalf("deletion Job created = %t, want %t (%v)", gotJob, tt.wantJob, jobErr)
			}
			cnpgErr := cl.Get(context.Background(), client.ObjectKey{Namespace: testNamespace, Name: "b"}, &cnpgv1.Backup{})
			if gone := apierrors.IsNotFound(cnpgErr); gone != tt.wantCNPGGone {
				t.Fatalf("CNPG Backup deleted = %t, want %t (%v)", gone, tt.wantCNPGGone, cnpgErr)
			}
		})
	}
}

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
		name               string
		instanceStorage    string
		clusterArchiveTo   string
		backupStorage      string
		cnpgBackupExists   bool
		previouslyObserved bool
		wantState          backupv1alpha1.BackupState
		wantCNPGBackup     bool
	}{
		{
			name:               "native retention removed an observed operator backup",
			instanceStorage:    "s3",
			clusterArchiveTo:   "s3",
			backupStorage:      "s3",
			previouslyObserved: true,
			wantState:          backupv1alpha1.BackupStateFailed,
		},
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
			if tt.previouslyObserved {
				backup.Status.OperatorBackupRef = &commonv1alpha1.TypedObjectRef{
					Group: cnpgv1.SchemeGroupVersion.Group, Kind: "Backup", Name: backup.Name,
				}
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
