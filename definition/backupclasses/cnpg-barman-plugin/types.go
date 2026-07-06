// Package cnpgbarmanplugin contains schema-bearing Go types for the
// "cnpg-barman-plugin" BackupClass.
//
// +k8s:openapi-gen=true
package cnpgbarmanplugin

// CnpgBarmanPluginBackupConfig is validated against Backup.spec.config and
// per-schedule config. Maps to cnpgv1.BackupSpec fields for plugin backups.
type CnpgBarmanPluginBackupConfig struct {
	// Target overrides the cluster default backup target for this run.
	// +kubebuilder:validation:Enum=primary;prefer-standby
	Target string `json:"target,omitempty"`
}
