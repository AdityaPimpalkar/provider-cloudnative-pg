# Provider Definition

This directory contains all the files you edit when creating or maintaining an
OpenEverest provider. Everything related to the provider's **identity**,
**versions**, **topologies**, **UI**, and **custom schemas** lives here.

> **For a complete development guide, see [PROVIDER_DEVELOPMENT.md](github.com/openeverest/provider-sdk/blob/main/PROVIDER_DEVELOPMENT.md).**

## Directory Layout

```
definition/
├── provider.yaml                    # Provider name, component→type mapping, component type kinds
├── versions.yaml                    # Component types and their version/image catalog
├── monitoring.yaml                  # What each component type exposes to monitoring
├── types.go                         # Shared types (TopologyType, GlobalConfig)
├── components/
│   └── types.go                     # Component custom spec types
└── topologies/
    └── /
        ├── topology.yaml            # Topology config + UI schema (co-located)
        └── types.go                 # Topology-specific config types
```

## Quick Reference

| File | Purpose | When to edit |
|------|---------|--------------|
| `provider.yaml` | Names the provider, maps logical component names to types, and declares each type's `kind` (matched by monitoring classes). | When adding/removing a component. |
| `monitoring.yaml` | The monitoring contract: which component types serve metrics (`Native` or `Exporter`). | When a component starts or stops publishing metrics. |
| `versions.yaml` | Lists available versions and container images per component type. | When adding operator releases. |
| `types.go` | Shared Go types used across the provider. | When adding provider-wide types. |
| `components/types.go` | Go structs for component custom specs. | When a component needs custom config. |
| `topologies/<name>/topology.yaml` | Defines a deployment topology and its UI rendering. | When adding a topology or changing its UI. |
| `topologies/<name>/types.go` | Go struct for topology-specific custom config. | When a topology needs custom config fields. |

## Adding Components and Topologies

Use the provider-sdk CLI to add components and topologies:

```bash
# Add a new component
provider-sdk add component --name backupAgent --type backup

# Add a new topology
provider-sdk add topology --name replicaSet
```

## How It All Fits Together

```
definition/ files
       │
       ▼
provider-sdk generate  ──▶  charts/<name>/generated/provider-spec.yaml
       │
       ▼
Helm chart template    ──▶  .Files.Get "generated/provider-spec.yaml"
```

Run `make generate` to regenerate all generated files.
