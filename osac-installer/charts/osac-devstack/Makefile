# OSAC devstack chart Makefile

CLI_TOOLS_IMAGE ?= ghcr.io/osac-project/cli-tools:latest

.PHONY: build-cli-tools
build-cli-tools: ## Build multi-arch CLI tools image for dev-full hooks

ifneq ($(origin OPENSHELL_SANDBOX),undefined)
	@if [ "$(origin CLI_TOOLS_IMAGE)" != file ]; then \
		printf 'Carbonite build-cli-tools does not support CLI_TOOLS_IMAGE overrides\n' >&2; \
		exit 1; \
	fi
	@if [ ! -x ../../../tools/carbonite/host-build ]; then \
		printf 'Carbonite host-build helper is missing; enable host builds on the host and re-enter the sandbox\n' >&2; \
		exit 1; \
	fi
	@../../../tools/carbonite/host-build devstack-cli-tools
else
	podman build --platform linux/amd64,linux/arm64 \
		--manifest $(CLI_TOOLS_IMAGE) \
		-f Containerfile.cli-tools .
endif

.PHONY: push-cli-tools
push-cli-tools: ## Push multi-arch CLI tools image

ifneq ($(origin OPENSHELL_SANDBOX),undefined)
	@printf 'Carbonite push-cli-tools is unsupported by the host-build channel\n' >&2; exit 1
else
	podman manifest push --all $(CLI_TOOLS_IMAGE)
endif

.PHONY: build-push-cli-tools

ifneq ($(origin OPENSHELL_SANDBOX),undefined)
build-push-cli-tools: ## Build and push CLI tools image
	@printf 'Carbonite build-push-cli-tools is unsupported by the host-build channel\n' >&2; exit 1
else
build-push-cli-tools: build-cli-tools push-cli-tools ## Build and push CLI tools image
endif

.PHONY: help
help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-20s\033[0m %s\n", $$1, $$2}'
