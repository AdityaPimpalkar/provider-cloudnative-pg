package barman

import (
	"context"
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
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestArchiveObjectStoreName(t *testing.T) {
	barmanPlugin := func(enabled *bool) cnpgv1.PluginConfiguration {
		return cnpgv1.PluginConfiguration{
			Name:       PluginName,
			Enabled:    enabled,
			Parameters: map[string]string{PluginParameterObjectStore: "s3"},
		}
	}

	tests := []struct {
		name    string
		plugins []cnpgv1.PluginConfiguration
		want    string
	}{
		{name: "enabled plugin", plugins: []cnpgv1.PluginConfiguration{barmanPlugin(pointer.To(true))}, want: "s3"},
		{name: "plugin enabled by default", plugins: []cnpgv1.PluginConfiguration{barmanPlugin(nil)}, want: "s3"},
		{name: "disabled plugin", plugins: []cnpgv1.PluginConfiguration{barmanPlugin(pointer.To(false))}},
		{name: "no plugins"},
		{
			name: "only other plugins",
			plugins: []cnpgv1.PluginConfiguration{{
				Name:       "other.example.com",
				Parameters: map[string]string{PluginParameterObjectStore: "s3"},
			}},
		},
		{
			name: "barman plugin after another plugin",
			plugins: []cnpgv1.PluginConfiguration{
				{Name: "other.example.com"},
				barmanPlugin(pointer.To(true)),
			},
			want: "s3",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cluster := &cnpgv1.Cluster{Spec: cnpgv1.ClusterSpec{Plugins: tt.plugins}}
			if got := ArchiveObjectStoreName(cluster); got != tt.want {
				t.Fatalf("ArchiveObjectStoreName() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestObjectStoreCRDMissing(t *testing.T) {
	if got := ObjectStoreCRDMissing(nil); got != nil {
		t.Fatalf("nil error mapped to %+v", got)
	}
	if got := ObjectStoreCRDMissing(apierrors.NewNotFound(schema.GroupResource{Resource: "objectstores"}, "s3")); got != nil {
		t.Fatalf("NotFound mapped to %+v", got)
	}
	noMatch := &meta.NoKindMatchError{GroupKind: schema.GroupKind{Group: barmancloudv1.GroupVersion.Group, Kind: "ObjectStore"}}
	got := ObjectStoreCRDMissing(noMatch)
	if got == nil || got.Reason != ReasonBarmanPluginMissing {
		t.Fatalf("NoMatch = %+v, want Reason %q", got, ReasonBarmanPluginMissing)
	}
}

// noMatchObjectStoreClient returns NoMatch for ObjectStore Gets to simulate a
// cluster without the Barman Cloud Plugin CRDs.
type noMatchObjectStoreClient struct {
	client.Client
}

func (c noMatchObjectStoreClient) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	if _, ok := obj.(*barmancloudv1.ObjectStore); ok {
		return &meta.NoKindMatchError{
			GroupKind: schema.GroupKind{Group: barmancloudv1.GroupVersion.Group, Kind: "ObjectStore"},
		}
	}
	return c.Client.Get(ctx, key, obj, opts...)
}

func TestSyncBackupInfrastructureMissingCRD(t *testing.T) {
	scheme := runtime.NewScheme()
	barmancloudv1.AddKnownTypes(scheme)
	for _, add := range []func(*runtime.Scheme) error{
		corev1.AddToScheme, corev1alpha1.AddToScheme, backupv1alpha1.AddToScheme, cnpgv1.AddToScheme,
	} {
		if err := add(scheme); err != nil {
			t.Fatal(err)
		}
	}

	instance := &corev1alpha1.Instance{
		ObjectMeta: metav1.ObjectMeta{Name: "pg", Namespace: testNamespace},
		Spec: corev1alpha1.InstanceSpec{
			Backup: &corev1alpha1.InstanceBackupSpec{
				Enabled:  true,
				Storages: []corev1alpha1.InstanceBackupStorage{storageWithSchedules("s3")},
			},
		},
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
	base := fake.NewClientBuilder().WithScheme(scheme).WithObjects(instance, bg, secret).Build()
	ctx := controller.NewContext(context.Background(), noMatchObjectStoreClient{Client: base}, instance, "provider-cloudnative-pg")

	_, err := SyncBackupInfrastructure(ctx)
	bce := controller.AsBackupConfigError(err)
	if bce == nil || bce.Reason != ReasonBarmanPluginMissing {
		t.Fatalf("error = %v, want BackupConfigError reason %q", err, ReasonBarmanPluginMissing)
	}
}
