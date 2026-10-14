package provider

import (
	"fmt"
	"maps"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	"github.com/cloudnative-pg/cloudnative-pg/pkg/certs"
	"github.com/cloudnative-pg/cloudnative-pg/pkg/utils"
	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1validation "k8s.io/apimachinery/pkg/apis/meta/v1/validation"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/validation/field"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/openeverest/openeverest/v2/provider-runtime/controller"

	"github.com/adityapimpalkar/provider-cloudnative-pg/definition/components"
	replicaset "github.com/adityapimpalkar/provider-cloudnative-pg/definition/topologies/replicaSet"
)

const (
	podMonitorSuffix = "-metrics"

	// metricsPortName is the container port CloudNativePG gives the exporter
	// of the instance manager (9187) on every instance pod.
	metricsPortName = "metrics"
)

func podMonitorName(instance string) string {
	return instance + podMonitorSuffix
}

// prometheusParameters returns spec.topology.parameters.monitoring.prometheus,
// or the zero value (disabled) when it is not set.
func prometheusParameters(c *controller.Context) (replicaset.PrometheusParameters, error) {
	raw := c.Instance().GetTopologyParameters()
	if raw == nil || raw.Raw == nil {
		return replicaset.PrometheusParameters{}, nil
	}
	var params replicaset.ReplicaSetTopologyParameters
	if err := c.DecodeTopologyParameters(&params); err != nil {
		return replicaset.PrometheusParameters{}, fmt.Errorf("failed to decode topology parameters: %w", err)
	}
	if params.Monitoring == nil || params.Monitoring.Prometheus == nil {
		return replicaset.PrometheusParameters{}, nil
	}
	return *params.Monitoring.Prometheus, nil
}

func validatePrometheus(c *controller.Context, custom *components.CNPGCustomSpec) error {
	prometheus, err := prometheusParameters(c)
	if err != nil {
		return err
	}
	if !prometheus.Enabled {
		return nil
	}
	labelsPath := field.NewPath("spec", "topology", "parameters", "monitoring", "prometheus", "podMonitorLabels")
	if errs := metav1validation.ValidateLabels(prometheus.PodMonitorLabels, labelsPath); len(errs) > 0 {
		return errs.ToAggregate()
	}
	//nolint:staticcheck // Rejecting the deprecated field, which would scrape every pod twice.
	if custom.Monitoring != nil && custom.Monitoring.EnablePodMonitor {
		return fmt.Errorf("monitoring.prometheus.enabled already creates a PodMonitor; unset spec.components.engine.parameters.monitoring.enablePodMonitor")
	}
	return nil
}

// syncPodMonitor creates the PodMonitor that scrapes the cluster's instance
// pods, or removes it once Prometheus monitoring is turned off. Without the
// Prometheus Operator CRDs the PodMonitor is skipped.
func syncPodMonitor(c *controller.Context, pg *cnpgv1.Cluster) error {
	prometheus, err := prometheusParameters(c)
	if err != nil {
		return err
	}

	if !prometheus.Enabled {
		err := c.Delete(&monitoringv1.PodMonitor{ObjectMeta: c.ObjectMeta(podMonitorName(c.Name()))})
		if err != nil && !meta.IsNoMatchError(err) {
			return fmt.Errorf("failed to delete PodMonitor: %w", err)
		}
		return nil
	}

	err = c.Apply(buildPodMonitor(c, pg, prometheus.PodMonitorLabels))
	if meta.IsNoMatchError(err) {
		log.FromContext(c.Context()).Info("PodMonitor CRD not installed, skipping the PodMonitor; install the Prometheus Operator", "instance", c.Name())
		return nil
	}
	if err != nil {
		return fmt.Errorf("failed to apply PodMonitor: %w", err)
	}
	return nil
}

// buildPodMonitor builds the PodMonitor for the cluster. The extra labels
// never override the managed ones that tie it to the Instance.
func buildPodMonitor(c *controller.Context, pg *cnpgv1.Cluster, extraLabels map[string]string) *monitoringv1.PodMonitor {
	objectMeta := c.ObjectMeta(podMonitorName(c.Name()))
	labels := maps.Clone(extraLabels)
	if labels == nil {
		labels = map[string]string{}
	}
	maps.Copy(labels, objectMeta.Labels)
	objectMeta.Labels = labels

	endpoint := monitoringv1.PodMetricsEndpoint{Port: new(metricsPortName)}
	if pg.IsMetricsTLSEnabled() {
		// Same TLS settings as the PodMonitor CloudNativePG itself builds.
		endpoint.Scheme = new(monitoringv1.SchemeHTTPS)
		endpoint.TLSConfig = &monitoringv1.SafeTLSConfig{
			CA: monitoringv1.SecretOrConfigMap{
				Secret: &corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{Name: pg.GetServerCASecretName()},
					Key:                  certs.CACertKey,
				},
			},
			ServerName: new(pg.GetServiceReadWriteName()),
			// The instance certificates are not issued for the pod addresses.
			InsecureSkipVerify: new(true),
		}
	}

	return &monitoringv1.PodMonitor{
		ObjectMeta: objectMeta,
		Spec: monitoringv1.PodMonitorSpec{
			Selector: metav1.LabelSelector{MatchLabels: map[string]string{
				utils.ClusterLabelName: pg.Name,
				utils.PodRoleLabelName: string(utils.PodRoleInstance),
			}},
			PodMetricsEndpoints: []monitoringv1.PodMetricsEndpoint{endpoint},
		},
	}
}
