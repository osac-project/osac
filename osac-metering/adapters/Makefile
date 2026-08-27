# Copyright (c) 2026 Red Hat Inc.
#
# Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except
# in compliance with the License. You may obtain a copy of the License at
#
#   http://www.apache.org/licenses/LICENSE-2.0

GO_VERSION ?= $(shell grep '^go ' go.mod | cut -d' ' -f2)
GOTOOLCHAIN ?= go$(GO_VERSION)
GINKGO ?= go run github.com/onsi/ginkgo/v2/ginkgo
CONTAINER_TOOL ?= podman

# Image URLs for building/pushing the two adapter images.
IMG_M360_ADAPTER ?= ghcr.io/osac-project/metering-m360-adapter:latest
IMG_ECHO_ADAPTER ?= ghcr.io/osac-project/metering-echo-adapter:latest

include $(shell git rev-parse --show-toplevel)/tools/golangci-lint.mk

.PHONY: all build-echo-adapter build-m360-adapter test lint clean \
	image-build-m360-adapter image-push-m360-adapter \
	image-build-echo-adapter image-push-echo-adapter

all: build-echo-adapter build-m360-adapter

build-echo-adapter:
	@mkdir -p bin
	go build -o bin/echo-adapter ./cmd/echo-adapter

build-m360-adapter:
	@mkdir -p bin
	go build -o bin/m360-adapter ./cmd/m360-adapter

test:
	$(GINKGO) run -r .

lint: golangci-lint
	$(GOLANGCI_LINT) run ./...

# Context is `..` (osac-metering/), not `.` -- both adapter Containerfiles
# also COPY the sibling schema/ directory, which only exists one level up
# from adapters/ (matches build-metering-{m360,echo}-adapter-image.yaml's
# `context: osac-metering`, `file: osac-metering/adapters/Containerfile.*`).
image-build-m360-adapter:
	$(CONTAINER_TOOL) build -t $(IMG_M360_ADAPTER) -f Containerfile.m360-adapter ..

image-push-m360-adapter:
	$(CONTAINER_TOOL) push $(IMG_M360_ADAPTER)

image-build-echo-adapter:
	$(CONTAINER_TOOL) build -t $(IMG_ECHO_ADAPTER) -f Containerfile.echo-adapter ..

image-push-echo-adapter:
	$(CONTAINER_TOOL) push $(IMG_ECHO_ADAPTER)

clean:
	rm -rf bin/
