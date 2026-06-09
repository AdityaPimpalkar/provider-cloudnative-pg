// Package components contains custom spec types for provider component types.
//
// Each struct here corresponds to a component type defined in versions.yaml
// and is converted to an OpenAPI schema during generation.
// Add fields when a component type needs custom configuration beyond
// what the base Instance spec provides.
//
// +k8s:openapi-gen=true
package components

// PostgresqlCustomSpec defines custom configuration for postgresql components.
// Add fields here when the postgresql component type needs custom configuration
// beyond what the base Instance spec provides.
type PostgresqlCustomSpec struct {
	// ResizeInUseVolumes controls whether existing PVCs are resized when storage grows.
	// Defaults to true when unset.
	ResizeInUseVolumes *bool `json:"resizeInUseVolumes,omitempty"`

	PostgreSQLParameters *PostgreSQLParameters `json:"postgresql,omitempty"`
}

type PostgreSQLParameters struct {
	Parameters map[string]string `json:"parameters,omitempty"`
}
