package provider

import (
	"context"
	"testing"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	cnpgutils "github.com/cloudnative-pg/cloudnative-pg/pkg/utils"
	commonv1alpha1 "github.com/openeverest/openeverest/v2/api/common/v1alpha1"
	corev1alpha1 "github.com/openeverest/openeverest/v2/api/core/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/adityapimpalkar/provider-cloudnative-pg/internal/cnpg/barman"
	"github.com/adityapimpalkar/provider-cloudnative-pg/internal/common"
)

func TestMirror(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := corev1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}

	params := &runtime.RawExtension{Raw: []byte(`{"target":"primary"}`)}
	instance := func(provider string) *corev1alpha1.Instance {
		return &corev1alpha1.Instance{
			ObjectMeta: metav1.ObjectMeta{Name: "pg", Namespace: "ns"},
			Spec: corev1alpha1.InstanceSpec{
				ProviderRef: commonv1alpha1.ObjectRef{Name: provider},
				Backup: &corev1alpha1.InstanceBackupSpec{
					Enabled:  true,
					ClassRef: commonv1alpha1.ObjectRef{Name: "cnpg-barman-plugin"},
					Storages: []corev1alpha1.InstanceBackupStorage{{
						StorageRef: commonv1alpha1.ObjectRef{Name: "s3"},
						Schedules: []corev1alpha1.InstanceBackupSchedule{
							{Name: "daily", Cron: "0 2 * * *", Enabled: true, Parameters: params},
						},
					}},
				},
			},
		}
	}
	scheduledBackup := func(sbName string) *cnpgv1.Backup {
		b := &cnpgv1.Backup{
			ObjectMeta: metav1.ObjectMeta{Name: "pg-daily-20261005", Namespace: "ns"},
			Spec: cnpgv1.BackupSpec{
				Cluster:             cnpgv1.LocalObjectReference{Name: "pg"},
				PluginConfiguration: barman.PluginConfiguration("s3"),
			},
		}
		if sbName != "" {
			b.Labels = map[string]string{cnpgutils.ParentScheduledBackupLabelName: sbName}
		}
		return b
	}

	tests := []struct {
		name     string
		instance *corev1alpha1.Instance
		backup   *cnpgv1.Backup
		want     bool
	}{
		{name: "scheduled backup", instance: instance(common.ProviderName), backup: scheduledBackup("pg-daily"), want: true},
		{name: "on-demand backup", instance: instance(common.ProviderName), backup: scheduledBackup("")},
		{name: "ScheduledBackup not managed by the instance", instance: instance(common.ProviderName), backup: scheduledBackup("manual")},
		{name: "instance of another provider", instance: instance("other"), backup: scheduledBackup("pg-daily")},
		{name: "instance missing", backup: scheduledBackup("pg-daily")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			builder := fake.NewClientBuilder().WithScheme(scheme)
			if tt.instance != nil {
				builder = builder.WithObjects(tt.instance)
			}

			got, err := New().Mirror(context.Background(), builder.Build(), tt.backup)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !tt.want {
				if got != nil {
					t.Fatalf("expected no mirror, got %+v", got)
				}
				return
			}
			if got == nil {
				t.Fatal("expected a mirrored Backup")
			}
			if got.Name != tt.backup.Name || got.Namespace != "ns" {
				t.Errorf("mirrored %s/%s, want ns/%s", got.Namespace, got.Name, tt.backup.Name)
			}
			if got.Spec.Origin.InstanceRef == nil || got.Spec.Origin.InstanceRef.Name != "pg" {
				t.Errorf("origin = %+v, want instance pg", got.Spec.Origin)
			}
			if got.Spec.ClassRef.Name != "cnpg-barman-plugin" || got.Spec.StorageRef.Name != "s3" {
				t.Errorf("classRef/storageRef = %q/%q", got.Spec.ClassRef.Name, got.Spec.StorageRef.Name)
			}
			if got.Spec.ScheduleName != "daily" || got.Spec.Parameters == nil || string(got.Spec.Parameters.Raw) != string(params.Raw) {
				t.Errorf("schedule = %q, parameters = %v", got.Spec.ScheduleName, got.Spec.Parameters)
			}
		})
	}
}
