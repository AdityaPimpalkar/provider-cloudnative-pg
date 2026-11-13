// Package replicaset contains custom spec types for the replicaSet topology.
//
// +k8s:openapi-gen=true
package replicaset

// ReplicaSetTopologyParameters is validated against spec.topology.parameters.
type ReplicaSetTopologyParameters struct {
	// Deprecated: Monitoring has no effect. Metrics are collected by listing
	// a MonitoringDestination in spec.monitoring.destinations: a monitoring
	// extension creates the scrape objects. It is still accepted because
	// Instances created by earlier releases carry it.
	Monitoring *MonitoringParameters `json:"monitoring,omitempty"`
}

// MonitoringParameters is deprecated and has no effect; see
// ReplicaSetTopologyParameters.Monitoring.
type MonitoringParameters struct {
	// Deprecated: Prometheus has no effect.
	Prometheus *PrometheusParameters `json:"prometheus,omitempty"`
}

// PrometheusParameters is deprecated and has no effect. Earlier releases
// created a PodMonitor named <instance>-metrics for it; the provider now
// deletes that PodMonitor.
type PrometheusParameters struct {
	// Deprecated: Enabled has no effect.
	Enabled bool `json:"enabled,omitempty"`

	// Deprecated: PodMonitorLabels has no effect.
	PodMonitorLabels map[string]string `json:"podMonitorLabels,omitempty"`
}
