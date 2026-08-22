# Copyright (c) 2026 Red Hat Inc.
#
# Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except
# in compliance with the License. You may obtain a copy of the License at
#
#   http://www.apache.org/licenses/LICENSE-2.0

GO_VERSION ?= $(shell grep '^go ' go.mod | cut -d' ' -f2)
GOTOOLCHAIN ?= go$(GO_VERSION)
GINKGO ?= go run github.com/onsi/ginkgo/v2/ginkgo

include $(shell git rev-parse --show-toplevel)/tools/golangci-lint.mk

.PHONY: all build-echo-adapter build-m360-adapter test lint clean

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

clean:
	rm -rf bin/
