# Image URL to use for building/pushing image targets
IMG ?= ghcr.io/osac-project/osac-ui:latest

CONTAINER_TOOL ?= podman
CONTAINERFILE ?= Containerfile

##@ Build

.PHONY: image-build
image-build: ## Build the container image.
ifneq ($(origin OPENSHELL_SANDBOX),undefined)
	@if [ "$(origin IMG)" != file ] || [ "$(origin CONTAINERFILE)" != file ] || [ "$(origin CONTAINER_TOOL)" != file ]; then \
		printf 'Carbonite image-build does not support IMG, CONTAINERFILE, or CONTAINER_TOOL overrides\n' >&2; \
		exit 1; \
	fi
	@if [ ! -x ../tools/carbonite/host-build ]; then \
		printf 'Carbonite host-build helper is missing; enable host builds on the host and re-enter the sandbox\n' >&2; \
		exit 1; \
	fi
	@../tools/carbonite/host-build ui
else
	$(CONTAINER_TOOL) build -f $(CONTAINERFILE) -t $(IMG) ..
endif

.PHONY: image-push
image-push: ## Push the container image.
	$(CONTAINER_TOOL) push $(IMG)

.PHONY: kind-load-image
kind-load-image: ## Load the built image into the dev-full Kind cluster.
	$(call kind-load-image,$(IMG))

.PHONY: kind-build-load-image
kind-build-load-image: image-build ## Build the image and load it into the dev-full Kind cluster.
	$(call kind-load-image,$(IMG))

include $(shell git rev-parse --show-toplevel)/osac-installer/scripts/dev-full/kind-load-image.mk
