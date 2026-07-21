## Location to install dependencies to
LOCALBIN ?= $(shell pwd)/bin
$(LOCALBIN):
	mkdir -p $(LOCALBIN)

# CONTAINER_TOOL defines the container tool to be used for building images.
CONTAINER_TOOL ?= docker

# OpenEverest branch to use for OpenEverest CRD installation.
OPENEVEREST_BRANCH ?= release-2.0

# Image URL to use for building/pushing image targets
IMG ?= ghcr.io/adityapimpalkar/provider-cloudnative-pg:latest
_IMG_REPO = $(firstword $(subst :, ,$(IMG)))
_IMG_TAG = $(lastword $(subst :, ,$(IMG)))

# k3d cluster name (must match dev/k3d_config.yaml)
K3D_CLUSTER_NAME ?= provider-cloudnative-pg-dev

# controller-gen version
CONTROLLER_TOOLS_VERSION ?= v0.18.0
CONTROLLER_GEN ?= $(LOCALBIN)/controller-gen-$(CONTROLLER_TOOLS_VERSION)

# yq version for YAML processing
YQ_VERSION ?= v4.44.6
YQ ?= $(LOCALBIN)/yq-$(YQ_VERSION)

# golangci-lint version (v2.9.0+ required for Go 1.26)
GOLANGCI_LINT_VERSION ?= v2.12.2
GOLANGCI_LINT ?= $(LOCALBIN)/golangci-lint-$(GOLANGCI_LINT_VERSION)

# Helm chart directory
CHART_DIR ?= charts/provider-cloudnative-pg
CNPG_HELM_REPO ?= https://cloudnative-pg.github.io/charts

BARMAN_PLUGIN_VERSION ?= v0.13.0

.PHONY: help
help: ## Display this help.
	@awk 'BEGIN {FS = ":.*##"; printf "\nUsage:\n  make \033[36m<target>\033[0m\n"} /^[a-zA-Z_0-9-]+:.*?##/ { printf "  \033[36m%-20s\033[0m %s\n", $$1, $$2 } /^##@/ { printf "\n\033[1m%s\033[0m\n", substr($$0, 5) } ' $(MAKEFILE_LIST)

##@ Development

.PHONY: run
run: generate ## Run the provider locally.
	go run cmd/provider/main.go

.PHONY: lint
lint: golangci-lint ## Run golangci-lint.
	$(GOLANGCI_LINT) run

.PHONY: vet
vet: ## Run go vet.
	go vet ./...

.PHONY: mod-tidy
mod-tidy: ## Verify go.mod and go.sum are tidy.
	@go mod tidy
	@if git diff --quiet -- go.mod go.sum; then \
		echo "go.mod and go.sum are tidy."; \
	else \
		echo "ERROR: go.mod or go.sum need updating. Run 'go mod tidy' and commit."; \
		git diff -- go.mod go.sum; \
		exit 1; \
	fi

.PHONY: test
test: ## Run unit tests.
	go test ./... -coverprofile cover.out

.PHONY: ci
ci: mod-tidy build vet lint test verify helm-lint helm-template ## Run all CI checks locally.

##@ Code Generation

.PHONY: manifests
manifests: controller-gen ## Generate RBAC manifests using controller-gen from kubebuilder markers.
	$(CONTROLLER_GEN) rbac:roleName=manager-role paths="./..." output:rbac:dir=config/rbac

.PHONY: helm-sync-rbac
helm-sync-rbac: yq ## Sync generated RBAC rules into the Helm chart.
	@echo "Syncing RBAC rules from config/rbac/role.yaml to Helm chart..."
	@$(YQ) '.rules' config/rbac/role.yaml > $(CHART_DIR)/generated/rbac-rules.yaml
	@echo "Done."

.PHONY: generate
generate: manifests helm-sync-rbac ## Run all code generation (RBAC + Helm sync + provider spec from definition/).
	go generate ./...
	@echo "All generation complete."

.PHONY: verify
verify: ## Verify that generated files are up-to-date (for CI).
	@$(MAKE) generate
	@if git diff --quiet -- config/ $(CHART_DIR)/generated/; then \
		echo "Generated files are up-to-date."; \
	else \
		echo "ERROR: Generated files are out of date. Run 'make generate' and commit the changes."; \
		git diff -- config/ $(CHART_DIR)/generated/; \
		exit 1; \
	fi

##@ Build

.PHONY: build
build: generate ## Build provider binary.
	go build -o bin/provider cmd/provider/main.go

.PHONY: docker-build
docker-build: ## Build docker image.
	$(CONTAINER_TOOL) build -t ${IMG} .

.PHONY: docker-push
docker-push: ## Push docker image.
	$(CONTAINER_TOOL) push ${IMG}

##@ Helm

.PHONY: helm-deps
helm-deps: ## Download Helm chart dependencies.
	@helm repo add cnpg $(CNPG_HELM_REPO) >/dev/null 2>&1 || true
	helm repo update cnpg
	helm dependency build $(CHART_DIR)

.PHONY: helm-install
helm-install: helm-deps ## Install the provider using Helm.
	helm install provider-cloudnative-pg $(CHART_DIR) --create-namespace

.PHONY: helm-upgrade
helm-upgrade: helm-deps ## Upgrade the provider using Helm.
	helm upgrade provider-cloudnative-pg $(CHART_DIR)

.PHONY: helm-uninstall
helm-uninstall: ## Uninstall the provider using Helm.
	helm uninstall provider-cloudnative-pg

.PHONY: helm-template
helm-template: helm-deps ## Render Helm chart templates locally (dry-run).
	helm template provider-cloudnative-pg $(CHART_DIR)

.PHONY: helm-lint
helm-lint: helm-deps ## Lint the Helm chart.
	helm lint $(CHART_DIR)

##@ Testing

.PHONY: test-integration
test-integration: ## Run all integration tests (kuttl) against a running cluster.
	. ./test/vars.sh && kubectl kuttl test --config ./test/integration/kuttl.yaml

.PHONY: test-integration-core
test-integration-core: ## Run core integration tests (kuttl).
	. ./test/vars.sh && kubectl kuttl test --config ./test/integration/kuttl-core.yaml

.PHONY: test-integration-core-replicaset
test-integration-core-replicaset: ## Run core replicaset integration tests (kuttl).
	. ./test/vars.sh && kubectl kuttl test --config ./test/integration/kuttl-core.yaml --test "replicaset"

.PHONY: load-image
load-image: ## Import the provider image (IMG) into the k3d cluster.
	k3d image import ${IMG} -c ${K3D_CLUSTER_NAME}

.PHONY: deploy-provider-ci
deploy-provider-ci: helm-deps ## Deploy the provider via Helm for CI (IMG must already be imported into k3d).
	helm upgrade --install provider-cloudnative-pg $(CHART_DIR) \
		--create-namespace \
		--namespace provider-system \
		--set image.repository=$(_IMG_REPO) \
		--set image.tag=$(_IMG_TAG) \
		--set image.pullPolicy=Never \
		--wait --timeout 5m

.PHONY: install-crds
install-crds: ## Install OpenEverest CRDs into the cluster.
	kubectl apply -f https://raw.githubusercontent.com/openeverest/openeverest/$(OPENEVEREST_BRANCH)/config/crd/bases/core.openeverest.io_providers.yaml
	kubectl apply -f https://raw.githubusercontent.com/openeverest/openeverest/$(OPENEVEREST_BRANCH)/config/crd/bases/core.openeverest.io_instances.yaml
	kubectl apply -f https://raw.githubusercontent.com/openeverest/openeverest/$(OPENEVEREST_BRANCH)/config/crd/bases/backup.openeverest.io_backupclasses.yaml
	kubectl apply -f https://raw.githubusercontent.com/openeverest/openeverest/$(OPENEVEREST_BRANCH)/config/crd/bases/backup.openeverest.io_backupstorages.yaml
	kubectl apply -f https://raw.githubusercontent.com/openeverest/openeverest/$(OPENEVEREST_BRANCH)/config/crd/bases/backup.openeverest.io_backups.yaml
	kubectl apply -f https://raw.githubusercontent.com/openeverest/openeverest/$(OPENEVEREST_BRANCH)/config/crd/bases/backup.openeverest.io_restores.yaml

.PHONY: install-cloudnative-pg
install-cloudnative-pg: install-crds ## Install CRDs, CloudNativePG operator, Barman plugin, and BackupClasses.
	helm repo add cnpg https://cloudnative-pg.github.io/charts
	helm upgrade --install cnpg \
	  --namespace cnpg-system \
	  --create-namespace \
	  cnpg/cloudnative-pg
	$(MAKE) install-barman-plugin
	$(MAKE) install-backupclasses

.PHONY: install-barman-plugin
install-barman-plugin: ## Install cert-manager and the CNPG-I Barman Cloud Plugin (cnpg-system).
	helm upgrade --install cert-manager oci://quay.io/jetstack/charts/cert-manager \
	  --version v1.20.3 \
	  --namespace cert-manager --create-namespace \
	  --set crds.enabled=true
	kubectl apply -f https://github.com/cloudnative-pg/plugin-barman-cloud/releases/download/$(BARMAN_PLUGIN_VERSION)/manifest.yaml

.PHONY: install-backupclasses
install-backupclasses: ## Install provider BackupClass CRs into the cluster.
	kubectl apply -f charts/provider-cloudnative-pg/generated/backupclasses/cnpg-barman-plugin.yaml

##@ Local Development Cluster

.PHONY: k3d-cluster-up
k3d-cluster-up: ## Create a local k3d cluster for development.
	@if ! k3d cluster list | grep -q "$(K3D_CLUSTER_NAME)"; then \
		echo "Creating K3D cluster $(K3D_CLUSTER_NAME) for Tilt development"; \
		k3d cluster create --config ./dev/k3d_config.yaml; \
	else \
		echo "K3D cluster $(K3D_CLUSTER_NAME) already exists"; \
	fi

.PHONY: k3d-cluster-down
k3d-cluster-down: ## Delete the local k3d cluster.
	@if k3d cluster list | grep -q "$(K3D_CLUSTER_NAME)"; then \
		echo "Destroying K3D cluster $(K3D_CLUSTER_NAME)"; \
		k3d cluster delete --config ./dev/k3d_config.yaml; \
	else \
		echo "K3D cluster $(K3D_CLUSTER_NAME) does not exist"; \
	fi

.PHONY: k3d-cluster-reset
k3d-cluster-reset: k3d-cluster-down k3d-cluster-up ## Reset the local k3d cluster.

##@ Tilt Development

.PHONY: dev-up
dev-up: k3d-cluster-up ## Create the k3d cluster and start the Tilt dev environment.
	tilt up -f dev/Tiltfile

.PHONY: dev-down
dev-down: ## Stop the Tilt dev environment (keeps the cluster).
	tilt down -f dev/Tiltfile

.PHONY: dev-destroy
dev-destroy: k3d-cluster-down ## Stop Tilt and delete the k3d cluster.

##@ Tool Dependencies

.PHONY: controller-gen
controller-gen: $(CONTROLLER_GEN) ## Install controller-gen.
$(CONTROLLER_GEN): $(LOCALBIN)
	$(call go-install-tool,$(CONTROLLER_GEN),sigs.k8s.io/controller-tools/cmd/controller-gen,$(CONTROLLER_TOOLS_VERSION))

.PHONY: yq
yq: $(YQ) ## Install yq.
$(YQ): $(LOCALBIN)
	@echo "Installing yq $(YQ_VERSION)..."
	@GOBIN=$(LOCALBIN) go install github.com/mikefarah/yq/v4@$(YQ_VERSION) && mv $(LOCALBIN)/yq $(YQ)

.PHONY: golangci-lint
golangci-lint: $(GOLANGCI_LINT) ## Install golangci-lint.
$(GOLANGCI_LINT): $(LOCALBIN)
	$(call go-install-tool,$(GOLANGCI_LINT),github.com/golangci/golangci-lint/v2/cmd/golangci-lint,$(GOLANGCI_LINT_VERSION))

# go-install-tool will 'go install' any package with custom target and target name. Usage:
# $(call go-install-tool,<target>,<package>,<version>)
define go-install-tool
@[ -f $(1) ] || { \
set -e; \
package=$(2)@$(3); \
echo "Installing $${package}"; \
GOBIN=$(LOCALBIN) go install $${package}; \
mv -f $$(echo "$(1)" | sed "s/-$(3)$$//") $(1); \
}
endef
