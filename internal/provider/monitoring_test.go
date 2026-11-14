package provider

import (
	"context"
	"errors"
	"os"
	"testing"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/yaml"

	commonv1alpha1 "github.com/openeverest/openeverest/v2/api/common/v1alpha1"
	corev1alpha1 "github.com/openeverest/openeverest/v2/api/core/v1alpha1"
	"github.com/openeverest/openeverest/v2/provider-runtime/controller"

	"github.com/adityapimpalkar/provider-cloudnative-pg/internal/common"
)

func monitoringContext(t *testing.T, funcs interceptor.Funcs, objs ...client.Object) *controller.Context {
	t.Helper()
	scheme := runtime.NewScheme()
	for _, add := range []func(*runtime.Scheme) error{corev1alpha1.AddToScheme, cnpgv1.AddToScheme} {
		require.NoError(t, add(scheme))
	}
	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objs...).WithInterceptorFuncs(funcs).Build()
	in := &corev1alpha1.Instance{ObjectMeta: metav1.ObjectMeta{Name: "pg", Namespace: testNamespace}}
	return controller.NewContext(context.Background(), cl, in, common.ProviderName)
}

// monitoredCluster is the Instance's Cluster, initialized or not, with
// readyInstances of instances ready and metrics served over TLS or not.
func monitoredCluster(initialized bool, ready, instances int, tls bool) *cnpgv1.Cluster {
	pg := &cnpgv1.Cluster{ObjectMeta: metav1.ObjectMeta{Name: "pg", Namespace: testNamespace}}
	pg.Spec.Instances = instances
	pg.Status.Instances = instances
	pg.Status.ReadyInstances = ready
	status := metav1.ConditionFalse
	if initialized {
		status = metav1.ConditionTrue
	}
	pg.Status.Conditions = []metav1.Condition{{Type: string(cnpgv1.ConditionInitialized), Status: status}}
	if tls {
		pg.Spec.Monitoring = &cnpgv1.MonitoringConfiguration{TLSConfig: &cnpgv1.ClusterMonitoringTLSConfiguration{Enabled: true}}
	}
	return pg
}

// The instance manager's endpoint is published once CloudNativePG has
// initialized the cluster, and kept while later changes roll the pods.
func TestMonitoringSources(t *testing.T) {
	engine := corev1alpha1.MetricsEndpoint{
		Component: common.ComponentEngine,
		PodSelector: map[string]string{
			controller.InstanceLabel:  "pg",
			controller.ComponentLabel: common.ComponentEngine,
		},
		Port:   intstr.FromString("metrics"),
		Path:   "/metrics",
		Scheme: "http",
	}
	withTLS := engine
	withTLS.Scheme = "https"
	withTLS.TLS = &corev1alpha1.MetricsEndpointTLS{
		TLSConfig: commonv1alpha1.TLSConfig{
			CA:                 &commonv1alpha1.CABundleSource{SecretKeyRef: &commonv1alpha1.SecretKeyRef{Name: "pg-ca", Key: "ca.crt"}},
			InsecureSkipVerify: true,
		},
		ServerName: "pg-rw",
	}

	for _, tc := range []struct {
		name string
		objs []client.Object
		want []corev1alpha1.MetricsEndpoint
	}{
		{name: "no cluster yet"},
		{name: "cluster initializing", objs: []client.Object{monitoredCluster(false, 0, 3, false)}},
		{name: "cluster ready", objs: []client.Object{monitoredCluster(true, 3, 3, false)}, want: []corev1alpha1.MetricsEndpoint{engine}},
		{name: "rolling update", objs: []client.Object{monitoredCluster(true, 2, 3, false)}, want: []corev1alpha1.MetricsEndpoint{engine}},
		{name: "metrics over TLS", objs: []client.Object{monitoredCluster(true, 3, 3, true)}, want: []corev1alpha1.MetricsEndpoint{withTLS}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := monitoringContext(t, interceptor.Funcs{}, tc.objs...)

			got, err := New().MonitoringSources(c)

			require.NoError(t, err)
			require.NotNil(t, got, "nil would clear the published sources")
			assert.Equal(t, tc.want, got.Metrics)
			assert.Empty(t, got.Connections)
			assert.Empty(t, got.Credentials)
		})
	}
}

// An error reading the cluster keeps the published sources.
func TestMonitoringSourcesReadError(t *testing.T) {
	boom := errors.New("boom")
	c := monitoringContext(t, interceptor.Funcs{
		Get: func(context.Context, client.WithWatch, client.ObjectKey, client.Object, ...client.GetOption) error {
			return boom
		},
	})

	_, err := New().MonitoringSources(c)

	require.ErrorIs(t, err, boom)
}

// The runtime publishes an endpoint only for a component type that declares
// metrics, and sets its kind from the type.
func TestProviderDeclaresEngineMetrics(t *testing.T) {
	data, err := os.ReadFile(providerSpecPath)
	require.NoError(t, err)
	var provider corev1alpha1.Provider
	require.NoError(t, yaml.Unmarshal(data, &provider))
	spec := provider.Spec
	engineType := spec.Components[common.ComponentEngine].Type

	assert.Equal(t, "postgresql", spec.ComponentTypes[engineType].Kind)
	require.NotNil(t, spec.Monitoring)
	assert.Equal(t, corev1alpha1.MetricsSourceNative, spec.Monitoring.ComponentTypes[engineType].Metrics)
	assert.NotContains(t, spec.Monitoring.ComponentTypes, spec.Components[common.ComponentProxy].Type,
		"PgBouncer's endpoint is not published yet")
	assert.Empty(t, spec.Monitoring.Integrations, "the provider renders no operator-native integration")
}

// The PodMonitor earlier releases created is deleted by name, and a cluster
// without the Prometheus Operator CRDs is not an error.
func TestDeleteLegacyPodMonitor(t *testing.T) {
	noMatch := &meta.NoKindMatchError{GroupKind: podMonitorGVK.GroupKind()}
	for _, tc := range []struct {
		name    string
		err     error
		wantErr bool
	}{
		{name: "deleted"},
		{name: "no PodMonitor CRD", err: noMatch},
		{name: "API error", err: errors.New("boom"), wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var deleted []client.Object
			c := monitoringContext(t, interceptor.Funcs{
				Delete: func(_ context.Context, _ client.WithWatch, obj client.Object, _ ...client.DeleteOption) error {
					deleted = append(deleted, obj)
					return tc.err
				},
			})

			err := deleteLegacyPodMonitor(c)

			if tc.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
			require.Len(t, deleted, 1)
			assert.Equal(t, podMonitorGVK, deleted[0].GetObjectKind().GroupVersionKind())
			assert.Equal(t, "pg-metrics", deleted[0].GetName())
			assert.Equal(t, testNamespace, deleted[0].GetNamespace())
		})
	}
}

// CloudNativePG's own PodMonitor would scrape every pod a second time, so the
// deprecated enablePodMonitor never reaches the Cluster; the rest of the
// monitoring configuration does.
func TestDisableOperatorPodMonitor(t *testing.T) {
	custom := &cnpgv1.MonitoringConfiguration{
		EnablePodMonitor: true, //nolint:staticcheck // The deprecated field under test.
		TLSConfig:        &cnpgv1.ClusterMonitoringTLSConfiguration{Enabled: true},
	}
	pg := &cnpgv1.Cluster{Spec: cnpgv1.ClusterSpec{Monitoring: custom}}

	disableOperatorPodMonitor(pg)

	assert.False(t, pg.Spec.Monitoring.EnablePodMonitor) //nolint:staticcheck // The deprecated field under test.
	assert.True(t, pg.IsMetricsTLSEnabled())
	assert.True(t, custom.EnablePodMonitor, "the decoded parameters are not modified") //nolint:staticcheck // The deprecated field under test.

	empty := &cnpgv1.Cluster{}
	disableOperatorPodMonitor(empty)
	assert.Nil(t, empty.Spec.Monitoring)
}
