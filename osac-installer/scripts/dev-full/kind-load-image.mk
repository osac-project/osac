# Shared Make helper for loading a component image into the dev-full Kind cluster.
# The implementation remains in kind-load-image.sh so it is independent of each
# component's shell and Make settings.

KIND_INSTALLER_DIR ?= $(abspath $(dir $(lastword $(MAKEFILE_LIST)))/../..)
KIND_LOAD_IMAGE_SCRIPT ?= $(KIND_INSTALLER_DIR)/scripts/dev-full/kind-load-image.sh
KIND_CLUSTER_NAME ?= osac-dev

PROFILE=dev-full
NS=osac
define kind-load-image
	PLATFORM="kind" PROFILE="$(PROFILE)" NS="$(NS)" \
	KIND_CLUSTER_NAME="$(KIND_CLUSTER_NAME)" KUBECONFIG="$(KUBECONFIG)" \
	CONTAINER_TOOL="$(CONTAINER_TOOL)" "$(KIND_LOAD_IMAGE_SCRIPT)" "$(1)"
endef
