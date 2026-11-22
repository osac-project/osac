# Copyright (c) 2026 Red Hat Inc.
#
# Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except
# in compliance with the License. You may obtain a copy of the License at
#
#   http://www.apache.org/licenses/LICENSE-2.0

.PHONY: all clean helm-lint helm-unit render-test test go-test

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

clean:
	rm -rf charts/osac-observability/charts
