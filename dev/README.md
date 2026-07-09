# Provider development with Tilt

This directory contains a [Tilt](https://tilt.dev/) setup for developing
`provider-cloudnative-pg`. It installs the latest released OpenEverest v2 core and
then builds and deploys this provider, with live-reload on every code change.

You do **not** need a local checkout of the OpenEverest core.

## Prerequisites

- [Docker](https://docs.docker.com/get-docker/)
- [kubectl](https://kubernetes.io/docs/tasks/tools/)
- [Helm](https://helm.sh/docs/intro/install/)
- [k3d](https://k3d.io/)
- [Tilt](https://docs.tilt.dev/install.html)

## Quick start

```bash
# 1. (Optional) configure the environment
cp dev/.env.example dev/.env

# 2. Create the local cluster and start Tilt
make dev-up
```

Tilt opens its dashboard at <http://localhost:10350>. Once everything is green:

- The Everest UI/API is available at <http://localhost:8080>
  (default credentials: `admin` / `admin`).
- Apply an example Instance to exercise the provider:

  ```bash
  kubectl apply -f examples/instance-example.yaml
  kubectl get instances -w
  ```

Edit any provider Go code and Tilt rebuilds the binary and live-updates the
running pod without recreating it.

To tear things down:

```bash
make dev-down      # stop Tilt (keeps the cluster)
make dev-destroy   # stop Tilt and delete the cluster
```

## Configuration

All settings live in `dev/.env` (see `dev/.env.example`). Common options:

| Variable | Default | Description |
|----------|---------|-------------|
| `INSTALL_OPENEVEREST` | `true` | Install the released OpenEverest core. |
| `OPENEVEREST_VERSION` | _(latest)_ | Pin a specific core chart version. |
| `PROVIDER_NAMESPACE` | `default` | Namespace for the provider + DB operator. |
| `ENABLE_BARMAN_PLUGIN` | `false` | Install cert-manager and the Barman Cloud Plugin in `cnpg-system`, plus a `BackupStorage` CR that uses MinIO from the OpenEverest dev environment. |
| `BARMAN_PLUGIN_VERSION` | `v0.13.0` | Barman plugin release manifest. |

> **Note:** While OpenEverest v2 is in pre-release, the Helm repository only
> publishes pre-release tags (e.g. `2.0.0-dev.1`). Helm's "latest" resolution
> skips pre-releases, so you must set `OPENEVEREST_VERSION` explicitly until
> v2.0.0 is generally available.

## Developing the core and the provider together

When OpenEverest core is already running from a **separate Tilt instance**, both
Tilts must target the **same Kubernetes cluster** (for example `k3d-everest-dev`).
Do **not** run `make dev-up` in this repo — that creates a different cluster.

### 1. Start OpenEverest core (other repo)

In the **openeverest** repo:

```bash
make dev-up
```

Wait until `everest-server` and `everest-controller` are running in
`everest-system`. The core Tilt installs the OpenEverest CRDs.

### 2. Install provider prerequisites (this repo, one-time per cluster)

Point kubectl at the **same cluster** as the core Tilt:

```bash
kubectl config use-context k3d-everest-dev   # adjust to your core cluster
```

Install the CloudNativePG operator and Barman plugin **before** provider Tilt:

```bash
make install-cloudnative-pg
make install-barman-plugin
```

`make install-crds` is only needed if the OpenEverest core is **not** running
(the core Tilt already installs those CRDs).

### 3. Configure provider Tilt

```bash
cp dev/.env.example dev/.env
```

Set at minimum:

```bash
INSTALL_OPENEVEREST=false
CLOUDNATIVE_PG_ENABLED=false      # CNPG installed by make install-cloudnative-pg
PROVIDER_NAMESPACE=default
K8S_CONTEXT=k3d-everest-dev       # same cluster as core Tilt
DOCKER_REGISTRY_URL=localhost:4003  # docker port k3d-registry
ENABLE_BARMAN_PLUGIN=false          # Barman installed by make install-barman-plugin
```

### 4. Start provider Tilt on a different port

```bash
kubectl config use-context k3d-everest-dev
tilt up -f dev/Tiltfile --port 10351
```

- Core Tilt UI: http://localhost:10350
- Provider Tilt UI: http://localhost:10351
- OpenEverest UI: http://localhost:8080

### 5. Verify

```bash
kubectl get providers
kubectl get pods -n default -l app.kubernetes.io/name=provider-cloudnative-pg
kubectl get pods -n cnpg-system
```

The provider should appear in the OpenEverest UI after refresh.

### Why `CLOUDNATIVE_PG_ENABLED=false`?

The provider chart can bundle the CNPG operator as a subchart. If CNPG is
already installed as Helm release `cnpg` (`make install-cloudnative-pg`),
leaving the subchart enabled causes Helm ownership conflicts on CNPG CRDs.
