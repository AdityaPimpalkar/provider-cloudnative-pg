package provider

import (
	"context"
	"strings"
	"testing"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	corev1alpha1 "github.com/openeverest/openeverest/v2/api/core/v1alpha1"
	"github.com/openeverest/openeverest/v2/provider-runtime/controller"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/adityapimpalkar/provider-cloudnative-pg/definition/components"
	"github.com/adityapimpalkar/provider-cloudnative-pg/internal/common"
)

func TestBuildPooler(t *testing.T) {
	c := proxyContext(t, proxyComponent(`{"poolMode":"transaction"}`, ptr.To(int32(2)), proxyResources()))

	pooler := buildPooler(c, c.Instance().Spec.Components[common.ComponentProxy], mustProxyParams(t, c))

	if pooler.Name != "pg-pooler" {
		t.Fatalf("name = %q", pooler.Name)
	}
	if pooler.Spec.Cluster.Name != "pg" {
		t.Fatalf("cluster = %q", pooler.Spec.Cluster.Name)
	}
	if pooler.Spec.Type != cnpgv1.PoolerTypeRW {
		t.Fatalf("type = %q", pooler.Spec.Type)
	}
	if pooler.Spec.PgBouncer == nil || pooler.Spec.PgBouncer.PoolMode != cnpgv1.PgBouncerPoolModeTransaction {
		t.Fatalf("pool mode = %+v", pooler.Spec.PgBouncer)
	}
	if pooler.Spec.Instances == nil || *pooler.Spec.Instances != 2 {
		t.Fatalf("instances = %v", pooler.Spec.Instances)
	}
	if pooler.Spec.Template == nil || pooler.Spec.Template.ObjectMeta.Labels[controller.ComponentLabel] != common.ComponentProxy {
		t.Fatalf("pod labels = %+v", pooler.Spec.Template)
	}
	containers := pooler.Spec.Template.Spec.Containers
	if len(containers) != 1 || containers[0].Name != pgbouncerContainerName {
		t.Fatalf("containers = %+v", containers)
	}
	if containers[0].Resources.Requests.Cpu().Cmp(resource.MustParse("100m")) != 0 {
		t.Fatalf("cpu request = %s", containers[0].Resources.Requests.Cpu())
	}
	if containers[0].Image != "" {
		t.Fatalf("image = %q, the operator selects the PgBouncer image", containers[0].Image)
	}
}

func TestBuildPoolerDefaults(t *testing.T) {
	c := proxyContext(t, corev1alpha1.ComponentSpec{})

	pooler := buildPooler(c, c.Instance().Spec.Components[common.ComponentProxy], mustProxyParams(t, c))

	if pooler.Spec.Instances == nil || *pooler.Spec.Instances != 1 {
		t.Fatalf("instances = %v, want the operator default of 1", pooler.Spec.Instances)
	}
	if pooler.Spec.PgBouncer.PoolMode != "" {
		t.Fatalf("pool mode = %q, want empty so the operator default (session) applies", pooler.Spec.PgBouncer.PoolMode)
	}
	if len(pooler.Spec.Template.Spec.Containers) != 0 {
		t.Fatalf("containers = %+v, want none without resources", pooler.Spec.Template.Spec.Containers)
	}
}

func TestValidateProxy(t *testing.T) {
	tests := []struct {
		name    string
		proxy   corev1alpha1.ComponentSpec
		wantErr string
	}{
		{name: "transaction", proxy: proxyComponent(`{"poolMode":"transaction"}`, ptr.To(int32(1)), nil)},
		{name: "session", proxy: proxyComponent(`{"poolMode":"session"}`, nil, nil)},
		{name: "omitted mode", proxy: corev1alpha1.ComponentSpec{}},
		{name: "statement is not a CloudNativePG mode", proxy: proxyComponent(`{"poolMode":"statement"}`, nil, nil), wantErr: "statement"},
		{name: "zero replicas", proxy: proxyComponent("", ptr.To(int32(0)), nil), wantErr: "at least 1"},
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

func TestSyncPooler(t *testing.T) {
	t.Run("creates the pooler", func(t *testing.T) {
		c, cl := proxyContextWithClient(t, proxyComponent(`{"poolMode":"session"}`, ptr.To(int32(1)), nil))
		if err := syncPooler(c); err != nil {
			t.Fatal(err)
		}
		got := &cnpgv1.Pooler{}
		if err := cl.Get(context.Background(), client.ObjectKey{Namespace: testNamespace, Name: "pg-pooler"}, got); err != nil {
			t.Fatal(err)
		}
		if got.Spec.Type != cnpgv1.PoolerTypeRW || got.Spec.PgBouncer.PoolMode != cnpgv1.PgBouncerPoolModeSession {
			t.Fatalf("spec = %+v", got.Spec)
		}
	})

	t.Run("deletes the pooler when the component is removed", func(t *testing.T) {
		c, cl := proxyContextWithClient(t)
		if err := cl.Create(context.Background(), &cnpgv1.Pooler{ObjectMeta: metav1.ObjectMeta{Name: "pg-pooler", Namespace: testNamespace}}); err != nil {
			t.Fatal(err)
		}
		if err := syncPooler(c); err != nil {
			t.Fatal(err)
		}
		err := cl.Get(context.Background(), client.ObjectKey{Namespace: testNamespace, Name: "pg-pooler"}, &cnpgv1.Pooler{})
		if client.IgnoreNotFound(err) != nil || err == nil {
			t.Fatalf("pooler get err = %v, want not found", err)
		}
	})
}

func proxyComponent(parameters string, replicas *int32, resources *corev1.ResourceRequirements) corev1alpha1.ComponentSpec {
	spec := corev1alpha1.ComponentSpec{Replicas: replicas, Resources: resources}
	if parameters != "" {
		spec.Parameters = &runtime.RawExtension{Raw: []byte(parameters)}
	}
	return spec
}

func proxyResources() *corev1.ResourceRequirements {
	return &corev1.ResourceRequirements{
		Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("100m"), corev1.ResourceMemory: resource.MustParse("128Mi")},
		Limits:   corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("100m"), corev1.ResourceMemory: resource.MustParse("128Mi")},
	}
}

func proxyContext(t *testing.T, proxy ...corev1alpha1.ComponentSpec) *controller.Context {
	t.Helper()
	c, _ := proxyContextWithClient(t, proxy...)
	return c
}

func proxyContextWithClient(t *testing.T, proxy ...corev1alpha1.ComponentSpec) (*controller.Context, client.Client) {
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
	return controller.NewContext(context.Background(), cl, instance, common.ProviderName), cl
}

func mustProxyParams(t *testing.T, c *controller.Context) components.PgBouncerParameters {
	t.Helper()
	params, err := proxyParameters(c, c.Instance().Spec.Components[common.ComponentProxy])
	if err != nil {
		t.Fatal(err)
	}
	return params
}
