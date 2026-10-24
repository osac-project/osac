# Copyright (c) 2026 Red Hat Inc.
#
# Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except
# in compliance with the License. You may obtain a copy of the License at
#
#   http://www.apache.org/licenses/LICENSE-2.0

.PHONY: all clean helm-lint test lint rules-render-test rules-unchanged-test

all: test lint

helm-lint:
	helm lint charts/osac-metering/

test:
	$(MAKE) -C schema test
	$(MAKE) -C metering-service test
	$(MAKE) -C adapters test
	$(MAKE) rules-render-test
	$(MAKE) rules-unchanged-test

# The revision ConfigMap is immutable, so its render must not change across
# chart versions; see scripts/test-rules-render.sh.
rules-render-test:
	./scripts/test-rules-render.sh

# A released revision must never be edited; see scripts/test-rules-unchanged.sh.
rules-unchanged-test:
	./scripts/test-rules-unchanged.sh

lint: helm-lint
	$(MAKE) -C schema lint
	$(MAKE) -C metering-service lint
	$(MAKE) -C adapters lint

clean:
	$(MAKE) -C schema clean
	$(MAKE) -C metering-service clean
	$(MAKE) -C adapters clean
