# OSAC Installer -- single deployment API.
#
# Entities:  Infra (osac-deps + osac-infra), Osac (osac chart), Test
# Parameters are REQUIRED -- missing params produce hard errors.
#
# Usage:
# Every deployment target requires PLATFORM, PROFILE, and NS. No exceptions.
#   make install         PLATFORM=kind|openshift  PROFILE=dev|dev-full|vmaas-ci|bmaas-ci|bmaas-netbox-ci|caas-ci|full-ci  NS=osac
#   make install-multicluster PLATFORM=openshift PROFILE=vmaas-ci NS=osac KUBECONFIG=... REMOTE_KUBECONFIG=...
#   make prepare-remote-cluster NS=osac REMOTE_KUBECONFIG=...
#   make uninstall       PLATFORM=kind|openshift  PROFILE=dev|dev-full|vmaas-ci|bmaas-ci|bmaas-netbox-ci|caas-ci|full-ci  NS=osac
#   make install-infra   PLATFORM=kind|openshift  PROFILE=dev|dev-full|vmaas-ci|bmaas-ci|bmaas-netbox-ci|caas-ci|full-ci  NS=osac
#   make install-osac    PLATFORM=kind|openshift  PROFILE=dev|dev-full|vmaas-ci|bmaas-ci|bmaas-netbox-ci|caas-ci|full-ci  NS=osac
#   make uninstall-osac  PLATFORM=kind|openshift  PROFILE=dev|dev-full|vmaas-ci|bmaas-ci|bmaas-netbox-ci|caas-ci|full-ci  NS=osac
#   make uninstall-infra PLATFORM=kind|openshift  PROFILE=dev|dev-full|vmaas-ci|bmaas-ci|bmaas-netbox-ci|caas-ci|full-ci  NS=osac
#   make test            PLATFORM=kind|openshift  PROFILE=dev|dev-full|vmaas-ci|bmaas-ci|bmaas-netbox-ci|caas-ci|full-ci  NS=osac  SUITE=all|fulfillment|compute-storage|operator|bmf
#
# PROFILE=dev-full (kind only) is a superset of dev: it runs the same chart-based
# control plane, then layers on the full local dev stack (KubeVirt/CDI/Multus, AWX,
# UI, seeded catalog) via scripts/dev-full/. It needs a rootful container runtime
# (Linux: rootful podman or Docker; macOS: Docker Desktop or Colima) and /dev/kvm on Linux —
# see scripts/dev-full/kind-runtime.sh and README.md.
#
# Helm chart management (no parameters required):
#   make helm-lint
#   make mce-render-test
#   make helm-validate

SHELL := /usr/bin/env bash -o pipefail
.SHELLFLAGS := -ec

# Extra helm --set/--set-string args appended to the OSAC chart command.
# Caller-supplied Helm args are data, never command text: the value is
# frozen verbatim -- Make-side references such as DRY_RUN_HELM_FLAGS below
# read the frozen text without expanding Make expressions in it -- and
# exported. Recipes parse it into the ARGV array with split_argv from the
# environment, so shell metacharacters in a caller value reach Helm as
# argument text instead of executing, while quotes and backslashes still
# group a value that contains spaces into one argument. The array is
# expanded with the ${ARGV[@]+...} form so an empty list stays valid under
# `set -u` on every supported bash.
EXTRA_HELM_ARGS ?=
override EXTRA_HELM_ARGS := $(value EXTRA_HELM_ARGS)
export EXTRA_HELM_ARGS
# Optional PEM CA bundle file for Keycloak's external OIDC identity providers.
OIDC_IDP_CA_FILE ?=
export OIDC_IDP_CA_FILE
OIDC_IDP_CA_HELM_ARGS = $(if $(strip $(OIDC_IDP_CA_FILE)),--set-file keycloak.oidcIdpTrust.caPem="$${OIDC_IDP_CA_FILE}")
FULFILLMENT_GINKGO_ARGS ?=
FULFILLMENT_IT_REPORT_DIR ?=
fulfillment-json-report = $(if $(strip $(FULFILLMENT_IT_REPORT_DIR)),--json-report="$(abspath $(FULFILLMENT_IT_REPORT_DIR)/$(1).json)")
MCP_TEST_ENABLED ?= false

ifeq ($(MCP_TEST_ENABLED),true)
MCP_TEST_HELM_ARGS := --set service.mcp.enabled=true --set-string service.mcp.externalHostname=mcp.osac.localhost --set service.mcp.externalPort=8443
MCP_TEST_URL := https://mcp.osac.localhost:8443
endif
DRY_RUN ?= 0
DRY_RUN_HELM_FLAGS := $(filter --%,$(EXTRA_HELM_ARGS))
DRY_RUN_INVALID_HELM_FLAGS := $(filter-out --set --set-string --set=% --set-string=%,$(DRY_RUN_HELM_FLAGS))

REMOTE_STORAGE_CLASS ?= lvms-vg1
REMOTE_KUBECONFIG_SECRET_NAME ?= osac-remote-kubeconfig
REMOTE_KUBECONFIG_SECRET_KEY ?= kubeconfig
# Freeze command-line values without expanding Make expressions in them. The
# gate's validate_remote_kubeconfig_secret check (and install-osac's re-check)
# receive them through the environment and validate them before the shared
# Helm args below are expanded; the DNS-subdomain and Secret-key grammars
# cannot produce shell metacharacters, so those args expand as a word list.
override REMOTE_KUBECONFIG_SECRET_NAME := $(value REMOTE_KUBECONFIG_SECRET_NAME)
override REMOTE_KUBECONFIG_SECRET_KEY := $(value REMOTE_KUBECONFIG_SECRET_KEY)
export REMOTE_KUBECONFIG_SECRET_NAME REMOTE_KUBECONFIG_SECRET_KEY
# REMOTE_API_ADDRESS is caller-supplied and is read by the parse-time
# multicluster conditionals below and by install recipes, so it is frozen
# verbatim before either runs: a Make expression in the value must not
# execute at parse time. The https check reads it from the exported
# environment rather than from Make expansion into recipe text.
override REMOTE_API_ADDRESS := $(value REMOTE_API_ADDRESS)
export REMOTE_API_ADDRESS
# NS is validated through the exported environment -- never through Make
# expansion into command text -- so a value carrying shell metacharacters
# cannot execute. Freezing the value verbatim (like the identifiers above)
# also keeps Make expressions such as $(shell ...) from running at
# expansion time, before validation can reject the value. Every target
# that interpolates NS validates it first through the validate-ns
# prerequisite (the gate reuses it), so a value that reaches any recipe
# text is namespace-safe. The freeze sits outside the MAKECMDGOALS gate
# above because make auto-exports command-line variables by expanding
# them: a goal the gate does not cover (install-dry-run,
# prepare-remote-cluster, validate-ns itself) would otherwise run the
# expressions in an unvalidated NS at recipe-launch time.
override NS := $(value NS)
export NS
# REMOTE_KUBECONFIG is caller-supplied and read by the parse-time
# multicluster conditionals and by the REMOTE_CLUSTER_HELM_ARGS below, and
# install-multicluster/prepare-remote-cluster pass it to sub-makes. Freeze
# it verbatim before any of those run so a Make expression in the value
# cannot execute at parse or export time; recipes read it from the exported
# environment instead of Make expansion into shell text. It has no
# Make-level default, so an unconditional freeze is safe.
override REMOTE_KUBECONFIG := $(value REMOTE_KUBECONFIG)
export REMOTE_KUBECONFIG
# Freeze a caller-supplied KUBECONFIG here, before the first parse-time
# $(shell ...) call: make expands command-line variables when it exports them
# into a shell function's environment, so a Make expression in the value would
# run there even though nothing reads KUBECONFIG yet. The kind-file default
# and the export are applied below, where KIND_KUBECONFIG is known.
ifneq ($(filter command line environment override,$(origin KUBECONFIG)),)
override KUBECONFIG := $(value KUBECONFIG)
endif

# Remote-cluster kubeconfig Helm flags, shared by the input-validation gate's
# osac-chart render and install-osac's upgrade so both see the same merged
# values. They only name Secret metadata; the kubeconfig file itself is
# staged by scripts/stage-remote-cluster-secret.sh at install time.
REMOTE_CLUSTER_HELM_ARGS :=
ifneq ($(strip $(REMOTE_KUBECONFIG)),)
REMOTE_CLUSTER_HELM_ARGS := \
	--set-string operator.remoteClusterKubeconfig=/var/run/secrets/remote/$(REMOTE_KUBECONFIG_SECRET_KEY) \
	--set-string operator.remoteClusterKubeconfigSecretName=$(REMOTE_KUBECONFIG_SECRET_NAME) \
	--set-string operator.remoteClusterKubeconfigSecretKey=$(REMOTE_KUBECONFIG_SECRET_KEY) \
	--set-string aap.configAsCode.remoteClusterKubeconfigSecretName=$(REMOTE_KUBECONFIG_SECRET_NAME) \
	--set-string aap.configAsCode.remoteClusterKubeconfigSecretKey=$(REMOTE_KUBECONFIG_SECRET_KEY)
endif

# Work around HTTP/2 stream resets from proxy.golang.org during container
# builds (see osac-test-infra#543).  Override with `make test GODEBUG=` to
# disable, or set a different value to experiment.
GODEBUG ?= http2client=0
TOPOLVM_BACKING_SIZE ?= 20G

# -- Fail-fast parameter validation ------------------------------------
# Guards fire at parse time, gated by MAKECMDGOALS so `make help` works.

ALL_TARGETS := install install-multicluster uninstall install-infra uninstall-infra install-osac uninstall-osac install-devstack kind-build-load-images validate-inputs kind-load-images test

ifneq ($(filter $(ALL_TARGETS),$(MAKECMDGOALS)),)
ifndef PLATFORM
$(error PLATFORM is required (kind|openshift))
endif
ifeq ($(filter $(PLATFORM),kind openshift),)
$(error PLATFORM=$(PLATFORM) is invalid; must be kind or openshift)
endif
ifndef PROFILE
$(error PROFILE is required (dev|dev-full|vmaas-ci|bmaas-ci|bmaas-netbox-ci|caas-ci|full-ci))
endif
ifeq ($(filter $(PROFILE),dev dev-full vmaas-ci bmaas-ci bmaas-netbox-ci caas-ci full-ci),)
$(error PROFILE=$(PROFILE) is invalid; must be dev, dev-full, vmaas-ci, bmaas-ci, bmaas-netbox-ci, caas-ci, or full-ci)
endif
ifndef NS
$(error NS is required (namespace for OSAC instance, e.g. NS=osac))
endif
ifeq ($(PLATFORM),kind)
ifeq ($(filter $(PROFILE),dev dev-full),)
$(error PLATFORM=kind only supports PROFILE=dev or PROFILE=dev-full)
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

ifneq ($(filter install-multicluster,$(MAKECMDGOALS)),)
ifneq ($(PLATFORM),openshift)
$(error install-multicluster requires PLATFORM=openshift)
endif
ifneq ($(PROFILE),vmaas-ci)
$(error install-multicluster requires PROFILE=vmaas-ci)
endif
ifndef REMOTE_KUBECONFIG
$(error REMOTE_KUBECONFIG is required (workload cluster kubeconfig))
endif
endif
ifneq ($(filter install-devstack,$(MAKECMDGOALS)),)
ifneq ($(PLATFORM),kind)
$(error install-devstack requires PLATFORM=kind)
endif
ifneq ($(PROFILE),dev-full)
$(error install-devstack requires PROFILE=dev-full)
endif
endif
ifneq ($(filter install-osac,$(MAKECMDGOALS)),)
ifneq ($(strip $(REMOTE_KUBECONFIG)$(REMOTE_API_ADDRESS)),)
ifneq ($(PLATFORM),openshift)
$(error remote-cluster installation requires PLATFORM=openshift)
endif
ifneq ($(PROFILE),vmaas-ci)
$(error remote-cluster installation requires PROFILE=vmaas-ci)
endif
ifeq ($(strip $(REMOTE_KUBECONFIG)),)
$(error REMOTE_KUBECONFIG is required when REMOTE_API_ADDRESS is set)
endif
endif
endif
ifneq ($(filter install,$(MAKECMDGOALS)),)
ifneq ($(strip $(REMOTE_KUBECONFIG)$(REMOTE_API_ADDRESS)),)
$(error install does not configure the multicluster infrastructure overlay; use install-multicluster or run install-infra and install-osac separately)
endif
endif

ifneq ($(filter prepare-remote-cluster,$(MAKECMDGOALS)),)
ifndef NS
$(error NS is required (namespace for remote workload access, e.g. NS=osac))
endif
ifeq ($(strip $(REMOTE_KUBECONFIG)),)
$(error REMOTE_KUBECONFIG is required (workload cluster kubeconfig))
endif
endif

SUITE_TARGETS := test
ifneq ($(filter $(SUITE_TARGETS),$(MAKECMDGOALS)),)
ifndef SUITE
$(error SUITE is required (all|fulfillment|compute-storage|operator|bmf))
endif
ifeq ($(filter $(SUITE),all fulfillment compute-storage operator bmf),)
$(error SUITE=$(SUITE) is invalid; must be all, fulfillment, compute-storage, operator, or bmf)
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
# Override both to install-osac from a published OCI chart artifact (e.g.
# oci://registry.stage.redhat.io/cloud-extensions/osac-chart) instead of
# building the chart from this checkout -- for validating a real staged
# release rather than the current commit. Leave unset for normal use.
# OSAC_CHART_OCI_VERSION is a semver tag, passed to Helm as --version; for a
# ref already pinned by content digest (OSAC_CHART_OCI_REF containing
# @sha256:...), leave OSAC_CHART_OCI_VERSION unset instead -- Helm parses
# --version as a semver constraint unconditionally, so it can never carry a
# digest or any other non-semver string. Installing a @sha256:-pinned ref
# requires Helm 3.17+ (the release that added OCI install-by-digest); see
# docs/guides/installation/helm-deployment-guide.md's Phase 2 section.
OSAC_CHART_OCI_REF ?=
OSAC_CHART_OCI_VERSION ?=
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

# Browser-facing hostname of the OSAC UI Route. The osac-ui chart names its
# Route "osac-ui" in the instance namespace, so OpenShift would auto-assign
# exactly this host; pinning it makes the value knowable at install-infra time,
# where Keycloak needs it for the osac-ui client's redirectUris (OSAC-3884).
# Recursively expanded on purpose -- DOMAIN is $(eval)'d inside the recipes.
UI_HOSTNAME     = osac-ui-$(NS).$(DOMAIN)

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

# install-multicluster's recipe passes the multicluster infra overlay to its
# recursive install-infra; the gate must see the same effective values, or an
# invalid overlay would only surface after prepare-remote-cluster has already
# changed the remote workload cluster. `override` keeps the append even when
# INFRA_VALUES_EXTRA is set on the command line, matching the install's
# unconditional overlay.
ifneq ($(filter install-multicluster,$(MAKECMDGOALS)),)
override INFRA_VALUES_EXTRA += -f values/vmaas-ci/multicluster-infra.yaml
endif

# Caller-supplied values lists are data, never command text. Freezing the
# final value verbatim (after every profile assignment and the multicluster
# append above) means no Make expression in a command-line or environment
# value expands -- including at export time -- and recipes word-split
# $${INFRA_VALUES_EXTRA}/$${INSTANCE_VALUES_EXTRA} from the environment,
# so shell metacharacters in a caller value reach Helm as argument text
# instead of executing. Makefile-authored profile values contain no Make
# expressions, so freezing them is a no-op.
override INFRA_VALUES_EXTRA := $(value INFRA_VALUES_EXTRA)
export INFRA_VALUES_EXTRA
override INSTANCE_VALUES_EXTRA := $(value INSTANCE_VALUES_EXTRA)
export INSTANCE_VALUES_EXTRA

# Kind cluster create/delete routing. dev-full needs a rootful runtime (KubeVirt
# chowns /dev/kvm), so it goes through the cross-platform wrapper. Apple Silicon
# also uses the wrapper for both Kind profiles, keeping image build/load and Kind
# provider selection aligned.
KIND_RUNTIME := scripts/dev-full/kind-runtime.sh
KIND_ARM64_CLI_IMAGE := quay.io/openshift/origin-cli:4.20.0
KIND_AAP_CLI_IMAGE := quay.io/openshift/origin-cli:4.19

# quay.io/openshift/origin-cli:4.20.0 has no arm64 manifest. On Apple Silicon,
# build a compatible local image with the same tag so every chart can continue
# using the shared cliImage value without platform-specific values files.
HOST_IS_MAC_ARM := $(shell if [ "$$(uname -s)" = "Darwin" ] && { [ "$$(uname -m)" = "arm64" ] || [ "$$(sysctl -in hw.optional.arm64 2>/dev/null)" = "1" ]; }; then echo true; fi)
KIND_ARM64_CLI_IMAGE_SCRIPT := scripts/dev-full/build-arm64-cli-image.sh
DEVSTACK_HELM_ARGS :=
DEVSTACK_CLUSTER_CHECK :=
ifeq ($(PLATFORM)/$(PROFILE),kind/dev-full)
ifeq ($(HOST_IS_MAC_ARM),true)
DEVSTACK_HELM_ARGS := --set arm64VmCompatibility.enabled=true
DEVSTACK_CLUSTER_CHECK := scripts/dev-full/check-cluster-compatibility.sh
endif
endif
# Apple Silicon and dev-full use the runtime wrapper. Plain non-macOS dev
# keeps its existing direct Kind/container-tool behavior.
KIND_RUNTIME_REQUIRED := $(if $(filter true,$(HOST_IS_MAC_ARM)),true,$(if $(filter dev-full,$(PROFILE)),true,false))
KIND_REQUIRE_UV := false
ifeq ($(PLATFORM)/$(PROFILE),kind/dev-full)
ifeq ($(HOST_IS_MAC_ARM),true)
KIND_REQUIRE_UV := true
endif
endif

ifeq ($(PLATFORM)/$(PROFILE)/$(KIND_RUNTIME_REQUIRED),kind/dev/false)
ifeq ($(strip $(KIND_EXPERIMENTAL_PROVIDER)),)
ifneq ($(shell command -v podman 2>/dev/null),)
override export KIND_EXPERIMENTAL_PROVIDER := podman
endif
endif
endif

# Helm 4 moved post-renderers into plugins; Helm 3 still accepts an executable path.
HELM_MAJOR := $(shell version=$$(helm version --short 2>/dev/null) || exit; printf '%s\n' "$$version" | sed -nE 's/^v([0-9]+).*/\1/p')
# Only meaningful to compare when HELM_MAJOR is 3 -- Helm 4 always postdates
# 3.17, the release that added OCI chart install by @sha256: digest.
HELM_MINOR := $(shell version=$$(helm version --short 2>/dev/null) || exit; printf '%s\n' "$$version" | sed -nE 's/^v[0-9]+\.([0-9]+).*/\1/p')
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

# Container tool and Kind cluster config.
CONTAINER_TOOL ?= $(or $(shell command -v podman 2>/dev/null),$(shell command -v docker 2>/dev/null),$(error neither podman nor docker found in PATH))
# On Apple Silicon, honor an explicitly selected container tool when the Kind
# provider has not been set separately. This keeps an existing Docker- or
# Podman-backed cluster on the same runtime for all installer operations.
KIND_RUNTIME_PROVIDER := $(strip $(KIND_EXPERIMENTAL_PROVIDER))
ifeq ($(HOST_IS_MAC_ARM),true)
ifeq ($(KIND_RUNTIME_PROVIDER),)
ifneq ($(filter command line environment,$(origin CONTAINER_TOOL)),)
KIND_RUNTIME_PROVIDER := $(notdir $(CONTAINER_TOOL))
endif
endif
endif
KIND_RUNTIME_ENV := KIND_PROFILE=$(PROFILE) \
	KIND_OSAC_NAMESPACE=$(NS) \
	TOPOLVM_BACKING_SIZE=$(TOPOLVM_BACKING_SIZE) \
	KIND_REQUIRE_UV=$(KIND_REQUIRE_UV) \
	$(if $(KIND_RUNTIME_PROVIDER),KIND_EXPERIMENTAL_PROVIDER=$(KIND_RUNTIME_PROVIDER))
KIND_RUNTIME_CMD := $(KIND_RUNTIME_ENV) $(KIND_RUNTIME)
KIND_CLUSTER_NAME ?= osac-dev
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
# Use the caller's KUBECONFIG if they set a non-empty one, otherwise the kind
# file. An empty-but-defined value still gets the default: a shell that exports
# KUBECONFIG= would otherwise leave helm/kubectl on localhost:8080. A caller
# value was frozen verbatim above, under an origin guard; here it is only
# compared through $(value ...) so a Make expression in it cannot expand. The
# kind-file default stays chart-owned text; it needs override because the
# frozen value carries origin "override", which a plain assignment cannot
# replace.
ifeq ($(strip $(value KUBECONFIG)),)
override KUBECONFIG := $(KIND_KUBECONFIG)
endif
export KUBECONFIG

# These local image references match the dev-full chart values.
KIND_FULFILLMENT_IMAGE := ghcr.io/osac-project/fulfillment-service:latest
KIND_OPERATOR_IMAGE := ghcr.io/osac-project/osac-operator:latest
KIND_METERING_IMAGE := ghcr.io/osac-project/metering-service:latest
KIND_AAP_IMAGE := ghcr.io/osac-project/osac-aap:latest
# UI has no component Makefile, so the installer keeps its local image reference.
KIND_UI_IMAGE := ghcr.io/osac-project/osac-ui:latest
KIND_LOAD_IMAGE_SCRIPT ?= $(abspath scripts/dev-full/kind-load-image.sh)
define kind-load-local-image
	$(KIND_RUNTIME_ENV) NS="$(NS)" \
	KIND_CLUSTER_NAME="$(KIND_CLUSTER_NAME)" KUBECONFIG="$${KUBECONFIG}" \
	CONTAINER_TOOL="$(CONTAINER_TOOL)" "$(KIND_LOAD_IMAGE_SCRIPT)" "$(1)"
endef

define kind-load-image
	@if [ "$(KIND_RUNTIME_REQUIRED)" = "true" ]; then \
		tmpfile=$$(mktemp /tmp/kind-image-XXXXXX.tar); \
		trap 'rm -f "$$tmpfile"' EXIT; \
		$(KIND_RUNTIME_CMD) container save $(1) > "$$tmpfile"; \
		$(KIND_RUNTIME_CMD) load image-archive "$$tmpfile" --name $(KIND_CLUSTER_NAME); \
	elif [ "$$(basename $(CONTAINER_TOOL))" = "podman" ]; then \
		tmpfile=$$(mktemp /tmp/kind-image-XXXXXX.tar); \
		trap 'rm -f "$$tmpfile"' EXIT; \
		$(CONTAINER_TOOL) save $(1) -o "$$tmpfile"; \
		kind load image-archive "$$tmpfile" --name $(KIND_CLUSTER_NAME); \
	else \
		kind load docker-image $(1) --name $(KIND_CLUSTER_NAME); \
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
kind-load-images: validate-ns ## Load previously built dev-full images into Kind (PLATFORM=kind PROFILE=dev-full NS= required)
	@if [ "$(PLATFORM)/$(PROFILE)" != "kind/dev-full" ]; then \
		echo "ERROR: kind-load-images requires PLATFORM=kind PROFILE=dev-full" >&2; exit 1; \
	fi
	$(MAKE) -C ../fulfillment-service kind-load-image IMG=$(KIND_FULFILLMENT_IMAGE) NS=$(NS) KIND_CLUSTER_NAME=$(KIND_CLUSTER_NAME) CONTAINER_TOOL=$(CONTAINER_TOOL)
	$(MAKE) -C ../osac-operator kind-load-image IMG=$(KIND_OPERATOR_IMAGE) NS=$(NS) KIND_CLUSTER_NAME=$(KIND_CLUSTER_NAME) CONTAINER_TOOL=$(CONTAINER_TOOL)
	$(MAKE) -C ../osac-aap kind-load-image IMG=$(KIND_AAP_IMAGE) NS=$(NS) KIND_CLUSTER_NAME=$(KIND_CLUSTER_NAME) CONTAINER_TOOL=$(CONTAINER_TOOL)
	$(call kind-load-local-image,$(KIND_METERING_IMAGE))
	$(call kind-load-local-image,$(KIND_UI_IMAGE))

.PHONY: kind-build-load-images
kind-build-load-images: ## Build and load dev-full images into Kind (PLATFORM=kind PROFILE=dev-full NS= required)
	@if [ "$(PLATFORM)/$(PROFILE)" != "kind/dev-full" ]; then \
		echo "ERROR: kind-build-load-images requires PLATFORM=kind PROFILE=dev-full" >&2; exit 1; \
	fi
	$(MAKE) -C ../fulfillment-service image-build IMG=$(KIND_FULFILLMENT_IMAGE) CONTAINER_TOOL=$(CONTAINER_TOOL)
	$(MAKE) -C ../osac-operator image-build IMG=$(KIND_OPERATOR_IMAGE) CONTAINER_TOOL=$(CONTAINER_TOOL)
	$(MAKE) -C ../osac-metering/metering-service image-build IMG=$(KIND_METERING_IMAGE) CONTAINER_TOOL=$(CONTAINER_TOOL)
ifeq ($(HOST_IS_MAC_ARM),true)
	$(KIND_RUNTIME_ENV) scripts/dev-full/build-arm64-cli-image.sh $(KIND_CLUSTER_NAME) $(KIND_AAP_CLI_IMAGE) 4.19.0
# The AAP build uses CONTAINER_TOOL. Copy the ARM64 CLI image there when the
# Kind provider uses a different engine.
ifneq ($(KIND_RUNTIME_PROVIDER),)
ifneq ($(KIND_RUNTIME_PROVIDER),$(notdir $(CONTAINER_TOOL)))
	@set -e; archive=$$(mktemp "$${TMPDIR:-/tmp}/osac-aap-cli-image-XXXXXX.tar"); \
		trap 'rm -f "$$archive"' EXIT; \
		$(KIND_RUNTIME_CMD) container save $(KIND_AAP_CLI_IMAGE) -o "$$archive"; \
		$(CONTAINER_TOOL) load -i "$$archive"
endif
endif
endif
	$(MAKE) -C ../osac-aap execution-environment-build IMG=$(KIND_AAP_IMAGE) CONTAINER_TOOL=$(CONTAINER_TOOL)
	$(CONTAINER_TOOL) build -f ../osac-ui/Containerfile -t $(KIND_UI_IMAGE) ..
	$(MAKE) kind-load-images PLATFORM=kind PROFILE=dev-full NS=$(NS) KIND_CLUSTER_NAME=$(KIND_CLUSTER_NAME) CONTAINER_TOOL=$(CONTAINER_TOOL)
##@ Entity: Infra

.PHONY: install-infra
install-infra: validate-inputs helm-infra-deps ## Install infrastructure (PLATFORM= PROFILE= NS= required)
ifeq ($(PLATFORM),kind)
ifeq ($(HOST_IS_MAC_ARM),true)
	@$(KIND_RUNTIME_CMD) check
	@$(KIND_RUNTIME_CMD) create-cluster $(KIND_CLUSTER_NAME) $(KIND_CONFIG) $(KIND_KUBECONFIG)
else ifeq ($(PROFILE),dev-full)
	@$(KIND_RUNTIME_CMD) check
	@$(KIND_RUNTIME_CMD) create-cluster $(KIND_CLUSTER_NAME) $(KIND_CONFIG) $(KIND_KUBECONFIG)
else
	@if kind get nodes --name $(KIND_CLUSTER_NAME) | grep -q .; then \
		echo "Kind cluster '$(KIND_CLUSTER_NAME)' already exists"; \
	else \
		echo "Creating Kind cluster '$(KIND_CLUSTER_NAME)'..."; \
		kind create cluster --name $(KIND_CLUSTER_NAME) --config $(KIND_CONFIG) --wait 60s; \
	fi; \
	if [[ "$${KIND_EXPERIMENTAL_PROVIDER:-}" == podman ]]; then \
		source scripts/lib.sh; \
		ensure_podman_pid_limits "$(KIND_CLUSTER_NAME)" kind podman; \
	fi; \
	kind export kubeconfig --name $(KIND_CLUSTER_NAME) --kubeconfig $(KIND_KUBECONFIG)
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
		-f $(INFRA_VALUES) $${INFRA_VALUES_EXTRA} \
		--set osacNamespace=$(NS) \
		$(INFRA_HELM_ARGS) \
		$(OIDC_IDP_CA_HELM_ARGS) \
		--wait-for-jobs --timeout 30m \
	|| { echo "=== osac-infra hook jobs ==="; kubectl get jobs,pods,cm -n osac-infra || true; kubectl logs -n osac-infra -l job-name=osac-infra-apply-ca-bundle --tail=200 || true; kubectl get deploy,endpoints,certificate -n cert-manager || true; exit 1; }
	@bash -c 'source scripts/lib.sh && wait_for_infra_ready kubectl'
	@for f in ../osac-operator/config/crd/fakes/*.yaml; do \
		case "$$(basename "$$f")" in \
			*osac.openshift.io*|kustomization.*) continue ;; \
		esac; \
		kubectl apply --server-side --force-conflicts -f "$$f"; \
	done
else
	$(eval OCP_VERSION := $(shell oc get clusterversion version -o jsonpath='{.status.desired.version}' | cut -d. -f1,2 | grep -Eo '^[0-9]+\.[0-9]+' | head -1))
	@bash -c 'source scripts/lib.sh && \
		for ns in ansible-aap cert-manager-operator cert-manager openshift-storage metallb-system multicluster-engine openshift-cnv osac-kafka; do \
			wait_for_namespace_cleanup "$$ns"; \
		done'
	helm upgrade --install osac-deps $(DEPS_CHART) \
		--namespace osac-deps --create-namespace \
		--values $(INFRA_VALUES) $${INFRA_VALUES_EXTRA} \
		--set lvms.channel=stable-$(OCP_VERSION) \
		$(INFRA_HELM_ARGS) \
		--timeout 30m --wait
	@bash -c 'source scripts/lib.sh && wait_for_namespace_cleanup keycloak'
	$(eval DOMAIN := $(shell oc get ingresses.config/cluster -o jsonpath='{.spec.domain}' | grep -Eo '^[a-zA-Z0-9]([a-zA-Z0-9-]*[a-zA-Z0-9])?(\.[a-zA-Z0-9]([a-zA-Z0-9-]*[a-zA-Z0-9])?)*' | head -1))
	@[ -n "$(DOMAIN)" ] || { echo "ERROR: could not determine cluster domain" >&2; exit 1; }
	# Render the full chart because the EVPN Job is optional and --show-only fails
	# when EVPN is disabled. Match Helm's wait to its deadline plus a margin.
	@evpn_job_deadline="$$(helm template osac-infra $(INFRA_CHART) \
		--namespace osac-infra \
		--values $(INFRA_VALUES) $${INFRA_VALUES_EXTRA} \
		--set osacNamespace=$(NS) \
		--set lvms.channel=stable-$(OCP_VERSION) \
		--set keycloak.hostname=https://keycloak-keycloak.$(DOMAIN) \
		--set keycloak.route.hostname=https://keycloak-keycloak.$(DOMAIN) \
		--set "bundledVault.keycloakIssuerUrl=https://keycloak-keycloak.$(DOMAIN)/realms/osac" \
		$(INFRA_HELM_ARGS) \
		$(OIDC_IDP_CA_HELM_ARGS) \
		2>/dev/null \
		| awk \
			-e '/^kind: Job$$/ { job=1 }' \
			-e 'job && /^  name: osac-infra-configure-evpn$$/ { evpn=1 }' \
			-e 'evpn && /^  activeDeadlineSeconds:/ { print $$2; exit }' \
			-e '/^---$$/ { job=0; evpn=0 }')"; \
	helm_timeout_seconds=3600; \
	if [ -n "$$evpn_job_deadline" ]; then \
		helm_timeout_seconds=$$((evpn_job_deadline + 300)); \
	fi; \
	helm upgrade --install osac-infra $(INFRA_CHART) \
		--namespace osac-infra --create-namespace \
		--values $(INFRA_VALUES) $${INFRA_VALUES_EXTRA} \
		--set osacNamespace=$(NS) \
		--set lvms.channel=stable-$(OCP_VERSION) \
		--set keycloak.hostname=https://keycloak-keycloak.$(DOMAIN) \
		--set keycloak.route.hostname=keycloak-keycloak.$(DOMAIN) \
		--set keycloak.uiUrl=https://$(UI_HOSTNAME) \
		--set "bundledVault.keycloakIssuerUrl=https://keycloak-keycloak.$(DOMAIN)/realms/osac" \
		$(OIDC_IDP_CA_HELM_ARGS) \
		$(INFRA_HELM_ARGS) \
		--timeout "$${helm_timeout_seconds}s" --wait-for-jobs \
	|| { \
		echo "=== osac-infra release status ==="; \
		helm status osac-infra -n osac-infra --show-desc || true; \
		echo "=== osac-infra hook jobs ==="; \
		oc get jobs,pods,cm -n osac-infra || true; \
		oc logs -n osac-infra -l job-name=osac-infra-apply-ca-bundle --tail=200 || true; \
		echo "=== ca-bundle resources ==="; \
		oc get bundle.trust.cert-manager.io ca-bundle -o yaml || true; \
		oc get configmap ca-bundle -n osac-infra -o yaml || true; \
		echo "=== cert-manager and trust-manager ==="; \
		oc get deploy,endpoints,certificate -n cert-manager || true; \
		oc logs deploy/trust-manager -n cert-manager --tail=100 || true; \
		exit 1; \
	}
	@bash -c 'source scripts/lib.sh && wait_for_infra_ready oc'
endif

.PHONY: uninstall-infra
uninstall-infra: validate-ns ## Uninstall infrastructure (PLATFORM= PROFILE= NS= required)
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
	kind delete cluster --name $(KIND_CLUSTER_NAME)
endif
else
	helm uninstall osac-infra --namespace osac-infra --ignore-not-found --wait --timeout 10m
	helm uninstall osac-deps --namespace osac-deps --ignore-not-found
endif

##@ Entity: Osac

AAP_LICENSE_FILE ?= values/$(PROFILE)/license.zip
# A caller-supplied path is frozen verbatim (the default stays a Make
# expression, so it is guarded by origin) and reaches the shell as
# environment data rather than as recipe text.
ifneq ($(filter command line environment,$(origin AAP_LICENSE_FILE)),)
override AAP_LICENSE_FILE := $(value AAP_LICENSE_FILE)
endif
export AAP_LICENSE_FILE

.PHONY: validate-osac-chart-oci-ref
validate-osac-chart-oci-ref: ## Validate OSAC_CHART_OCI_REF/OSAC_CHART_OCI_VERSION and the Helm version they require
ifdef OSAC_CHART_OCI_REF
	# A ref pinned by content digest (@sha256:...) already names an exact
	# artifact; Helm rejects --version there too (it's always parsed as a
	# semver constraint, and a digest string is never one), so
	# OSAC_CHART_OCI_VERSION must stay unset in that case instead of being
	# required.
	@case "$(OSAC_CHART_OCI_REF)" in \
		*@sha256:*) \
			[ -z "$(OSAC_CHART_OCI_VERSION)" ] || { echo "ERROR: OSAC_CHART_OCI_VERSION must not be set when OSAC_CHART_OCI_REF already pins a @sha256: digest" >&2; exit 1; }; \
			case "$(HELM_MAJOR)" in \
				4) ;; \
				3) [ -n "$(HELM_MINOR)" ] && [ "$(HELM_MINOR)" -ge 17 ] || { echo "ERROR: installing an OCI chart pinned by @sha256: digest requires Helm 3.17 or later (detected $$(helm version --short 2>/dev/null)); see docs/guides/installation/helm-deployment-guide.md" >&2; exit 1; } ;; \
				*) echo "ERROR: could not detect a supported Helm version; installing an OCI chart pinned by @sha256: digest requires Helm 3.17 or later" >&2; exit 1 ;; \
			esac ;; \
		*) [ -n "$(OSAC_CHART_OCI_VERSION)" ] || { echo "ERROR: OSAC_CHART_OCI_VERSION is required when OSAC_CHART_OCI_REF is set without an @sha256: digest" >&2; exit 1; } ;; \
	esac
else
	@[ -z "$(OSAC_CHART_OCI_VERSION)" ] || { echo "ERROR: OSAC_CHART_OCI_VERSION is set without OSAC_CHART_OCI_REF -- this would silently install the local checkout's chart, ignoring the version" >&2; exit 1; }
endif

# The BMF subchart requires netbox.tokenValue when the named token Secret is
# absent; profiles like bmaas-netbox-ci rely on the infra chart's
# netbox-token-seed hook to create that Secret during a real install. Offline
# rendering cannot see cluster Secrets (helm template's lookup returns nil),
# so the gate's osac-chart renders go through render_osac_chart_offline
# (scripts/lib.sh): only the specific "tokenValue is required" lookup failure
# is retried with this render-only placeholder, which is never passed to an
# installation -- and only when netbox_seed_check (run in the same recipe
# line, right before the render) saw this install's infra values deploy the
# seed hook, or when the gate's read-only cluster lookup
# (netbox_existing_secret_matches in scripts/lib.sh) confirmed the token
# Secret the chart reads already exists with its key; without either, a
# missing token is a real input error. A token supplied by the caller
# through any channel
# (--set, --set-string, or a values file) renders without the placeholder,
# so the gate validates the caller's actual value. The placeholder is
# generated at runtime so no token-shaped literal sits in the tree; every
# invocation uses a fresh value, which also keeps the render-only token
# distinct from anything a caller could supply.
NETBOX_OFFLINE_PLACEHOLDER := offline-validation-placeholder-$(shell date +%s)

.PHONY: validate-inputs
# An OCI-ref install renders the pulled chart, so the local osac chart's
# dependencies are not built for it (mirrors the install-osac guard below).
ifneq ($(OSAC_CHART_OCI_REF),)
validate-inputs: helm-infra-deps validate-osac-chart-oci-ref
else
validate-inputs: helm-osac-deps helm-infra-deps validate-osac-chart-oci-ref ## Render every applicable chart offline, rejecting invalid inputs before any cluster change
endif
# dev-full is the only profile whose gate renders the devstack chart. Its
# dependencies are a prerequisite, not a recipe step, so make shares (and
# deduplicates) the build with install-devstack's own even under make -j.
ifeq ($(PLATFORM),kind)
ifeq ($(PROFILE),dev-full)
validate-inputs: helm-devstack-deps
endif
endif
# The namespace is interpolated into later recipe command text, so every
# target that interpolates it checks it through validate-ns; the gate runs
# dependency preparation before that check, but before provisioning recipes.
# Like the remote-cluster identifiers below, the value reaches the
# validator through the exported environment, never through Make
# expansion into the quoted bash -c string.
.PHONY: validate-ns
validate-ns:
	@bash -c 'source scripts/lib.sh && validate_namespace "$${NS:?NS is required}"' \
	|| { echo "ERROR: invalid NS; no cluster changes were made. NS must be a lowercase DNS label (a-z, 0-9, '-', at most 63 characters)." >&2; exit 1; }
validate-inputs: validate-ns
# Remote-cluster identifiers are static inputs: the gate rejects an invalid
# REMOTE_KUBECONFIG_SECRET_NAME/KEY before any cluster mutation. install-osac
# re-checks them, but only after install-infra and namespace creation have
# already changed the cluster. The identifiers reach the validator through
# the exported environment, never through Make expansion into the quoted
# bash -c string, so a value cannot break out of the quoting.
ifneq ($(strip $(REMOTE_KUBECONFIG)),)
	@bash -c 'source scripts/lib.sh && \
		validate_remote_kubeconfig_secret "$${REMOTE_KUBECONFIG_SECRET_NAME}" "$${REMOTE_KUBECONFIG_SECRET_KEY}"' \
	|| { echo "ERROR: invalid remote-cluster inputs; no cluster changes were made. Fix REMOTE_KUBECONFIG_SECRET_NAME/KEY and retry." >&2; exit 1; }
endif
ifeq ($(PLATFORM),kind)
	@source scripts/lib.sh && render_chart_offline osac-infra $(INFRA_CHART) \
		--namespace osac-infra \
		-f $(INFRA_VALUES) $${INFRA_VALUES_EXTRA} \
		--set osacNamespace=$(NS) \
		$(INFRA_HELM_ARGS) \
		$(OIDC_IDP_CA_HELM_ARGS) \
	|| { echo "ERROR: invalid installer inputs for osac-infra; no cluster changes were made. Fix the values reported above and retry." >&2; exit 1; }
	@source scripts/lib.sh && split_argv EXTRA_HELM_ARGS "$${EXTRA_HELM_ARGS}" && \
	netbox_seed_check osac-infra $(INFRA_CHART) \
		--namespace osac-infra \
		-f $(INFRA_VALUES) $${INFRA_VALUES_EXTRA} \
		--set osacNamespace=$(NS) \
		$(INFRA_HELM_ARGS) $(OIDC_IDP_CA_HELM_ARGS) && \
	render_osac_chart_offline "$(NETBOX_OFFLINE_PLACEHOLDER)" \
		osac $(if $(OSAC_CHART_OCI_REF),$(OSAC_CHART_OCI_REF)$(if $(OSAC_CHART_OCI_VERSION), --version $(OSAC_CHART_OCI_VERSION)),$(OSAC_CHART)) \
		-f $(INSTANCE_VALUES) $${INSTANCE_VALUES_EXTRA} \
		--namespace $(NS) \
		--set-string global.osacDeploymentId=$(NS)/osac \
		$(INSTANCE_HELM_ARGS) \
		$${ARGV[@]+"$${ARGV[@]}"} \
		$(REMOTE_CLUSTER_HELM_ARGS) \
	|| { echo "ERROR: invalid installer inputs for the osac chart; no cluster changes were made. Fix the values reported above and retry." >&2; exit 1; }
ifeq ($(PROFILE),dev-full)
	@source scripts/lib.sh && render_chart_offline osac-devstack $(CHARTS)/osac-devstack \
		--namespace $(NS) \
		-f $(INSTANCE_VALUES) $${INSTANCE_VALUES_EXTRA} \
		--set osacNamespace=$(NS) \
		--set kindClusterName=$(KIND_CLUSTER_NAME) \
		--set ui.enabled=true \
		$(DEVSTACK_HELM_ARGS) \
	|| { echo "ERROR: invalid installer inputs for osac-devstack; no cluster changes were made. Fix the values reported above and retry." >&2; exit 1; }
endif
else
	$(eval OCP_VERSION := $(shell oc get clusterversion version -o jsonpath='{.status.desired.version}' | cut -d. -f1,2 | grep -Eo '^[0-9]+\.[0-9]+' | head -1))
	$(eval DOMAIN := $(shell oc get ingresses.config/cluster -o jsonpath='{.spec.domain}' | grep -Eo '^[a-zA-Z0-9]([a-zA-Z0-9-]*[a-zA-Z0-9])?(\.[a-zA-Z0-9]([a-zA-Z0-9-]*[a-zA-Z0-9])?)*' | head -1))
	@[ -n "$(OCP_VERSION)" ] || { echo "ERROR: could not determine the OpenShift version for input validation. Is oc logged in?" >&2; exit 1; }
	@[ -n "$(DOMAIN)" ] || { echo "ERROR: could not determine cluster domain for input validation. Is oc logged in?" >&2; exit 1; }
	@source scripts/lib.sh && render_chart_offline osac-deps $(DEPS_CHART) \
		--namespace osac-deps \
		--values $(INFRA_VALUES) $${INFRA_VALUES_EXTRA} \
		--set lvms.channel=stable-$(OCP_VERSION) \
		$(INFRA_HELM_ARGS) \
	|| { echo "ERROR: invalid installer inputs for osac-deps; no cluster changes were made. Fix the values reported above and retry." >&2; exit 1; }
	@source scripts/lib.sh && render_chart_offline osac-infra $(INFRA_CHART) \
		--namespace osac-infra \
		--values $(INFRA_VALUES) $${INFRA_VALUES_EXTRA} \
		--set osacNamespace=$(NS) \
		--set lvms.channel=stable-$(OCP_VERSION) \
		--set keycloak.hostname=https://keycloak-keycloak.$(DOMAIN) \
		--set keycloak.route.hostname=keycloak-keycloak.$(DOMAIN) \
		--set keycloak.uiUrl=https://$(UI_HOSTNAME) \
		--set "bundledVault.keycloakIssuerUrl=https://keycloak-keycloak.$(DOMAIN)/realms/osac" \
		$(INFRA_HELM_ARGS) \
		$(OIDC_IDP_CA_HELM_ARGS) \
	|| { echo "ERROR: invalid installer inputs for osac-infra; no cluster changes were made. Fix the values reported above and retry." >&2; exit 1; }
	@source scripts/lib.sh && split_argv EXTRA_HELM_ARGS "$${EXTRA_HELM_ARGS}" && \
	netbox_seed_check osac-infra $(INFRA_CHART) \
		--namespace osac-infra \
		--values $(INFRA_VALUES) $${INFRA_VALUES_EXTRA} \
		--set osacNamespace=$(NS) \
		--set lvms.channel=stable-$(OCP_VERSION) \
		--set keycloak.hostname=https://keycloak-keycloak.$(DOMAIN) \
		--set keycloak.route.hostname=keycloak-keycloak.$(DOMAIN) \
		--set keycloak.uiUrl=https://$(UI_HOSTNAME) \
		--set "bundledVault.keycloakIssuerUrl=https://keycloak-keycloak.$(DOMAIN)/realms/osac" \
		$(INFRA_HELM_ARGS) $(OIDC_IDP_CA_HELM_ARGS) && \
	render_osac_chart_offline "$(NETBOX_OFFLINE_PLACEHOLDER)" \
		osac $(if $(OSAC_CHART_OCI_REF),$(OSAC_CHART_OCI_REF)$(if $(OSAC_CHART_OCI_VERSION), --version $(OSAC_CHART_OCI_VERSION)),$(OSAC_CHART)) \
		-f $(INSTANCE_VALUES) $${INSTANCE_VALUES_EXTRA} \
		--namespace $(NS) \
		--set-string global.osacDeploymentId=$(NS)/osac \
		--set service.externalHostname=fulfillment-api-$(NS).$(DOMAIN) \
		--set service.internalHostname=fulfillment-internal-api-$(NS).$(DOMAIN) \
		--set global.clusterDomain=$(DOMAIN) \
		--set ui.auth.oidcIssuerURL=https://keycloak-keycloak.$(DOMAIN)/realms/osac \
		--set ui.externalHostname=$(UI_HOSTNAME) \
		$(INSTANCE_HELM_ARGS) \
		$${ARGV[@]+"$${ARGV[@]}"} \
		$(REMOTE_CLUSTER_HELM_ARGS) \
	|| { echo "ERROR: invalid installer inputs for the osac chart; no cluster changes were made. Fix the values reported above and retry." >&2; exit 1; }
endif
	@echo "Installer input validation passed."

.PHONY: validate-osac-inputs
# The AAP license and the remote API address are static inputs, but they
# belong to the OSAC install only: `make install` runs install-infra
# first, whose namespace creation and readiness waits must not happen
# before a missing license or a non-https remote address is rejected.
# They are checked here, after the shared gate and before any cluster
# change, so install-infra alone still requires no license. The
# REMOTE_KUBECONFIG readability check stays at install time (and in the
# gate) because the file's permissions can change between validation and
# install.
validate-osac-inputs: validate-inputs
ifneq ($(PLATFORM),kind)
	@[ -n "$${AAP_LICENSE_FILE}" ] || { echo "ERROR: AAP_LICENSE_FILE is required for PLATFORM=openshift" >&2; exit 1; }
	@[ -f "$${AAP_LICENSE_FILE}" ] || { echo "ERROR: AAP_LICENSE_FILE not found; check the path you supplied" >&2; exit 1; }
	@if [ -n "$${REMOTE_API_ADDRESS}" ]; then case "$${REMOTE_API_ADDRESS}" in https://*) ;; *) echo "ERROR: REMOTE_API_ADDRESS must use https://" >&2; exit 1 ;; esac; fi
endif

.PHONY: install-osac
install-osac: validate-osac-inputs validate-osac-chart-oci-ref ## Install OSAC instance (PLATFORM= PROFILE= NS= required)
ifndef OSAC_CHART_OCI_REF
	rm -rf $(OSAC_CHART)/charts
	helm --repository-config $(LOCAL_HELM_REPOSITORY_CONFIG) dependency build $(OSAC_CHART)
endif
ifneq ($(PLATFORM),kind)
	oc create namespace $(NS) --dry-run=client -o yaml | oc apply -f -
	if [ -n "$${REMOTE_KUBECONFIG}" ]; then \
		test -r "$${REMOTE_KUBECONFIG}" || { echo "ERROR: REMOTE_KUBECONFIG is not readable; check the path you supplied" >&2; exit 1; }; \
		HUB_KUBECONFIG="$${KUBECONFIG}" \
		REMOTE_KUBECONFIG="$${REMOTE_KUBECONFIG}" \
		REMOTE_API_ADDRESS="$${REMOTE_API_ADDRESS}" \
		INSTALLER_NAMESPACE="$(NS)" \
		bash scripts/stage-remote-cluster-secret.sh; \
	fi
	oc create secret generic config-as-code-manifest-ig \
		--from-file=license.zip="$${AAP_LICENSE_FILE}" \
		-n $(NS) --dry-run=client -o yaml | oc apply --server-side -f -
	oc label secret config-as-code-manifest-ig \
		osac.openshift.io/project=osac-aap \
		-n $(NS) --overwrite
	@bash -c 'source scripts/lib.sh && check_postgres_prerequisites "$(NS)" "$(INFRA_VALUES)"'
	$(eval DOMAIN := $(shell oc get ingresses.config/cluster -o jsonpath='{.spec.domain}' | grep -Eo '^[a-zA-Z0-9]([a-zA-Z0-9-]*[a-zA-Z0-9])?(\.[a-zA-Z0-9]([a-zA-Z0-9-]*[a-zA-Z0-9])?)*' | head -1))
	@[ -n "$(DOMAIN)" ] || { echo "ERROR: Could not determine cluster domain. Is oc logged in?" >&2; exit 1; }
endif
	@source scripts/lib.sh; \
	if [[ -n "$${REMOTE_KUBECONFIG:-}" ]]; then \
		validate_remote_kubeconfig_secret "$${REMOTE_KUBECONFIG_SECRET_NAME}" "$${REMOTE_KUBECONFIG_SECRET_KEY}"; \
	fi; \
	split_argv EXTRA_HELM_ARGS "$${EXTRA_HELM_ARGS}"; \
	helm upgrade --install osac $(if $(OSAC_CHART_OCI_REF),$(OSAC_CHART_OCI_REF)$(if $(OSAC_CHART_OCI_VERSION), --version $(OSAC_CHART_OCI_VERSION)),$(OSAC_CHART)) \
		-f $(INSTANCE_VALUES) $${INSTANCE_VALUES_EXTRA} \
		--namespace $(NS) --create-namespace \
		--set-string global.osacDeploymentId=$(NS)/osac \
		$(if $(DOMAIN),--set service.externalHostname=fulfillment-api-$(NS).$(DOMAIN) --set service.internalHostname=fulfillment-internal-api-$(NS).$(DOMAIN) --set global.clusterDomain=$(DOMAIN) --set ui.auth.oidcIssuerURL=https://keycloak-keycloak.$(DOMAIN)/realms/osac --set ui.externalHostname=$(UI_HOSTNAME)) \
		$(INSTANCE_HELM_ARGS) \
		$${ARGV[@]+"$${ARGV[@]}"} $(OSAC_HELM_FORCE_CONFLICTS) $(REMOTE_CLUSTER_HELM_ARGS) \
		--wait --timeout 40m
.PHONY: uninstall-osac
uninstall-osac: validate-ns ## Uninstall OSAC instance (PLATFORM= PROFILE= NS= required)
	helm uninstall osac --namespace $(NS) --ignore-not-found

##@ Entity: Dev Stack (kind PROFILE=dev-full only)

.PHONY: install-devstack
install-devstack: validate-inputs helm-devstack-deps ## Install dev-full extras: KubeVirt/CDI/Multus + AWX + UI Gateway route + seeded catalog
ifneq ($(PLATFORM)/$(PROFILE),kind/dev-full)
	@echo "ERROR: install-devstack requires PLATFORM=kind PROFILE=dev-full" >&2; exit 1
endif
	@case "$(HELM_MAJOR)" in 3|4) ;; *) echo "ERROR: install-devstack requires Helm 3 or 4; could not detect a supported Helm version" >&2; exit 1;; esac
	$(DEVSTACK_CLUSTER_CHECK)
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
		-f $(INSTANCE_VALUES) $${INSTANCE_VALUES_EXTRA} \
		--namespace $(NS) --create-namespace \
		--set osacNamespace=$(NS) \
		--set kindClusterName=$(KIND_CLUSTER_NAME) \
		--set ui.enabled=true \
		$(DEVSTACK_HELM_ARGS) \
		--post-renderer $(DEV_FULL_HELM_POST_RENDERER) \
		$(DEVSTACK_HELM_APPLY_ARGS) \
		--wait --timeout 30m

##@ Composite

.PHONY: install
ifeq ($(DRY_RUN),1)
install: install-dry-run ## Render full install manifests without cluster changes (DRY_RUN=1)
else ifeq ($(PROFILE),dev-full)
ifeq ($(HOST_IS_MAC_ARM),true)
install: validate-osac-inputs install-infra ## Full ARM64 Mac install: infra + local images + osac + dev stack
	$(MAKE) kind-build-load-images PLATFORM=kind PROFILE=dev-full NS=$(NS) KIND_CLUSTER_NAME=$(KIND_CLUSTER_NAME) CONTAINER_TOOL=$(CONTAINER_TOOL)
	$(MAKE) install-osac PLATFORM=kind PROFILE=dev-full NS=$(NS) KIND_CLUSTER_NAME=$(KIND_CLUSTER_NAME) CONTAINER_TOOL=$(CONTAINER_TOOL)
	$(MAKE) install-devstack PLATFORM=kind PROFILE=dev-full NS=$(NS) KIND_CLUSTER_NAME=$(KIND_CLUSTER_NAME) CONTAINER_TOOL=$(CONTAINER_TOOL)
else
install: validate-osac-inputs install-infra install-osac install-devstack ## Full install: infra + osac (+ dev stack for dev-full)
endif
else
install: validate-osac-inputs install-infra install-osac ## Full install: infra + osac (PLATFORM= PROFILE= required)
endif
# Under `make -j install` prerequisites with no dependency edge run in
# parallel, so install-infra's namespace creation and readiness waits could
# start while validate-osac-inputs is still rejecting a missing AAP license
# or a non-https remote address. Order only the composite goal: standalone
# `make install-infra` still needs no license, and DRY_RUN=1 maps install to
# install-dry-run, which does no cluster change.
ifneq ($(DRY_RUN),1)
ifneq ($(filter install,$(MAKECMDGOALS)),)
install-infra: validate-osac-inputs
endif
endif

.PHONY: install-dry-run
install-dry-run: validate-ns helm-deps validate-osac-chart-oci-ref ## Render every release in make install without connecting to a cluster
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
		-f $(INFRA_VALUES) $${INFRA_VALUES_EXTRA} \
		--set osacNamespace=$(NS) \
		$(INFRA_HELM_ARGS) \
		$(OIDC_IDP_CA_HELM_ARGS)
else
	helm template osac-deps $(DEPS_CHART) \
		--namespace osac-deps \
		--values $(INFRA_VALUES) $${INFRA_VALUES_EXTRA} \
		--set lvms.channel=stable-$(DRY_RUN_OCP_VERSION) \
		$(INFRA_HELM_ARGS)
	helm template osac-infra $(INFRA_CHART) \
		--namespace osac-infra \
		--values $(INFRA_VALUES) $${INFRA_VALUES_EXTRA} \
		--set osacNamespace=$(NS) \
		--set lvms.channel=stable-$(DRY_RUN_OCP_VERSION) \
		--set keycloak.hostname=https://keycloak-keycloak.$(DRY_RUN_CLUSTER_DOMAIN) \
		--set keycloak.route.hostname=keycloak-keycloak.$(DRY_RUN_CLUSTER_DOMAIN) \
		--set keycloak.uiUrl=https://osac-ui-$(NS).$(DRY_RUN_CLUSTER_DOMAIN) \
		--set "bundledVault.keycloakIssuerUrl=https://keycloak-keycloak.$(DRY_RUN_CLUSTER_DOMAIN)/realms/osac" \
		$(INFRA_HELM_ARGS) \
		$(OIDC_IDP_CA_HELM_ARGS)
endif
	source scripts/lib.sh && split_argv EXTRA_HELM_ARGS "$${EXTRA_HELM_ARGS}" && \
	helm template osac $(if $(OSAC_CHART_OCI_REF),$(OSAC_CHART_OCI_REF)$(if $(OSAC_CHART_OCI_VERSION), --version $(OSAC_CHART_OCI_VERSION)),$(OSAC_CHART)) \
		-f $(INSTANCE_VALUES) $${INSTANCE_VALUES_EXTRA} \
		--namespace $(NS) \
		--set-string global.osacDeploymentId=$(NS)/osac \
		$(if $(filter openshift,$(PLATFORM)),--set service.externalHostname=fulfillment-api-$(NS).$(DRY_RUN_CLUSTER_DOMAIN) --set service.internalHostname=fulfillment-internal-api-$(NS).$(DRY_RUN_CLUSTER_DOMAIN) --set global.clusterDomain=$(DRY_RUN_CLUSTER_DOMAIN) --set ui.auth.oidcIssuerURL=https://keycloak-keycloak.$(DRY_RUN_CLUSTER_DOMAIN)/realms/osac --set ui.externalHostname=osac-ui-$(NS).$(DRY_RUN_CLUSTER_DOMAIN)) \
		$(INSTANCE_HELM_ARGS) \
		$${ARGV[@]+"$${ARGV[@]}"}
ifeq ($(PROFILE),dev-full)
	helm dependency build charts/osac-devstack
	helm template osac-devstack charts/osac-devstack \
		--namespace $(NS) \
		-f $(INSTANCE_VALUES) $${INSTANCE_VALUES_EXTRA} \
		--set osacNamespace=$(NS) \
		--set kindClusterName=$(KIND_CLUSTER_NAME) \
		--set ui.enabled=true \
		$(DEVSTACK_HELM_ARGS)
endif

# The parse-time guard above pins PLATFORM=openshift PROFILE=vmaas-ci for
# this goal, and the two phase sub-makes pin the same pair. The gate above
# therefore already rendered every chart for the same values, input
# overrides and remote-cluster arguments, so the phases skip their own
# copies instead of repeating those renders. The namespace check is covered
# by the parent gate too (both phases receive the same NS it validated),
# and the phases' other prerequisites (chart dependencies, OCI-ref checks)
# stay in force.
MULTICLUSTER_SKIP_GATE := -o validate-inputs -o validate-osac-inputs

.PHONY: install-multicluster
install-multicluster: validate-inputs ## Prepare a VMaaS workload cluster, then install OSAC on management in remote mode
	# Paths stay out of these errors: a local path can carry user or
	# customer names, and this output reaches terminals and CI logs.
	@test -r "$${KUBECONFIG}" || { echo "ERROR: KUBECONFIG is not readable; check the path you supplied" >&2; exit 1; }
	@test -r "$${REMOTE_KUBECONFIG}" || { echo "ERROR: REMOTE_KUBECONFIG is not readable; check the path you supplied" >&2; exit 1; }
	@test -f "$${AAP_LICENSE_FILE}" || { echo "ERROR: AAP_LICENSE_FILE not found; check the path you supplied" >&2; exit 1; }
	@if [ -n "$${REMOTE_API_ADDRESS}" ]; then case "$${REMOTE_API_ADDRESS}" in https://*) ;; *) echo "ERROR: REMOTE_API_ADDRESS must use https://" >&2; exit 1 ;; esac; fi
	@oc --kubeconfig "$${KUBECONFIG}" get clusterversion version >/dev/null
	@oc --kubeconfig "$${REMOTE_KUBECONFIG}" get clusterversion version >/dev/null
	@for entry in "KUBECONFIG:$${KUBECONFIG}" "REMOTE_KUBECONFIG:$${REMOTE_KUBECONFIG}"; do \
		var="$${entry%%:*}"; kubeconfig="$${entry#*:}"; \
		if [ "$$(oc --kubeconfig "$$kubeconfig" auth can-i '*' '*')" != yes ]; then \
			echo "ERROR: cluster-admin access is required for the cluster in $${var}" >&2; exit 1; \
		fi; \
	done
	@hub_api="$$(oc --kubeconfig "$${KUBECONFIG}" config view --minify -o jsonpath='{.clusters[0].cluster.server}')"; \
	remote_api="$$(oc --kubeconfig "$${REMOTE_KUBECONFIG}" config view --minify -o jsonpath='{.clusters[0].cluster.server}')"; \
	if [ -z "$$hub_api" ] || [ -z "$$remote_api" ] || [ "$$hub_api" = "$$remote_api" ]; then \
		echo "ERROR: KUBECONFIG and REMOTE_KUBECONFIG must specify different API server endpoints" >&2; exit 1; \
	fi
	$(MAKE) prepare-remote-cluster NS=$(NS) \
		REMOTE_KUBECONFIG="$${REMOTE_KUBECONFIG}" \
		REMOTE_STORAGE_CLASS="$(REMOTE_STORAGE_CLASS)" \
		EVPN_VALUES="$(INFRA_VALUES)"
	$(MAKE) $(MULTICLUSTER_SKIP_GATE) install-infra PLATFORM=openshift PROFILE=vmaas-ci NS=$(NS) \
		KUBECONFIG="$${KUBECONFIG}" \
		INFRA_VALUES_EXTRA="$${INFRA_VALUES_EXTRA}"
	$(MAKE) $(MULTICLUSTER_SKIP_GATE) install-osac PLATFORM=openshift PROFILE=vmaas-ci NS=$(NS) \
		KUBECONFIG="$${KUBECONFIG}" \
		REMOTE_KUBECONFIG="$${REMOTE_KUBECONFIG}" \
		REMOTE_API_ADDRESS="$${REMOTE_API_ADDRESS}" \
		EXTRA_HELM_ARGS="$${EXTRA_HELM_ARGS}"

.PHONY: prepare-remote-cluster
prepare-remote-cluster: validate-ns helm-infra-deps ## Prepare the remote VMaaS workload cluster without changing management
	@test -r "$${REMOTE_KUBECONFIG}" || { echo "ERROR: REMOTE_KUBECONFIG is not readable; check the path you supplied" >&2; exit 1; }
	@oc --kubeconfig "$${REMOTE_KUBECONFIG}" get clusterversion version >/dev/null
	REMOTE_KUBECONFIG="$${REMOTE_KUBECONFIG}" \
	REMOTE_STORAGE_CLASS="$(REMOTE_STORAGE_CLASS)" \
	EVPN_VALUES="$(EVPN_VALUES)" \
	INSTALLER_NAMESPACE="$(NS)" \
	bash scripts/setup-remote-cluster.sh prepare

.PHONY: uninstall
uninstall: uninstall-osac uninstall-infra ## Full uninstall: osac + infra (PLATFORM= PROFILE= required)

##@ Entity: Test

# The integration installs must run the PR's metering-service image: the
# released one still requires the FULFILLMENT_* environment variables this
# chart no longer sets, so it crash-loops before the tests can run. Metering
# is not under test in these suites; it only has to start.
METERING_IT_IMAGE := localhost/metering-service:it
METERING_IT_IMAGE_ARGS := --set metering.image.repository=localhost/metering-service --set metering.image.tag=it --set metering.image.pullPolicy=Never

.PHONY: test
test: validate-ns ## Run integration tests (SUITE= required: all|fulfillment|compute-storage|operator|bmf)
ifeq ($(SUITE),all)
	$(MAKE) test SUITE=fulfillment
	$(MAKE) test SUITE=operator
	$(MAKE) test SUITE=bmf
endif
ifneq ($(filter $(SUITE),fulfillment compute-storage),)
ifneq ($(strip $(FULFILLMENT_IT_REPORT_DIR)),)
# A reused local output directory must not retain old reports for phases a failed run never reaches.
	rm -f $(foreach phase,$(if $(filter fulfillment,$(SUITE)),service-only networking-operator bmaas-disabled) compute-storage,"$(abspath $(FULFILLMENT_IT_REPORT_DIR)/$(phase).json)")
endif
# Exercise event-driven reconciliation without full synchronization in every test phase.
	python3 -m unittest discover -s ../fulfillment-service/it/testdata -p test_compute_storage_aap.py
	$(call kind-container-build,--build-arg GODEBUG=$(GODEBUG) -t localhost/fulfillment-service:it -f ../fulfillment-service/Containerfile ..)
	$(call kind-load-image,localhost/fulfillment-service:it)
	$(call kind-container-build,--build-arg GODEBUG=$(GODEBUG) -t $(METERING_IT_IMAGE) -f ../osac-metering/metering-service/Containerfile ..)
	$(call kind-load-image,$(METERING_IT_IMAGE))
	$(call kind-container-build,--build-arg GODEBUG=$(GODEBUG) -t localhost/osac-operator:it -f ../osac-operator/Containerfile ..)
	$(call kind-load-image,localhost/osac-operator:it)
endif
ifeq ($(SUITE),fulfillment)
	$(MAKE) install-osac PLATFORM=$(PLATFORM) PROFILE=$(PROFILE) NS=$(NS) \
		EXTRA_HELM_ARGS="--set service.images.service.repository=localhost/fulfillment-service --set service.images.service.tag=it --set service.images.service.pullPolicy=Never --set service.controller.sync=false --set operator.enabled=false --set global.networking.provisioningEnabled=false $(MCP_TEST_HELM_ARGS) $(METERING_IT_IMAGE_ARGS)"
# OSAC and BMF CRDs come from the installed charts. Only HostedCluster is external.
	kubectl apply --server-side --field-manager=helm --force-conflicts \
		-f ../fulfillment-service/it/crds/hostedclusters.hypershift.openshift.io.yaml
# No operator runs in this phase: the specs report networking and tenant status
# through the private API as the operator's feedback controllers would, so a
# running operator would race them. The networking-operator phase covers it.
	cd ../fulfillment-service && KIND_CLUSTER="$(KIND_CLUSTER_NAME)" IT_HTTPS_PORT="$(FULFILLMENT_IT_HTTPS_PORT)" IT_MCP_DEPLOYED_URL="$(MCP_TEST_URL)" ginkgo run $(call fulfillment-json-report,service-only) --timeout 1h15m -v --skip 'Networking with operator|ComputeInstance storage handoff' $(FULFILLMENT_GINKGO_ARGS) it
	$(MAKE) install-osac PLATFORM=$(PLATFORM) PROFILE=$(PROFILE) NS=$(NS) \
		EXTRA_HELM_ARGS="--set service.enabled=true --set service.images.service.repository=localhost/fulfillment-service --set service.images.service.tag=it --set service.images.service.pullPolicy=Never --set service.controller.sync=false --set operator.enabled=true --set operator.image.repository=localhost/osac-operator --set operator.image.tag=it --set operator.image.pullPolicy=Never --set operator.controllers.tenant=false --set operator.controllers.storage=false --set operator.controllers.volume=false --set operator.controllers.networking=true --set operator.controllers.computeInstance=false --set operator.controllers.clusterOrder=false --set operator.networkManagers.enabled=false --set global.services.bmaas.enabled=false --set global.services.maas.enabled=false --set global.networking.provisioningEnabled=false $(METERING_IT_IMAGE_ARGS)"
	cd ../fulfillment-service && KIND_CLUSTER="$(KIND_CLUSTER_NAME)" IT_HTTPS_PORT="$(FULFILLMENT_IT_HTTPS_PORT)" ginkgo run $(call fulfillment-json-report,networking-operator) --timeout 30m -v --label-filter 'networking-operator' it
	$(MAKE) install-osac PLATFORM=$(PLATFORM) PROFILE=$(PROFILE) NS=$(NS) \
		EXTRA_HELM_ARGS="--set service.images.service.repository=localhost/fulfillment-service --set service.images.service.tag=it --set service.images.service.pullPolicy=Never --set service.controller.sync=false --set global.services.bmaas.enabled=false --set operator.enabled=false --set global.networking.provisioningEnabled=false $(METERING_IT_IMAGE_ARGS)"
	cd ../fulfillment-service && KIND_CLUSTER="$(KIND_CLUSTER_NAME)" IT_HTTPS_PORT="$(FULFILLMENT_IT_HTTPS_PORT)" IT_TEST_SUITE=bmaas-disabled ginkgo run $(call fulfillment-json-report,bmaas-disabled) --timeout 1h -v --focus 'Service enablement' it
endif
ifneq ($(filter $(SUITE),fulfillment compute-storage),)
	$(MAKE) install-osac PLATFORM=$(PLATFORM) PROFILE=$(PROFILE) NS=$(NS) \
		EXTRA_HELM_ARGS="--set service.enabled=true --set service.images.service.repository=localhost/fulfillment-service --set service.images.service.tag=it --set service.images.service.pullPolicy=Never --set service.controller.sync=false --set operator.enabled=true --set operator.image.repository=localhost/osac-operator --set operator.image.tag=it --set operator.image.pullPolicy=Never --set operator.controllers.tenant=false --set operator.controllers.storage=true --set operator.controllers.volume=false --set operator.controllers.networking=false --set operator.controllers.computeInstance=true --set operator.controllers.clusterOrder=false --set operator.networkManagers.enabled=false --set-string operator.aap.url=http://compute-storage-aap.$(NS).svc.cluster.local:8080/api --set operator.aap.statusPollInterval=1s --set global.services.bmaas.enabled=false --set global.services.maas.enabled=false --set global.networking.provisioningEnabled=false $(METERING_IT_IMAGE_ARGS)"
	cd ../fulfillment-service && KIND_CLUSTER="$(KIND_CLUSTER_NAME)" IT_HTTPS_PORT="$(FULFILLMENT_IT_HTTPS_PORT)" IT_TEST_SUITE=compute-storage ginkgo run $(call fulfillment-json-report,compute-storage) --timeout 30m -v --label-filter 'compute-storage' it
endif
ifeq ($(SUITE),operator)
	$(call kind-container-build,--build-arg GODEBUG=$(GODEBUG) -t localhost/osac-operator:it -f ../osac-operator/Containerfile ..)
	$(call kind-load-image,localhost/osac-operator:it)
	$(call kind-container-build,--build-arg GODEBUG=$(GODEBUG) -t $(METERING_IT_IMAGE) -f ../osac-metering/metering-service/Containerfile ..)
	$(call kind-load-image,$(METERING_IT_IMAGE))
	# The AAP base URL is test configuration, not a credential: the bearer
	# token is never logged. It is set inline, like the compute-storage phase
	# above, so the operator phase needs no extra values file.
	$(MAKE) install-osac PLATFORM=$(PLATFORM) PROFILE=$(PROFILE) NS=$(NS) \
		EXTRA_HELM_ARGS="--set operator.image.repository=localhost/osac-operator --set operator.image.tag=it --set operator.image.pullPolicy=Never --set operator.fulfillment.serverAddress= --set service.enabled=false --set hubAccess.enabled=false --set validation.enabled=false --set global.fulfillmentTrust.enabled=false --set global.networking.provisioningEnabled=false --set-string operator.aap.url=http://osac-aap-recorder.$(NS).svc.cluster.local:8080/api --set operator.aap.statusPollInterval=1s $(METERING_IT_IMAGE_ARGS)"
	cd ../osac-operator && IT_TEST_SUITE=operator ginkgo run --timeout 30m -v test/integration
	# Provisioning-enabled phase: the operator resolves NetworkClass manager
	# registrations (chart-rendered netris and cudn_evpn), which
	# it skips when provider networking is disabled. No AAP or networking CR is needed.
	$(MAKE) install-osac PLATFORM=$(PLATFORM) PROFILE=$(PROFILE) NS=$(NS) \
		EXTRA_HELM_ARGS="--set operator.image.repository=localhost/osac-operator --set operator.image.tag=it --set operator.image.pullPolicy=Never --set operator.fulfillment.serverAddress= --set service.enabled=false --set hubAccess.enabled=false --set validation.enabled=false --set global.fulfillmentTrust.enabled=false --set global.networking.provisioningEnabled=true --set global.networking.fabricManager=netris --set global.networking.k8sManager=cudn_evpn $(METERING_IT_IMAGE_ARGS)"
	cd ../osac-operator && ginkgo run --timeout 15m -v --label-filter 'networkclass' test/integration
endif
ifeq ($(SUITE),bmf)
	$(call kind-container-build,--build-arg GODEBUG=$(GODEBUG) -t localhost/bmf-operator:it -f ../bare-metal-fulfillment-operator/Containerfile ..)
	$(call kind-load-image,localhost/bmf-operator:it)
	$(call kind-container-build,--build-arg GODEBUG=$(GODEBUG) -t $(METERING_IT_IMAGE) -f ../osac-metering/metering-service/Containerfile ..)
	$(call kind-load-image,$(METERING_IT_IMAGE))
	kubectl apply --server-side --force-conflicts -f ../bare-metal-fulfillment-operator/test/crds/
	$(MAKE) install-osac PLATFORM=$(PLATFORM) PROFILE=$(PROFILE) NS=$(NS) \
		EXTRA_HELM_ARGS="--set global.services.bmaas.enabled=true --set bmf.replicaCount=1 --set bmf.image.repository=localhost/bmf-operator --set bmf.image.tag=it --set bmf.image.pullPolicy=Never --set bmf.secretAPI.url= --set operator.enabled=false --set operator.networkManagers.enabled=true --set service.enabled=false --set hubAccess.enabled=false --set validation.enabled=false --set global.networking.provisioningEnabled=false $(METERING_IT_IMAGE_ARGS)"
	cd ../bare-metal-fulfillment-operator && ginkgo run --timeout 30m -v test/integration
endif

##@ Charts

.PHONY: sync-charts
sync-charts: helm-deps ## Rebuild chart dependencies (alias for helm-deps)

.PHONY: helm-osac-deps
helm-osac-deps: ## Build just charts/osac's own dependencies (no devstack)
	rm -rf $(OSAC_CHART)/charts
	helm --repository-config $(LOCAL_HELM_REPOSITORY_CONFIG) dependency build $(OSAC_CHART)

.PHONY: helm-deps
helm-deps: helm-osac-deps ## Build OSAC and devstack Helm chart dependencies
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
			--set service.externalHostname=fulfillment-api.example.com \
			--set service.internalHostname=fulfillment-internal-api.example.com \
			--set global.clusterDomain=example.com \
			--set-string global.osacDeploymentId=ci/osac \
			--set ui.auth.oidcIssuerURL=https://keycloak-keycloak.example.com/realms/osac > "$$caas_render"; \
		test "$$(rg -c '^  OSAC_FULFILLMENT_ENDPOINT:' "$$caas_render")" -eq 1; \
		helm template osac $(OSAC_CHART) \
			--values values/vmaas-ci/instance.yaml \
			--set service.externalHostname=fulfillment-api.example.com \
			--set service.internalHostname=fulfillment-internal-api.example.com \
			--set global.clusterDomain=example.com \
			--set-string global.osacDeploymentId=ci/osac \
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
			--values charts/osac/ci/full-values.yaml > "$$full_render"; \
		test "$$(rg -c '^  OSAC_FULFILLMENT_ENDPOINT:' "$$full_render")" -eq 1; \
		! rg -q '# Source: osac/charts/(csiDriver|csiBackends)/' "$$full_render"; \
		helm template osac $(OSAC_CHART) \
			--values charts/osac/ci/full-values.yaml \
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
		assert_helm_failure 'namespace.*must be one of' \
			helm template csi-driver ../osac-csi-driver/charts/csi-driver \
			--set namespace=not-osac-csi; \
		echo 'CSI render checks passed.'

.PHONY: fulfillment-trust-render-test
fulfillment-trust-render-test: helm-deps ## Check verified fulfillment transport renders
	python3 scripts/test-fulfillment-trust-render.py

.PHONY: service-account-idp-render-test
service-account-idp-render-test: helm-infra-deps ## Check service-account management credentials and realm permissions
	python3 scripts/test-service-account-idp-render.py

.PHONY: metering-auth-render-test
metering-auth-render-test: helm-deps ## Check metering service-account auth renders in supported profiles
	python3 scripts/test-metering-auth-render.py

.PHONY: event-publisher-render-test
event-publisher-render-test: helm-deps ## Check the event publisher and metering topic wiring renders
	python3 scripts/test-event-publisher-render.py

.PHONY: billing-render-test
billing-render-test: helm-osac-deps ## Check billing provider config renders and validates
	uv run --project .. python scripts/test-billing-render.py

.PHONY: mce-render-test
mce-render-test: helm-infra-deps ## Check disabled MCE defaults, explicit enablement, and CaaS inheritance
	uv run --project .. python scripts/test-mce-render.py

.PHONY: evpn-render-test
evpn-render-test: helm-infra-deps ## Check EVPN host networking renders
	uv run --project .. python scripts/test-evpn-render.py

.PHONY: fulfillment-auth-test
fulfillment-auth-test: ## Check Fulfillment service-account token exchange behavior
	./scripts/test-fulfillment-auth.sh

.PHONY: dev-full-render-test
dev-full-render-test: helm-deps helm-infra-deps ## Check dev-full TLS and Helm post-rendering
	./scripts/test-dev-full-render.sh

.PHONY: openbao-security-context-test
openbao-security-context-test: helm-infra-deps ## Check OpenBao securityContext renders numeric UID on Kind, omits on OpenShift
	./scripts/test-openbao-security-context.sh

.PHONY: ensure-inotify
ensure-inotify: override export _INOTIFY_PROVIDER := $(KIND_RUNTIME_PROVIDER)
ensure-inotify: ## Opt in to native Linux host inotify preparation (floor 512)
	@source scripts/lib.sh && ensure_inotify_instances

.PHONY: infra-ready-wait-test
infra-ready-wait-test: ## Check install-infra waits for the issuer, PostgreSQL and Kafka when the profile creates them (stub kubectl)
	bash scripts/test-infra-ready-wait.sh

.PHONY: kind-runtime-test
kind-runtime-test: ## Check Kind runtime, sudo, image loading, and cluster cleanup with command stubs
	python3 scripts/test-kind-runtime.py

.PHONY: bmf-netbox-render-test
bmf-netbox-render-test: helm-deps ## Check NetBox, Metal3 management, and Secret API Helm wiring
	uv run --project .. python scripts/test-bmf-netbox-render.py

.PHONY: netris-ca-lifecycle-test
netris-ca-lifecycle-test: helm-deps ## Check Netris CA validation and protection
	uv run --project .. python scripts/test-netris-ca-lifecycle.py

.PHONY: chart-name-independence-render-test
chart-name-independence-render-test: helm-osac-deps ## Check cross-chart secret/cert names survive repackaging under a different Chart.yaml name
	uv run --project .. python scripts/test-chart-name-independence-render.py

.PHONY: helm-validate
helm-validate: helm-lint service-account-idp-render-test csi-render-test helm-networking-test netris-ca-lifecycle-test network-class-step-test input-validation-test fulfillment-trust-render-test fulfillment-auth-test metering-auth-render-test event-publisher-render-test billing-render-test mce-render-test evpn-render-test dev-full-render-test openbao-security-context-test bmf-netbox-render-test chart-name-independence-render-test ## Validate all charts (lint + template)
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
	./scripts/validate-keycloak-ui-redirect-uris.py
	@echo "Validation passed."

.PHONY: helm-networking-test
helm-networking-test: helm-deps ## Test global.networking values, validation, and networking manifests
	python3 scripts/test-networking-values.py
	python3 scripts/test_it_networking_checks.py

.PHONY: network-class-step-test
network-class-step-test: ## Test the installer NetworkClass step against a fake fulfillment API
	python3 scripts/test-network-class-step.py

.PHONY: input-validation-test
input-validation-test: helm-deps helm-infra-deps ## Test the upfront input-validation gate wiring and schema rejections
	./scripts/test-input-validation.sh
