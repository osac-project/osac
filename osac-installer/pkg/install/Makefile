# Copyright (c) 2026 Red Hat Inc.
#
# Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except
# in compliance with the License. You may obtain a copy of the License at
#
#   http://www.apache.org/licenses/LICENSE-2.0

GINKGO ?= go run github.com/onsi/ginkgo/v2/ginkgo

include $(shell git rev-parse --show-toplevel)/tools/golangci-lint.mk

.PHONY: test lint lint-fix

test:
	$(GINKGO) run -r .

lint: golangci-lint
	$(GOLANGCI_LINT) run ./...

lint-fix: golangci-lint
	$(GOLANGCI_LINT) run --fix ./...
