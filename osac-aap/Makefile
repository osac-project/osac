# Integration tests for osac.workflows collection
# Note: Must be run from repository root directory

.PHONY: test lint role-coverage role-coverage-report execution-environment-build execution-environment-push kind-load-image

# Task and Python coverage of the network manager roles (netris, cudn_evpn).
# `make test` measures it on every run and fails on a gap; see
# docs/INTEGRATION-TESTING.md and tests/coverage/role_coverage.py.
ROLE_COVERAGE_DIR ?= $(CURDIR)/tests/coverage/.runs
ROLE_COVERAGE_ENV = OSAC_TASK_COVERAGE_DIR=$(ROLE_COVERAGE_DIR) \
	ANSIBLE_CALLBACK_PLUGINS=$(CURDIR)/tests/coverage/callback_plugins \
	ANSIBLE_CALLBACKS_ENABLED=osac_task_coverage
ROLE_COVERAGE_PYTEST = --cov --cov-config=tests/coverage/coveragerc --cov-report=json:$(ROLE_COVERAGE_DIR)/python.json

test:
	rm -rf "$(ROLE_COVERAGE_DIR)" && mkdir -p "$(ROLE_COVERAGE_DIR)"
	$(ROLE_COVERAGE_ENV) uv run --group development pytest tests/unit $(ROLE_COVERAGE_PYTEST)
	@cd tests/integration && { \
		set -e; \
		trap './teardown_test_env.sh' EXIT; \
		echo "=== Setting up test environment ==="; \
		./setup_test_env.sh; \
		echo ""; \
		echo "=== Running integration tests ==="; \
		$(ROLE_COVERAGE_ENV) ./run_tests.sh; \
	}
	$(MAKE) role-coverage-report

# Only what measures the two roles: the unit tests, then their targets on a
# small Kind cluster of its own, without the setup of the whole of `make test`.
role-coverage:
	rm -rf "$(ROLE_COVERAGE_DIR)" && mkdir -p "$(ROLE_COVERAGE_DIR)"
	$(ROLE_COVERAGE_ENV) uv run --group development pytest tests/unit $(ROLE_COVERAGE_PYTEST)
	$(ROLE_COVERAGE_ENV) tests/coverage/run_role_targets.sh
	$(MAKE) role-coverage-report

role-coverage-report:
	uv run --group development python tests/coverage/role_coverage.py --dir "$(ROLE_COVERAGE_DIR)" \
		--python-coverage "$(ROLE_COVERAGE_DIR)/python.json"

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
