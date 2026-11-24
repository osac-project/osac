# Shared image build/load recipes for installer and component integration tests.

define kind-load-image
	@if [ "$(KIND_RUNTIME_REQUIRED)" = "true" ]; then \
		tmpfile=$$(mktemp /tmp/kind-image-XXXXXX); \
		trap 'rm -f "$$tmpfile"' EXIT; \
		$(KIND_RUNTIME_CMD) container save $(1) > "$$tmpfile"; \
		$(KIND_RUNTIME_CMD) load image-archive "$$tmpfile" --name $(KIND_CLUSTER_NAME); \
	elif [ "$$(basename $(CONTAINER_TOOL))" = "podman" ]; then \
		tmpfile=$$(mktemp /tmp/kind-image-XXXXXX); \
		trap 'rm -f "$$tmpfile"' EXIT; \
		$(CONTAINER_TOOL) save $(1) -o "$$tmpfile"; \
		kind load image-archive "$$tmpfile" --name $(KIND_CLUSTER_NAME); \
	else \
		kind load docker-image $(1) --name $(KIND_CLUSTER_NAME); \
	fi
endef

# GitHub-hosted runners intermittently fail container builds on transient
# network errors: registry.access.redhat.com answers manifest HEAD requests
# with 403 (rate limiting of shared runner egress IPs), proxy.golang.org
# resets connections mid-download (see osac-test-infra#543) and blob reads
# end with "unexpected EOF". BuildKit caches completed steps, so a retry only
# repeats the failed one; deterministic failures (a broken Containerfile, an
# unsatisfiable dependency) still fail once the retries are exhausted.
define kind-container-build
	@if [ "$(KIND_RUNTIME_REQUIRED)" = "true" ]; then \
		build_cmd="$(KIND_RUNTIME_CMD) container build"; \
	else \
		build_cmd="$(CONTAINER_TOOL) build"; \
	fi; \
	for attempt in 1 2 3; do \
		if $$build_cmd $(1); then exit 0; fi; \
		if [ "$$attempt" -lt 3 ]; then \
			echo "WARNING: container build attempt $$attempt/3 failed; retrying in 30s" >&2; \
			sleep 30; \
		fi; \
	done; \
	echo 'ERROR: container build failed after 3 attempts' >&2; \
	exit 1
endef
