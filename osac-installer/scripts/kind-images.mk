# Shared image build/load recipes for installer and component integration tests.

define kind-load-image
	@if [ "$(KIND_RUNTIME_REQUIRED)" = "true" ]; then \
		tmpfile=$$(mktemp /tmp/kind-image-XXXXXX.tar); \
		trap 'rm -f "$$tmpfile"' EXIT; \
		$(KIND_RUNTIME_CMD) container save $(1) > "$$tmpfile"; \
		$(KIND_RUNTIME_CMD) load image-archive "$$tmpfile" --name $(KIND_CLUSTER_NAME); \
	elif [ "$$(basename $(CONTAINER_TOOL))" = "podman" ]; then \
		tmpfile=$$(mktemp /tmp/kind-image-XXXXXX.tar); \
		trap 'rm -f "$$tmpfile"' EXIT; \
		$(CONTAINER_TOOL) save $(1) -o "$$tmpfile"; \
		kind load image-archive "$$tmpfile" --name $(KIND_CLUSTER_NAME); \
	else \
		kind load docker-image $(1) --name $(KIND_CLUSTER_NAME); \
	fi
endef

define kind-container-build
	@if [ "$(KIND_RUNTIME_REQUIRED)" = "true" ]; then \
		$(KIND_RUNTIME_CMD) container build $(1); \
	else \
		$(CONTAINER_TOOL) build $(1); \
	fi
endef
