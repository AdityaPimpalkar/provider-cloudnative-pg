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
	// PriorityClassName sets the Kubernetes PriorityClass of the instance pods.
	// The PriorityClass must exist; changing it rolls the instance pods.
	PriorityClassName string `json:"priorityClassName,omitempty"`
	// RequireTLS rejects client connections that do not use TLS by placing a
	// "hostnossl all all all reject" rule before the user's pg_hba rules.
	// Unset or false keeps CNPG's default, which also accepts unencrypted
	// connections. Applied by CNPG with a configuration reload, no restart.
	RequireTLS *bool `json:"requireTLS,omitempty"`

	// PgHBA declares which clients may connect to which databases as which
	// users: pg_hba.conf records, one per line, such as
	//
	//   hostssl orders orders_app 10.0.0.0/8 scram-sha-256
	//   host    orders all        all        reject
	//
	// Blank lines and # comments are ignored. Records are checked in order,
	// after the operator's own rules (replication, pooler, metrics) and before
	// the default rule that lets every user in with a password, so end with a
	// reject record to allow only what is listed. Records may be host, hostssl
	// or hostnossl with the methods scram-sha-256, md5, cert, ldap, radius or
	// reject; trust is not allowed. Cannot be combined with postgresql.pg_hba.
	// Applied by CNPG with a configuration reload, no restart.
	PgHBA string `json:"pgHBA,omitempty"`

	// WALStorage provisions a separate WAL PVC per PostgreSQL instance using
	// CNPG's native storage configuration. Once enabled it cannot be removed.
	WALStorage *cnpgv1.StorageConfiguration `json:"walStorage,omitempty"`

	ResizeInUseVolumes *bool `json:"resizeInUseVolumes,omitempty"`

	PersistentVolumeClaimTemplate *corev1.PersistentVolumeClaimSpec `json:"pvcTemplate,omitempty"`

	Affinity *cnpgv1.AffinityConfiguration `json:"affinity,omitempty"`

	PostgresConfiguration *cnpgv1.PostgresConfiguration `json:"postgresql,omitempty"`

	Managed *cnpgv1.ManagedConfiguration `json:"managed,omitempty"`

	Bootstrap *BootstrapConfiguration `json:"bootstrap,omitempty"`

	Certificates *cnpgv1.CertificatesConfiguration `json:"certificates,omitempty"`

	Monitoring *cnpgv1.MonitoringConfiguration `json:"monitoring,omitempty"`

	// ReplicationSlots configures CNPG replication slots: the physical slots of
	// the HA standbys, synchronization of user-defined physical slots to the
	// standbys, and failover of logical decoding slots
	// (highAvailability.synchronizeLogicalDecoding). With synchronizeLogicalDecoding
	// on PostgreSQL 17+, the provider turns on hot_standby_feedback and
	// sync_replication_slots unless they are set explicitly.
	// highAvailability.slotPrefix cannot change while HA slots are enabled.
	ReplicationSlots *cnpgv1.ReplicationSlotsConfiguration `json:"replicationSlots,omitempty"`

	// Extensions configures first-class PostgreSQL extensions managed by this provider.
	Extensions *ExtensionsSpec `json:"extensions,omitempty"`
}

// ExtensionsSpec lists optional extensions the provider can install and enable.
// Image-volume extensions are mounted from the images in the provider's
// extensions ClusterImageCatalog (Helm values extensionCatalog) and need
// PostgreSQL 18 plus Kubernetes ImageVolume support (1.35+, or 1.33/1.34 with
// the ImageVolume feature gate); adding one rolls the instance pods.
type ExtensionsSpec struct {
	// TimescaleDB (image volume, Apache-2.0 edition) runs CREATE EXTENSION
	// timescaledb in the application database. Cannot be turned off once enabled.
	TimescaleDB *ExtensionToggle `json:"timescaledb,omitempty"`

	// TimescaleDBCommunity (image volume, Community Edition) is TimescaleDB with
	// the Timescale License (TSL) components such as compression and continuous
	// aggregates. The TSL is not open source: it allows internal use but
	// prohibits offering TimescaleDB to third parties as a database service.
	// Requires PostgreSQL 18.6+; cannot be combined with timescaledb. Can only
	// be enabled at creation; cannot be turned off.
	TimescaleDBCommunity *ExtensionToggle `json:"timescaledbCommunity,omitempty"`

	// PgVector runs CREATE EXTENSION vector in the application database. It ships
	// in the CloudNativePG standard images (PostgreSQL 14+), so no image change or
	// restart is needed. Cannot be turned off once enabled.
	PgVector *ExtensionToggle `json:"pgvector,omitempty"`

	// PgAudit enables audit logging. It ships in the standard images; the operator
	// preloads and creates it in every database while a pgaudit.* parameter is
	// set, so the provider defaults pgaudit.log to "ddl, role". Can be turned off.
	PgAudit *ExtensionToggle `json:"pgaudit,omitempty"`

	// PgStatStatements tracks planning and execution statistics of all SQL
	// statements (pg_stat_statements) and exports the top statements as
	// metrics. It ships in the standard images; the operator preloads and
	// creates it in every database while a pg_stat_statements.* parameter is
	// set, so the provider defaults pg_stat_statements.track to "top". Enabled
	// by default on new instances; instances created without it keep it off
	// until it is enabled. Turning it on or off restarts the instances.
	PgStatStatements *ExtensionToggle `json:"pgStatStatements,omitempty"`

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

	// AGE (image volume, Apache AGE graph database) preloads age, runs CREATE
	// EXTENSION age in the application database and grants its owner USAGE on
	// ag_catalog. Can only be enabled at creation; cannot be turned off.
	AGE *ExtensionToggle `json:"age,omitempty"`

	// PgCron (image volume) preloads pg_cron, sets cron.database_name to the
	// application database and cron.use_background_workers to on, runs CREATE
	// EXTENSION pg_cron there and grants its owner USAGE on the cron schema, so
	// the owner can schedule jobs. Jobs run on the primary only. Can only be
	// enabled at creation; cannot be turned off.
	PgCron *ExtensionToggle `json:"pgcron,omitempty"`

	// PgRepack (image volume) runs CREATE EXTENSION pg_repack in the
	// application database. The pg_repack client is in the instance pods at
	// /extensions/pg-repack/bin/pg_repack and runs as the superuser. Cannot be
	// turned off once enabled.
	PgRepack *ExtensionToggle `json:"pgrepack,omitempty"`
}

// ExtensionToggle switches a first-class extension on.
type ExtensionToggle struct {
	Enabled bool `json:"enabled"`
}

type BootstrapConfiguration struct {
	InitDB *cnpgv1.BootstrapInitDB `json:"initdb,omitempty"`
}

// PgBouncerParameters is the proxy component's configuration.
// The Pooler type is always rw. Instance count and pod resources come from
// the component's replicas and resources.
type PgBouncerParameters struct {
	// PoolMode is the PgBouncer pool mode. CloudNativePG accepts session and
	// transaction. Empty selects the operator default, session.
	PoolMode string `json:"poolMode,omitempty"`
}
