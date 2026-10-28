# Copyright (c) 2026 Red Hat Inc.
#
# Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except
# in compliance with the License. You may obtain a copy of the License at
#
#   http://www.apache.org/licenses/LICENSE-2.0

# Single definition of "inside an OpenShell sandbox" (tools/openshell.mk).
OSAC_REPO_ROOT := $(abspath $(dir $(lastword $(MAKEFILE_LIST)))/../..)
include $(OSAC_REPO_ROOT)/tools/openshell.mk

BINARY_NAME ?= metering-service
IMG ?= ghcr.io/osac-project/metering-service:latest
CONTAINER_TOOL ?= podman
GO_VERSION ?= $(shell grep '^go ' go.mod | cut -d' ' -f2)
GOTOOLCHAIN ?= go$(GO_VERSION)
GINKGO ?= go run github.com/onsi/ginkgo/v2/ginkgo

# Derive the repo root from this Makefile's location rather than git: the
# Carbonite sandbox mounts the worktree without its host-only gitdir, so
# `git rev-parse --show-toplevel` fails there and the include path collapses.

include $(OSAC_REPO_ROOT)/tools/golangci-lint.mk

.PHONY: build test lint generate image-build image-push clean

build:
	go build -o bin/$(BINARY_NAME) ./cmd/metering-service

test:
	go test ./cmd/metering-service
	$(GINKGO) run -r internal

lint: golangci-lint
	$(GOLANGCI_LINT) run ./...

# Proto codegen is no longer per-module. Regenerate the shared
# tree once from the repo-root proto/ module: `make -C ../../proto generate`.
generate:
	$(MAKE) -C ../../proto generate

# Context is the repo root (`../..`), not osac-metering/ -- the Containerfile
# COPYs the sibling schema/ directory AND the top-level proto/ module (the
# latter reachable only from the repo root). Matches
# build-metering-service-image.yaml's `context: .`.
image-build:
ifeq ($(OPENSHELL_SANDBOX_ACTIVE),true)
	@if [ "$(origin IMG)" != file ] || [ "$(origin CONTAINER_TOOL)" != file ]; then \
		printf 'Carbonite image-build does not support IMG or CONTAINER_TOOL overrides\n' >&2; \
		exit 1; \
	fi
	@if [ ! -x ../../tools/carbonite/host-build ]; then \
		printf 'Carbonite host-build helper is missing; enable host builds on the host and re-enter the sandbox\n' >&2; \
		exit 1; \
	fi
	@../../tools/carbonite/host-build metering-service
else
	$(CONTAINER_TOOL) build -t $(IMG) -f Containerfile ../..
endif

image-push:
	$(CONTAINER_TOOL) push $(IMG)

clean:
	rm -rf bin/
