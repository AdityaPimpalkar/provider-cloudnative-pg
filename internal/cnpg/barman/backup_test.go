package barman

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/AlekSi/pointer"
	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	barmancloudv1 "github.com/cloudnative-pg/plugin-barman-cloud/api/v1"
	backupv1alpha1 "github.com/openeverest/openeverest/v2/api/backup/v1alpha1"
	commonv1alpha1 "github.com/openeverest/openeverest/v2/api/common/v1alpha1"
	corev1alpha1 "github.com/openeverest/openeverest/v2/api/core/v1alpha1"
	"github.com/openeverest/openeverest/v2/provider-runtime/controller"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestNativeRetention(t *testing.T) {
	timePolicy := func(duration string) *corev1alpha1.BackupScheduleRetention {
		return &corev1alpha1.BackupScheduleRetention{
			Type: corev1alpha1.BackupScheduleRetentionTypeTime, Duration: duration,
		}
	}
	withRetention := func(name string, policy *corev1alpha1.BackupScheduleRetention) corev1alpha1.InstanceBackupSchedule {
		s := schedule(name, "0 2 * * *", true)
		s.Retention = policy
		return s
	}
	disabled := withRetention("paused", timePolicy("7d"))
	disabled.Enabled = false
	tests := []struct {
		name      string
		schedules []corev1alpha1.InstanceBackupSchedule
		want      string
		wantErr   string
	}{
		{name: "no schedules"},
		{name: "keep all", schedules: []corev1alpha1.InstanceBackupSchedule{withRetention("daily", nil)}},
		{name: "days", schedules: []corev1alpha1.InstanceBackupSchedule{withRetention("daily", timePolicy("30d"))}, want: "30d"},
		{name: "weeks", schedules: []corev1alpha1.InstanceBackupSchedule{withRetention("daily", timePolicy("4w"))}, want: "4w"},
		{name: "months", schedules: []corev1alpha1.InstanceBackupSchedule{withRetention("daily", timePolicy("2m"))}, want: "2m"},
		{name: "matching schedules", schedules: []corev1alpha1.InstanceBackupSchedule{
			withRetention("daily", timePolicy("30d")), withRetention("weekly", timePolicy("30d")),
		}, want: "30d"},
		{name: "conflicting schedules", schedules: []corev1alpha1.InstanceBackupSchedule{
			withRetention("daily", timePolicy("30d")), withRetention("weekly", timePolicy("7d")),
		}, wantErr: "same time retention"},
		{name: "keep all conflicts", schedules: []corev1alpha1.InstanceBackupSchedule{
			withRetention("daily", nil), withRetention("weekly", timePolicy("7d")),
		}, wantErr: "same time retention"},
		{name: "disabled schedule conflicts", schedules: []corev1alpha1.InstanceBackupSchedule{
			withRetention("daily", timePolicy("30d")), disabled,
		}, wantErr: "same time retention"},
		{name: "count rejected", schedules: []corev1alpha1.InstanceBackupSchedule{
			withRetention("daily", &corev1alpha1.BackupScheduleRetention{Type: corev1alpha1.BackupScheduleRetentionTypeCount}),
		}, wantErr: "only time retention"},
		{name: "invalid duration", schedules: []corev1alpha1.InstanceBackupSchedule{withRetention("daily", timePolicy("0d"))}, wantErr: "invalid retention duration"},
		{name: "empty duration", schedules: []corev1alpha1.InstanceBackupSchedule{withRetention("daily", timePolicy(""))}, wantErr: "invalid retention duration"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			storage := storageWithSchedules("s3", tt.schedules...)
			cfg := &corev1alpha1.InstanceBackupSpec{
				Enabled: true, Storages: []corev1alpha1.InstanceBackupStorage{storage},
			}
			err := ValidateSchedules(cfg)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("ValidateSchedules error = %v, want %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			scheme := runtime.NewScheme()
			barmancloudv1.AddKnownTypes(scheme)
			for _, add := range []func(*runtime.Scheme) error{
				corev1.AddToScheme, corev1alpha1.AddToScheme,
				backupv1alpha1.AddToScheme, cnpgv1.AddToScheme,
			} {
				if err := add(scheme); err != nil {
					t.Fatal(err)
				}
			}
			instance := &corev1alpha1.Instance{
				ObjectMeta: metav1.ObjectMeta{Name: "pg", Namespace: testNamespace},
				Spec:       corev1alpha1.InstanceSpec{Backup: cfg},
			}
			bg := &backupv1alpha1.BackupStorage{
				ObjectMeta: metav1.ObjectMeta{Name: "s3", Namespace: testNamespace},
				Spec: backupv1alpha1.BackupStorageSpec{S3: &backupv1alpha1.BackupStorageS3Spec{
					Bucket: "backups", CredentialsSecretRef: commonv1alpha1.SecretRef{Name: "credentials"},
				}},
			}
			secret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: "credentials", Namespace: testNamespace},
				Data:       map[string][]byte{"AWS_ACCESS_KEY_ID": []byte("test"), "AWS_SECRET_ACCESS_KEY": []byte("test")},
			}
			cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(instance, bg, secret).Build()
			ctx := controller.NewContext(context.Background(), cl, instance, "provider-cloudnative-pg")
			if _, err := SyncBackupInfrastructure(ctx); err != nil {
				t.Fatal(err)
			}
			got := &barmancloudv1.ObjectStore{}
			key := client.ObjectKey{Namespace: testNamespace, Name: "s3"}
			if err := cl.Get(context.Background(), key, got); err != nil {
				t.Fatal(err)
			}
			if got.Spec.RetentionPolicy != tt.want {
				t.Fatalf("ObjectStore retention = %q, want %q", got.Spec.RetentionPolicy, tt.want)
			}
			for i := range cfg.Storages[0].Schedules {
				cfg.Storages[0].Schedules[i].Retention = nil
			}
			if _, err := SyncBackupInfrastructure(ctx); err != nil {
				t.Fatal(err)
			}
			if err := cl.Get(context.Background(), key, got); err != nil {
				t.Fatal(err)
			}
			if got.Spec.RetentionPolicy != "" {
				t.Fatalf("removed retention is still configured: %q", got.Spec.RetentionPolicy)
			}
		})
	}
}

func TestSharedStorageRetention(t *testing.T) {
	const provider = "provider-cloudnative-pg"
	instanceWith := func(name, providerName, storage, retention string, enabled bool) *corev1alpha1.Instance {
		s := schedule("daily", "0 2 * * *", true)
		if retention != "" {
			s.Retention = &corev1alpha1.BackupScheduleRetention{
				Type: corev1alpha1.BackupScheduleRetentionTypeTime, Duration: retention,
			}
		}
		return &corev1alpha1.Instance{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: testNamespace},
			Spec: corev1alpha1.InstanceSpec{
				ProviderRef: commonv1alpha1.ObjectRef{Name: providerName},
				Backup: &corev1alpha1.InstanceBackupSpec{
					Enabled:  enabled,
					Storages: []corev1alpha1.InstanceBackupStorage{storageWithSchedules(storage, s)},
				},
			},
		}
	}
	tests := []struct {
		name    string
		peer    *corev1alpha1.Instance
		stored  *string
		wantErr string
	}{
		{name: "no peer"},
		{name: "peer with the same retention", peer: instanceWith("other", provider, "s3", "30d", true)},
		{name: "peer with a different retention", peer: instanceWith("other", provider, "s3", "7d", true), wantErr: "other (7d)"},
		{name: "peer keeps all", peer: instanceWith("other", provider, "s3", "", true), wantErr: "other (keep all)"},
		{
			name: "stored policy already matches this Instance", peer: instanceWith("other", provider, "s3", "7d", true),
			stored: pointer.To("30d"),
		},
		{
			name: "stored policy written by the peer", peer: instanceWith("other", provider, "s3", "7d", true),
			stored: pointer.To("7d"), wantErr: "other (7d)",
		},
		{name: "peer uses another storage", peer: instanceWith("other", provider, "gcs", "7d", true)},
		{name: "peer has backups disabled", peer: instanceWith("other", provider, "s3", "7d", false)},
		{name: "peer belongs to another provider", peer: instanceWith("other", "provider-other", "s3", "7d", true)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scheme := runtime.NewScheme()
			barmancloudv1.AddKnownTypes(scheme)
			for _, add := range []func(*runtime.Scheme) error{
				corev1.AddToScheme, corev1alpha1.AddToScheme, backupv1alpha1.AddToScheme, cnpgv1.AddToScheme,
			} {
				if err := add(scheme); err != nil {
					t.Fatal(err)
				}
			}
			instance := instanceWith("pg", provider, "s3", "30d", true)
			objects := []client.Object{
				instance,
				&backupv1alpha1.BackupStorage{
					ObjectMeta: metav1.ObjectMeta{Name: "s3", Namespace: testNamespace},
					Spec: backupv1alpha1.BackupStorageSpec{S3: &backupv1alpha1.BackupStorageS3Spec{
						Bucket: "backups", CredentialsSecretRef: commonv1alpha1.SecretRef{Name: "credentials"},
					}},
				},
				&corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{Name: "credentials", Namespace: testNamespace},
					Data:       map[string][]byte{"AWS_ACCESS_KEY_ID": []byte("test"), "AWS_SECRET_ACCESS_KEY": []byte("test")},
				},
			}
			if tt.peer != nil {
				objects = append(objects, tt.peer)
			}
			if tt.stored != nil {
				objects = append(objects, &barmancloudv1.ObjectStore{
					ObjectMeta: metav1.ObjectMeta{Name: "s3", Namespace: testNamespace},
					Spec:       barmancloudv1.ObjectStoreSpec{RetentionPolicy: *tt.stored},
				})
			}
			cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).Build()
			ctx := controller.NewContext(context.Background(), cl, instance, provider)

			_, err := SyncBackupInfrastructure(ctx)
			got := &barmancloudv1.ObjectStore{}
			getErr := cl.Get(context.Background(), client.ObjectKey{Namespace: testNamespace, Name: "s3"}, got)
			if tt.wantErr != "" {
				bce := controller.AsBackupConfigError(err)
				if bce == nil || bce.Reason != "BackupRetentionConflict" || !strings.Contains(bce.Message, tt.wantErr) {
					t.Fatalf("error = %v, want BackupRetentionConflict mentioning %q", err, tt.wantErr)
				}
				if tt.stored == nil && !apierrors.IsNotFound(getErr) {
					t.Fatalf("ObjectStore was created despite the conflict: %v", getErr)
				}
				if tt.stored != nil && got.Spec.RetentionPolicy != *tt.stored {
					t.Fatalf("stored retention changed to %q despite the conflict", got.Spec.RetentionPolicy)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if getErr != nil || got.Spec.RetentionPolicy != "30d" {
				t.Fatalf("ObjectStore retention = %q (%v), want 30d", got.Spec.RetentionPolicy, getErr)
			}
		})
	}
}

func TestEndpointCARef(t *testing.T) {
	scheme := runtime.NewScheme()
	for _, add := range []func(*runtime.Scheme) error{corev1.AddToScheme, corev1alpha1.AddToScheme} {
		if err := add(scheme); err != nil {
			t.Fatal(err)
		}
	}
	caSecret := func(data map[string][]byte) *corev1.Secret {
		return &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "s3" + EndpointCASecretSuffix, Namespace: testNamespace},
			Data:       data,
		}
	}

	tests := []struct {
		name       string
		endpoint   string
		secret     *corev1.Secret
		wantRef    bool
		wantConfig bool
	}{
		{name: "http ignores CA Secret", endpoint: "http://minio:9000", secret: caSecret(map[string][]byte{EndpointCAKey: []byte("pem")})},
		{name: "https without CA Secret uses system trust store", endpoint: "https://s3.us-east-1.amazonaws.com"},
		{name: "https with CA Secret", endpoint: "HTTPS://seaweedfs", secret: caSecret(map[string][]byte{EndpointCAKey: []byte("pem")}), wantRef: true},
		{name: "https with CA Secret missing key", endpoint: "https://seaweedfs", secret: caSecret(nil), wantConfig: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			instance := &corev1alpha1.Instance{ObjectMeta: metav1.ObjectMeta{Name: "pg", Namespace: testNamespace}}
			objects := []client.Object{instance}
			if tt.secret != nil {
				objects = append(objects, tt.secret)
			}
			cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).Build()
			ctx := controller.NewContext(context.Background(), cl, instance, "provider-cloudnative-pg")

			ref, err := endpointCARef(ctx, "s3", tt.endpoint)

			var configErr *controller.BackupConfigError
			if tt.wantConfig {
				if !errors.As(err, &configErr) {
					t.Fatalf("expected BackupConfigError, got %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if gotRef := ref != nil; gotRef != tt.wantRef {
				t.Fatalf("ref = %+v, wantRef %v", ref, tt.wantRef)
			}
			if ref != nil && (ref.Name != "s3"+EndpointCASecretSuffix || ref.Key != EndpointCAKey) {
				t.Fatalf("unexpected ref %+v", ref)
			}
		})
	}
}
