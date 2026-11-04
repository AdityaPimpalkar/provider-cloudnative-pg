package barman

import (
	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	barmancloudv1 "github.com/cloudnative-pg/plugin-barman-cloud/api/v1"
	"github.com/openeverest/openeverest/v2/provider-runtime/controller"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
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

func EnsureObjectStoreAvailable(c *controller.Context) error {
	err := c.Get(&barmancloudv1.ObjectStore{}, ".")
	if err == nil || apierrors.IsNotFound(err) {
		return nil
	}
	if bce := ObjectStoreCRDMissing(err); bce != nil {
		return bce
	}
	return nil
}

func ObjectStoreCRDMissing(err error) *controller.BackupConfigError {
	if !meta.IsNoMatchError(err) {
		return nil
	}
	return &controller.BackupConfigError{
		Reason: ReasonBarmanPluginMissing,
		Message: "Barman Cloud Plugin is not installed (ObjectStore CRD missing). " +
			"Keep chart value plugin-barman-cloud.enabled=true (requires cert-manager), " +
			"or install the plugin separately.",
	}
}
