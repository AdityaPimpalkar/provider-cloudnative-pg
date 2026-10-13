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
// Image-volume extensions come from the CloudNativePG extension images
// (https://github.com/cloudnative-pg/postgres-extensions-containers) and need
// PostgreSQL 18 plus Kubernetes ImageVolume support (1.35+, or 1.33/1.34 with
// the ImageVolume feature gate); adding one rolls the instance pods.
type ExtensionsSpec struct {
	// TimescaleDB (image volume, Apache-2.0 edition) runs CREATE EXTENSION
	// timescaledb in the application database. Cannot be turned off once enabled.
	TimescaleDB *ExtensionToggle `json:"timescaledb,omitempty"`

	// PgVector runs CREATE EXTENSION vector in the application database. It ships
	// in the CloudNativePG standard images (PostgreSQL 14+), so no image change or
	// restart is needed. Cannot be turned off once enabled.
	PgVector *ExtensionToggle `json:"pgvector,omitempty"`

	// PgAudit enables audit logging. It ships in the standard images; the operator
	// preloads and creates it in every database while a pgaudit.* parameter is
	// set, so the provider defaults pgaudit.log to "ddl, role". Can be turned off.
	PgAudit *ExtensionToggle `json:"pgaudit,omitempty"`

	// PostGIS (image volume) runs CREATE EXTENSION postgis in the application
	// database. Cannot be turned off once enabled.
	PostGIS *ExtensionToggle `json:"postgis,omitempty"`

	// PgRouting (image volume) runs CREATE EXTENSION pgrouting in the application
	// database. Requires postgis. Cannot be turned off once enabled.
	PgRouting *ExtensionToggle `json:"pgrouting,omitempty"`

	// PgIVM (image volume) preloads pg_ivm and runs CREATE EXTENSION pg_ivm in the
	// application database. Cannot be turned off once enabled.
	PgIVM *ExtensionToggle `json:"pgivm,omitempty"`

	// Wal2JSON (image volume) makes the wal2json logical decoding output plugin
	// available to replication slots. There is no CREATE EXTENSION.
	Wal2JSON *ExtensionToggle `json:"wal2json,omitempty"`
}

// ExtensionToggle switches a first-class extension on.
type ExtensionToggle struct {
	Enabled bool `json:"enabled"`
}

type BootstrapConfiguration struct {
	InitDB *cnpgv1.BootstrapInitDB `json:"initdb,omitempty"`
}
