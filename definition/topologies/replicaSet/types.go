// Package replicaset contains custom spec types for the replicaSet topology.
//
// Add fields to ReplicaSetTopologyConfig and reference it via configSchema in
// topology.yaml when this topology needs custom configuration.
//
// +k8s:openapi-gen=true
package replicaset

// ReplicaSetTopologyConfig defines configuration for the replicaSet topology.
// Add fields here when the replicaSet topology needs custom configuration
// beyond what the base Instance spec provides.
//
// Example:
//   type ReplicaSetTopologyConfig struct {
//       NumShards int32 `json:"numShards,omitempty"`
//   }
//
// Then reference it in topology.yaml:
//   config:
//     configSchema: ReplicaSetTopologyConfig
type ReplicaSetTopologyConfig struct{}
