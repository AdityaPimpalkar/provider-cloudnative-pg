# provider-cloudnative-pg

An [OpenEverest](https://github.com/openeverest) provider.


> ⚠️🚧 THIS PROVIDER PLUGIN IS IN DEVELOPMENT/TESTING. DO NOT USE IN PRODUCTION!! 🚧⚠️

## Prerequisites

Go 1.26+
A Kubernetes cluster (k3d, kind, or remote)
[OpenEverest CRDs](https://github.com/openeverest/openeverest) installed (`make install-crds`)
[CloudNativePG operator](https://cloudnative-pg.io/documentation/current/installation_upgrade/) (`make install-cloudnative-pg`)


## CloudNativePG Coverage

This provider translates OpenEverest `Instance` CRs into [CloudNativePG](https://cloudnative-pg.io/) `Cluster` resources. CloudNativePG manages the full PostgreSQL lifecycle — HA replication, automated failover, rolling updates, backup/PITR, connection pooling, and more — but this provider currently exposes only a subset of that surface.

The sections below list what is implemented today versus what is still needed for testing.

## Feature Status

### Implemented

These features are implemented in `internal/provider/provider.go` and can be used for dev/testing today.

- [x] **Provider reconciliation** — `Validate`, `Sync`, and `Status` lifecycle hooks
- [x] **CloudNativePG Cluster management** — creates and updates `postgresql.cnpg.io/v1` Cluster resources; watches owned Clusters for status updates
- [x] **Instance validation** — requires `engine` component with replicas (≥ 1), storage size, and CPU/memory requests and limits (requests must equal limits)
- [x] **HA replica count** — `engine.replicas` → `Cluster.spec.instances`
- [x] **Storage** — `engine.storage.size` → PVC size; optional `engine.storage.storageClass`; `customSpec.resizeInUseVolumes` (defaults to `true`)
- [x] **Resource limits** — `engine.resources` → `Cluster.spec.resources` (Guaranteed QoS enforced at validation)
- [x] **Pod anti-affinity** — `Cluster.spec.affinity.enablePodAntiAffinity` defaults to `true` (CNPG default `topologyKey` applies)
- [x] **Image resolution** — explicit `engine.image` override, version catalog lookup, or default image from provider spec
- [x] **PostgreSQL version catalog** — versions 14.23, 15.18, 16.14, 17.10 (default), and 18.4 (`definition/versions.yaml`)
- [x] **Connection details** — reports `postgresql` URI with host (`-rw` service), port, credentials, and database when the CNPG cluster is ready
- [x] **UI schema** — `replicaSet` topology exposes version, node count, CPU, memory, and disk in `definition/topologies/replicaSet/topology.yaml`

### Pending

These are commonly used CloudNativePG features that are **not** implemented yet. The provider is suitable for dev/testing but not production-ready until the critical items below are addressed.

- [ ] **Backup and disaster recovery** — no `ScheduledBackup` CR or `Cluster.spec.backup.barmanObjectStore` (continuous WAL archiving, PITR, retention policy)
- [ ] **Bootstrap / initial database** — no `Cluster.spec.bootstrap.initdb` (custom database name, owner, or credentials secret); connection details rely on CNPG defaults (`app` / `postgres`)
- [ ] **PostgreSQL tuning parameters** — `PostgresqlCustomSpec` fields (`maxConnections`, `sharedBuffers`, `effectiveCacheSize`, `workMem`, WAL/log settings) are defined in `definition/components/types.go` but only `resizeInUseVolumes` is mapped to the Cluster spec
- [ ] **Secrets RBAC** — `Status()` reads credential Secrets but the generated ClusterRole (`config/rbac/role.yaml`) does not grant `secrets` access; the kubebuilder marker in `internal/provider/rbac.go` is commented out
- [ ] **CloudNativePG operator packaging** — the CNPG operator is an external prerequisite; it is not installed or bundled by this Helm chart (`charts/provider-cloudnative-pg/Chart.yaml` TODO)
- [ ] **Connection pooling** — no `Pooler` CR (PgBouncer) support
- [ ] **Monitoring** — no `Cluster.spec.monitoring.enablePodMonitor` or CNPG condition surfacing in Instance status
- [ ] **Read service endpoints** — `Status()` exposes only the `-rw` (primary) service; `-ro` (replicas) and `-r` (any) endpoints are not reported
- [ ] **Anti-affinity topology key** — `topologyKey` (e.g. `topology.kubernetes.io/zone` vs `kubernetes.io/hostname`) is not configurable
- [ ] **TLS / certificates** — no `serverTLSSecret` or custom certificate configuration
- [ ] **Superuser access control** — no `enableSuperuserAccess` or `superuserSecret` configuration
- [ ] **Custom cleanup logic** — `Cleanup()` is a no-op (owned Cluster resources are garbage-collected via owner references; no explicit cleanup for external resources such as backups)
- [ ] **RBAC marker cleanup** — placeholder kubebuilder markers remain in `Validate()`; CNPG cluster rules exist in committed RBAC but active source markers are only in comments in `internal/provider/rbac.go`
- [ ] **Automated tests** — no `*_test.go` files in the repository
- [ ] **CI integration tests** — `.github/workflows/test.yaml` exists but the `test/` directory is absent, CNPG operator install is a TODO, and `make test-integration` cannot run

#### Planned

- [ ] **Primary update strategy** — no `primaryUpdateStrategy` or `primaryUpdateMethod` configuration
- [ ] **Managed databases** — no `Database` CR support for per-tenant database provisioning
- [ ] **Logical replication** — no `Publication` or `Subscription` CRs
- [ ] **Replica clusters (DR)** — no cross-region disaster recovery cluster configuration
- [ ] **Extensions / init SQL** — no `postInitSQL` or `postInitApplicationSQL` support
- [ ] **Import from existing DB** — no `bootstrap.pg_basebackup` or `bootstrap.recovery` support

## Quick Start

```bash
# Generate all manifests (RBAC, provider spec, Helm chart)
make generate

# Run the provider locally (for development)
make run

# Install cloudnative-pg helm chart
make install-cloudnative-pg

# Or deploy with Helm
make helm-install

# Verify Provider CR exists before creating an Instance
make check-provider

# Apply minimal example instance (guarded by preflight check)
make apply-example-simple
```

## Development

### Make Targets

| Target                  | Description                                                |
|-------------------------|-------------------------------------------------------------|
| `make generate`         | Run all code generation (RBAC + Helm sync + provider spec) |
| `make run`              | Run the provider locally                                   |
| `make build`            | Build the provider binary                                  |
| `make docker-build`     | Build the container image                                  |
| `make helm-install`     | Deploy with Helm                                           |
| `make check-provider`   | Verify `Provider/provider-cloudnative-pg` exists           |
| `make apply-example-simple` | Apply minimal example Instance with preflight guard   |
| `make helm-template`    | Render Helm templates locally (dry-run)                    |
| `make test`             | Run unit tests                                             |
| `make test-integration` | Run kuttl integration tests                                |
| `make verify`           | Check generated files are up-to-date (CI)                  |
| `make lint`             | Run golangci-lint                                          |

> For development patterns (RBAC, watches, code generation), see [PROVIDER_DEVELOPMENT.md](https://github.com/openeverest/provider-sdk/blob/main/PROVIDER_DEVELOPMENT.md).

### Helm

```bash
# Install
helm install provider-cloudnative-pg charts/provider-cloudnative-pg/ --create-namespace

# Upgrade
helm upgrade provider-cloudnative-pg charts/provider-cloudnative-pg/

# Uninstall
helm uninstall provider-cloudnative-pg
```

### Local

```bash
# Create a local k3d cluster
make k3d-cluster-up

# Run the provider locally against the cluster
make run

# Run integration tests (NOT IMPLEMENTED)
make test-integration

# Tear down the cluster
make k3d-cluster-down
```

## License

Apache License 2.0 — see [LICENSE](LICENSE) for details.
