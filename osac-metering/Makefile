# Copyright (c) 2026 Red Hat Inc.
#
# Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except
# in compliance with the License. You may obtain a copy of the License at
#
#   http://www.apache.org/licenses/LICENSE-2.0

.PHONY: all clean helm-lint test lint

all: test lint

helm-lint:
	helm lint charts/osac-metering/

test:
	$(MAKE) -C schema test
	$(MAKE) -C metering-service test
	$(MAKE) -C adapters test

lint: helm-lint
	$(MAKE) -C schema lint
	$(MAKE) -C metering-service lint
	$(MAKE) -C adapters lint

clean:
	$(MAKE) -C schema clean
	$(MAKE) -C metering-service clean
	$(MAKE) -C adapters clean
