# Image URL to use for building/pushing image targets
IMG ?= ghcr.io/osac-project/osac-ui:latest

CONTAINER_TOOL ?= podman
CONTAINERFILE ?= Containerfile

##@ Build

.PHONY: image-build
image-build: ## Build the container image.
	$(CONTAINER_TOOL) build -f $(CONTAINERFILE) -t $(IMG) ..

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
