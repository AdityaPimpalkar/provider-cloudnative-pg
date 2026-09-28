package barman

import (
	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
)

func PluginConfiguration(storageName string) *cnpgv1.BackupPluginConfiguration {
	return &cnpgv1.BackupPluginConfiguration{
		Name: PluginName,
		Parameters: map[string]string{
			PluginParameterObjectStore: storageName,
		},
	}
}

// ArchiveObjectStoreName returns the ObjectStore the Cluster archives WAL to,
// which is also where the plugin writes every base backup.
func ArchiveObjectStoreName(cluster *cnpgv1.Cluster) string {
	for _, plugin := range cluster.Spec.Plugins {
		if plugin.Name == PluginName && plugin.IsEnabled() {
			return plugin.Parameters[PluginParameterObjectStore]
		}
	}
	return ""
}
