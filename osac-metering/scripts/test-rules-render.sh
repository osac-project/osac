#!/usr/bin/env bash
# Check that the immutable rule revision ConfigMap renders byte-identically
# across chart versions. Kubernetes rejects any update to an immutable
# ConfigMap, so a label or annotation that changes on a chart upgrade — helm's
# chart-version label, or a checksum annotation — fails the upgrade at apply
# time once the ConfigMap exists.
set -euo pipefail

cd "$(dirname "$0")/.."

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

# The first document of the render is the revision ConfigMap.
config_map() {
	awk 'NR > 1 && /^---$/ {exit} {print}' "$1"
}

helm template test charts/osac-metering --show-only templates/rules.yaml > "$tmp/current.yaml"

cp -r charts/osac-metering "$tmp/bumped"
# Rewrite the chart version without sed -i: its argument handling differs between GNU and
# BSD sed, and make test runs this script on both.
sed 's/^version: .*/version: 9.9.9/' "$tmp/bumped/Chart.yaml" > "$tmp/bumped/Chart.yaml.tmp"
mv "$tmp/bumped/Chart.yaml.tmp" "$tmp/bumped/Chart.yaml"
helm template test "$tmp/bumped" --show-only templates/rules.yaml > "$tmp/bumped.yaml"

config_map "$tmp/current.yaml" > "$tmp/current-configmap.yaml"
config_map "$tmp/bumped.yaml" > "$tmp/bumped-configmap.yaml"

if ! diff -u "$tmp/current-configmap.yaml" "$tmp/bumped-configmap.yaml"; then
	echo "The revision ConfigMap must render identically across chart versions:" \
		"it is immutable, so anything that changes on upgrade fails the upgrade" \
		"at apply time." >&2
	exit 1
fi

grep -qx 'immutable: true' "$tmp/current-configmap.yaml" || {
	echo "The revision ConfigMap must stay natively immutable." >&2
	exit 1
}

if grep -q 'helm.sh/chart:' "$tmp/current-configmap.yaml"; then
	echo "The revision ConfigMap must not carry the chart-version label;" \
		"only static labels belong on it." >&2
	exit 1
fi

# The admission policy objects stay mutable, so they keep the full label set
# and the two renders differ exactly there.
if diff -q "$tmp/current.yaml" "$tmp/bumped.yaml" > /dev/null; then
	echo "The admission policy objects must carry the chart-version label." >&2
	exit 1
fi
if ! grep -q 'helm.sh/chart: osac-metering-9.9.9' "$tmp/bumped.yaml"; then
	echo "The admission policy objects must carry the chart-version label." >&2
	exit 1
fi

echo "Rule revision render checks passed."
