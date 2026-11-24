# Fixed-purpose CaaS integration environment: Kind, dev infrastructure, namespace osac.
SHELL := /usr/bin/env bash -o pipefail
.SHELLFLAGS := -ec
.DEFAULT_GOAL := check

KIND_CLUSTER_NAME ?= osac-dev-caas
ifeq ($(strip $(value KUBECONFIG)),)
override KUBECONFIG := $(HOME)/.kube/$(KIND_CLUSTER_NAME)-kind.kubeconfig
else
override KUBECONFIG := $(value KUBECONFIG)
endif
export KUBECONFIG

# Keep caller-supplied project inputs literal when Make exports them to shells.
ifneq ($(origin PROJECT_URL),undefined)
override PROJECT_URL := $(value PROJECT_URL)
endif
ifneq ($(origin PROJECT_COMMIT),undefined)
override PROJECT_COMMIT := $(value PROJECT_COMMIT)
endif

# Configuration
ROOT_DIR := $(shell git rev-parse --show-toplevel)
INSTALLER_DIR := $(ROOT_DIR)/osac-installer
CONTAINER_TOOL ?= podman
override KIND_EXPERIMENTAL_PROVIDER := $(notdir $(CONTAINER_TOOL))
LOG_LEVEL ?= info
PROCS ?= 4
GINKGO_ARGS ?= --label-filter=caas-it
# Optional JUnit output: one file per ginkgo suite (<dir>/<prepare|cluster>.xml),
# written only when the directory is set. CI passes it in and merges the files
# into the run's single junit.xml log bundle.
JUNIT_REPORT_DIR ?=
junit-report = $(if $(strip $(JUNIT_REPORT_DIR)),--junit-report="$(abspath $(JUNIT_REPORT_DIR)/$(1).xml)")
ifeq ($(origin PROJECT_URL),undefined)
PROJECT_URL := $(or $(shell git -C "$(ROOT_DIR)" remote get-url origin 2>/dev/null),https://github.com/osac-project/osac.git)
endif
ifeq ($(origin PROJECT_COMMIT),undefined)
PROJECT_COMMIT := $(shell git -C "$(ROOT_DIR)" rev-parse HEAD)
endif
export PROJECT_URL PROJECT_COMMIT
FULFILLMENT_PROJECT_DIR := $(ROOT_DIR)/fulfillment-service
export CONTAINER_TOOL KIND_CLUSTER_NAME KIND_EXPERIMENTAL_PROVIDER FULFILLMENT_PROJECT_DIR

# Shared tooling and fixed deployment inputs
KIND_RUNTIME_REQUIRED := false
include $(ROOT_DIR)/tools/golangci-lint.mk
include $(INSTALLER_DIR)/scripts/kind-images.mk

SIMULATOR_IMAGE := localhost/osac-caas-simulator:it
EE_IMAGE := localhost/osac-aap:it
INSTALLER_ARGS = PLATFORM=kind PROFILE=dev NS=osac KIND_CLUSTER_NAME="$(KIND_CLUSTER_NAME)" KUBECONFIG="$$KUBECONFIG"
BOOTSTRAP_IMAGE_ARGS := --set-string images.ee=$(EE_IMAGE) --set images.eePullPolicy=Never
BOOTSTRAP_ARGS = $(BOOTSTRAP_IMAGE_ARGS) \
  --set-string "project.url=$$PROJECT_URL" --set-string "project.commit=$$PROJECT_COMMIT"
OSAC_VALUES := -f $(INSTALLER_DIR)/values/dev/kind-instance.yaml -f values/instance.yaml \
  --set-string global.osacDeploymentId=osac/osac
OSAC_APPLY = helm upgrade --install osac $(INSTALLER_DIR)/charts/osac $(OSAC_VALUES) \
  --namespace osac --create-namespace --wait --timeout 40m

ifneq ($(filter prepare test _deploy,$(MAKECMDGOALS)),)
ifneq ($(strip $(OSAC_CHART_OCI_REF) $(OSAC_CHART_OCI_VERSION)),)
$(error CaaS integration deploys the local OSAC chart; unset OSAC_CHART_OCI_REF and OSAC_CHART_OCI_VERSION)
endif
endif

.PHONY: help
help:
	@printf '%s\n' \
	  'Workflow:' \
	  '  prepare       Create infrastructure, build/load images, deploy and prepare fixtures' \
	  '  test          Prepare the installation, then run cluster scenarios' \
	  '  fixtures      Prepare persistent users, tenants and hub on an existing deployment' \
	  '  run           Run selected cluster scenarios against the prepared installation' \
	  '' \
	  'Host CLI (replaces the current login and prints whoami):' \
	  '  Requires /etc/hosts entries from fulfillment-service/README.md (Running integration tests).' \
	  '  login-admin   Log in as the test administrator' \
	  '  login-user    Log in as the regular test user' \
	  '' \
	  'Checks and maintenance:' \
	  '  check         Check modules, Go code, pinned CRDs and charts (default)' \
	  '  unit          Run internal unit suites without a cluster' \
	  '  lint          Check Go code' \
	  '  lint-fix      Apply formatting and automatic lint fixes' \
	  '  prepare-crds  Download and verify the pinned CRDs' \
	  '  destroy       Delete the selected Kind cluster and all its contents' \
	  '  help          Show these commands' \
	  '' \
	  'Internal steps (advanced use; stop tests before deployment/image operations):' \
	  '  _infrastructure         Create Kind and install infrastructure/AWX' \
	  '  _images                 Build and load all required test images' \
	  '  _deploy                 Deploy services and providers using loaded images' \
	  '  _simulator-image-build  Build only the simulator image' \
	  '  _simulator-image-load   Load the simulator image and restart its deployment if present' \
	  '  _mod-check              Check module tidiness and integrity' \
	  '  _chart-check            Verify pinned CRDs and lint/render charts' \
	  '  _helm-check             Require Helm 3'

# Lifecycle: persistent preparation completes before cluster scenarios start.
.PHONY: prepare test fixtures run
prepare: _prepare-check _kubeconfig-check
	@printf 'AWX commit: %s\n' "$$PROJECT_COMMIT"
	@tmp=$$(mktemp -d); trap 'rm -rf "$$tmp"' EXIT; \
	  if ! GIT_TERMINAL_PROMPT=0 git clone --quiet --bare --filter=tree:0 -- "$$PROJECT_URL" "$$tmp/repo"; then \
	    echo 'ERROR: Cannot read PROJECT_URL. Check the URL, network access and repository permissions.' >&2; \
	    exit 1; \
	  fi; \
	  if [[ $$(GIT_NO_LAZY_FETCH=1 git -C "$$tmp/repo" cat-file -t "$$PROJECT_COMMIT" 2>/dev/null) != commit ]]; then \
	    printf 'ERROR: Commit %s is unavailable from PROJECT_URL.\n' "$$PROJECT_COMMIT" >&2; \
	    echo 'Check PROJECT_COMMIT, or push your feature branch to your contributor fork and set PROJECT_URL to that fork.' >&2; \
	    exit 1; \
	  fi; \
	  echo 'AWX Git revision verified.'
	$(MAKE) -j2 _prepare-platform _ee-image-build
	$(call kind-load-image,$(EE_IMAGE))
	$(MAKE) _deploy
	$(MAKE) fixtures

test:
	$(MAKE) prepare
	$(MAKE) run

fixtures: _kubeconfig-check
ifneq ($(strip $(JUNIT_REPORT_DIR)),)
	rm -f "$(abspath $(JUNIT_REPORT_DIR)/prepare.xml)"
endif
	NS=osac KIND_CLUSTER="$(KIND_CLUSTER_NAME)" IT_HTTPS_PORT=8443 \
	  ginkgo run --procs=1 --fail-on-empty --timeout=15m -v $(call junit-report,prepare) ./suites/prepare -- --log-level=$(LOG_LEVEL)

run: _kubeconfig-check
ifneq ($(strip $(JUNIT_REPORT_DIR)),)
	rm -f "$(abspath $(JUNIT_REPORT_DIR)/cluster.xml)"
endif
	NS=osac KIND_CLUSTER="$(KIND_CLUSTER_NAME)" IT_HTTPS_PORT=8443 \
	  ginkgo run --procs=$(PROCS) --fail-on-empty --timeout=45m -v $(call junit-report,cluster) $(GINKGO_ARGS) ./suites/cluster -- --log-level=$(LOG_LEVEL)

# Log the host CLI into the prepared test installation as either seeded user.
.PHONY: login-admin login-user
login-admin login-user: _kubeconfig-check
	@umask 077; \
	  config_dir="$${OSAC_CONFIG:-$(HOME)/.config/osac}"; \
	  mkdir -p "$$config_dir"; \
	  kubectl --namespace osac get configmap ca-bundle \
	    -o jsonpath='{.data.bundle\.pem}' > "$$config_dir/caas-it-ca.pem"; \
	  password_file="$$(mktemp)"; trap 'rm -f "$$password_file"' EXIT; \
	  printf '%s' password > "$$password_file"; \
	  osac login --ca-file "$$config_dir/caas-it-ca.pem" \
	    --flow password --user "$(@:login-%=%)" --password-file "$$password_file" \
	    --scopes openid,organization \
	    https://fulfillment-api.osac.svc.cluster.local:8443
	@osac whoami

# Public development checks and maintenance.
.PHONY: check unit lint lint-fix prepare-crds destroy
check: _mod-check lint _chart-check

unit:
	ginkgo run -r internal suites/fixtures

lint: golangci-lint
	$(GOLANGCI_LINT) run ./...

lint-fix: golangci-lint
	$(GOLANGCI_LINT) fmt ./...
	$(GOLANGCI_LINT) run ./... --fix

prepare-crds:
	bash charts/prepare-crds.sh

destroy:
	kind delete cluster --name "$(KIND_CLUSTER_NAME)"

# Internal preparation and deployment steps.
.PHONY: _infrastructure _images _deploy
.PHONY: _prepare-platform _go-images _ee-image-build

# Create the cluster before building and loading Go images.
_prepare-platform:
	$(MAKE) _infrastructure
	$(MAKE) _go-images

_infrastructure: _helm-check
	@$(select-kind-kubeconfig); \
	$(MAKE) -C $(INSTALLER_DIR) install-infra $(INSTALLER_ARGS) KIND_KUBECONFIG="$$kind_kubeconfig" \
	  KIND_CONFIG="$(INSTALLER_DIR)/kind-config-dev-caas.yaml" KIND_FAKE_CRDS=false
	$(MAKE) -C $(INSTALLER_DIR) helm-devstack-deps
	helm upgrade --install osac-devstack $(INSTALLER_DIR)/charts/osac-devstack \
	  --namespace osac --create-namespace -f values/awx.yaml --set osacNamespace=osac \
	  --post-renderer $(INSTALLER_DIR)/scripts/dev-full/helm-post-renderer/helm-post-render.sh --wait --timeout 15m

# Standalone image build and loading.
_images:
	$(MAKE) _go-images
	$(MAKE) _ee-image-build
	$(call kind-load-image,$(EE_IMAGE))

_go-images:
	$(MAKE) -C $(INSTALLER_DIR) test-platform-images $(INSTALLER_ARGS)
	$(MAKE) -C $(ROOT_DIR)/bare-metal-fulfillment-operator image-build IMG=localhost/bmf-operator:it
	$(call kind-load-image,localhost/bmf-operator:it)
	$(MAKE) _simulator-image-build
	$(MAKE) _simulator-image-load

_ee-image-build:
	$(call kind-container-build,-t $(EE_IMAGE) -f $(ROOT_DIR)/osac-aap/Containerfile $(ROOT_DIR))

# Native APIs, AWX inputs and provider infrastructure precede application controller startup.
_deploy: _prepare-check _helm-check _kubeconfig-check
	$(MAKE) prepare-crds
	helm upgrade --install caas-it charts/bootstrap --namespace osac --create-namespace $(BOOTSTRAP_ARGS) --set bootstrap.enabled=false --wait --timeout 5m
	$(MAKE) -C $(INSTALLER_DIR) helm-osac-deps OSAC_CHART="$(INSTALLER_DIR)/charts/osac"
	$(OSAC_APPLY) --set operator.enabled=false,bmf.replicaCount=0
	@deployments="$$(kubectl --namespace osac --request-timeout=20s get deployments -o json | \
	  jq -r '.items[] | select(any(.spec.template.spec.containers[]; .image == "localhost/fulfillment-service:it")) | .metadata.name')"; \
	for deployment in $$deployments; do \
	  kubectl --namespace osac rollout restart "deployment/$$deployment"; \
	  kubectl --namespace osac rollout status "deployment/$$deployment" --timeout=180s; \
	done
	kubectl --namespace osac wait awx/awx --for=condition=Successful --timeout=900s
	kubectl --namespace osac rollout status deployment/awx-task --timeout=180s
	helm upgrade caas-it charts/bootstrap --namespace osac $(BOOTSTRAP_ARGS) --set bootstrap.enabled=true --wait --timeout 15m || { \
	  status=$$?; \
	  kubectl --namespace osac --request-timeout=20s logs job/caas-it-configure-awx --all-containers=true || true; \
	  exit "$$status"; \
	}
	helm upgrade --install osac-providers charts/providers --namespace osac \
	  --set-string images.simulator=$(SIMULATOR_IMAGE) --set images.simulatorPullPolicy=Never \
	  --set-string images.ignition=$(EE_IMAGE) --set images.ignitionPullPolicy=Never \
	  --set-string logLevel=$(LOG_LEVEL) --wait --timeout 10m
	$(OSAC_APPLY) --set operator.enabled=true,operator.replicaCount=1,bmf.replicaCount=1

# Simulator-only rebuild/load operations use the common Kind loader.
.PHONY: _simulator-image-build _simulator-image-load
_simulator-image-build:
	$(call kind-container-build,$(if $(filter podman,$(notdir $(CONTAINER_TOOL))),--format=docker) \
	  -f Containerfile -t $(SIMULATOR_IMAGE) $(ROOT_DIR))

_simulator-image-load: _kubeconfig-check
	$(call kind-load-image,$(SIMULATOR_IMAGE))
	@image="$$(kubectl --namespace osac --request-timeout=20s get deployment/osac-provider-simulator --ignore-not-found -o jsonpath='{.spec.template.spec.containers[0].image}')" || exit $$?; \
	if [[ "$$image" == "$(SIMULATOR_IMAGE)" ]]; then \
	  kubectl --namespace osac rollout restart deployment/osac-provider-simulator; \
	  kubectl --namespace osac rollout status deployment/osac-provider-simulator --timeout=180s; \
	fi

# Internal check helpers; unit execution stays separate from static/chart checks.
.PHONY: _mod-check _chart-check _helm-check _prepare-check _kubeconfig-check
_mod-check:
	go mod tidy -diff
	go mod verify

_chart-check: BOOTSTRAP_CHECK_ARGS := $(BOOTSTRAP_IMAGE_ARGS) \
  --set-string project.commit=0000000000000000000000000000000000000000 --set bootstrap.enabled=true
_chart-check: _helm-check prepare-crds
	helm lint charts/bootstrap --strict $(BOOTSTRAP_CHECK_ARGS)
	helm template caas-it charts/bootstrap --namespace osac --include-crds $(BOOTSTRAP_CHECK_ARGS) >/dev/null
	helm lint charts/providers --strict
	helm template osac-providers charts/providers --namespace osac --include-crds >/dev/null
	$(MAKE) -C $(INSTALLER_DIR) helm-osac-deps OSAC_CHART="$(INSTALLER_DIR)/charts/osac"
	helm template osac $(INSTALLER_DIR)/charts/osac $(OSAC_VALUES) --namespace osac \
	  --set operator.enabled=false,bmf.replicaCount=0 >/dev/null
	helm template osac $(INSTALLER_DIR)/charts/osac $(OSAC_VALUES) --namespace osac \
	  --set operator.enabled=true,operator.replicaCount=1,bmf.replicaCount=1 >/dev/null

_helm-check:
	@case "$$(helm version --short 2>/dev/null)" in v3.*) ;; *) echo 'ERROR: CaaS integration requires Helm 3' >&2; exit 1;; esac

# Validate the client search list and select the first filename for Kind writes.
define select-kind-kubeconfig
  remaining="$$KUBECONFIG:"; kind_kubeconfig=; \
  while [[ -n "$$remaining" ]]; do \
    path="$${remaining%%:*}"; remaining="$${remaining#*:}"; \
    case "$$path" in \
      '') ;; \
      /*) kind_kubeconfig="$${kind_kubeconfig:-$$path}" ;; \
      *) kind_kubeconfig=; break ;; \
    esac; \
  done; \
  [[ -n "$$kind_kubeconfig" ]] || { \
    echo 'ERROR: KUBECONFIG must contain an absolute filename and every nonempty entry must be absolute.' >&2; exit 1; \
  }
endef

_kubeconfig-check:
	@$(select-kind-kubeconfig)

_prepare-check:
	@https_repo='^https://([[:alnum:]_.-]+|\[[[:xdigit:].]*:[[:xdigit:]:.]*\])(:[0-9]+)?(/[^[:space:][:cntrl:]?#,\\]*)?$$'; \
	  [[ "$$PROJECT_URL" =~ $$https_repo ]] || { \
	    echo 'ERROR: Set PROJECT_URL to a credential-free HTTPS repository URL accessible from the bootstrap Job.' >&2; exit 1; \
	  }
	@[[ "$$PROJECT_COMMIT" =~ ^[0-9a-f]{40}$$ ]] || { \
	  echo 'ERROR: PROJECT_COMMIT must be a full 40-character lowercase commit ID.' >&2; exit 1; \
	}
	@command -v jq >/dev/null || { echo 'ERROR: CaaS preparation requires jq.' >&2; exit 1; }
