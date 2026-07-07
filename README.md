# provider-cloudnative-pg

An [OpenEverest](https://github.com/openeverest) provider that provisions PostgreSQL clusters via the [CloudNativePG](https://cloudnative-pg.io/) operator.

> ⚠️🚧 THIS PROVIDER PLUGIN IS IN DEVELOPMENT/TESTING. DO NOT USE IN PRODUCTION!! 🚧⚠️

## Project Status

The provider can create and reconcile CloudNativePG `Cluster` resources from OpenEverest `Instance` CRs for local development and testing. Core provisioning (replicas, storage, resources, version selection, bootstrap, PostgreSQL tuning, managed roles) is in place; kuttl integration tests cover basic provisioning and status reconciliation. Backup/DR, pooling, monitoring, and CI wiring for integration tests are still outstanding.

Recent additions:

- Bootstrap `initdb` configuration (database name, owner, credentials secret)
- Managed PostgreSQL roles with readiness gating in `Status()`
- Full `PostgresConfiguration` and `AffinityConfiguration` passthrough
- Expanded `replicaSet` UI schema (bootstrap, separate CPU/memory requests and limits, advanced settings)

## Prerequisites

- Go 1.26+
- A Kubernetes cluster (k3d, kind, or remote)
- [OpenEverest CRDs](https://github.com/openeverest/openeverest) installed (`make install-crds`)
- [CloudNativePG operator](https://cloudnative-pg.io/documentation/current/installation_upgrade/) (`make install-cloudnative-pg`)

## CloudNativePG Coverage

This provider translates OpenEverest `Instance` CRs into [CloudNativePG](https://cloudnative-pg.io/) `Cluster` resources. CloudNativePG manages the full PostgreSQL lifecycle — HA replication, automated failover, rolling updates, backup/PITR, connection pooling, and more — but this provider currently exposes only a subset of that surface.

The sections below list what is implemented today versus what is still needed for production use.

## Feature Status

### Implemented

These features are implemented in `internal/provider/provider.go` (and supporting packages) and can be used for dev/testing today.

- [x] **Provider reconciliation** — `Validate`, `Sync`, and `Status` lifecycle hooks
- [x] **CloudNativePG Cluster management** — creates and updates `postgresql.cnpg.io/v1` Cluster resources; watches owned Clusters for status updates
- [x] **Instance validation** — requires `engine` component with replicas (≥ 1), storage size, and CPU/memory requests and limits
- [x] **HA replica count** — `engine.replicas` → `Cluster.spec.instances`
- [x] **Storage** — `engine.storage.size` → PVC size; optional `engine.storage.storageClass`; `customSpec.resizeInUseVolumes` (defaults to `true`); optional `customSpec.pvcTemplate`
- [x] **Resource limits** — `engine.resources` → `Cluster.spec.resources` (requests and limits can be set independently)
- [x] **Affinity** — optional `customSpec.affinity` → `Cluster.spec.affinity` (pod anti-affinity, topology key, node affinity, etc.); CNPG operator defaults apply when unset
- [x] **Image resolution** — explicit `engine.image` override, version catalog lookup, or default image from provider spec
- [x] **PostgreSQL version catalog** — versions 14.23, 15.18, 16.14, 17.10 (default), and 18.4 (`definition/versions.yaml`)
- [x] **Bootstrap / initial database** — `customSpec.bootstrap.initdb` → `Cluster.spec.bootstrap.initdb` (database name, owner, optional credentials secret); connection details use the application database when configured
- [x] **PostgreSQL configuration** — `customSpec.postgresql` → `Cluster.spec.postgresConfiguration` (parameters, WAL settings, and other CNPG `PostgresConfiguration` fields)
- [x] **Managed PostgreSQL roles** — `customSpec.managed` → `Cluster.spec.managed`; `Status()` waits for role reconciliation before reporting Ready (`internal/cnpg/roles.go`)
- [x] **Connection details** — reports `postgresql` URI with host (`-rw` service), port, credentials, and database when the CNPG cluster is ready
- [x] **Secrets RBAC** — provider ClusterRole grants `secrets` get/list/watch/create/update/patch (generated from markers in `internal/provider/rbac.go`)
- [x] **UI schema** — `replicaSet` topology exposes version, bootstrap, node count, CPU/memory requests and limits, disk, storage class, and engine configuration in `definition/topologies/replicaSet/topology.yaml`

### Pending

These are commonly used CloudNativePG features that are **not** implemented yet. The provider is suitable for dev/testing but not production-ready until the critical items below are addressed.

- [ ] **Backup and disaster recovery** — CNPG-I [Barman Cloud Plugin](https://cloudnative-pg.github.io/plugin-barman-cloud/) path only (`cnpg-barman-plugin` BackupClass, `ObjectStore` CRs, `method: plugin`); native `barmanObjectStore` not planned unless requested
- [ ] **Bootstrap recovery methods** — only `initdb` is supported; `bootstrap.recovery` and `bootstrap.pg_basebackup` are not exposed
- [ ] **CloudNativePG operator packaging** — the CNPG operator is an external prerequisite; it is not installed or bundled by this Helm chart (`charts/provider-cloudnative-pg/Chart.yaml` TODO)
- [ ] **Connection pooling** — no `Pooler` CR (PgBouncer) support
- [ ] **Monitoring** — no `Cluster.spec.monitoring.enablePodMonitor` or CNPG condition surfacing in Instance status
- [ ] **Read service endpoints** — `Status()` exposes only the `-rw` (primary) service; `-ro` (replicas) and `-r` (any) endpoints are not reported
- [ ] **TLS / certificates** — no `serverTLSSecret` or custom certificate configuration
- [ ] **Superuser access control** — no `enableSuperuserAccess` or `superuserSecret` configuration
- [ ] **Custom cleanup logic** — `Cleanup()` is a no-op (owned Cluster resources are garbage-collected via owner references; no explicit cleanup for external resources such as backups)
- [ ] **RBAC marker cleanup** — placeholder kubebuilder markers remain in `Validate()`; active provider markers live in `internal/provider/rbac.go`
- [ ] **Automated tests** — no `*_test.go` files in the repository
- [ ] **CI integration tests** — kuttl tests exist under `test/integration/`; `.github/workflows/test.yaml` is still manual-only and needs CI wiring

#### Planned

- [ ] **Primary update strategy** — no `primaryUpdateStrategy` or `primaryUpdateMethod` configuration
- [ ] **Managed databases** — no `Database` CR support for per-tenant database provisioning
- [ ] **Logical replication** — no `Publication` or `Subscription` CRs
- [ ] **Replica clusters (DR)** — no cross-region disaster recovery cluster configuration
- [ ] **Extensions / init SQL** — no `postInitSQL` or `postInitApplicationSQL` support
- [ ] **Import from existing DB** — no `bootstrap.pg_basebackup` or `bootstrap.recovery` support (types stubbed in validation only)

## Quick Start

```bash
# Generate all manifests (RBAC, provider spec, Helm chart)
make generate

# Run the provider locally (for development)
make run

# Install CloudNativePG operator
make install-cloudnative-pg

# Or deploy the provider with Helm
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
| `make install-crds`     | Install OpenEverest CRDs                                   |
| `make install-cloudnative-pg` | Install the CloudNativePG operator via Helm          |
| `make helm-install`     | Deploy with Helm                                           |
| `make check-provider`   | Verify `Provider/provider-cloudnative-pg` exists           |
| `make apply-example-simple` | Apply minimal example Instance with preflight guard   |
| `make helm-template`    | Render Helm templates locally (dry-run)                    |
| `make test`             | Run unit tests                                             |
| `make test-integration` | Run kuttl integration tests                                |
| `make test-integration-core` | Run core kuttl integration tests                      |
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

# Install prerequisites
make install-crds
make install-cloudnative-pg

# Run the provider locally against the cluster
make run

# Run integration tests
make test-integration

# Tear down the cluster
make k3d-cluster-down
```

### Running Integration Tests

The `test/integration/` directory contains [kuttl](https://kuttl.dev/) tests that verify the provider's behavior.

#### Prerequisites

1. OpenEverest CRDs and the CloudNativePG operator installed (see [Local](#local) above)
2. Provider CR registered — `make helm-install` (or render and apply the Provider manifest from the chart)
3. Provider running in the background — use `make run`; if the provider was installed via Helm, scale its Deployment to 0 first to avoid duplicate reconciliation

```bash
make run
```

#### Running the Tests

```bash
make test-integration

# Or run the core replicaset suite only:
make test-integration-core-replicaset

# Or invoke kuttl directly:
. ./test/vars.sh && kubectl kuttl test --config ./test/integration/kuttl.yaml
```

**Note:** The tests assume the provider is already running. They create/update/delete `Instance` resources and assert that the corresponding CloudNativePG `Cluster` resources are reconciled correctly. Cluster readiness is mocked via status patches so tests do not require real PostgreSQL pods.

## License

Apache License 2.0 — see [LICENSE](LICENSE) for details.
