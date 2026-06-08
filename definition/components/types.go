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

	MaxConnections          *int32  `json:"maxConnections,omitempty"`
	SharedBuffers           *string `json:"sharedBuffers,omitempty"`
	EffectiveCacheSize      *string `json:"effectiveCacheSize,omitempty"`
	WorkMem                 *string `json:"workMem,omitempty"`
	MinWalSize              *string `json:"minWalSize,omitempty"`
	MaxWalSize              *string `json:"maxWalSize,omitempty"`
	LogStatement            *string `json:"logStatement,omitempty"`
	LogMinDurationStatement *int32  `json:"logMinDurationStatement,omitempty"`
	LogMinMessages          *int32  `json:"logMinMessages,omitempty"`
	LogConnections          *int32  `json:"logConnections,omitempty"`
	LogDisconnections       *int32  `json:"logDisconnections,omitempty"`
	LogRetentionTime        *int32  `json:"logRetentionTime,omitempty"`
}
