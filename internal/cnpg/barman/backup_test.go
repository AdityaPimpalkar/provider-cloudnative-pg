package barman

import (
	"context"
	"errors"
	"testing"

	corev1alpha1 "github.com/openeverest/openeverest/v2/api/core/v1alpha1"
	"github.com/openeverest/openeverest/v2/provider-runtime/controller"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

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
