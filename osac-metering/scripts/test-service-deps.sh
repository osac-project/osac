#!/usr/bin/env bash
# The metering service binary must not import the contract generator. The generator reads
# source and chart files and writes the published reference; it is a build tool, not part of
# the service image or its runtime dependency graph.
set -euo pipefail

cd "$(dirname "$0")/../metering-service"

forbidden=(
	"github.com/osac-project/osac-metering/tools/contract"
	"github.com/osac-project/osac-metering/cmd/metering-contract"
)

deps="$(go list -deps ./cmd/metering-service)"

for package in "${forbidden[@]}"; do
	if grep -qxF "${package}" <<<"${deps}"; then
		echo "ERROR: metering-service imports ${package}." >&2
		echo "The contract generator is a build tool; keep it out of the service binary." >&2
		exit 1
	fi
done

echo "metering-service does not import the contract generator."
