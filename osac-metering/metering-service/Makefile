# Copyright (c) 2026 Red Hat Inc.
#
# Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except
# in compliance with the License. You may obtain a copy of the License at
#
#   http://www.apache.org/licenses/LICENSE-2.0

BINARY_NAME ?= metering-service
IMG ?= ghcr.io/osac-project/metering-service:latest
GINKGO ?= go run github.com/onsi/ginkgo/v2/ginkgo
GOLANGCI_LINT_VERSION ?= v2.12.1

LOCALBIN ?= $(shell pwd)/bin
GOLANGCI_LINT ?= $(LOCALBIN)/golangci-lint

.PHONY: build test lint generate image-build clean

build:
	go build -o bin/$(BINARY_NAME) ./cmd/metering-service

test:
	$(GINKGO) run -r internal

lint: $(GOLANGCI_LINT)
	$(GOLANGCI_LINT) run ./...

$(GOLANGCI_LINT):
	@mkdir -p $(LOCALBIN)
	GOBIN=$(LOCALBIN) go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)

generate:
	buf generate

image-build:
	podman build -t $(IMG) -f Containerfile .

clean:
	rm -rf bin/
