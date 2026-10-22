package common

const (
	PostgresDefaultVersion = "18.6"

	// DefaultExtensionCatalog is the extensions ClusterImageCatalog rendered by
	// the Helm chart, unless EXTENSION_CATALOG names another one.
	DefaultExtensionCatalog = "provider-cloudnative-pg-extensions"
)
