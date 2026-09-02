# OSAC devstack chart Makefile

CLI_TOOLS_IMAGE ?= ghcr.io/osac-project/cli-tools:latest

.PHONY: build-cli-tools
build-cli-tools: ## Build multi-arch CLI tools image for dev-full hooks
	podman build --platform linux/amd64,linux/arm64 \
		-t $(CLI_TOOLS_IMAGE) \
		-f Containerfile.cli-tools .

.PHONY: push-cli-tools
push-cli-tools: ## Push multi-arch CLI tools image
	podman push $(CLI_TOOLS_IMAGE)

.PHONY: build-push-cli-tools
build-push-cli-tools: build-cli-tools push-cli-tools ## Build and push CLI tools image

.PHONY: help
help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-20s\033[0m %s\n", $$1, $$2}'
