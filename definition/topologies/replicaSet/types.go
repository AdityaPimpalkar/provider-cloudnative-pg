// Package replicaset contains custom spec types for the replicaSet topology.
//
// +k8s:openapi-gen=true
package replicaset

// ReplicaSetTopologyParameters is validated against spec.topology.parameters.
type ReplicaSetTopologyParameters struct {
	Monitoring *MonitoringParameters `json:"monitoring,omitempty"`
}

// MonitoringParameters configures how the instance's metrics are collected.
type MonitoringParameters struct {
	Prometheus *PrometheusParameters `json:"prometheus,omitempty"`
}

// PrometheusParameters configures scraping of the CloudNativePG metrics
// exporter (port "metrics" on every instance pod) by the Prometheus Operator.
type PrometheusParameters struct {
	// Enabled creates a PodMonitor named <instance>-metrics; disabling it
	// removes the PodMonitor.
	Enabled bool `json:"enabled,omitempty"`

	// PodMonitorLabels are added to the PodMonitor so that a Prometheus
	// podMonitorSelector picks it up, e.g. release: kube-prometheus-stack.
	PodMonitorLabels map[string]string `json:"podMonitorLabels,omitempty"`
}
