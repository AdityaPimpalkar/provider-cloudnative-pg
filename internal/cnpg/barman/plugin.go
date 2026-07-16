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
