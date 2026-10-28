# Copyright (c) 2026 Red Hat Inc.
#
# Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except
# in compliance with the License. You may obtain a copy of the License at
#
#   http://www.apache.org/licenses/LICENSE-2.0

GO_VERSION ?= $(shell grep '^go ' go.mod | cut -d' ' -f2)
GOTOOLCHAIN ?= go$(GO_VERSION)
GINKGO ?= go run github.com/onsi/ginkgo/v2/ginkgo

# Derive the repo root from this Makefile's location rather than git: the
# Carbonite sandbox mounts the worktree without its host-only gitdir, so
# `git rev-parse --show-toplevel` fails there and the include path collapses.
OSAC_REPO_ROOT := $(abspath $(dir $(lastword $(MAKEFILE_LIST)))/../..)

include $(OSAC_REPO_ROOT)/tools/golangci-lint.mk

.PHONY: all test lint clean

all: test lint

test:
	$(GINKGO) run -r .

lint: golangci-lint
	$(GOLANGCI_LINT) run ./...

clean:
	rm -rf bin/
