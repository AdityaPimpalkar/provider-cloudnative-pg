package barman

import (
	"context"
	"strings"
	"testing"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	commonv1alpha1 "github.com/openeverest/openeverest/v2/api/common/v1alpha1"
	corev1alpha1 "github.com/openeverest/openeverest/v2/api/core/v1alpha1"
	"github.com/openeverest/openeverest/v2/provider-runtime/controller"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

const testNamespace = "ns"

func storageWithSchedules(name string, schedules ...corev1alpha1.InstanceBackupSchedule) corev1alpha1.InstanceBackupStorage {
	return corev1alpha1.InstanceBackupStorage{
		StorageRef: commonv1alpha1.ObjectRef{Name: name},
		Schedules:  schedules,
	}
}

func schedule(name, cron string, enabled bool) corev1alpha1.InstanceBackupSchedule {
	return corev1alpha1.InstanceBackupSchedule{Name: name, Cron: cron, Enabled: enabled}
}

func TestToCNPGSchedule(t *testing.T) {
	tests := []struct {
		cron    string
		want    string
		wantErr bool
	}{
		{cron: "0 2 * * *", want: "0 0 2 * * *"},
		{cron: "  */15 * * * 1-5 ", want: "0 */15 * * * 1-5"},
		{cron: "@daily", want: "@daily"},
		{cron: "0 0 2 * * *", wantErr: true},
		{cron: "0 2 * *", wantErr: true},
		{cron: "@every 1h", wantErr: true},
		{cron: "", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.cron, func(t *testing.T) {
			got, err := toCNPGSchedule(tt.cron)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got %q", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestScheduledBackupName(t *testing.T) {
	if got := scheduledBackupName("pg", "daily"); got != "pg-daily" {
		t.Fatalf("got %q, want %q", got, "pg-daily")
	}

	longInstance := strings.Repeat("a", 60)
	first := scheduledBackupName(longInstance, "daily")
	second := scheduledBackupName(longInstance, "weekly")
	if len(first) > maxScheduledBackupNameLength || len(second) > maxScheduledBackupNameLength {
		t.Fatalf("names exceed %d chars: %q, %q", maxScheduledBackupNameLength, first, second)
	}
	if first == second {
		t.Fatalf("truncated names collide: %q", first)
	}
	if first != scheduledBackupName(longInstance, "daily") {
		t.Fatal("name is not deterministic")
	}
}

func TestValidateSchedules(t *testing.T) {
	tests := []struct {
		name    string
		backup  *corev1alpha1.InstanceBackupSpec
		wantErr string
	}{
		{name: "nil backup"},
		{
			name: "valid",
			backup: &corev1alpha1.InstanceBackupSpec{Storages: []corev1alpha1.InstanceBackupStorage{
				storageWithSchedules("s3", schedule("daily", "0 2 * * *", true)),
			}},
		},
		{
			name: "invalid name",
			backup: &corev1alpha1.InstanceBackupSpec{Storages: []corev1alpha1.InstanceBackupStorage{
				storageWithSchedules("s3", schedule("Daily_Backup", "0 2 * * *", true)),
			}},
			wantErr: "name",
		},
		{
			name: "invalid cron",
			backup: &corev1alpha1.InstanceBackupSpec{Storages: []corev1alpha1.InstanceBackupStorage{
				storageWithSchedules("s3", schedule("daily", "0 0 2 * * *", true)),
			}},
			wantErr: "5 fields",
		},
		{
			name: "invalid parameters",
			backup: &corev1alpha1.InstanceBackupSpec{Storages: []corev1alpha1.InstanceBackupStorage{
				storageWithSchedules("s3", corev1alpha1.InstanceBackupSchedule{
					Name: "daily", Cron: "0 2 * * *", Parameters: &runtime.RawExtension{Raw: []byte(`{"target":`)},
				}),
			}},
			wantErr: "decode",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateSchedules(tt.backup)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("expected error containing %q, got %v", tt.wantErr, err)
			}
		})
	}
}

func TestBuildScheduledBackups(t *testing.T) {
	backup := &corev1alpha1.InstanceBackupSpec{
		Enabled: true,
		Storages: []corev1alpha1.InstanceBackupStorage{
			storageWithSchedules("s3",
				corev1alpha1.InstanceBackupSchedule{
					Name: "daily", Cron: "0 2 * * *", Enabled: true,
					Parameters: &runtime.RawExtension{Raw: []byte(`{"target":"primary"}`)},
				},
				schedule("paused", "0 3 * * *", false),
			),
		},
	}

	got, err := buildScheduledBackups("pg", backup)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 ScheduledBackup (disabled schedule skipped), got %d", len(got))
	}

	sb := got[0]
	if sb.Name != "pg-daily" {
		t.Errorf("name = %q, want pg-daily", sb.Name)
	}
	if sb.Spec.Schedule != "0 0 2 * * *" {
		t.Errorf("schedule = %q, want seconds-prefixed cron", sb.Spec.Schedule)
	}
	if sb.Spec.Cluster.Name != "pg" {
		t.Errorf("cluster = %q, want pg", sb.Spec.Cluster.Name)
	}
	if sb.Spec.Method != cnpgv1.BackupMethodPlugin {
		t.Errorf("method = %q, want plugin", sb.Spec.Method)
	}
	if sb.Spec.Target != cnpgv1.BackupTargetPrimary {
		t.Errorf("target = %q, want primary", sb.Spec.Target)
	}
	if sb.Spec.PluginConfiguration == nil || sb.Spec.PluginConfiguration.Parameters[PluginParameterObjectStore] != "s3" {
		t.Errorf("plugin configuration does not reference storage s3: %+v", sb.Spec.PluginConfiguration)
	}

	backup.Enabled = false
	got, err = buildScheduledBackups("pg", backup)
	if err != nil || len(got) != 0 {
		t.Fatalf("expected no ScheduledBackups when backups are disabled, got %d (err %v)", len(got), err)
	}
}

func TestSyncScheduledBackups(t *testing.T) {
	scheme := runtime.NewScheme()
	for _, add := range []func(*runtime.Scheme) error{cnpgv1.AddToScheme, corev1alpha1.AddToScheme} {
		if err := add(scheme); err != nil {
			t.Fatal(err)
		}
	}

	instance := &corev1alpha1.Instance{
		ObjectMeta: metav1.ObjectMeta{Name: "pg", Namespace: testNamespace, UID: types.UID("instance-uid")},
		Spec: corev1alpha1.InstanceSpec{Backup: &corev1alpha1.InstanceBackupSpec{
			Enabled:  true,
			Storages: []corev1alpha1.InstanceBackupStorage{storageWithSchedules("s3", schedule("daily", "0 2 * * *", true))},
		}},
	}
	controllerRef := func(uid types.UID) []metav1.OwnerReference {
		isController := true
		return []metav1.OwnerReference{{
			APIVersion: corev1alpha1.GroupVersion.String(), Kind: "Instance", Name: "owner", UID: uid, Controller: &isController,
		}}
	}
	stale := &cnpgv1.ScheduledBackup{ObjectMeta: metav1.ObjectMeta{
		Name: "pg-removed", Namespace: testNamespace, OwnerReferences: controllerRef(instance.UID),
	}}
	foreign := &cnpgv1.ScheduledBackup{ObjectMeta: metav1.ObjectMeta{
		Name: "other-daily", Namespace: testNamespace, OwnerReferences: controllerRef("other-uid"),
	}}
	unmanaged := &cnpgv1.ScheduledBackup{ObjectMeta: metav1.ObjectMeta{Name: "manual", Namespace: testNamespace}}

	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(instance, stale, foreign, unmanaged).Build()
	ctx := controller.NewContext(context.Background(), cl, instance, "provider-cloudnative-pg")

	if err := SyncScheduledBackups(ctx); err != nil {
		t.Fatalf("sync: %v", err)
	}

	list := &cnpgv1.ScheduledBackupList{}
	if err := cl.List(context.Background(), list, client.InNamespace(testNamespace)); err != nil {
		t.Fatal(err)
	}
	names := map[string]cnpgv1.ScheduledBackup{}
	for _, sb := range list.Items {
		names[sb.Name] = sb
	}
	if _, ok := names["pg-removed"]; ok {
		t.Error("stale ScheduledBackup owned by the Instance was not deleted")
	}
	for _, name := range []string{"other-daily", "manual"} {
		if _, ok := names[name]; !ok {
			t.Errorf("ScheduledBackup %q not owned by the Instance was deleted", name)
		}
	}
	created, ok := names["pg-daily"]
	if !ok {
		t.Fatal("desired ScheduledBackup pg-daily was not created")
	}
	if !metav1.IsControlledBy(&created, instance) {
		t.Error("created ScheduledBackup is not controlled by the Instance")
	}

	instance.Spec.Backup.Enabled = false
	if err := SyncScheduledBackups(ctx); err != nil {
		t.Fatalf("sync with backups disabled: %v", err)
	}
	if err := cl.Get(context.Background(), client.ObjectKey{Namespace: testNamespace, Name: "pg-daily"}, &cnpgv1.ScheduledBackup{}); err == nil {
		t.Error("ScheduledBackup was not removed after disabling backups")
	}
}
