package provider

import (
	"context"
	"strings"
	"testing"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	corev1alpha1 "github.com/openeverest/openeverest/v2/api/core/v1alpha1"
	"github.com/openeverest/openeverest/v2/provider-runtime/controller"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/adityapimpalkar/provider-cloudnative-pg/internal/common"
)

func TestValidateProxy(t *testing.T) {
	tests := []struct {
		name    string
		proxy   corev1alpha1.ComponentSpec
		wantErr string
	}{
		{name: "transaction", proxy: proxyComponent(`{"poolMode":"transaction"}`, ptr.To(int32(1)))},
		{name: "session", proxy: proxyComponent(`{"poolMode":"session"}`, nil)},
		{name: "omitted mode", proxy: corev1alpha1.ComponentSpec{}},
		{name: "statement is not a CloudNativePG mode", proxy: proxyComponent(`{"poolMode":"statement"}`, nil), wantErr: "statement"},
		{name: "zero replicas", proxy: proxyComponent("", ptr.To(int32(0))), wantErr: "at least 1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateProxy(proxyContext(t, tt.proxy))
			if tt.wantErr == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error = %v, want %q", err, tt.wantErr)
			}
		})
	}
}

func TestValidateProxyRejectsANameTheServiceCannotUse(t *testing.T) {
	c := proxyContext(t, corev1alpha1.ComponentSpec{})
	c.Instance().Name = strings.Repeat("a", 60)
	if err := validateProxy(c); err == nil {
		t.Fatal("expected the pooler Service name to be rejected")
	}
}

func proxyComponent(parameters string, replicas *int32) corev1alpha1.ComponentSpec {
	spec := corev1alpha1.ComponentSpec{Replicas: replicas}
	if parameters != "" {
		spec.Parameters = &runtime.RawExtension{Raw: []byte(parameters)}
	}
	return spec
}

func proxyContext(t *testing.T, proxy ...corev1alpha1.ComponentSpec) *controller.Context {
	t.Helper()
	scheme := runtime.NewScheme()
	for _, add := range []func(*runtime.Scheme) error{cnpgv1.AddToScheme, corev1.AddToScheme, corev1alpha1.AddToScheme} {
		if err := add(scheme); err != nil {
			t.Fatal(err)
		}
	}
	instance := &corev1alpha1.Instance{ObjectMeta: metav1.ObjectMeta{Name: "pg", Namespace: testNamespace, UID: "uid"}}
	if len(proxy) > 0 {
		instance.Spec.Components = map[string]corev1alpha1.ComponentSpec{common.ComponentProxy: proxy[0]}
	}
	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(instance).Build()
	return controller.NewContext(context.Background(), cl, instance, common.ProviderName)
}
