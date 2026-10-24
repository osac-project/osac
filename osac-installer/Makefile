# OSAC Installer -- single deployment API.
#
# Entities:  Infra (osac-deps + osac-infra), Osac (osac chart), Test
# Parameters are REQUIRED -- missing params produce hard errors.
#
# Usage:
# Every deployment target requires PLATFORM, PROFILE, and NS. No exceptions.
#   make install         PLATFORM=kind|openshift  PROFILE=dev|dev-full|vmaas-ci|bmaas-ci|bmaas-netbox-ci|caas-ci|full-ci|cudn-evpn-netris-test  NS=osac
#   make uninstall       PLATFORM=kind|openshift  PROFILE=dev|dev-full|vmaas-ci|bmaas-ci|bmaas-netbox-ci|caas-ci|full-ci|cudn-evpn-netris-test  NS=osac
#   make install-infra   PLATFORM=kind|openshift  PROFILE=dev|dev-full|vmaas-ci|bmaas-ci|bmaas-netbox-ci|caas-ci|full-ci|cudn-evpn-netris-test  NS=osac
#   make install-osac    PLATFORM=kind|openshift  PROFILE=dev|dev-full|vmaas-ci|bmaas-ci|bmaas-netbox-ci|caas-ci|full-ci|cudn-evpn-netris-test  NS=osac
#   make uninstall-osac  PLATFORM=kind|openshift  PROFILE=dev|dev-full|vmaas-ci|bmaas-ci|bmaas-netbox-ci|caas-ci|full-ci|cudn-evpn-netris-test  NS=osac
#   make uninstall-infra PLATFORM=kind|openshift  PROFILE=dev|dev-full|vmaas-ci|bmaas-ci|bmaas-netbox-ci|caas-ci|full-ci|cudn-evpn-netris-test  NS=osac
#   make test            PLATFORM=kind|openshift  PROFILE=dev|dev-full|vmaas-ci|bmaas-ci|bmaas-netbox-ci|caas-ci|full-ci|cudn-evpn-netris-test  NS=osac  SUITE=all|fulfillment|operator|bmf
#
# PROFILE=dev-full (kind only) is a superset of dev: it runs the same chart-based
# control plane, then layers on the full local dev stack (KubeVirt/CDI/Multus, AWX,
# UI, seeded catalog) via scripts/dev-full/. It needs a rootful container runtime
# (Linux: rootful podman or Docker; macOS: Docker Desktop) and /dev/kvm on Linux —
# see scripts/dev-full/kind-runtime.sh and README.md.
#
# Helm chart management (no parameters required):
#   make helm-lint
#   make mce-render-test
#   make helm-validate

SHELL := /usr/bin/env bash -o pipefail
.SHELLFLAGS := -ec

# Extra helm --set/--set-string args appended to the OSAC chart command.
EXTRA_HELM_ARGS ?=
FULFILLMENT_GINKGO_ARGS ?=
MCP_TEST_ENABLED ?= false

ifeq ($(MCP_TEST_ENABLED),true)
MCP_TEST_HELM_ARGS := --set service.mcp.enabled=true --set-string service.mcp.externalHostname=mcp.osac.localhost --set service.mcp.externalPort=8443
MCP_TEST_URL := https://mcp.osac.localhost:8443
endif
# Build the operator from this checkout and install that image instead of the
# registry image the values files pin. Kind only: a remote cluster cannot pull
# a localhost image. The flag's --set values win over EXTRA_HELM_ARGS; use
# OPERATOR_IMAGE_TAG to select another locally loaded tag.
USE_LOCAL_OPERATOR ?= false
# With USE_LOCAL_OPERATOR a rebuilt image must change the Deployment so the
# operator pod rolls, so default to a tag unique to this invocation. It is
# computed once, so build, load and Helm all use the same value.
ifeq ($(USE_LOCAL_OPERATOR),true)
LOCAL_BUILD_STAMP := $(shell date -u +%Y%m%d%H%M%S)
OPERATOR_IMAGE_TAG ?= local-$(LOCAL_BUILD_STAMP)
else
OPERATOR_IMAGE_TAG ?= it
endif
# The tag is expanded into helm arguments and a shell command, so reject
# anything that is not a plain image tag. The value reaches the shell through
# the environment; it is never spliced into shell text.
export OPERATOR_IMAGE_TAG
OPERATOR_IMAGE_TAG_VALID := $(shell [ -n "$$OPERATOR_IMAGE_TAG" ] && printf '%s' "$$OPERATOR_IMAGE_TAG" | grep -Eqx '[A-Za-z0-9_][A-Za-z0-9_.-]*' && [ "$$(printf '%s' "$$OPERATOR_IMAGE_TAG" | wc -l)" -eq 0 ] && [ $${#OPERATOR_IMAGE_TAG} -le 128 ] && echo yes || echo no)
ifneq ($(OPERATOR_IMAGE_TAG_VALID),yes)
$(error OPERATOR_IMAGE_TAG is not a valid image tag; use letters, digits, dot, dash or underscore)
endif
OPERATOR_IMAGE := localhost/osac-operator:$(OPERATOR_IMAGE_TAG)
OPERATOR_IMAGE_HELM_ARGS := --set operator.image.repository=localhost/osac-operator --set-string operator.image.tag=$(OPERATOR_IMAGE_TAG) --set operator.image.pullPolicy=Never
ifeq ($(USE_LOCAL_OPERATOR),true)
LOCAL_OPERATOR_HELM_ARGS := $(OPERATOR_IMAGE_HELM_ARGS)
endif

DRY_RUN ?= 0
DRY_RUN_HELM_FLAGS := $(filter --%,$(EXTRA_HELM_ARGS))
DRY_RUN_INVALID_HELM_FLAGS := $(filter-out --set --set-string --set=% --set-string=%,$(DRY_RUN_HELM_FLAGS))

# Work around HTTP/2 stream resets from proxy.golang.org during container
# builds (see osac-test-infra#543).  Override with `make test GODEBUG=` to
# disable, or set a different value to experiment.
GODEBUG ?= http2client=0

# -- Fail-fast parameter validation ------------------------------------
# Guards fire at parse time, gated by MAKECMDGOALS so `make help` works.

ALL_TARGETS := install uninstall install-infra uninstall-infra install-osac uninstall-osac install-devstack kind-load-images local-operator-image test

ifneq ($(filter $(ALL_TARGETS),$(MAKECMDGOALS)),)
ifndef PLATFORM
$(error PLATFORM is required (kind|openshift))
endif
ifeq ($(filter $(PLATFORM),kind openshift),)
$(error PLATFORM=$(PLATFORM) is invalid; must be kind or openshift)
endif
ifndef PROFILE
$(error PROFILE is required (dev|dev-full|vmaas-ci|bmaas-ci|bmaas-netbox-ci|caas-ci|full-ci|cudn-evpn-netris-test))
endif
ifeq ($(filter $(PROFILE),dev dev-full vmaas-ci bmaas-ci bmaas-netbox-ci caas-ci full-ci cudn-evpn-netris-test),)
$(error PROFILE=$(PROFILE) is invalid; must be dev, dev-full, vmaas-ci, bmaas-ci, bmaas-netbox-ci, caas-ci, full-ci, or cudn-evpn-netris-test)
endif
ifndef NS
$(error NS is required (namespace for OSAC instance, e.g. NS=osac))
endif
ifeq ($(PLATFORM),kind)
ifeq ($(filter $(PROFILE),dev dev-full),)
$(error PLATFORM=kind only supports PROFILE=dev or PROFILE=dev-full)
endif
endif
ifeq ($(filter $(USE_LOCAL_OPERATOR),true false),)
$(error USE_LOCAL_OPERATOR=$(USE_LOCAL_OPERATOR) is invalid; must be true or false)
endif
ifeq ($(USE_LOCAL_OPERATOR),true)
ifneq ($(PLATFORM),kind)
$(error USE_LOCAL_OPERATOR=true requires PLATFORM=kind (a remote cluster cannot pull a localhost image))
endif
endif
# The target loads the image into a Kind cluster, so it is Kind-only even when
# invoked directly with the flag at its default.
ifneq ($(filter local-operator-image,$(MAKECMDGOALS)),)
ifneq ($(PLATFORM),kind)
$(error local-operator-image requires PLATFORM=kind (it loads the image into a Kind cluster))
endif
endif
ifneq ($(filter install,$(MAKECMDGOALS)),)
ifeq ($(DRY_RUN),1)
ifneq ($(strip $(DRY_RUN_INVALID_HELM_FLAGS)),)
$(error DRY_RUN=1 only accepts --set/--set-string overrides in EXTRA_HELM_ARGS; unsupported: $(DRY_RUN_INVALID_HELM_FLAGS))
endif
ifeq ($(PLATFORM),openshift)
ifeq ($(strip $(DRY_RUN_CLUSTER_DOMAIN)),)
$(error DRY_RUN_CLUSTER_DOMAIN is required for PLATFORM=openshift DRY_RUN=1)
endif
ifeq ($(strip $(DRY_RUN_OCP_VERSION)),)
$(error DRY_RUN_OCP_VERSION is required for PLATFORM=openshift DRY_RUN=1 (major.minor, e.g. 4.20))
endif
endif
else ifneq ($(DRY_RUN),0)
$(error DRY_RUN must be 0 or 1)
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
ifeq ($(MCP_TEST_ENABLED),true)
ifneq ($(PLATFORM),kind)
$(error MCP_TEST_ENABLED=true requires PLATFORM=kind (the MCP test endpoint uses mcp.osac.localhost:8443))
endif
endif
endif

# -- Paths and constants -----------------------------------------------

CHARTS         := charts
DEPS_CHART     := $(CHARTS)/osac-deps
INFRA_CHART    := $(CHARTS)/osac-infra
OSAC_CHART     := $(CHARTS)/osac
# Runtime supplies hostnames and credentials; use the minimal CI fixture for
# local chart checks without loosening the required-values schema.
HELM_VALIDATION_VALUES := $(OSAC_CHART)/ci/default-values.yaml
# One archive and repository per external subchart declared in the chart's
# Chart.yaml; both lists are derived, so adding a dependency to osac-infra or
# osac-devstack requires no Makefile change.
INFRA_DEP_ARCHIVES := $(shell awk '/^dependencies:/ {deps = 1; next} deps && /^  - name:/ {dep = $$3} deps && /^    version:/ {gsub(/"/, ""); print "$(INFRA_CHART)/charts/" dep "-" $$2 ".tgz"} deps && /^[^ ]/ {deps = 0}' $(INFRA_CHART)/Chart.yaml)
INFRA_DEP_REPOS := $(shell awk '/^dependencies:/ {deps = 1; next} deps && /^  - name:/ {dep = $$3} deps && /^    repository:/ {gsub(/"/, ""); if ($$2 ~ /^https/) print dep "=" $$2} deps && /^[^ ]/ {deps = 0}' $(INFRA_CHART)/Chart.yaml)
DEVSTACK_DEP_REPOS := $(shell awk '/^dependencies:/ {deps = 1; next} deps && /^  - name:/ {dep = $$3} deps && /^    repository:/ {gsub(/"/, ""); if ($$2 ~ /^https/) print dep "=" $$2} deps && /^[^ ]/ {deps = 0}' $(CHARTS)/osac-devstack/Chart.yaml)
# The osac chart only has local file:// dependencies; ignore user Helm repos.
LOCAL_HELM_REPOSITORY_CONFIG := /dev/null

# Values file selection: kind uses kind-specific files, OpenShift uses profile files.
# dev-full reuses the dev control-plane values, plus an overlay that disables
# AAP-based networking provisioning and enables its local TopoLVM StorageClass.
INSTANCE_VALUES_EXTRA :=
INSTANCE_HELM_ARGS :=
INFRA_VALUES_EXTRA :=
INFRA_HELM_ARGS :=
ifeq ($(PLATFORM),kind)
INFRA_VALUES    := values/dev/kind-infra.yaml
INSTANCE_VALUES := values/dev/kind-instance.yaml
ifeq ($(PROFILE),dev-full)
INSTANCE_VALUES_EXTRA := -f values/dev/kind-instance-devfull.yaml
# AWX and the OSAC operator share NS; build the in-cluster URL from the selected namespace.
INSTANCE_HELM_ARGS := --set-string operator.aap.url=http://awx-service.$(NS).svc.cluster.local:80/api
INFRA_VALUES_EXTRA := -f values/dev/kind-infra-devfull.yaml
# Keep the OpenShift LVMS operator off on Kind and enable its TopoLVM CSI path.
INFRA_HELM_ARGS := --set topolvm.enabled=true --set lvms.enabled=false
endif
else ifeq ($(PROFILE),bmaas-netbox-ci)
INFRA_VALUES         := values/bmaas-ci/infra.yaml
INFRA_VALUES_EXTRA   := -f values/bmaas-netbox-ci/infra.yaml
INSTANCE_VALUES      := values/bmaas-ci/instance.yaml
INSTANCE_VALUES_EXTRA := -f values/bmaas-netbox-ci/instance.yaml
else ifdef PROFILE
INFRA_VALUES    := values/$(PROFILE)/infra.yaml
INSTANCE_VALUES := values/$(PROFILE)/instance.yaml
endif

# Kind cluster create/delete routing. dev-full needs a rootful runtime (KubeVirt
# chowns /dev/kvm), so it goes through the cross-platform wrapper. Apple Silicon
# also uses the wrapper for both Kind profiles, keeping image build/load and Kind
# provider selection aligned.
KIND_RUNTIME := scripts/dev-full/kind-runtime.sh
KIND_ARM64_CLI_IMAGE := quay.io/openshift/origin-cli:4.20.0

# quay.io/openshift/origin-cli:4.20.0 has no arm64 manifest. On Apple Silicon,
# build a compatible local image with the same tag so every chart can continue
# using the shared cliImage value without platform-specific values files.
HOST_IS_MAC_ARM := $(shell if [ "$$(uname -s)" = "Darwin" ] && { [ "$$(uname -m)" = "arm64" ] || [ "$$(sysctl -in hw.optional.arm64 2>/dev/null)" = "1" ]; }; then echo true; fi)
KIND_ARM64_CLI_IMAGE_SCRIPT := scripts/dev-full/build-arm64-cli-image.sh
# Apple Silicon and dev-full use the runtime wrapper. Plain non-macOS dev
# keeps its existing direct Kind/container-tool behavior.
KIND_RUNTIME_REQUIRED := $(if $(filter true,$(HOST_IS_MAC_ARM)),true,$(if $(filter dev-full,$(PROFILE)),true,false))

# Helm 4 moved post-renderers into plugins; Helm 3 still accepts an executable path.
HELM_MAJOR := $(shell version=$$(helm version --short 2>/dev/null) || exit; printf '%s\n' "$$version" | sed -nE 's/^v([0-9]+).*/\1/p')
DEVSTACK_HELM_APPLY_ARGS :=
ifeq ($(HELM_MAJOR),4)
DEV_FULL_HELM_POST_RENDERER := osac-dev-full-awx-image
# AWX mutates its resources, so devstack uses client-side apply. Helm rejects
# --force-conflicts with this mode, including during initial CRD creation.
DEVSTACK_HELM_APPLY_ARGS := --server-side=false
# Helm 4 SSA: AAP operator owns app.kubernetes.io/managed-by on the osac-aap CR.
OSAC_HELM_FORCE_CONFLICTS := --force-conflicts
else ifeq ($(HELM_MAJOR),3)
DEV_FULL_HELM_POST_RENDERER := scripts/dev-full/helm-post-renderer/helm-post-render.sh
OSAC_HELM_FORCE_CONFLICTS :=
else
DEV_FULL_HELM_POST_RENDERER :=
OSAC_HELM_FORCE_CONFLICTS :=
endif

# NS reaches shell text through KIND_RUNTIME_ENV and helm arguments, so accept
# only a valid Kubernetes namespace name. Targets that do not take NS leave it
# unset; the install targets require it in the block above.
ifneq ($(strip $(NS)),)
export NS
NS_VALID := $(shell [ -n "$$NS" ] && printf '%s' "$$NS" | grep -Eqx '[a-z0-9]([a-z0-9-]*[a-z0-9])?' && [ "$$(printf '%s' "$$NS" | wc -l)" -eq 0 ] && [ $${#NS} -le 63 ] && echo yes || echo no)
ifneq ($(NS_VALID),yes)
$(error NS must be a valid Kubernetes namespace name)
endif
endif

# PROFILE is expanded into shell recipes through KIND_RUNTIME_ENV, so accept
# only the supported profiles even for targets outside the install set.
ifneq ($(strip $(PROFILE)),)
export PROFILE
PROFILE_VALID := $(shell printf '%s' "$$PROFILE" | grep -Eqx 'dev|dev-full|vmaas-ci|bmaas-ci|bmaas-netbox-ci|caas-ci|full-ci|cudn-evpn-netris-test' && [ "$$(printf '%s' "$$PROFILE" | wc -l)" -eq 0 ] && echo yes || echo no)
ifneq ($(PROFILE_VALID),yes)
$(error PROFILE is not a supported profile)
endif
endif

# Container tool and Kind cluster config.
CONTAINER_TOOL ?= $(or $(shell command -v podman 2>/dev/null),$(shell command -v docker 2>/dev/null),$(error neither podman nor docker found in PATH))
# CONTAINER_TOOL is interpolated into shell recipes and passed to child makes,
# so accept only a bare executable name or an absolute path to one. The value
# reaches the shell through the environment; it is never spliced into shell
# text.
export CONTAINER_TOOL
CONTAINER_TOOL_VALID := $(shell [ -n "$$CONTAINER_TOOL" ] && printf '%s' "$$CONTAINER_TOOL" | grep -Eqx '[A-Za-z0-9_-]+|/[A-Za-z0-9_./-]+' && [ "$$(printf '%s' "$$CONTAINER_TOOL" | wc -l)" -eq 0 ] && echo yes || echo no)
ifneq ($(CONTAINER_TOOL_VALID),yes)
$(error CONTAINER_TOOL must be an executable name or an absolute path, using only letters, digits, dot, dash, underscore or slash)
endif
# kind defaults to the Docker provider. On Apple Silicon, honor an explicitly
# selected container tool when the Kind provider has not been set separately;
# this keeps an existing Docker- or Podman-backed cluster on the same runtime
# for all installer operations. On other platforms a podman container tool
# means there is no Docker socket, so use the podman provider.
# KIND_EXPERIMENTAL_PROVIDER is expanded into shell recipes through
# KIND_RUNTIME_ENV, so accept only the providers kind supports.
export KIND_EXPERIMENTAL_PROVIDER
KIND_EXPERIMENTAL_PROVIDER_VALID := $(shell if [ -z "$$KIND_EXPERIMENTAL_PROVIDER" ]; then echo yes; elif printf '%s' "$$KIND_EXPERIMENTAL_PROVIDER" | grep -Eqx 'docker|podman' && [ "$$(printf '%s' "$$KIND_EXPERIMENTAL_PROVIDER" | wc -l)" -eq 0 ]; then echo yes; else echo no; fi)
ifneq ($(KIND_EXPERIMENTAL_PROVIDER_VALID),yes)
$(error KIND_EXPERIMENTAL_PROVIDER must be docker or podman)
endif
KIND_RUNTIME_PROVIDER := $(strip $(KIND_EXPERIMENTAL_PROVIDER))
ifeq ($(KIND_RUNTIME_PROVIDER),)
ifeq ($(HOST_IS_MAC_ARM),true)
ifneq ($(filter command line environment,$(origin CONTAINER_TOOL)),)
KIND_RUNTIME_PROVIDER := $(notdir $(CONTAINER_TOOL))
endif
else ifeq ($(notdir $(CONTAINER_TOOL)),podman)
KIND_RUNTIME_PROVIDER := podman
endif
endif
KIND_RUNTIME_ENV := KIND_PROFILE=$(PROFILE) \
	KIND_OSAC_NAMESPACE=$(NS) \
	$(if $(KIND_RUNTIME_PROVIDER),KIND_EXPERIMENTAL_PROVIDER=$(KIND_RUNTIME_PROVIDER))
KIND_RUNTIME_CMD := $(KIND_RUNTIME_ENV) $(KIND_RUNTIME)
KIND_CLUSTER_NAME ?= osac-dev
# The cluster name becomes a container name and appears unquoted in kind
# commands, so accept only a plain name.
export KIND_CLUSTER_NAME
KIND_CLUSTER_NAME_VALID := $(shell printf '%s' "$$KIND_CLUSTER_NAME" | grep -Eqx '[A-Za-z0-9][A-Za-z0-9_.-]*' && [ "$$(printf '%s' "$$KIND_CLUSTER_NAME" | wc -l)" -eq 0 ] && [ $${#KIND_CLUSTER_NAME} -le 63 ] && echo yes || echo no)
ifneq ($(KIND_CLUSTER_NAME_VALID),yes)
$(error KIND_CLUSTER_NAME must be a plain cluster name, using only letters, digits, dot, dash or underscore)
endif
KIND_CONFIG := kind-config.yaml
ifeq ($(PROFILE),dev-full)
KIND_CONFIG := kind-config-dev-full.yaml
endif
# dev-full runs its cluster in a separate (rootful) podman daemon; give it a
# distinct kubeconfig so it never collides with the rootless PROFILE=dev cluster
# of the same name (both would otherwise share this one file and clobber it).
ifeq ($(PROFILE),dev-full)
KIND_KUBECONFIG := $(HOME)/.kube/$(KIND_CLUSTER_NAME)-kind-root.kubeconfig
else
KIND_KUBECONFIG := $(HOME)/.kube/$(KIND_CLUSTER_NAME)-kind.kubeconfig
endif
# Use the caller's KUBECONFIG if they set a non-empty one, otherwise the kind file.
# `?=` is not enough: a shell that exports KUBECONFIG= (empty-but-defined) would keep
# it empty and helm/kubectl would fall back to localhost:8080. `$(or ...)` skips empty.
export KUBECONFIG := $(or $(KUBECONFIG),$(KIND_KUBECONFIG))

# UI has no component Makefile, so the installer keeps its local image reference.
KIND_UI_IMAGE := ghcr.io/osac-project/osac-ui:latest
KIND_LOAD_IMAGE_SCRIPT ?= $(abspath scripts/dev-full/kind-load-image.sh)
define kind-load-ui-image
	$(KIND_RUNTIME_ENV) NS="$(NS)" \
	KIND_CLUSTER_NAME="$(KIND_CLUSTER_NAME)" KUBECONFIG="$(KUBECONFIG)" \
	CONTAINER_TOOL="$(CONTAINER_TOOL)" "$(KIND_LOAD_IMAGE_SCRIPT)" "$(1)"
endef

define kind-load-image
	@if [ "$(KIND_RUNTIME_REQUIRED)" = "true" ]; then \
		tmpfile=$$(mktemp /tmp/kind-image-XXXXXX.tar); \
		trap 'rm -f "$$tmpfile"' EXIT; \
		$(KIND_RUNTIME_CMD) container save $(1) -o "$$tmpfile"; \
		$(KIND_RUNTIME_CMD) load image-archive "$$tmpfile" --name $(KIND_CLUSTER_NAME); \
	elif [ "$$(basename $(CONTAINER_TOOL))" = "podman" ]; then \
		tmpfile=$$(mktemp /tmp/kind-image-XXXXXX.tar); \
		trap 'rm -f "$$tmpfile"' EXIT; \
		$(CONTAINER_TOOL) save $(1) -o "$$tmpfile"; \
		$(KIND_RUNTIME_ENV) kind load image-archive "$$tmpfile" --name $(KIND_CLUSTER_NAME); \
	else \
		$(KIND_RUNTIME_ENV) kind load docker-image $(1) --name $(KIND_CLUSTER_NAME); \
	fi
endef

define kind-container-build
	@if [ "$(KIND_RUNTIME_REQUIRED)" = "true" ]; then \
		$(KIND_RUNTIME_CMD) container build $(1); \
	else \
		$(CONTAINER_TOOL) build $(1); \
	fi
endef

# -- Help --------------------------------------------------------------

.PHONY: help
help: ## Display this help
	@awk 'BEGIN {FS = ":.*##"; printf "\nUsage:\n  make \033[36m<target>\033[0m\n"} /^[a-zA-Z_0-9-]+:.*?##/ { printf "  \033[36m%-30s\033[0m %s\n", $$1, $$2 } /^##@/ { printf "\n\033[1m%s\033[0m\n", substr($$0, 5) } ' $(MAKEFILE_LIST)

##@ Entity: Local Kind Images

.PHONY: kind-load-images
kind-load-images: ## Load previously built dev-full images into Kind (PLATFORM=kind PROFILE=dev-full NS= required)
	@if [ "$(PLATFORM)/$(PROFILE)" != "kind/dev-full" ]; then \
		echo "ERROR: kind-load-images requires PLATFORM=kind PROFILE=dev-full" >&2; exit 1; \
	fi
	$(MAKE) -C ../fulfillment-service kind-load-image NS=$(NS) KIND_CLUSTER_NAME=$(KIND_CLUSTER_NAME) CONTAINER_TOOL=$(CONTAINER_TOOL)
	$(MAKE) -C ../osac-operator kind-load-image NS=$(NS) KIND_CLUSTER_NAME=$(KIND_CLUSTER_NAME) CONTAINER_TOOL=$(CONTAINER_TOOL)
	$(MAKE) -C ../osac-aap kind-load-image NS=$(NS) KIND_CLUSTER_NAME=$(KIND_CLUSTER_NAME) CONTAINER_TOOL=$(CONTAINER_TOOL)
	$(call kind-load-ui-image,$(KIND_UI_IMAGE))
##@ Entity: Infra

.PHONY: install-infra
install-infra: helm-infra-deps ## Install infrastructure (PLATFORM= PROFILE= NS= required)
ifeq ($(PLATFORM),kind)
ifeq ($(HOST_IS_MAC_ARM),true)
	@$(KIND_RUNTIME_CMD) check
	@$(KIND_RUNTIME_CMD) create-cluster $(KIND_CLUSTER_NAME) $(KIND_CONFIG) $(KIND_KUBECONFIG)
else ifeq ($(PROFILE),dev-full)
	@$(KIND_RUNTIME_CMD) check
	@$(KIND_RUNTIME_CMD) create-cluster $(KIND_CLUSTER_NAME) $(KIND_CONFIG) $(KIND_KUBECONFIG)
else
	@if $(KIND_RUNTIME_ENV) kind get nodes --name $(KIND_CLUSTER_NAME) | grep -q .; then \
		echo "Kind cluster '$(KIND_CLUSTER_NAME)' already exists"; \
	else \
		echo "Creating Kind cluster '$(KIND_CLUSTER_NAME)'..."; \
		$(KIND_RUNTIME_ENV) kind create cluster --name $(KIND_CLUSTER_NAME) --config $(KIND_CONFIG) --wait 60s; \
	fi
	@$(KIND_RUNTIME_ENV) kind export kubeconfig --name $(KIND_CLUSTER_NAME) --kubeconfig $(KIND_KUBECONFIG)
endif
ifeq ($(HOST_IS_MAC_ARM),true)
	@$(KIND_RUNTIME_ENV) $(KIND_ARM64_CLI_IMAGE_SCRIPT) $(KIND_CLUSTER_NAME) $(KIND_ARM64_CLI_IMAGE)
endif
	helm upgrade --install cert-manager oci://quay.io/jetstack/charts/cert-manager \
		--version v1.20.0 --namespace cert-manager --create-namespace \
		--set crds.enabled=true --wait --timeout 5m
	helm upgrade --install trust-manager oci://quay.io/jetstack/charts/trust-manager \
		--version v0.22.0 --namespace cert-manager \
		--set defaultPackage.enabled=false --wait --timeout 5m
	helm upgrade --install envoy-gateway oci://docker.io/envoyproxy/gateway-helm \
		--version v1.6.5 --namespace envoy-gateway --create-namespace \
		--wait --timeout 5m
	helm upgrade --install strimzi-cluster-operator oci://quay.io/strimzi-helm/strimzi-kafka-operator \
		--version 1.1.0 --namespace osac-kafka --create-namespace \
		--wait --timeout 5m
	helm upgrade --install osac-infra $(INFRA_CHART) \
		--namespace osac-infra --create-namespace \
		-f $(INFRA_VALUES) $(INFRA_VALUES_EXTRA) \
		--set osacNamespace=$(NS) \
		$(INFRA_HELM_ARGS) \
		--wait-for-jobs --timeout 30m \
	|| { echo "=== osac-infra hook jobs ==="; kubectl get jobs,pods,cm -n osac-infra || true; kubectl logs -n osac-infra -l job-name=osac-infra-apply-ca-bundle --tail=200 || true; kubectl get deploy,endpoints,certificate -n cert-manager || true; exit 1; }
	@for f in ../osac-operator/config/crd/fakes/*.yaml; do \
		case "$$(basename "$$f")" in \
			*osac.openshift.io*|kustomization.*) continue ;; \
		esac; \
		kubectl apply --server-side --force-conflicts -f "$$f"; \
	done
else
	$(eval OCP_VERSION := $(shell oc get clusterversion version -o jsonpath='{.status.desired.version}' | cut -d. -f1,2))
	@bash -c 'source scripts/lib.sh && \
		for ns in ansible-aap cert-manager-operator cert-manager openshift-storage metallb-system multicluster-engine openshift-cnv osac-kafka; do \
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
		--values $(INFRA_VALUES) $(INFRA_VALUES_EXTRA) \
		--set osacNamespace=$(NS) \
		--set lvms.channel=stable-$(OCP_VERSION) \
		--set keycloak.hostname=https://keycloak-keycloak.$(DOMAIN) \
		--set keycloak.route.hostname=keycloak-keycloak.$(DOMAIN) \
		--set "bundledVault.keycloakIssuerUrl=https://keycloak-keycloak.$(DOMAIN)/realms/osac" \
		--timeout 30m --wait-for-jobs \
	|| { echo "=== osac-infra hook jobs ==="; oc get jobs,pods,cm -n osac-infra || true; oc logs -n osac-infra -l job-name=osac-infra-apply-ca-bundle --tail=200 || true; oc get deploy,endpoints,certificate -n cert-manager || true; exit 1; }
endif

.PHONY: uninstall-infra
uninstall-infra: ## Uninstall infrastructure (PLATFORM= PROFILE= NS= required)
ifeq ($(PLATFORM),kind)
ifeq ($(HOST_IS_MAC_ARM),true)
	@$(KIND_RUNTIME_CMD) delete-cluster $(KIND_CLUSTER_NAME)
else ifeq ($(PROFILE),dev-full)
	@$(KIND_RUNTIME_CMD) delete-cluster $(KIND_CLUSTER_NAME)
else
	helm uninstall osac-infra --namespace osac-infra --ignore-not-found
	helm uninstall strimzi-cluster-operator --namespace osac-kafka --ignore-not-found
	helm uninstall envoy-gateway --namespace envoy-gateway --ignore-not-found
	helm uninstall trust-manager --namespace cert-manager --ignore-not-found
	helm uninstall cert-manager --namespace cert-manager --ignore-not-found
	$(KIND_RUNTIME_ENV) kind delete cluster --name $(KIND_CLUSTER_NAME)
endif
else
	helm uninstall osac-infra --namespace osac-infra --ignore-not-found --wait --timeout 10m
	helm uninstall osac-deps --namespace osac-deps --ignore-not-found
endif

##@ Entity: Osac

AAP_LICENSE_FILE ?= values/$(PROFILE)/license.zip

.PHONY: local-operator-image
local-operator-image: ## Build the operator from this checkout and load it into Kind
ifeq ($(KIND_RUNTIME_REQUIRED),true)
	$(call kind-container-build,-t $(OPERATOR_IMAGE) -f ../osac-operator/Containerfile ..)
else
	$(MAKE) -C ../osac-operator image-build IMG='$(OPERATOR_IMAGE)' CONTAINER_TOOL='$(CONTAINER_TOOL)'
endif
	$(call kind-load-image,$(OPERATOR_IMAGE))

.PHONY: install-osac
install-osac: $(if $(filter true,$(USE_LOCAL_OPERATOR)),local-operator-image) ## Install OSAC instance (PLATFORM= PROFILE= NS= required)
	rm -rf $(OSAC_CHART)/charts
	helm --repository-config $(LOCAL_HELM_REPOSITORY_CONFIG) dependency build $(OSAC_CHART)
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
	@helm upgrade --install osac $(OSAC_CHART) \
		-f $(INSTANCE_VALUES) $(INSTANCE_VALUES_EXTRA) \
		--namespace $(NS) --create-namespace \
		--set-string global.osacDeploymentId=$(NS)/osac \
		$(if $(DOMAIN),--set service.externalHostname=fulfillment-api-$(NS).$(DOMAIN) --set service.internalHostname=fulfillment-internal-api-$(NS).$(DOMAIN) --set global.clusterDomain=$(DOMAIN) --set ui.auth.oidcIssuerURL=https://keycloak-keycloak.$(DOMAIN)/realms/osac) \
		$(INSTANCE_HELM_ARGS) \
		$(EXTRA_HELM_ARGS) $(LOCAL_OPERATOR_HELM_ARGS) $(OSAC_HELM_FORCE_CONFLICTS) \
		--wait --timeout 40m

.PHONY: uninstall-osac
uninstall-osac: ## Uninstall OSAC instance (PLATFORM= PROFILE= NS= required)
	helm uninstall osac --namespace $(NS) --ignore-not-found

##@ Entity: Dev Stack (kind PROFILE=dev-full only)

.PHONY: install-devstack
install-devstack: helm-devstack-deps ## Install dev-full extras: KubeVirt/CDI/Multus + AWX + UI Gateway route + seeded catalog
ifneq ($(PLATFORM)/$(PROFILE),kind/dev-full)
	@echo "ERROR: install-devstack requires PLATFORM=kind PROFILE=dev-full" >&2; exit 1
endif
	@case "$(HELM_MAJOR)" in 3|4) ;; *) echo "ERROR: install-devstack requires Helm 3 or 4; could not detect a supported Helm version" >&2; exit 1;; esac
  # Pre-install: node-level CNI setup (requires host container runtime access)
	$(KIND_RUNTIME_ENV) scripts/dev-full/install-virt-node-setup.sh $(KIND_CLUSTER_NAME)
ifeq ($(HELM_MAJOR),4)
	@if helm plugin list | awk '$$1 == "osac-dev-full-awx-image" { found=1 } END { exit !found }'; then \
		helm plugin uninstall osac-dev-full-awx-image; \
	fi
	helm plugin install scripts/dev-full/helm-post-renderer
endif
	# Helm install: operators + AWX + UI Gateway route + seeding
	helm upgrade --install osac-devstack charts/osac-devstack \
		-f $(INSTANCE_VALUES) $(INSTANCE_VALUES_EXTRA) \
		--namespace $(NS) --create-namespace \
		--set osacNamespace=$(NS) \
		--set kindClusterName=$(KIND_CLUSTER_NAME) \
		--set ui.enabled=true \
		--post-renderer $(DEV_FULL_HELM_POST_RENDERER) \
		$(DEVSTACK_HELM_APPLY_ARGS) \
		--wait --timeout 30m

##@ Composite

.PHONY: install
ifeq ($(DRY_RUN),1)
install: install-dry-run ## Render full install manifests without cluster changes (DRY_RUN=1)
else ifeq ($(PROFILE),dev-full)
install: install-infra install-osac install-devstack ## Full install: infra + osac (+ dev stack for dev-full)
else
install: install-infra install-osac ## Full install: infra + osac (PLATFORM= PROFILE= required)
endif

.PHONY: install-dry-run
install-dry-run: helm-deps ## Render every release in make install without connecting to a cluster
ifeq ($(PLATFORM),kind)
	helm template cert-manager oci://quay.io/jetstack/charts/cert-manager \
		--version v1.20.0 --namespace cert-manager \
		--set crds.enabled=true
	helm template trust-manager oci://quay.io/jetstack/charts/trust-manager \
		--version v0.22.0 --namespace cert-manager \
		--set defaultPackage.enabled=false
	helm template envoy-gateway oci://docker.io/envoyproxy/gateway-helm \
		--version v1.6.5 --namespace envoy-gateway
	helm template strimzi-cluster-operator oci://quay.io/strimzi-helm/strimzi-kafka-operator \
		--version 1.1.0 --namespace osac-kafka
	helm template osac-infra $(INFRA_CHART) \
		--namespace osac-infra \
		-f $(INFRA_VALUES) $(INFRA_VALUES_EXTRA) \
		--set osacNamespace=$(NS)
else
	helm template osac-deps $(DEPS_CHART) \
		--namespace osac-deps \
		--values $(INFRA_VALUES) \
		--set lvms.channel=stable-$(DRY_RUN_OCP_VERSION)
	helm template osac-infra $(INFRA_CHART) \
		--namespace osac-infra \
		--values $(INFRA_VALUES) \
		--set osacNamespace=$(NS) \
		--set lvms.channel=stable-$(DRY_RUN_OCP_VERSION) \
		--set keycloak.hostname=https://keycloak-keycloak.$(DRY_RUN_CLUSTER_DOMAIN) \
		--set keycloak.route.hostname=keycloak-keycloak.$(DRY_RUN_CLUSTER_DOMAIN) \
		--set "bundledVault.keycloakIssuerUrl=https://keycloak-keycloak.$(DRY_RUN_CLUSTER_DOMAIN)/realms/osac"
endif
	helm template osac $(OSAC_CHART) \
		-f $(INSTANCE_VALUES) $(INSTANCE_VALUES_EXTRA) \
		--namespace $(NS) \
		--set-string global.osacDeploymentId=$(NS)/osac \
		$(if $(filter openshift,$(PLATFORM)),--set service.externalHostname=fulfillment-api-$(NS).$(DRY_RUN_CLUSTER_DOMAIN) --set service.internalHostname=fulfillment-internal-api-$(NS).$(DRY_RUN_CLUSTER_DOMAIN) --set global.clusterDomain=$(DRY_RUN_CLUSTER_DOMAIN) --set ui.auth.oidcIssuerURL=https://keycloak-keycloak.$(DRY_RUN_CLUSTER_DOMAIN)/realms/osac) \
		$(EXTRA_HELM_ARGS) $(LOCAL_OPERATOR_HELM_ARGS)
ifeq ($(PROFILE),dev-full)
	helm dependency build charts/osac-devstack
	helm template osac-devstack charts/osac-devstack \
		--namespace $(NS) \
		--set osacNamespace=$(NS) \
		--set kindClusterName=$(KIND_CLUSTER_NAME) \
		--set ui.enabled=true
endif

.PHONY: uninstall
uninstall: uninstall-osac uninstall-infra ## Full uninstall: osac + infra (PLATFORM= PROFILE= required)

##@ Entity: Test

.PHONY: test
test: $(if $(filter fulfillment operator,$(SUITE)),local-operator-image) ## Run integration tests (SUITE= required: all|fulfillment|operator|bmf)
ifeq ($(SUITE),all)
	$(MAKE) test SUITE=fulfillment
	$(MAKE) test SUITE=operator
	$(MAKE) test SUITE=bmf
endif
ifeq ($(SUITE),fulfillment)
# Exercise event-driven reconciliation without full synchronization in every test phase.
	$(call kind-container-build,--build-arg GODEBUG=$(GODEBUG) -t localhost/fulfillment-service:it -f ../fulfillment-service/Containerfile ..)
	$(call kind-load-image,localhost/fulfillment-service:it)
	$(MAKE) install-osac PLATFORM=$(PLATFORM) PROFILE=$(PROFILE) NS=$(NS) USE_LOCAL_OPERATOR=false \
		EXTRA_HELM_ARGS="--set service.images.service.repository=localhost/fulfillment-service --set service.images.service.tag=it --set service.images.service.pullPolicy=Never --set service.controller.sync=false --set operator.enabled=false --set operator.networkManagers.enabled=true --set global.networking.networkClass.enabled=false $(MCP_TEST_HELM_ARGS)"
	@for f in ../fulfillment-service/it/crds/*.yaml; do kubectl apply --server-side --field-manager=helm --force-conflicts -f "$$f"; done
# Manager capability discovery and readiness both require the operator, so keep them isolated from the service-only suite.
	cd ../fulfillment-service && KIND_CLUSTER="$(KIND_CLUSTER_NAME)" IT_HTTPS_PORT="$(FULFILLMENT_IT_HTTPS_PORT)" IT_MCP_DEPLOYED_URL="$(MCP_TEST_URL)" ginkgo run --timeout 1h15m -v --skip 'NetworkClass manager (capability discovery|readiness)' $(FULFILLMENT_GINKGO_ARGS) it
	$(MAKE) install-osac PLATFORM=$(PLATFORM) PROFILE=$(PROFILE) NS=$(NS) USE_LOCAL_OPERATOR=false \
		EXTRA_HELM_ARGS="--set service.images.service.repository=localhost/fulfillment-service --set service.images.service.tag=it --set service.images.service.pullPolicy=Never --set service.controller.sync=false --set operator.enabled=true $(OPERATOR_IMAGE_HELM_ARGS) --set operator.networkManagers.enabled=true --set global.networking.networkClass.enabled=false"
	cd ../fulfillment-service && KIND_CLUSTER="$(KIND_CLUSTER_NAME)" IT_HTTPS_PORT="$(FULFILLMENT_IT_HTTPS_PORT)" ginkgo run --timeout 1h -v --focus 'NetworkClass manager capability discovery' it
	$(MAKE) install-osac PLATFORM=$(PLATFORM) PROFILE=$(PROFILE) NS=$(NS) USE_LOCAL_OPERATOR=false \
		EXTRA_HELM_ARGS="--set service.enabled=true --set service.images.service.repository=localhost/fulfillment-service --set service.images.service.tag=it --set service.images.service.pullPolicy=Never --set service.controller.sync=false --set operator.enabled=true $(OPERATOR_IMAGE_HELM_ARGS) --set operator.controllers.tenant=false --set operator.controllers.storage=false --set operator.controllers.volume=false --set operator.controllers.networking=true --set operator.controllers.networkingProvisioning=false --set operator.networkManagers.enabled=true --set operator.networkManagers.fabricManagers.netris.enabled=true --set global.services.caas.enabled=false --set global.services.vmaas.enabled=false --set global.services.bmaas.enabled=false --set global.services.maas.enabled=false --set global.networking.networkClass.enabled=false --set aap.instanceGroups.publishTemplates.enabled=false"
	cd ../fulfillment-service && KIND_CLUSTER="$(KIND_CLUSTER_NAME)" IT_HTTPS_PORT="$(FULFILLMENT_IT_HTTPS_PORT)" ginkgo run --timeout 15m -v --focus 'NetworkClass manager readiness' it
	$(MAKE) install-osac PLATFORM=$(PLATFORM) PROFILE=$(PROFILE) NS=$(NS) USE_LOCAL_OPERATOR=false \
		EXTRA_HELM_ARGS="--set service.images.service.repository=localhost/fulfillment-service --set service.images.service.tag=it --set service.images.service.pullPolicy=Never --set service.controller.sync=false --set global.services.bmaas.enabled=false --set operator.enabled=false --set operator.networkManagers.enabled=true --set global.networking.networkClass.enabled=false"
	cd ../fulfillment-service && KIND_CLUSTER="$(KIND_CLUSTER_NAME)" IT_HTTPS_PORT="$(FULFILLMENT_IT_HTTPS_PORT)" IT_TEST_SUITE=bmaas-disabled ginkgo run --timeout 1h -v --focus 'Service enablement' it
endif
ifeq ($(SUITE),operator)
	$(MAKE) install-osac PLATFORM=$(PLATFORM) PROFILE=$(PROFILE) NS=$(NS) USE_LOCAL_OPERATOR=false \
		EXTRA_HELM_ARGS="$(OPERATOR_IMAGE_HELM_ARGS) --set operator.fulfillment.serverAddress= --set service.enabled=false --set hubAccess.enabled=false --set validation.enabled=false --set global.fulfillmentTrust.enabled=false --set global.networking.networkClass.enabled=false"
	cd ../osac-operator && ginkgo run --timeout 30m -v test/integration
endif
ifeq ($(SUITE),bmf)
	$(call kind-container-build,--build-arg GODEBUG=$(GODEBUG) -t localhost/bmf-operator:it -f ../bare-metal-fulfillment-operator/Containerfile ..)
	$(call kind-load-image,localhost/bmf-operator:it)
	kubectl apply --server-side --force-conflicts -f ../bare-metal-fulfillment-operator/test/crds/
	$(MAKE) install-osac PLATFORM=$(PLATFORM) PROFILE=$(PROFILE) NS=$(NS) USE_LOCAL_OPERATOR=false \
		EXTRA_HELM_ARGS="--set global.services.bmaas.enabled=true --set bmf.replicaCount=1 --set bmf.image.repository=localhost/bmf-operator --set bmf.image.tag=it --set bmf.image.pullPolicy=Never --set operator.enabled=false --set operator.networkManagers.enabled=true --set service.enabled=false --set hubAccess.enabled=false --set validation.enabled=false --set global.networking.networkClass.enabled=false"
	cd ../bare-metal-fulfillment-operator && ginkgo run --timeout 30m -v test/integration
endif

##@ Charts

.PHONY: sync-charts
sync-charts: helm-deps ## Rebuild chart dependencies (alias for helm-deps)

.PHONY: helm-deps
helm-deps: ## Build OSAC and devstack Helm chart dependencies
	rm -rf $(OSAC_CHART)/charts
	helm --repository-config $(LOCAL_HELM_REPOSITORY_CONFIG) dependency build $(OSAC_CHART)
	$(MAKE) helm-devstack-deps

.PHONY: helm-infra-deps
helm-infra-deps: $(INFRA_DEP_ARCHIVES) ## Vendor the osac-infra chart dependencies

# One rule for every archive: `helm dependency build` vendors all declared
# dependencies in a single pass, so any missing archive triggers a full build.
# Repositories are registered from Chart.yaml so helm's lock-based resolution
# can find them on rebuilds.
ifneq ($(INFRA_DEP_ARCHIVES),)
$(INFRA_DEP_ARCHIVES):
	@for repo in $(INFRA_DEP_REPOS); do \
		helm repo add "$${repo%%=*}" "$${repo#*=}" --force-update || exit 1; \
	done
	helm dependency build $(INFRA_CHART)
endif

.PHONY: helm-devstack-deps
helm-devstack-deps: ## Build osac-devstack Helm dependencies
	@for repo in $(DEVSTACK_DEP_REPOS); do \
		helm repo add "$${repo%%=*}" "$${repo#*=}" --force-update || exit 1; \
	done
	helm dependency build $(CHARTS)/osac-devstack

.PHONY: helm-lint
helm-lint: helm-deps helm-infra-deps ## Lint all charts
	helm lint $(DEPS_CHART)
	helm lint $(INFRA_CHART)
	helm lint $(OSAC_CHART) --values $(HELM_VALIDATION_VALUES)

.PHONY: helm-template
helm-template: helm-infra-deps ## Dry-run render all templates
	helm template osac-deps $(DEPS_CHART)
	helm template osac-infra $(INFRA_CHART)
	helm template osac $(OSAC_CHART) --values $(HELM_VALIDATION_VALUES)

# Schema-error patterns accept Helm 3 dotted paths and Helm 4 JSON pointers.
.PHONY: csi-render-test
csi-render-test: helm-deps helm-infra-deps ## Verify CSI and storage-provider chart rendering
	@set -euo pipefail; \
		caas_render="$$(mktemp)"; \
		vmaas_render="$$(mktemp)"; \
		default_operator_render="$$(mktemp)"; \
		full_render="$$(mktemp)"; \
		custom_render="$$(mktemp)"; \
		node_render="$$(mktemp)"; \
		infra_render="$$(mktemp)"; \
		error_render="$$(mktemp)"; \
		trap 'rm -f "$$caas_render" "$$vmaas_render" "$$default_operator_render" "$$full_render" "$$custom_render" "$$node_render" "$$infra_render" "$$error_render"' EXIT; \
		assert_helm_failure() { \
			expected="$$1"; shift; \
			if "$$@" >/dev/null 2>"$$error_render"; then \
				printf 'Helm unexpectedly accepted invalid values; expected error pattern: %s\n' "$$expected" >&2; \
				exit 1; \
			fi; \
			if ! rg -q -- "$$expected" "$$error_render"; then \
				printf 'Helm failed without the expected error pattern: %s\n' "$$expected" >&2; \
				cat "$$error_render" >&2; \
				exit 1; \
			fi; \
		}; \
		helm template osac $(OSAC_CHART) \
			--values values/caas-ci/instance.yaml \
			--set operator.networkManagers.enabled=true \
			--set service.externalHostname=fulfillment-api.example.com \
			--set service.internalHostname=fulfillment-internal-api.example.com \
			--set global.clusterDomain=example.com \
			--set ui.auth.oidcIssuerURL=https://keycloak-keycloak.example.com/realms/osac > "$$caas_render"; \
		test "$$(rg -c '^  OSAC_FULFILLMENT_ENDPOINT:' "$$caas_render")" -eq 1; \
		helm template osac $(OSAC_CHART) \
			--values values/vmaas-ci/instance.yaml \
			--set operator.networkManagers.enabled=true \
			--set service.externalHostname=fulfillment-api.example.com \
			--set service.internalHostname=fulfillment-internal-api.example.com \
			--set global.clusterDomain=example.com \
			--set ui.auth.oidcIssuerURL=https://keycloak-keycloak.example.com/realms/osac > "$$vmaas_render"; \
		rg -Uq 'name: OSAC_VENDOR_CONTROLLERS\n[[:blank:]]+value: "lvms=none"' "$$vmaas_render"; \
		test "$$(rg -c '^  OSAC_FULFILLMENT_ENDPOINT:' "$$vmaas_render")" -eq 1; \
		helm template osac $(OSAC_CHART) \
			--values $(OSAC_CHART)/ci/default-values.yaml > "$$default_operator_render"; \
		! rg -q 'name: OSAC_VENDOR_CONTROLLERS' "$$default_operator_render"; \
		test "$$(rg -c '^  OSAC_FULFILLMENT_ENDPOINT:' "$$default_operator_render")" -eq 1; \
		assert_helm_failure 'operator[./]vendorControllers[./]lvms' \
			helm template osac $(OSAC_CHART) \
			--values $(OSAC_CHART)/ci/default-values.yaml \
			--set-string operator.vendorControllers.lvms=; \
		assert_helm_failure 'operator[./]vendorControllers[./]lvms' \
			helm template osac $(OSAC_CHART) \
			--values $(OSAC_CHART)/ci/default-values.yaml \
			--set operator.vendorControllers.lvms=42; \
		helm template osac $(OSAC_CHART) \
			--values charts/osac/ci/full-values.yaml \
			--set operator.networkManagers.enabled=true > "$$full_render"; \
		test "$$(rg -c '^  OSAC_FULFILLMENT_ENDPOINT:' "$$full_render")" -eq 1; \
		! rg -q '# Source: osac/charts/(csiDriver|csiBackends)/' "$$full_render"; \
		helm template osac $(OSAC_CHART) \
			--values charts/osac/ci/full-values.yaml \
			--set operator.networkManagers.enabled=true \
			--set operator.fulfillment.configMap.name=osac-custom-fulfillment-config > "$$custom_render"; \
		rg -q '^  name: "?osac-custom-fulfillment-config"?$$' "$$custom_render"; \
		helm template csi-driver ../osac-csi-driver/charts/csi-driver \
			--show-only templates/node-daemonset.yaml \
			--set namespace=osac-csi \
			--set controller.fulfillment.configMap.name=osac-csi-fulfillment-config \
			--set controller.fulfillment.credentials.secretName=osac-csi-driver-credentials \
			--set controller.fulfillment.allowStub=false > "$$node_render"; \
		rg -Fq -- '--fulfillment-endpoint=$$(OSAC_FULFILLMENT_ENDPOINT)' "$$node_render"; \
		rg -Fq -- '--fulfillment-issuer-url=$$(OSAC_FULFILLMENT_ISSUER_URL)' "$$node_render"; \
		rg -Fq -- '--fulfillment-client-id=$$(FULFILLMENT_CLIENT_ID)' "$$node_render"; \
		rg -q 'name: osac-csi-fulfillment-config' "$$node_render"; \
		rg -q 'secretName: osac-csi-driver-credentials' "$$node_render"; \
		helm template osac-infra $(INFRA_CHART) \
			--values values/caas-ci/infra.yaml > "$$infra_render"; \
		! rg -q 'osac-infra-create-csi-creds|osac-csi-driver-credentials' "$$infra_render"; \
		assert_helm_failure "at '/namespace': value must be one of" \
			helm template csi-driver ../osac-csi-driver/charts/csi-driver \
			--set namespace=not-osac-csi; \
		echo 'CSI render checks passed.'

.PHONY: fulfillment-trust-render-test
fulfillment-trust-render-test: helm-deps ## Check verified fulfillment transport renders
	python3 scripts/test-fulfillment-trust-render.py

.PHONY: metering-auth-render-test
metering-auth-render-test: helm-deps ## Check metering service-account auth renders in supported profiles
	python3 scripts/test-metering-auth-render.py

.PHONY: mce-render-test
mce-render-test: helm-infra-deps ## Check disabled MCE defaults, explicit enablement, and CaaS inheritance
	uv run --project .. python scripts/test-mce-render.py

.PHONY: fulfillment-auth-test
fulfillment-auth-test: ## Check Fulfillment service-account token exchange behavior
	./scripts/test-fulfillment-auth.sh

.PHONY: dev-full-render-test
dev-full-render-test: helm-deps helm-infra-deps ## Check dev-full TLS and Helm post-rendering
	./scripts/test-dev-full-render.sh

.PHONY: openbao-security-context-test
openbao-security-context-test: helm-infra-deps ## Check OpenBao securityContext renders numeric UID on Kind, omits on OpenShift
	./scripts/test-openbao-security-context.sh

.PHONY: kind-runtime-test
kind-runtime-test: ## Check Kind runtime, sudo, image loading, and cluster cleanup with command stubs
	python3 scripts/test-kind-runtime.py

.PHONY: bmf-netbox-render-test
bmf-netbox-render-test: helm-deps ## Check NetBox, Metal3 management, and Secret API Helm wiring
	uv run --project .. python scripts/test-bmf-netbox-render.py

.PHONY: helm-validate
helm-validate: helm-lint csi-render-test helm-networking-test fulfillment-trust-render-test fulfillment-auth-test metering-auth-render-test mce-render-test dev-full-render-test openbao-security-context-test bmf-netbox-render-test ## Validate all charts (lint + template)
	@# bmaas-netbox-ci layers bmaas-ci as its base (see the profile-selection
	@# block); validate the same combination the installer deploys.
	@for d in values/*/; do \
		infra="$${d}infra.yaml"; \
		extra=""; \
		case "$${d%/}" in \
			values/bmaas-netbox-ci) extra="--values values/bmaas-ci/infra.yaml";; \
		esac; \
		[ -f "$$infra" ] && echo "Validating $$infra..." && \
			helm template osac-deps $(DEPS_CHART) $$extra --values "$$infra" && \
			helm template osac-infra $(INFRA_CHART) $$extra --values "$$infra"; \
	done
	@echo "Validating values/dev/kind-infra.yaml..."
	helm template osac-infra $(INFRA_CHART) --values values/dev/kind-infra.yaml
	./scripts/validate-keycloak-credentials.sh
	./scripts/validate-hub-access-rbac.sh
	@echo "Validation passed."

.PHONY: helm-networking-test
helm-networking-test: helm-deps helm-infra-deps ## Test networking fabricManager/k8sManager validation and derivation
	./scripts/test-networking-values.sh
