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
	$(CONTAINER_TOOL) build -t ${IMG} -f ${CONTAINERFILE} --build-arg VERSION=$(VERSION) ..

.PHONY: image-push
image-push: ## Push container image.
	$(CONTAINER_TOOL) push ${IMG}
