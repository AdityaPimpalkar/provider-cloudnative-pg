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
