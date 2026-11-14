package provider

import (
	"encoding/json"
	"fmt"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	"github.com/cloudnative-pg/cloudnative-pg/pkg/certs"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/util/validation/field"

	commonv1alpha1 "github.com/openeverest/openeverest/v2/api/common/v1alpha1"
	corev1alpha1 "github.com/openeverest/openeverest/v2/api/core/v1alpha1"
	"github.com/openeverest/openeverest/v2/provider-runtime/controller"

	"github.com/adityapimpalkar/provider-cloudnative-pg/definition/components"
	replicaset "github.com/adityapimpalkar/provider-cloudnative-pg/definition/topologies/replicaSet"
	"github.com/adityapimpalkar/provider-cloudnative-pg/internal/common"
)

const (
	// metricsPortName is the container port CloudNativePG gives the exporter
	// of the instance manager (9187) on every instance pod.
	metricsPortName = "metrics"
	metricsPath     = "/metrics"
)

var _ controller.MonitoringSourcesProvider = (*Provider)(nil)

// MonitoringSources implements controller.MonitoringSourcesProvider. The
// CloudNativePG instance manager serves OpenMetrics on every PostgreSQL pod
// (metrics: Native in definition/monitoring.yaml), so no exporter is switched
// on or off for a bound class. The endpoint is published once CloudNativePG
// has initialized the cluster, and stays published while later changes roll
// the pods, like the connection details. Monitoring extensions turn it into
// scrape objects; other scrapers can read it from the Instance status.
func (p *Provider) MonitoringSources(c *controller.Context) (*corev1alpha1.MonitoringSources, error) {
	pg := &cnpgv1.Cluster{}
	found, err := c.Exists(pg, c.Name())
	if err != nil {
		return nil, fmt.Errorf("get CloudNativePG cluster: %w", err)
	}
	sources := &corev1alpha1.MonitoringSources{}
	if !found || !pg.IsInitialized() {
		return sources, nil
	}
	sources.Metrics = []corev1alpha1.MetricsEndpoint{engineMetricsEndpoint(c.Name(), pg)}
	return sources, nil
}

// engineMetricsEndpoint is the instance manager's endpoint on the PostgreSQL
// pods, over https when the cluster serves metrics with TLS.
func engineMetricsEndpoint(instance string, pg *cnpgv1.Cluster) corev1alpha1.MetricsEndpoint {
	endpoint := corev1alpha1.MetricsEndpoint{
		Component: common.ComponentEngine,
		PodSelector: map[string]string{
			controller.InstanceLabel:  instance,
			controller.ComponentLabel: common.ComponentEngine,
		},
		Port:   intstr.FromString(metricsPortName),
		Path:   metricsPath,
		Scheme: "http",
	}
	if pg.IsMetricsTLSEnabled() {
		// The TLS settings of the PodMonitor CloudNativePG itself builds: the
		// instance certificates are not issued for the pod addresses.
		endpoint.Scheme = "https"
		endpoint.TLS = &corev1alpha1.MetricsEndpointTLS{
			TLSConfig: commonv1alpha1.TLSConfig{
				CA: &commonv1alpha1.CABundleSource{SecretKeyRef: &commonv1alpha1.SecretKeyRef{
					Name: pg.GetServerCASecretName(),
					Key:  certs.CACertKey,
				}},
				InsecureSkipVerify: true,
			},
			ServerName: pg.GetServiceReadWriteName(),
		}
	}
	return endpoint
}

// disableOperatorPodMonitor turns off the PodMonitor CloudNativePG creates for
// the deprecated monitoring.enablePodMonitor parameter, which CloudNativePG
// then deletes: a monitoring extension scrapes the published endpoint, and
// both would scrape every pod twice.
func disableOperatorPodMonitor(pg *cnpgv1.Cluster) {
	if pg.Spec.Monitoring != nil {
		monitoring := pg.Spec.Monitoring.DeepCopy()
		//nolint:staticcheck // Turning off the deprecated field.
		monitoring.EnablePodMonitor = false
		pg.Spec.Monitoring = monitoring
	}
}

// podMonitorGVK is the Prometheus Operator PodMonitor, deleted only as
// metadata so the provider needs neither its types nor its CRD.
var podMonitorGVK = schema.GroupVersionKind{Group: "monitoring.coreos.com", Version: "v1", Kind: "PodMonitor"}

// legacyPodMonitorName is the name of the PodMonitor earlier releases created.
func legacyPodMonitorName(instance string) string {
	return instance + "-metrics"
}

// deleteLegacyPodMonitor removes the PodMonitor that releases before the
// monitoring contract created for spec.topology.parameters.monitoring.prometheus;
// a monitoring extension now creates the scrape objects from the published
// sources. Clusters without the Prometheus Operator CRDs are skipped.
// TODO: drop once no supported release created PodMonitors.
func deleteLegacyPodMonitor(c *controller.Context) error {
	pm := &metav1.PartialObjectMetadata{ObjectMeta: metav1.ObjectMeta{
		Name:      legacyPodMonitorName(c.Name()),
		Namespace: c.Namespace(),
	}}
	pm.SetGroupVersionKind(podMonitorGVK)
	if err := c.Delete(pm); err != nil && !meta.IsNoMatchError(err) {
		return fmt.Errorf("delete legacy PodMonitor: %w", err)
	}
	return nil
}

// monitoringWarnings reports the deprecated monitoring parameters an Instance
// still sets: they have no effect, and metrics are collected through
// spec.monitoring.destinations.
func monitoringWarnings(instance *corev1alpha1.Instance, custom *components.CNPGCustomSpec) []string {
	var warnings []string
	if raw := instance.GetTopologyParameters(); raw != nil && raw.Raw != nil {
		var params replicaset.ReplicaSetTopologyParameters
		// The schema check reports parameters that don't decode.
		//nolint:staticcheck // Reporting the deprecated parameter.
		if err := json.Unmarshal(raw.Raw, &params); err == nil &&
			params.Monitoring != nil && params.Monitoring.Prometheus != nil && params.Monitoring.Prometheus.Enabled {
			warnings = append(warnings, field.NewPath("spec", "topology", "parameters", "monitoring", "prometheus").String()+
				": deprecated and ignored; the provider no longer creates a PodMonitor. "+
				"List a MonitoringDestination in spec.monitoring.destinations to collect metrics")
		}
	}
	//nolint:staticcheck // Reporting the deprecated field.
	if custom.Monitoring != nil && custom.Monitoring.EnablePodMonitor {
		warnings = append(warnings, engineParams.Child("monitoring", "enablePodMonitor").String()+
			": ignored; CloudNativePG creates no PodMonitor. "+
			"List a MonitoringDestination in spec.monitoring.destinations to collect metrics")
	}
	return warnings
}
