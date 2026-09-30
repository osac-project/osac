#!/usr/bin/env bash
# Exercising the Cost adapter's delivery boundary: unit contract, race safety,
# build, and enabled Helm render. Run from any directory in the repository.
set -euo pipefail

script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
adapters_dir="$(cd -- "${script_dir}/.." && pwd)"
metering_dir="$(cd -- "${adapters_dir}/.." && pwd)"

cd "${adapters_dir}"
go test ./cmd/cost-management-adapter
go test -race ./cmd/cost-management-adapter
make build-cost-management-adapter

cd "${metering_dir}"
make helm-lint
helm template cost-management-adapter charts/cost-management-adapter \
	--set enabled=true \
	--set costManagement.apiUrl=https://cost.example.test \
	--set costManagement.apiTokenSecret=cost-api-token \
	>/dev/null
