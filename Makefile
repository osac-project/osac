IMAGE_NAME ?= osac-dev
DISTROBOX_NAME ?= osac
HOME_DIR ?= $(HOME)
CONTAINER_CMD ?= $(shell command -v podman 2>/dev/null || echo docker)
DISTROBOX_CONTEXT := tools/distrobox
ARGS ?=

.PHONY: image enter claude stop rm rebuild status help

help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-12s\033[0m %s\n", $$1, $$2}'

image: ## Build the distrobox image
	$(CONTAINER_CMD) build -t $(IMAGE_NAME) -f $(DISTROBOX_CONTEXT)/Containerfile $(DISTROBOX_CONTEXT)

enter: image ## Enter the distrobox (creates it on first run)
	@if ! distrobox list --no-color 2>/dev/null | awk -F'|' 'NR>1{gsub(/^ +| +$$/,"",$$2); print $$2}' | grep -Fxq "$(DISTROBOX_NAME)"; then \
		distrobox create --image $(IMAGE_NAME) --name $(DISTROBOX_NAME) --home "$(HOME_DIR)"; \
	fi
	distrobox enter $(DISTROBOX_NAME)

claude: image ## Run Claude Code inside distrobox (ARGS="--flag" to pass flags)
	@if ! distrobox list --no-color 2>/dev/null | awk -F'|' 'NR>1{gsub(/^ +| +$$/,"",$$2); print $$2}' | grep -Fxq "$(DISTROBOX_NAME)"; then \
		distrobox create --image $(IMAGE_NAME) --name $(DISTROBOX_NAME) --home "$(HOME_DIR)"; \
	fi
	distrobox enter $(DISTROBOX_NAME) -- claude $(ARGS)

stop: ## Stop the running distrobox container
	@distrobox stop -Y $(DISTROBOX_NAME) 2>/dev/null || true

rm: ## Remove the distrobox and its container
	@distrobox rm --force $(DISTROBOX_NAME) 2>/dev/null || true

rebuild: rm image enter ## Rebuild image from scratch and enter

status: ## Show distrobox and image status
	@echo "=== Images ==="
	@$(CONTAINER_CMD) images --filter reference=$(IMAGE_NAME) --format "table {{.Repository}}\t{{.Tag}}\t{{.Size}}\t{{.Created}}"
	@echo ""
	@echo "=== Distrobox ==="
	@distrobox list --no-color 2>/dev/null | head -1; distrobox list --no-color 2>/dev/null | awk -F'|' 'NR>1{gsub(/^ +| +$$/,"",$$2); if($$2=="$(DISTROBOX_NAME)") { print; found=1 }} END { if (!found) print "  (not created)" }'
