# OSAC Monorepo Makefile
# Single deployment path via osac-installer Helm charts.

KIND_CLUSTER_NAME ?= osac-dev
OSAC_NAMESPACE ?= osac
CONTAINER_TOOL ?= $(shell command -v podman >/dev/null 2>&1 && echo podman || echo docker)

PREREQS_CHART = osac-installer/charts/osac-prereqs
OSAC_CHART = osac-installer/charts/osac
KIND_PREREQS_VALUES = osac-installer/values/kind/prereqs.yaml
KIND_OSAC_VALUES = osac-installer/values/kind/osac.yaml
KIND_CONFIG = kind-dev/kind-config.yaml
KIND_KUBECONFIG = $(HOME)/.kube/$(KIND_CLUSTER_NAME)-kind.kubeconfig
export KUBECONFIG ?= $(KIND_KUBECONFIG)

# kind-load-image loads a container image into the Kind cluster.
# With podman, `kind load docker-image` fails because it uses the docker
# socket; fall back to save-then-load-archive instead.
define kind-load-image
	@if [ "$(CONTAINER_TOOL)" = "podman" ]; then \
		tmpfile=$$(mktemp /tmp/kind-image-XXXXXX.tar); \
		$(CONTAINER_TOOL) save $(1) -o "$$tmpfile"; \
		kind load image-archive "$$tmpfile" --name $(KIND_CLUSTER_NAME); \
		rm -f "$$tmpfile"; \
	else \
		kind load docker-image $(1) --name $(KIND_CLUSTER_NAME); \
	fi
endef

.PHONY: help
help: ## Display this help.
	@awk 'BEGIN {FS = ":.*##"; printf "\nUsage:\n  make \033[36m<target>\033[0m\n"} /^[a-zA-Z_0-9-]+:.*?##/ { printf "  \033[36m%-30s\033[0m %s\n", $$1, $$2 } /^##@/ { printf "\n\033[1m%s\033[0m\n", substr($$0, 5) } ' $(MAKEFILE_LIST)

##@ Development Environment

.PHONY: dev-env
dev-env: kind-create install-prereqs-kind install-fake-crds install-osac-kind ## Create Kind dev environment with OSAC (lightweight, ~8 min)

.PHONY: dev-env-full
dev-env-full: dev-env install-kubevirt install-awx seed-catalog ## Full dev environment with KubeVirt + AWX + catalog (~25 min)

.PHONY: kind-create
kind-create: ## Create Kind cluster (idempotent)
	@if kind get clusters | grep -q "^$(KIND_CLUSTER_NAME)$$"; then \
		echo "Kind cluster '$(KIND_CLUSTER_NAME)' already exists"; \
	else \
		echo "Creating Kind cluster '$(KIND_CLUSTER_NAME)'..."; \
		kind create cluster --name $(KIND_CLUSTER_NAME) --config $(KIND_CONFIG) --wait 60s; \
	fi
	@kind export kubeconfig --name $(KIND_CLUSTER_NAME) --kubeconfig $(HOME)/.kube/$(KIND_CLUSTER_NAME)-kind.kubeconfig
	@echo "KUBECONFIG=$(HOME)/.kube/$(KIND_CLUSTER_NAME)-kind.kubeconfig"

.PHONY: install-prereqs-kind
install-prereqs-kind: ## Install prerequisites (cert-manager, keycloak, envoy gateway) on Kind
	@echo "Installing cert-manager..."
	helm upgrade --install cert-manager oci://quay.io/jetstack/charts/cert-manager \
		--version v1.20.0 --namespace cert-manager --create-namespace \
		--set crds.enabled=true --wait --timeout 5m
	@echo "Installing trust-manager..."
	helm upgrade --install trust-manager oci://quay.io/jetstack/charts/trust-manager \
		--version v0.22.0 --namespace cert-manager \
		--set defaultPackage.enabled=false --wait --timeout 5m
	@echo "Installing Envoy Gateway..."
	helm upgrade --install envoy-gateway oci://docker.io/envoyproxy/gateway-helm \
		--version v1.6.5 --namespace envoy-gateway --create-namespace \
		--wait --timeout 5m
	helm upgrade --install osac-prereqs $(PREREQS_CHART) \
		-f $(KIND_PREREQS_VALUES) \
		--wait --timeout 10m

.PHONY: install-osac-kind
install-osac-kind: ## Install OSAC umbrella chart on Kind
	cd osac-installer && helm dependency build $(subst osac-installer/,,$(OSAC_CHART))
	helm upgrade --install osac $(OSAC_CHART) \
		-f $(KIND_OSAC_VALUES) \
		--namespace $(OSAC_NAMESPACE) --create-namespace \
		--wait --timeout 10m

.PHONY: install-fake-crds
install-fake-crds: ## Install fake CRDs for HyperShift, KubeVirt, OVN-K
	@for f in osac-operator/config/crd/fakes/*.yaml; do \
		case "$$(basename "$$f")" in \
			kustomization.yaml|*osac.openshift.io*) continue ;; \
		esac; \
		kubectl apply --server-side -f "$$f"; \
	done

##@ Integration Tests

.PHONY: integration-test
integration-test: integration-test-fulfillment integration-test-operator integration-test-bmf ## Run all integration tests

.PHONY: integration-test-fulfillment
integration-test-fulfillment: ## Run fulfillment-service integration tests
	$(CONTAINER_TOOL) build -t localhost/fulfillment-service:it -f fulfillment-service/Containerfile .
	$(call kind-load-image,localhost/fulfillment-service:it)
	helm upgrade --install osac $(OSAC_CHART) \
		-f $(KIND_OSAC_VALUES) \
		--set service.images.service=localhost/fulfillment-service:it \
		--namespace $(OSAC_NAMESPACE) --wait --timeout 5m
	@for f in fulfillment-service/it/crds/*.yaml; do kubectl apply --server-side --force-conflicts -f "$$f"; done
	cd fulfillment-service && ginkgo run --timeout 1h -v it

.PHONY: integration-test-operator
integration-test-operator: ## Run osac-operator integration tests
	$(CONTAINER_TOOL) build -t localhost/osac-operator:it -f osac-operator/Containerfile .
	$(call kind-load-image,localhost/osac-operator:it)
	helm upgrade --install osac $(OSAC_CHART) \
		-f $(KIND_OSAC_VALUES) \
		--set operator.image.repository=localhost/osac-operator \
		--set operator.image.tag=it \
		--set operator.image.pullPolicy=Never \
		--namespace $(OSAC_NAMESPACE) --wait --timeout 5m
	cd osac-operator && ginkgo run --timeout 30m -v test/integration

.PHONY: integration-test-bmf
integration-test-bmf: ## Run bare-metal-fulfillment-operator integration tests
	$(CONTAINER_TOOL) build -t localhost/bmf-operator:it -f bare-metal-fulfillment-operator/Containerfile .
	$(call kind-load-image,localhost/bmf-operator:it)
	helm upgrade --install osac $(OSAC_CHART) \
		-f $(KIND_OSAC_VALUES) \
		--set bmf.enabled=true \
		--set bmf.image.repository=localhost/bmf-operator \
		--set bmf.image.tag=it \
		--set bmf.image.pullPolicy=Never \
		--namespace $(OSAC_NAMESPACE) --wait --timeout 5m
	kubectl apply --server-side -f bare-metal-fulfillment-operator/test/crds/
	cd bare-metal-fulfillment-operator && ginkgo run --timeout 30m -v test/integration

##@ Full Environment (opt-in)

.PHONY: install-kubevirt
install-kubevirt: ## Install KubeVirt + CDI + Multus (for compute instance testing)
	@echo "Installing Multus..."
	kubectl apply -f https://raw.githubusercontent.com/k8snetworkplumbingwg/multus-cni/master/deployments/multus-daemonset.yml
	@KUBEVIRT_VERSION=$$(curl -sL https://storage.googleapis.com/kubevirt-prow/release/kubevirt/kubevirt/stable.txt); \
	echo "Installing KubeVirt $${KUBEVIRT_VERSION}..."; \
	kubectl apply -f "https://github.com/kubevirt/kubevirt/releases/download/$${KUBEVIRT_VERSION}/kubevirt-operator.yaml"; \
	kubectl apply -f "https://github.com/kubevirt/kubevirt/releases/download/$${KUBEVIRT_VERSION}/kubevirt-cr.yaml"
	@CDI_VERSION=$$(curl -sL "https://api.github.com/repos/kubevirt/containerized-data-importer/releases/latest" | grep '"tag_name"' | head -1 | sed 's/.*"tag_name": "\(.*\)".*/\1/'); \
	echo "Installing CDI $${CDI_VERSION}..."; \
	kubectl apply -f "https://github.com/kubevirt/containerized-data-importer/releases/download/$${CDI_VERSION}/cdi-operator.yaml"; \
	kubectl apply -f "https://github.com/kubevirt/containerized-data-importer/releases/download/$${CDI_VERSION}/cdi-cr.yaml"

.PHONY: install-awx
install-awx: ## Install AWX operator and instance
	helm upgrade --install osac-prereqs $(PREREQS_CHART) \
		-f $(KIND_PREREQS_VALUES) \
		--set awx.enabled=true \
		--wait --timeout 10m

.PHONY: seed-catalog
seed-catalog: ## Seed catalog data (templates, instance types, networking)
	@echo "Catalog seeding via fulfillment-service API..."
	@echo "TODO: implement catalog seeding via helm hooks or script"

##@ Teardown

.PHONY: teardown
teardown: ## Delete Kind cluster and all resources
	kind delete cluster --name $(KIND_CLUSTER_NAME)

.PHONY: teardown-osac
teardown-osac: ## Uninstall OSAC (keep cluster and prereqs)
	helm uninstall osac --namespace $(OSAC_NAMESPACE) --ignore-not-found
