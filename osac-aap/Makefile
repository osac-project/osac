# Integration tests for osac.workflows collection
# Note: Must be run from repository root directory

.PHONY: test lint execution-environment-build execution-environment-push kind-load-image

test:
	uv run --group development pytest tests/unit
	@echo "=== Setting up test environment ==="
	cd tests/integration && ./setup_test_env.sh
	@echo ""
	@echo "=== Running integration tests ==="
	cd tests/integration && ./run_tests.sh
	@echo ""
	@echo "=== Tearing down test environment ==="
	cd tests/integration && ./teardown_test_env.sh

lint:
	uv run ansible-lint

# Not a generic docker/Containerfile build like the other components --
# ansible-builder assembles the execution environment image from
# execution-environment.yaml, producing a local EE_LOCAL_TAG, which is
# then aliased to the real registry-qualified IMG (mirrors
# execution-environment.yml's own build+tag+push steps).
EE_LOCAL_TAG ?= osac-ee
IMG ?= ghcr.io/osac-project/osac-aap:latest
CONTAINER_TOOL ?= podman
EE_CONTAINER_PLATFORM ?=

EE_PLATFORM_ARGS = $(if $(EE_CONTAINER_PLATFORM),--extra-build-cli-args="--platform=$(EE_CONTAINER_PLATFORM)",)

execution-environment-build:
	uv sync --locked --all-extras --group development
	@case "$(EE_CONTAINER_PLATFORM)" in \
		*,*) \
			uv tool run ansible-builder create -vvv \
				-f "./execution-environment/execution-environment.yaml"; \
			$(CONTAINER_TOOL) build \
				--platform "$(EE_CONTAINER_PLATFORM)" \
				--manifest "$(EE_LOCAL_TAG)" \
				-f context/Containerfile context; \
			;; \
		*) \
			uv tool run ansible-builder build -vvv \
				--container-runtime $(CONTAINER_TOOL) \
				--tag "$(EE_LOCAL_TAG)" \
				$(EE_PLATFORM_ARGS) \
				-f "./execution-environment/execution-environment.yaml"; \
			;; \
	esac
	$(CONTAINER_TOOL) tag "$(EE_LOCAL_TAG)" "$(IMG)"

execution-environment-push:
	$(CONTAINER_TOOL) push "$(IMG)"

kind-load-image: ## Load the built execution-environment image into dev-full Kind.
	$(call kind-load-image,$(IMG))

include $(shell git rev-parse --show-toplevel)/osac-installer/scripts/dev-full/kind-load-image.mk
