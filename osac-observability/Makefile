# Copyright (c) 2026 Red Hat Inc.
#
# Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except
# in compliance with the License. You may obtain a copy of the License at
#
#   http://www.apache.org/licenses/LICENSE-2.0

.PHONY: all clean helm-lint helm-unit render-test test go-test image-build image-push

# The helper image (remote-write-config, query-allowlist,
# admin-credentials, query-auth-gate) is built from the monorepo root
# context, the same way every other component's image is, so the
# Containerfile can copy the shared go.work and sibling modules.
IMG ?= ghcr.io/osac-project/osac-observability:latest
CONTAINERFILE ?= Containerfile
CONTAINER_TOOL ?= podman
VERSION ?=

all: test

helm-lint:
	helm lint charts/osac-observability/

helm-unit: helm-lint
	helm unittest charts/osac-observability/

render-test:
	./scripts/test-render.sh

go-test:
	go test ./... -race

test: helm-unit render-test go-test

# Context is the monorepo root (..), not this directory: the Containerfile
# copies go.work, proto/ and the sibling modules it depends on.
image-build:
	$(CONTAINER_TOOL) build -t ${IMG} -f ${CONTAINERFILE} \
		--build-arg VERSION=$(VERSION) ..

image-push:
	$(CONTAINER_TOOL) push ${IMG}

clean:
	rm -rf charts/osac-observability/charts
