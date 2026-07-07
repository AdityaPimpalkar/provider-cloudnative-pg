// Package barman holds shared constants for the CNPG-I Barman Cloud Plugin
// backup path. See https://cloudnative-pg.github.io/plugin-barman-cloud/.
package barman

import (
	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
)

const (
	// PluginName is the CNPG-I plugin identifier referenced on Cluster.spec.plugins,
	// Backup.spec.pluginConfiguration, and ScheduledBackup.spec.pluginConfiguration.
	PluginName = "barman-cloud.cloudnative-pg.io"

	// PluginParameterObjectStore is the Cluster plugin parameter that names the
	// barmancloud.cnpg.io ObjectStore resource in the same namespace.
	PluginParameterObjectStore = "barmanObjectName"

	// BackupClassName is the OpenEverest BackupClass used for plugin-managed backups.
	BackupClassName = "cnpg-barman-plugin"
)

// PluginConfiguration returns the standard pluginConfiguration stanza for
// Backup and ScheduledBackup resources.
func PluginConfiguration() *cnpgv1.BackupPluginConfiguration {
	return &cnpgv1.BackupPluginConfiguration{
		Name: PluginName,
	}
}
