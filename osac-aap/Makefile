# Integration tests for osac.workflows collection
# Note: Must be run from repository root directory

.PHONY: test lint execution-environment-build execution-environment-push

test:
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

execution-environment-build:
	uv sync --locked --all-extras --group development
	uv tool run ansible-builder build -vvv \
		--container-runtime $(CONTAINER_TOOL) \
		--tag "$(EE_LOCAL_TAG)" \
		-f "./execution-environment/execution-environment.yaml"
	$(CONTAINER_TOOL) tag "$(EE_LOCAL_TAG)" "$(IMG)"

execution-environment-push:
	$(CONTAINER_TOOL) push "$(IMG)"
