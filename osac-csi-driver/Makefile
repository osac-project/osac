# OSAC CSI Driver Makefile
VERSION ?= 0.1.0

# Image URL to use all building/pushing image targets
IMG ?= ghcr.io/osac-project/osac-csi-driver:latest
# Name of Containerfile
CONTAINERFILE ?= Containerfile

# Go version for local make targets. Keep in sync with go.mod.
GO_VERSION ?= 1.26.3
GOTOOLCHAIN ?= go$(GO_VERSION)

# Get the currently used golang install path (in GOPATH/bin, unless GOBIN is set)
ifeq (,$(shell go env GOBIN))
GOBIN=$(shell go env GOPATH)/bin
else
GOBIN=$(shell go env GOBIN)
endif

# CONTAINER_TOOL defines the container tool to be used for building images.
CONTAINER_TOOL ?= podman

# Ldflags for version injection
GIT_COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
LDFLAGS = -X main.version=$(VERSION) -X main.gitCommit=$(GIT_COMMIT)

# Setting SHELL to bash allows bash commands to be executed by recipes.
SHELL = /usr/bin/env bash -o pipefail
.SHELLFLAGS = -ec

.PHONY: all
all: build

##@ General

.PHONY: help
help: ## Display this help.
	@awk 'BEGIN {FS = ":.*##"; printf "\nUsage:\n  make \033[36m<target>\033[0m\n"} /^[a-zA-Z_0-9-]+:.*?##/ { printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2 } /^##@/ { printf "\n\033[1m%s\033[0m\n", substr($$0, 5) } ' $(MAKEFILE_LIST)

##@ Development

.PHONY: fmt
fmt: ## Run go fmt against code.
	go fmt ./...

.PHONY: vet
vet: ## Run go vet against code.
	go vet ./...

.PHONY: test
test: fmt vet ## Run tests.
	GOTOOLCHAIN=$(GOTOOLCHAIN) go test ./... -coverprofile cover.out

.PHONY: lint
lint: golangci-lint ## Run golangci-lint linter.
	$(GOLANGCI_LINT) run

.PHONY: lint-fix
lint-fix: golangci-lint ## Run golangci-lint linter and perform fixes.
	$(GOLANGCI_LINT) run --fix

##@ Build

.PHONY: build
build: fmt vet ## Build the CSI driver binary.
	GOTOOLCHAIN=$(GOTOOLCHAIN) CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o bin/osac-csi-driver ./cmd/osac-csi-driver

.PHONY: clean
clean: ## Remove build artifacts.
	rm -rf bin/ cover.out

##@ Container

.PHONY: image-build
image-build: ## Build the container image.
	$(CONTAINER_TOOL) build -f $(CONTAINERFILE) -t $(IMG) --build-arg VERSION=$(VERSION) --build-arg GIT_COMMIT=$(GIT_COMMIT) ..

.PHONY: image-push
image-push: ## Push the container image.
	$(CONTAINER_TOOL) push $(IMG)

##@ Dependencies

include $(shell git rev-parse --show-toplevel)/tools/golangci-lint.mk
