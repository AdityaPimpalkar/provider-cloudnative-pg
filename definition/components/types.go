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

// PostgresqlCustomSpec defines custom configuration for postgresql components.
// Add fields here when the postgresql component type needs custom configuration
// beyond what the base Instance spec provides.
type PostgresqlCustomSpec struct {
	ResizeInUseVolumes *bool `json:"resizeInUseVolumes,omitempty"`

	PersistentVolumeClaimTemplate *corev1.PersistentVolumeClaimSpec `json:"pvcTemplate,omitempty"`

	Affinity *cnpgv1.AffinityConfiguration `json:"affinity,omitempty"`

	PostgresConfiguration *cnpgv1.PostgresConfiguration `json:"postgresql,omitempty"`

	Managed *cnpgv1.ManagedConfiguration `json:"managed,omitempty"`
}
