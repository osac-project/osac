OSAC_DEV_NAMESPACE ?= osac-operator-dev

# Image URL to use all building/pushing image targets
IMG ?= ghcr.io/osac-project/osac-operator:latest
# Name of Containerfile
CONTAINERFILE ?= Containerfile
# ENVTEST_K8S_VERSION refers to the version of kubebuilder assets to be downloaded by envtest binary.
ENVTEST_K8S_VERSION = 1.31.0

# Go version for local make targets. Keep in sync with go.mod.
GO_VERSION ?= 1.26.3
GOTOOLCHAIN ?= go$(GO_VERSION)
GOTOOLCHAIN_AUTO ?= $(GOTOOLCHAIN)+auto

# Get the currently used golang install path (in GOPATH/bin, unless GOBIN is set)
ifeq (,$(shell go env GOBIN))
GOBIN=$(shell go env GOPATH)/bin
else
GOBIN=$(shell go env GOBIN)
endif

# CONTAINER_TOOL defines the container tool to be used for building images.
# Be aware that the target commands are only tested with Docker which is
# scaffolded by default. However, you might want to replace it to use other
# tools. (i.e. podman)
CONTAINER_TOOL ?= podman

# KIND defines the path to your kind binary.
KIND = kind
KIND_CLUSTER_NAME ?= osac-dev

# Setting SHELL to bash allows bash commands to be executed by recipes.
# Options are set to exit when a recipe line exits non-zero or a piped command fails.
SHELL = /usr/bin/env bash -o pipefail
.SHELLFLAGS = -ec

.PHONY: all
all: build

##@ General

# The help target prints out all targets with their descriptions organized
# beneath their categories. The categories are represented by '##@' and the
# target descriptions by '##'. The awk command is responsible for reading the
# entire set of makefiles included in this invocation, looking for lines of the
# file as xyz: ## something, and then pretty-format the target and help. Then,
# if there's a line with ##@ something, that gets pretty-printed as a category.
# More info on the usage of ANSI control characters for terminal formatting:
# https://en.wikipedia.org/wiki/ANSI_escape_code#SGR_parameters
# More info on the awk command:
# http://linuxcommand.org/lc3_adv_awk.php

.PHONY: help
help: ## Display this help.
	@awk 'BEGIN {FS = ":.*##"; printf "\nUsage:\n  make \033[36m<target>\033[0m\n"} /^[a-zA-Z_0-9-]+:.*?##/ { printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2 } /^##@/ { printf "\n\033[1m%s\033[0m\n", substr($$0, 5) } ' $(MAKEFILE_LIST)

##@ Development

.PHONY: manifests
manifests: controller-gen ## Generate WebhookConfiguration, ClusterRole and CustomResourceDefinition objects.
	$(CONTROLLER_GEN) rbac:roleName=manager-role crd webhook paths="./..." paths="./api/..." output:crd:artifacts:config=config/crd/bases

.PHONY: generate
generate: controller-gen ## Generate code containing DeepCopy, DeepCopyInto, and DeepCopyObject method implementations.
	$(CONTROLLER_GEN) object:headerFile="hack/boilerplate.go.txt" paths="./..." paths="./api/..."

.PHONY: fmt
fmt: ## Run go fmt against code.
	go fmt ./...

.PHONY: vet
vet: ## Run go vet against code.
	go vet ./...

.PHONY: test
test: manifests generate fmt vet envtest ## Run tests.
	GOTOOLCHAIN=$(GOTOOLCHAIN_AUTO) KUBEBUILDER_ASSETS="$(shell $(ENVTEST) use $(ENVTEST_K8S_VERSION) --bin-dir $(LOCALBIN) -p path)" go test $$(go list ./... | grep -v '/test/integration$$') -coverprofile cover.out

.PHONY: helm-lint
helm-lint: ## Lint Helm charts.
	helm lint charts/operator/
	helm lint charts/operator-crds/

.PHONY: helm-validate
helm-validate: helm-lint ## Lint and template-render Helm charts.
	helm template osac-operator-crds charts/operator-crds/
	helm template osac-operator charts/operator/

# Run integration tests against a pre-existing Kind cluster deployed via
# the root Makefile's `make dev-env` + `make integration-test-operator`.
.PHONY: integration-tests
integration-tests: manifests generate helm-crds ## Run integration tests against a Kind cluster.
	ginkgo run --timeout 30m -v test/integration

.PHONY: lint
lint: golangci-lint ## Run golangci-lint linter
	$(GOLANGCI_LINT) run

.PHONY: lint-fix
lint-fix: golangci-lint ## Run golangci-lint linter and perform fixes
	$(GOLANGCI_LINT) run --fix

.PHONY: helm-crds
helm-crds: manifests ## Copy generated CRDs into the operator-crds Helm chart.
	@echo "Syncing CRDs to charts/operator-crds/templates/..."
	@python3 hack/sync-helm-crds.py

.PHONY: check-helm-crds
check-helm-crds: helm-crds ## Verify Helm CRD templates match config/crd/bases.
	@if ! git diff --quiet -- charts/operator-crds/templates/; then \
		echo "Helm CRD templates are out of sync with config/crd/bases. Run 'make helm-crds' and commit."; \
		git diff -- charts/operator-crds/templates/; \
		exit 1; \
	fi
	@if [ -n "$$(git ls-files --others --exclude-standard -- charts/operator-crds/templates/)" ]; then \
		echo "Missing Helm CRD templates (run 'make helm-crds' and commit):"; \
		git ls-files --others --exclude-standard -- charts/operator-crds/templates/; \
		exit 1; \
	fi

##@ API Module

API_VERSION ?= $(error API_VERSION is required (e.g. make tag-api API_VERSION=v0.0.2))

.PHONY: tag-api
tag-api: ## Tag a new release of the api/ Go module (e.g. make tag-api API_VERSION=v0.0.2).
	@echo "$(API_VERSION)" | grep -qE '^v[0-9]+\.[0-9]+\.[0-9]+$$' || \
		{ echo "Error: API_VERSION must match vX.Y.Z (got '$(API_VERSION)')"; exit 1; }
	@REMOTE=$$(git remote -v | grep 'osac-project/osac' | grep '(push)' | awk '{print $$1}' | head -1); \
	if [ -z "$$REMOTE" ]; then \
		echo "Error: no remote found pointing to osac-project/osac"; \
		exit 1; \
	fi; \
	if [ -z "$$(git status --porcelain)" ]; then \
		echo "Tagging osac-operator/api/$(API_VERSION) and pushing to $$REMOTE..." ; \
		git tag "osac-operator/api/$(API_VERSION)" ; \
		git push "$$REMOTE" "osac-operator/api/$(API_VERSION)" ; \
		echo "Tagged and pushed osac-operator/api/$(API_VERSION) to $$REMOTE" ; \
	else \
		echo "Error: working tree is dirty — commit or stash changes first" ; \
		exit 1 ; \
	fi

##@ Build

.PHONY: build
build: test ## Build manager and console-proxy binaries (runs all tests first).
	go build -o bin/manager cmd/main.go
	go build -o bin/console-proxy cmd/console-proxy/main.go

.PHONY: run
run: manifests generate fmt vet ## Run a controller from your host.
	go run ./cmd/main.go

# If you wish to build the manager image targeting other platforms you can use the --platform flag.
# (i.e. docker build --platform linux/arm64). However, you must enable docker buildKit for it.
# More info: https://docs.docker.com/develop/develop-images/build_enhancements/
.PHONY: image-build
image-build: ## Build container image with manager and console-proxy.
	$(CONTAINER_TOOL) build -t ${IMG} -f ${CONTAINERFILE} ..

.PHONY: image-push
image-push: ## Push container image.
	$(CONTAINER_TOOL) push ${IMG}

.PHONY: image-run
image-run: ## Run container image locally.
	$(CONTAINER_TOOL) run --rm --userns keep-id --name osac-controller \
		-v ${HOME}/.kube:/.kube -e HOME=/ \
		-e OSAC_CLUSTER_ORDER_NAMESPACE=$(OSAC_DEV_NAMESPACE) \
		-e OSAC_COMPUTE_INSTANCE_NAMESPACE=$(OSAC_DEV_NAMESPACE) \
		-e OSAC_HOSTPOOL_ORDER_NAMESPACE=$(OSAC_DEV_NAMESPACE) \
		${IMG}

.PHONY: kind-load
kind-load: image-build
	$(KIND) load docker-image -n ${KIND_CLUSTER_NAME} ${IMG}

# PLATFORMS defines the target platforms for the manager image be built to provide support to multiple
# architectures. (i.e. make docker-buildx IMG=myregistry/mypoperator:0.0.1). To use this option you need to:
# - be able to use docker buildx. More info: https://docs.docker.com/build/buildx/
# - have enabled BuildKit. More info: https://docs.docker.com/develop/develop-images/build_enhancements/
# - be able to push the image to your registry (i.e. if you do not set a valid value via IMG=<myregistry/image:<tag>> then the export will fail)
# To adequately provide solutions that are compatible with multiple platforms, you should consider using this option.
PLATFORMS ?= linux/arm64,linux/amd64,linux/s390x,linux/ppc64le
.PHONY: docker-buildx
docker-buildx: ## Build and push container image for cross-platform support
	sed -e '1 s/\(^FROM\)/FROM --platform=\$$\{BUILDPLATFORM\}/; t' -e ' 1,// s//FROM --platform=\$$\{BUILDPLATFORM\}/' $(CONTAINERFILE) > $(CONTAINERFILE).cross
	- $(CONTAINER_TOOL) buildx create --name osac-operator-builder
	$(CONTAINER_TOOL) buildx use osac-operator-builder
	- $(CONTAINER_TOOL) buildx build --push --platform=$(PLATFORMS) --tag ${IMG} -f $(CONTAINERFILE).cross .
	- $(CONTAINER_TOOL) buildx rm osac-operator-builder
	rm $(CONTAINERFILE).cross

.PHONY: build-installer
build-installer: manifests generate ## Generate a consolidated YAML with CRDs and deployment.
	mkdir -p dist
	helm template osac-operator-crds charts/operator-crds/ > dist/install.yaml
	echo "---" >> dist/install.yaml
	helm template osac-operator charts/operator/ --set image.repository=$(firstword $(subst :, ,${IMG})) --set image.tag=$(lastword $(subst :, ,${IMG})) >> dist/install.yaml

##@ Deployment

ifndef ignore-not-found
  ignore-not-found = false
endif

.PHONY: install
install: manifests helm-crds ## Install CRDs into the K8s cluster specified in ~/.kube/config.
	helm upgrade --install osac-operator-crds charts/operator-crds/

.PHONY: uninstall
uninstall: ## Uninstall CRDs from the K8s cluster specified in ~/.kube/config.
	helm uninstall osac-operator-crds --ignore-not-found

.PHONY: deploy
deploy: manifests helm-crds ## Deploy controller to the K8s cluster specified in ~/.kube/config.
	helm upgrade --install osac-operator charts/operator/ --set image.repository=$(firstword $(subst :, ,${IMG})) --set image.tag=$(lastword $(subst :, ,${IMG}))

.PHONY: undeploy
undeploy: ## Undeploy controller from the K8s cluster specified in ~/.kube/config.
	helm uninstall osac-operator --ignore-not-found

##@ Dependencies

## Location to install dependencies to
LOCALBIN ?= $(shell pwd)/bin
$(LOCALBIN):
	mkdir -p $(LOCALBIN)

## Tool Binaries
KUBECTL ?= kubectl
CONTROLLER_GEN ?= $(LOCALBIN)/controller-gen
ENVTEST ?= $(LOCALBIN)/setup-envtest
GOLANGCI_LINT = $(LOCALBIN)/golangci-lint

## Tool Versions
CONTROLLER_TOOLS_VERSION ?= v0.20.0
ENVTEST_VERSION ?= release-0.19
GOLANGCI_LINT_VERSION ?= v2.12.1

.PHONY: controller-gen
controller-gen: $(CONTROLLER_GEN) ## Download controller-gen locally if necessary.
$(CONTROLLER_GEN): $(LOCALBIN)
	$(call go-install-tool,$(CONTROLLER_GEN),sigs.k8s.io/controller-tools/cmd/controller-gen,$(CONTROLLER_TOOLS_VERSION))

.PHONY: envtest
envtest: $(ENVTEST) ## Download setup-envtest locally if necessary.
$(ENVTEST): $(LOCALBIN)
	$(call go-install-tool,$(ENVTEST),sigs.k8s.io/controller-runtime/tools/setup-envtest,$(ENVTEST_VERSION))

.PHONY: golangci-lint
golangci-lint: $(GOLANGCI_LINT) ## Download golangci-lint locally if necessary.
$(GOLANGCI_LINT): $(LOCALBIN)
	$(call go-install-tool,$(GOLANGCI_LINT),github.com/golangci/golangci-lint/v2/cmd/golangci-lint,$(GOLANGCI_LINT_VERSION))

# go-install-tool will 'go install' any package with custom target and name of binary, if it doesn't exist
# $1 - target path with name of binary
# $2 - package url which can be installed
# $3 - specific version of package
define go-install-tool
@[ -f "$(1)-$(3)" ] || { \
set -e; \
package=$(2)@$(3) ;\
echo "Downloading $${package}" ;\
rm -f $(1) ;\
GOTOOLCHAIN=$(GOTOOLCHAIN) GOBIN=$(LOCALBIN) go install $${package} ;\
mv $(1) $(1)-$(3) ;\
} ;\
ln -sf $(1)-$(3) $(1)
endef
