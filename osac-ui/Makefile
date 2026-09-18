# Image URL to use for building/pushing image targets
IMG ?= ghcr.io/osac-project/osac-ui:latest

CONTAINER_TOOL ?= podman
CONTAINERFILE ?= Containerfile

##@ Build

.PHONY: image-build
image-build: ## Build the container image.
	$(CONTAINER_TOOL) build -f $(CONTAINERFILE) -t $(IMG) .

.PHONY: image-push
image-push: ## Push the container image.
	$(CONTAINER_TOOL) push $(IMG)
