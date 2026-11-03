#!/usr/bin/env bash
# Granting or revoking a metering reader must render the reader's KafkaUser
# only: the metering Deployment must stay byte-identical, because a changed pod
# template rolls metering and interrupts billing. Removing a reader from the
# values must also remove its KafkaUser, so access cannot outlive the grant.
set -euo pipefail

cd "$(dirname "$0")/.."

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

deployment() {
	local out="$1"
	shift
	helm template test charts/osac-metering --show-only templates/deployment.yaml "$@" >"$out"
}

readers() {
	local out="$1"
	shift
	helm template test charts/osac-metering --show-only templates/readers.yaml "$@" >"$out"
}

deployment "$tmp/deployment-without-readers.yaml"
deployment "$tmp/deployment-with-reader.yaml" \
	--set 'readers[0].name=billing-reader'

if ! diff -u "$tmp/deployment-without-readers.yaml" "$tmp/deployment-with-reader.yaml"; then
	echo "The metering Deployment must not change when a reader is granted or" \
		"revoked: a changed pod template rolls metering and interrupts billing." >&2
	exit 1
fi

readers "$tmp/two-readers.yaml" \
	--set 'readers[0].name=billing-reader' \
	--set 'readers[1].name=audit-reader'
readers "$tmp/after-removal.yaml" \
	--set 'readers[0].name=audit-reader'

grep -q '^  name: billing-reader$' "$tmp/two-readers.yaml" || {
	echo "The grant must render the reader's KafkaUser." >&2
	exit 1
}
if grep -q '^  name: billing-reader$' "$tmp/after-removal.yaml"; then
	echo "Removing a reader from the values must remove its KafkaUser." >&2
	exit 1
fi
grep -q '^  name: audit-reader$' "$tmp/after-removal.yaml" || {
	echo "The remaining reader's KafkaUser must stay." >&2
	exit 1
}

echo "Reader grant, revoke, and removal render checks passed."
