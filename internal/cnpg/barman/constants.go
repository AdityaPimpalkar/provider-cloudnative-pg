// Package barman holds shared constants for the CNPG-I Barman Cloud Plugin
// backup path. See https://cloudnative-pg.github.io/plugin-barman-cloud/.
package barman

const (
	// KindBackup is the kind of the backup resource.
	KindBackup = "Backup"

	// PluginName is the CNPG-I plugin identifier referenced on Cluster.spec.plugins,
	// Backup.spec.pluginConfiguration, and ScheduledBackup.spec.pluginConfiguration.
	PluginName = "barman-cloud.cloudnative-pg.io"

	// PluginParameterObjectStore is the Cluster plugin parameter that names the
	// barmancloud.cnpg.io ObjectStore resource in the same namespace.
	PluginParameterObjectStore = "barmanObjectName"

	// EndpointCASecretSuffix is appended to the logical storage name to form the
	// Secret that ObjectStore.spec.configuration.endpointCA references.
	// Optional: create it in the Instance namespace (key EndpointCAKey) only for
	// HTTPS endpoints with a self-signed or private CA.
	EndpointCASecretSuffix = "-endpoint-ca"

	// EndpointCAKey is the Secret data key that must hold the S3 endpoint CA PEM.
	EndpointCAKey = "ca.crt"
)
