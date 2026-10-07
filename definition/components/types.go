// Package components contains custom spec types for provider component types.
//
// Each struct here corresponds to a component type defined in versions.yaml
// and is converted to an OpenAPI schema during generation.
// Add fields when a component type needs custom configuration beyond
// what the base Instance spec provides.
//
// +k8s:openapi-gen=true
package components

import (
	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	corev1 "k8s.io/api/core/v1"
)

// CNPGCustomSpec defines custom configuration for postgresql components.
// Add fields here when the postgresql component type needs custom configuration
// beyond what the base Instance spec provides.
type CNPGCustomSpec struct {
	ResizeInUseVolumes *bool `json:"resizeInUseVolumes,omitempty"`

	PersistentVolumeClaimTemplate *corev1.PersistentVolumeClaimSpec `json:"pvcTemplate,omitempty"`

	Affinity *cnpgv1.AffinityConfiguration `json:"affinity,omitempty"`

	PostgresConfiguration *cnpgv1.PostgresConfiguration `json:"postgresql,omitempty"`

	Managed *cnpgv1.ManagedConfiguration `json:"managed,omitempty"`

	Bootstrap *BootstrapConfiguration `json:"bootstrap,omitempty"`

	Certificates *cnpgv1.CertificatesConfiguration `json:"certificates,omitempty"`

	Monitoring *cnpgv1.MonitoringConfiguration `json:"monitoring,omitempty"`

	// Extensions configures first-class PostgreSQL extensions managed by this provider.
	Extensions *ExtensionsSpec `json:"extensions,omitempty"`
}

// ExtensionsSpec lists optional extensions the provider can install and enable.
type ExtensionsSpec struct {
	// TimescaleDB installs the Apache-2.0 timescaledb-oss image volume and enables
	// CREATE EXTENSION timescaledb in the application database.
	// Requires PostgreSQL 18 and Kubernetes ImageVolume support (1.35+, or 1.33/1.34
	// with the ImageVolume feature gate). See:
	// https://github.com/cloudnative-pg/postgres-extensions-containers/tree/main/timescaledb-oss
	TimescaleDB *TimescaleDBSpec `json:"timescaledb,omitempty"`
}

// TimescaleDBSpec enables the TimescaleDB OSS extension.
type TimescaleDBSpec struct {
	// Enabled installs the extension image on the Cluster and creates a CNPG
	// Database resource that runs CREATE EXTENSION timescaledb. Cannot be turned
	// off once enabled.
	Enabled bool `json:"enabled"`
}

type BootstrapConfiguration struct {
	InitDB *cnpgv1.BootstrapInitDB `json:"initdb,omitempty"`
}
