# OSAC Installer -- single deployment API.
#
# Entities:  Infra (osac-deps + osac-infra), Osac (osac chart), Test
# Parameters are REQUIRED -- missing params produce hard errors.
#
# Usage:
# Every deployment target requires PLATFORM, PROFILE, and NS. No exceptions.
#   make install         PLATFORM=kind|openshift  PROFILE=dev|vmaas-ci|bmaas-ci|caas-ci|full-ci  NS=osac
#   make uninstall       PLATFORM=kind|openshift  PROFILE=dev|vmaas-ci|bmaas-ci|caas-ci|full-ci  NS=osac
#   make install-infra   PLATFORM=kind|openshift  PROFILE=dev|vmaas-ci|bmaas-ci|caas-ci|full-ci  NS=osac
#   make install-osac    PLATFORM=kind|openshift  PROFILE=dev|vmaas-ci|bmaas-ci|caas-ci|full-ci  NS=osac
#   make uninstall-osac  PLATFORM=kind|openshift  PROFILE=dev|vmaas-ci|bmaas-ci|caas-ci|full-ci  NS=osac
#   make uninstall-infra PLATFORM=kind|openshift  PROFILE=dev|vmaas-ci|bmaas-ci|caas-ci|full-ci  NS=osac
#   make test            PLATFORM=kind|openshift  PROFILE=dev|vmaas-ci|bmaas-ci|caas-ci|full-ci  NS=osac  SUITE=all|fulfillment|operator|bmf
#
# Helm chart management (no parameters required):
#   make helm-lint
#   make helm-validate

SHELL := /usr/bin/env bash -o pipefail
.SHELLFLAGS := -ec

# Extra helm --set/--set-string args appended to install commands.
EXTRA_HELM_ARGS ?=

# -- Fail-fast parameter validation ------------------------------------
# Guards fire at parse time, gated by MAKECMDGOALS so `make help` works.

ALL_TARGETS := install uninstall install-infra uninstall-infra install-osac uninstall-osac test

ifneq ($(filter $(ALL_TARGETS),$(MAKECMDGOALS)),)
ifndef PLATFORM
$(error PLATFORM is required (kind|openshift))
endif
ifeq ($(filter $(PLATFORM),kind openshift),)
$(error PLATFORM=$(PLATFORM) is invalid; must be kind or openshift)
endif
ifndef PROFILE
$(error PROFILE is required (dev|vmaas-ci|bmaas-ci|caas-ci|full-ci))
endif
ifeq ($(filter $(PROFILE),dev vmaas-ci bmaas-ci caas-ci full-ci),)
$(error PROFILE=$(PROFILE) is invalid; must be dev, vmaas-ci, bmaas-ci, caas-ci, or full-ci)
endif
ifndef NS
$(error NS is required (namespace for OSAC instance, e.g. NS=osac))
endif
ifeq ($(PLATFORM),kind)
ifneq ($(PROFILE),dev)
$(error PLATFORM=kind only supports PROFILE=dev)
endif
endif
endif

SUITE_TARGETS := test
ifneq ($(filter $(SUITE_TARGETS),$(MAKECMDGOALS)),)
ifndef SUITE
$(error SUITE is required (all|fulfillment|operator|bmf))
endif
ifeq ($(filter $(SUITE),all fulfillment operator bmf),)
$(error SUITE=$(SUITE) is invalid; must be all, fulfillment, operator, or bmf)
endif
endif

# -- Paths and constants -----------------------------------------------

CHARTS         := charts
DEPS_CHART     := $(CHARTS)/osac-deps
INFRA_CHART    := $(CHARTS)/osac-infra
OSAC_CHART     := $(CHARTS)/osac

# Values file selection: kind uses kind-specific files, OpenShift uses profile files.
ifeq ($(PLATFORM),kind)
INFRA_VALUES    := values/dev/kind-infra.yaml
INSTANCE_VALUES := values/dev/kind-instance.yaml
else ifdef PROFILE
INFRA_VALUES    := values/$(PROFILE)/infra.yaml
INSTANCE_VALUES := values/$(PROFILE)/instance.yaml
endif

# Container tool and Kind cluster config.
CONTAINER_TOOL ?= $(or $(shell command -v podman 2>/dev/null),$(shell command -v docker 2>/dev/null),$(error neither podman nor docker found in PATH))
KIND_CLUSTER_NAME ?= osac-dev
KIND_CONFIG := kind-config.yaml
KIND_KUBECONFIG := $(HOME)/.kube/$(KIND_CLUSTER_NAME)-kind.kubeconfig
export KUBECONFIG ?= $(KIND_KUBECONFIG)

define kind-load-image
	@if [ "$$(basename $(CONTAINER_TOOL))" = "podman" ]; then \
		tmpfile=$$(mktemp /tmp/kind-image-XXXXXX.tar); \
		$(CONTAINER_TOOL) save $(1) -o "$$tmpfile"; \
		kind load image-archive "$$tmpfile" --name $(KIND_CLUSTER_NAME); \
		rm -f "$$tmpfile"; \
	else \
		kind load docker-image $(1) --name $(KIND_CLUSTER_NAME); \
	fi
endef

# -- Help --------------------------------------------------------------

.PHONY: help
help: ## Display this help
	@awk 'BEGIN {FS = ":.*##"; printf "\nUsage:\n  make \033[36m<target>\033[0m\n"} /^[a-zA-Z_0-9-]+:.*?##/ { printf "  \033[36m%-30s\033[0m %s\n", $$1, $$2 } /^##@/ { printf "\n\033[1m%s\033[0m\n", substr($$0, 5) } ' $(MAKEFILE_LIST)

##@ Entity: Infra

.PHONY: install-infra
install-infra: ## Install infrastructure (PLATFORM= PROFILE= NS= required)
ifeq ($(PLATFORM),kind)
	@if kind get clusters | grep -q "^$(KIND_CLUSTER_NAME)$$"; then \
		echo "Kind cluster '$(KIND_CLUSTER_NAME)' already exists"; \
	else \
		echo "Creating Kind cluster '$(KIND_CLUSTER_NAME)'..."; \
		kind create cluster --name $(KIND_CLUSTER_NAME) --config $(KIND_CONFIG) --wait 60s; \
	fi
	@kind export kubeconfig --name $(KIND_CLUSTER_NAME) --kubeconfig $(KIND_KUBECONFIG)
	helm upgrade --install cert-manager oci://quay.io/jetstack/charts/cert-manager \
		--version v1.20.0 --namespace cert-manager --create-namespace \
		--set crds.enabled=true --wait --timeout 5m
	helm upgrade --install trust-manager oci://quay.io/jetstack/charts/trust-manager \
		--version v0.22.0 --namespace cert-manager \
		--set defaultPackage.enabled=false --wait --timeout 5m
	helm upgrade --install envoy-gateway oci://docker.io/envoyproxy/gateway-helm \
		--version v1.6.5 --namespace envoy-gateway --create-namespace \
		--wait --timeout 5m
	helm upgrade --install osac-infra $(INFRA_CHART) \
		--namespace osac-infra --create-namespace \
		-f $(INFRA_VALUES) \
		--set osacNamespace=$(NS) \
		--wait-for-jobs --timeout 30m
	@for f in ../osac-operator/config/crd/fakes/*.yaml; do \
		case "$$(basename "$$f")" in \
			*osac.openshift.io*) continue ;; \
		esac; \
		kubectl apply --server-side --force-conflicts -f "$$f"; \
	done
else
	$(eval OCP_VERSION := $(shell oc get clusterversion version -o jsonpath='{.status.desired.version}' | cut -d. -f1,2))
	@bash -c 'source scripts/lib.sh && \
		for ns in ansible-aap cert-manager-operator cert-manager openshift-storage metallb-system multicluster-engine openshift-cnv; do \
			wait_for_namespace_cleanup "$$ns"; \
		done'
	helm upgrade --install osac-deps $(DEPS_CHART) \
		--namespace osac-deps --create-namespace \
		--values $(INFRA_VALUES) \
		--set lvms.channel=stable-$(OCP_VERSION) \
		--timeout 30m --wait
	@bash -c 'source scripts/lib.sh && wait_for_namespace_cleanup keycloak'
	$(eval DOMAIN := $(shell oc get ingresses.config/cluster -o jsonpath='{.spec.domain}'))
	@[ -n "$(DOMAIN)" ] || { echo "ERROR: could not determine cluster domain" >&2; exit 1; }
	helm upgrade --install osac-infra $(INFRA_CHART) \
		--namespace osac-infra --create-namespace \
		--values $(INFRA_VALUES) \
		--set osacNamespace=$(NS) \
		--set lvms.channel=stable-$(OCP_VERSION) \
		--set keycloak.hostname=https://keycloak-keycloak.$(DOMAIN) \
		--set keycloak.route.hostname=keycloak-keycloak.$(DOMAIN) \
		--timeout 30m --wait-for-jobs
endif

.PHONY: uninstall-infra
uninstall-infra: ## Uninstall infrastructure (PLATFORM= PROFILE= NS= required)
ifeq ($(PLATFORM),kind)
	helm uninstall osac-infra --namespace osac-infra --ignore-not-found
	helm uninstall envoy-gateway --namespace envoy-gateway --ignore-not-found
	helm uninstall trust-manager --namespace cert-manager --ignore-not-found
	helm uninstall cert-manager --namespace cert-manager --ignore-not-found
	kind delete cluster --name $(KIND_CLUSTER_NAME)
else
	helm uninstall osac-infra --namespace osac-infra --ignore-not-found --wait --timeout 10m
	helm uninstall osac-deps --namespace osac-deps --ignore-not-found
endif

##@ Entity: Osac

AAP_LICENSE_FILE ?= values/$(PROFILE)/license.zip

.PHONY: install-osac
install-osac: ## Install OSAC instance (PLATFORM= PROFILE= NS= required)
	helm dependency build $(OSAC_CHART)
ifneq ($(PLATFORM),kind)
	@[ -n "$(AAP_LICENSE_FILE)" ] || { echo "ERROR: AAP_LICENSE_FILE is required for PLATFORM=openshift" >&2; exit 1; }
	@[ -f "$(AAP_LICENSE_FILE)" ] || { echo "ERROR: AAP_LICENSE_FILE not found at $(AAP_LICENSE_FILE)" >&2; exit 1; }
	oc create namespace $(NS) --dry-run=client -o yaml | oc apply -f -
	oc create secret generic config-as-code-manifest-ig \
		--from-file=license.zip="$(AAP_LICENSE_FILE)" \
		-n $(NS) --dry-run=client -o yaml | oc apply --server-side -f -
	oc label secret config-as-code-manifest-ig \
		osac.openshift.io/project=osac-aap \
		-n $(NS) --overwrite
	@bash -c 'source scripts/lib.sh && check_postgres_prerequisites "$(NS)" "$(INFRA_VALUES)"'
	$(eval DOMAIN := $(shell oc get ingresses.config/cluster -o jsonpath='{.spec.domain}'))
	@[ -n "$(DOMAIN)" ] || { echo "ERROR: Could not determine cluster domain. Is oc logged in?" >&2; exit 1; }
endif
	helm upgrade --install osac $(OSAC_CHART) \
		-f $(INSTANCE_VALUES) \
		--namespace $(NS) --create-namespace \
		$(if $(DOMAIN),--set service.externalHostname=fulfillment-api-$(NS).$(DOMAIN) --set service.internalHostname=fulfillment-internal-api-$(NS).$(DOMAIN) --set service.auth.issuerUrl=https://keycloak-keycloak.$(DOMAIN)/realms/osac --set service.idp.url=https://keycloak-keycloak.$(DOMAIN)) \
		$(EXTRA_HELM_ARGS) \
		--wait --timeout 40m

.PHONY: uninstall-osac
uninstall-osac: ## Uninstall OSAC instance (PLATFORM= PROFILE= NS= required)
	helm uninstall osac --namespace $(NS) --ignore-not-found

##@ Composite

.PHONY: install
install: install-infra install-osac ## Full install: infra + osac (PLATFORM= PROFILE= required)

.PHONY: uninstall
uninstall: uninstall-osac uninstall-infra ## Full uninstall: osac + infra (PLATFORM= PROFILE= required)

##@ Entity: Test

.PHONY: test
test: ## Run integration tests (SUITE= required: all|fulfillment|operator|bmf)
ifeq ($(SUITE),all)
	$(MAKE) test SUITE=fulfillment
	$(MAKE) test SUITE=operator
	$(MAKE) test SUITE=bmf
endif
ifeq ($(SUITE),fulfillment)
	$(CONTAINER_TOOL) build -t localhost/fulfillment-service:it -f ../fulfillment-service/Containerfile ..
	$(call kind-load-image,localhost/fulfillment-service:it)
	$(MAKE) install-osac PLATFORM=$(PLATFORM) PROFILE=$(PROFILE) NS=$(NS) \
		EXTRA_HELM_ARGS="--set service.images.service=localhost/fulfillment-service:it --set operator.enabled=false"
	@for f in ../fulfillment-service/it/crds/*.yaml; do kubectl apply --server-side --force-conflicts -f "$$f"; done
	cd ../fulfillment-service && ginkgo run --timeout 1h -v it
endif
ifeq ($(SUITE),operator)
	$(CONTAINER_TOOL) build -t localhost/osac-operator:it -f ../osac-operator/Containerfile ..
	$(call kind-load-image,localhost/osac-operator:it)
	$(MAKE) install-osac PLATFORM=$(PLATFORM) PROFILE=$(PROFILE) NS=$(NS) \
		EXTRA_HELM_ARGS="--set operator.image.repository=localhost/osac-operator --set operator.image.tag=it --set operator.image.pullPolicy=Never --set service.enabled=false --set hubAccess.enabled=false --set validation.enabled=false"
	cd ../osac-operator && ginkgo run --timeout 30m -v test/integration
endif
ifeq ($(SUITE),bmf)
	$(CONTAINER_TOOL) build -t localhost/bmf-operator:it -f ../bare-metal-fulfillment-operator/Containerfile ..
	$(call kind-load-image,localhost/bmf-operator:it)
	kubectl apply --server-side --force-conflicts -f ../bare-metal-fulfillment-operator/test/crds/
	$(MAKE) install-osac PLATFORM=$(PLATFORM) PROFILE=$(PROFILE) NS=$(NS) \
		EXTRA_HELM_ARGS="--set bmf.enabled=true --set bmf.image.repository=localhost/bmf-operator --set bmf.image.tag=it --set bmf.image.pullPolicy=Never --set operator.enabled=false --set service.enabled=false --set hubAccess.enabled=false --set validation.enabled=false"
	cd ../bare-metal-fulfillment-operator && ginkgo run --timeout 30m -v test/integration
endif

##@ Charts

.PHONY: sync-charts
sync-charts: ## Update submodules and rebuild chart dependencies
	git submodule update --init --recursive
	helm dependency build $(OSAC_CHART)

.PHONY: helm-deps
helm-deps: ## Build Helm chart dependencies
	helm dependency build $(OSAC_CHART)

.PHONY: helm-lint
helm-lint: ## Lint all charts
	helm lint $(DEPS_CHART)
	helm lint $(INFRA_CHART)
	helm dependency build $(OSAC_CHART)
	helm lint $(OSAC_CHART)

.PHONY: helm-template
helm-template: ## Dry-run render all templates
	helm template osac-deps $(DEPS_CHART)
	helm template osac-infra $(INFRA_CHART)
	helm template osac $(OSAC_CHART)

.PHONY: helm-validate
helm-validate: helm-lint ## Validate all charts (lint + template)
	@for d in values/*/; do \
		infra="$${d}infra.yaml"; \
		[ -f "$$infra" ] && echo "Validating $$infra..." && \
			helm template osac-deps $(DEPS_CHART) --values "$$infra" && \
			helm template osac-infra $(INFRA_CHART) --values "$$infra"; \
	done
	./scripts/validate-keycloak-credentials.sh
	@echo "Validation passed."
