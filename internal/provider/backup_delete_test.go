package provider

import (
	"strings"
	"testing"

	"github.com/adityapimpalkar/provider-cloudnative-pg/internal/cnpg/barman"
	barmanapi "github.com/cloudnative-pg/barman-cloud/pkg/api"
	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	machineryapi "github.com/cloudnative-pg/machinery/pkg/api"
	barmancloudv1 "github.com/cloudnative-pg/plugin-barman-cloud/api/v1"
	backupv1alpha1 "github.com/openeverest/openeverest/v2/api/backup/v1alpha1"
	commonv1alpha1 "github.com/openeverest/openeverest/v2/api/common/v1alpha1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestBuildBackupDeletionJob(t *testing.T) {
	backup := &backupv1alpha1.Backup{
		ObjectMeta: metav1.ObjectMeta{Name: "daily-backup", Namespace: testNamespace},
		Spec: backupv1alpha1.BackupSpec{
			StorageRef: commonv1alpha1.ObjectRef{Name: "minio"},
		},
	}
	cnpgBackup := &cnpgv1.Backup{
		ObjectMeta: metav1.ObjectMeta{Name: backup.Name, Namespace: backup.Namespace},
		Spec:       cnpgv1.BackupSpec{Cluster: cnpgv1.LocalObjectReference{Name: "pg"}},
		Status: cnpgv1.BackupStatus{
			BackupID:        "20260101T120000",
			DestinationPath: "s3://backups/",
			ServerName:      "pg",
		},
	}
	objectStore := &barmancloudv1.ObjectStore{
		ObjectMeta: metav1.ObjectMeta{Name: "minio", Namespace: backup.Namespace},
		Spec: barmancloudv1.ObjectStoreSpec{
			Configuration: barmanapi.BarmanObjectStoreConfiguration{
				EndpointURL:     "http://minio:9000",
				DestinationPath: "s3://backups/",
				BarmanCredentials: barmanapi.BarmanCredentials{AWS: &barmanapi.S3Credentials{
					AccessKeyIDReference: &machineryapi.SecretKeySelector{
						LocalObjectReference: machineryapi.LocalObjectReference{Name: "minio-creds"},
						Key:                  "AWS_ACCESS_KEY_ID",
					},
					SecretAccessKeyReference: &machineryapi.SecretKeySelector{
						LocalObjectReference: machineryapi.LocalObjectReference{Name: "minio-creds"},
						Key:                  "AWS_SECRET_ACCESS_KEY",
					},
				}},
			},
		},
	}

	job, err := buildBackupDeletionJob(
		backup,
		cnpgBackup,
		objectStore,
		&backupv1alpha1.BackupStorageS3Spec{Region: "us-east-1"},
	)
	if err != nil {
		t.Fatalf("buildBackupDeletionJob: %v", err)
	}
	container := job.Spec.Template.Spec.Containers[0]
	if container.Image != barman.BackupDeletionImage {
		t.Errorf("image = %q", container.Image)
	}
	for _, arg := range []string{
		"--endpoint-url", "http://minio:9000",
		"--cloud-provider", "aws-s3",
		"--backup-id", "20260101T120000",
		"s3://backups/", "pg",
	} {
		if !containsString(container.Args, arg) {
			t.Errorf("arguments %v do not contain %q", container.Args, arg)
		}
	}
	if got := container.Env[0].ValueFrom.SecretKeyRef.Name; got != "minio-creds" {
		t.Errorf("access key Secret = %q", got)
	}
	if got := container.Env[2].Value; got != "us-east-1" {
		t.Errorf("AWS region = %q", got)
	}
	if job.Spec.BackoffLimit == nil || *job.Spec.BackoffLimit != 0 {
		t.Errorf("backoffLimit = %v, want 0", job.Spec.BackoffLimit)
	}
	pod := job.Spec.Template.Spec
	if pod.SecurityContext == nil || pod.SecurityContext.RunAsNonRoot == nil || !*pod.SecurityContext.RunAsNonRoot ||
		pod.SecurityContext.SeccompProfile == nil || pod.SecurityContext.SeccompProfile.Type != corev1.SeccompProfileTypeRuntimeDefault {
		t.Errorf("pod security context is not restricted: %+v", pod.SecurityContext)
	}
	sc := container.SecurityContext
	if sc == nil || sc.AllowPrivilegeEscalation == nil || *sc.AllowPrivilegeEscalation ||
		sc.Capabilities == nil || len(sc.Capabilities.Drop) != 1 || sc.Capabilities.Drop[0] != "ALL" {
		t.Errorf("container security context is not restricted: %+v", sc)
	}
	if container.Resources.Limits.Memory().IsZero() || container.Resources.Requests.Cpu().IsZero() {
		t.Errorf("resources are not set: %+v", container.Resources)
	}
}

func TestBackupDeletionJobNameLength(t *testing.T) {
	got := backupDeletionJobName(strings.Repeat("x", 100))
	if len(got) > 63 {
		t.Fatalf("name length = %d, want <= 63", len(got))
	}
	if !strings.HasPrefix(got, "delete-backup-") {
		t.Errorf("name = %q, missing prefix", got)
	}
}

func TestJobCompletionConditions(t *testing.T) {
	job := &batchv1.Job{Status: batchv1.JobStatus{Conditions: []batchv1.JobCondition{{
		Type: batchv1.JobComplete, Status: corev1.ConditionTrue,
	}}}}
	if !jobComplete(job) || jobFailed(job) {
		t.Fatal("completed Job condition was not recognized")
	}
	job.Status.Conditions[0].Type = batchv1.JobFailed
	if jobComplete(job) || !jobFailed(job) {
		t.Fatal("failed Job condition was not recognized")
	}
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
