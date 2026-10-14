package provider

import (
	"context"
	"testing"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	corev1alpha1 "github.com/openeverest/openeverest/v2/api/core/v1alpha1"
	"github.com/openeverest/openeverest/v2/provider-runtime/controller"

	"github.com/adityapimpalkar/provider-cloudnative-pg/definition/components"
)

func newPodMonitorTestContext(t *testing.T, topologyParameters string, objects ...client.Object) (*controller.Context, client.Client) {
	t.Helper()
	scheme := runtime.NewScheme()
	for _, add := range []func(*runtime.Scheme) error{corev1alpha1.AddToScheme, monitoringv1.AddToScheme} {
		if err := add(scheme); err != nil {
			t.Fatal(err)
		}
	}
	instance := &corev1alpha1.Instance{ObjectMeta: metav1.ObjectMeta{Name: "pg", Namespace: "ns", UID: "uid"}}
	if topologyParameters != "" {
		instance.Spec.Topology = &corev1alpha1.TopologySpec{
			Type:       "replicaSet",
			Parameters: &runtime.RawExtension{Raw: []byte(topologyParameters)},
		}
	}
	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).Build()
	return controller.NewContext(context.Background(), cl, instance, "provider-cloudnative-pg"), cl
}

func testCluster(tls bool) *cnpgv1.Cluster {
	pg := &cnpgv1.Cluster{ObjectMeta: metav1.ObjectMeta{Name: "pg", Namespace: "ns"}}
	if tls {
		pg.Spec.Monitoring = &cnpgv1.MonitoringConfiguration{TLSConfig: &cnpgv1.ClusterMonitoringTLSConfiguration{Enabled: true}}
	}
	return pg
}

func TestSyncPodMonitor(t *testing.T) {
	const enabled = `{"monitoring":{"prometheus":{"enabled":true,"podMonitorLabels":{"release":"kube-prometheus-stack","app.kubernetes.io/instance":"other"}}}}`

	tests := []struct {
		name       string
		parameters string
		tls        bool
		existing   bool
		wantExists bool
	}{
		{name: "no topology parameters"},
		{name: "disabled removes an existing PodMonitor", parameters: `{"monitoring":{"prometheus":{"enabled":false}}}`, existing: true},
		{name: "enabled", parameters: enabled, wantExists: true},
		{name: "enabled with metrics TLS", parameters: enabled, tls: true, wantExists: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var objects []client.Object
			if tt.existing {
				objects = append(objects, &monitoringv1.PodMonitor{ObjectMeta: metav1.ObjectMeta{Name: "pg-metrics", Namespace: "ns"}})
			}
			c, cl := newPodMonitorTestContext(t, tt.parameters, objects...)

			if err := syncPodMonitor(c, testCluster(tt.tls)); err != nil {
				t.Fatalf("syncPodMonitor: %v", err)
			}

			pm := &monitoringv1.PodMonitor{}
			err := cl.Get(context.Background(), client.ObjectKey{Namespace: "ns", Name: "pg-metrics"}, pm)
			if exists := err == nil; exists != tt.wantExists {
				t.Fatalf("PodMonitor exists = %t, want %t (get err: %v)", exists, tt.wantExists, err)
			}
			if !tt.wantExists {
				return
			}

			wantLabels := map[string]string{
				"app.kubernetes.io/managed-by": "everest",
				"app.kubernetes.io/instance":   "pg",
				"release":                      "kube-prometheus-stack",
			}
			for k, v := range wantLabels {
				if pm.Labels[k] != v {
					t.Errorf("label %s = %q, want %q", k, pm.Labels[k], v)
				}
			}
			if len(pm.OwnerReferences) != 1 || pm.OwnerReferences[0].Name != "pg" {
				t.Errorf("ownerReferences = %+v, want the Instance", pm.OwnerReferences)
			}
			if got := pm.Spec.Selector.MatchLabels; got["cnpg.io/cluster"] != "pg" || got["cnpg.io/podRole"] != "instance" {
				t.Errorf("selector = %v", got)
			}
			if len(pm.Spec.PodMetricsEndpoints) != 1 {
				t.Fatalf("endpoints = %+v", pm.Spec.PodMetricsEndpoints)
			}
			endpoint := pm.Spec.PodMetricsEndpoints[0]
			if endpoint.Port == nil || *endpoint.Port != "metrics" {
				t.Errorf("port = %v, want metrics", endpoint.Port)
			}
			if !tt.tls {
				if endpoint.Scheme != nil || endpoint.TLSConfig != nil {
					t.Errorf("expected plain HTTP, got scheme %v tlsConfig %+v", endpoint.Scheme, endpoint.TLSConfig)
				}
				return
			}
			if endpoint.Scheme == nil || *endpoint.Scheme != monitoringv1.SchemeHTTPS {
				t.Errorf("scheme = %v, want HTTPS", endpoint.Scheme)
			}
			if endpoint.TLSConfig == nil || endpoint.TLSConfig.CA.Secret == nil || endpoint.TLSConfig.CA.Secret.Name != "pg-ca" {
				t.Errorf("tlsConfig = %+v, want CA from secret pg-ca", endpoint.TLSConfig)
			}
		})
	}
}

func TestValidatePrometheus(t *testing.T) {
	podMonitor := &components.CNPGCustomSpec{Monitoring: &cnpgv1.MonitoringConfiguration{EnablePodMonitor: true}} //nolint:staticcheck // the conflict being rejected

	tests := []struct {
		name       string
		parameters string
		custom     *components.CNPGCustomSpec
		wantErr    bool
	}{
		{name: "not set", custom: podMonitor},
		{name: "disabled with the CloudNativePG PodMonitor", parameters: `{"monitoring":{"prometheus":{"enabled":false}}}`, custom: podMonitor},
		{name: "enabled", parameters: `{"monitoring":{"prometheus":{"enabled":true,"podMonitorLabels":{"release":"prom"}}}}`, custom: &components.CNPGCustomSpec{}},
		{name: "enabled with the CloudNativePG PodMonitor", parameters: `{"monitoring":{"prometheus":{"enabled":true}}}`, custom: podMonitor, wantErr: true},
		{name: "invalid label", parameters: `{"monitoring":{"prometheus":{"enabled":true,"podMonitorLabels":{"bad key!":"x"}}}}`, custom: &components.CNPGCustomSpec{}, wantErr: true},
		{name: "malformed parameters", parameters: `{"monitoring":{"prometheus":{"enabled":"yes"}}}`, custom: &components.CNPGCustomSpec{}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, _ := newPodMonitorTestContext(t, tt.parameters)
			err := validatePrometheus(c, tt.custom)
			if (err != nil) != tt.wantErr {
				t.Fatalf("validatePrometheus() error = %v, wantErr %t", err, tt.wantErr)
			}
		})
	}
}
