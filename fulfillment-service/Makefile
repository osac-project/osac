# Single definition of "inside an OpenShell sandbox" (tools/openshell.mk).
OSAC_REPO_ROOT := $(abspath $(dir $(lastword $(MAKEFILE_LIST)))/..)
include $(OSAC_REPO_ROOT)/tools/openshell.mk

# Image URL to use for building/pushing image targets
IMG ?= ghcr.io/osac-project/fulfillment-service:latest
# Name of Containerfile
CONTAINERFILE ?= Containerfile
# CONTAINER_TOOL defines the container tool to be used for building images.
CONTAINER_TOOL ?= podman
# Embedded into the binary via ldflags (see Containerfile's VERSION build
# arg); falls back to `git describe` inside the build if left empty.
VERSION ?=

# The Containerfile copies go.work plus every workspace module's go.mod/go.sum
# (see the Containerfile's own header comment), so the build context must be
# the mono-repo root, not fulfillment-service/ alone -- matches
# publish-image.yaml's `context: .`, `file: fulfillment-service/Containerfile`.
.PHONY: image-build
image-build: ## Build container image.

ifeq ($(OPENSHELL_SANDBOX_ACTIVE),true)
	@if [ "$(origin IMG)" != file ] || [ "$(origin CONTAINERFILE)" != file ] || \
		[ "$(origin CONTAINER_TOOL)" != file ] || [ "$(origin VERSION)" != file ]; then \
		printf 'Carbonite image-build does not support IMG, CONTAINERFILE, CONTAINER_TOOL, or VERSION overrides\n' >&2; \
		exit 1; \
	fi
	@if [ ! -x ../tools/carbonite/host-build ]; then \
		printf 'Carbonite host-build helper is missing; enable host builds on the host and re-enter the sandbox\n' >&2; \
		exit 1; \
	fi
	@../tools/carbonite/host-build fulfillment
else
	$(CONTAINER_TOOL) build -t ${IMG} -f ${CONTAINERFILE} --build-arg VERSION=$(VERSION) ..
endif

.PHONY: image-push
image-push: ## Push container image.
	$(CONTAINER_TOOL) push ${IMG}

.PHONY: kind-load-image
kind-load-image: ## Load the built image into the dev-full Kind cluster.
	$(call kind-load-image,$(IMG))

# Derive the repo root from this Makefile's location rather than git: the
# Carbonite sandbox mounts the worktree without its host-only gitdir, so
# `git rev-parse --show-toplevel` fails there and the include path collapses.

include $(OSAC_REPO_ROOT)/osac-installer/scripts/dev-full/kind-load-image.mk
