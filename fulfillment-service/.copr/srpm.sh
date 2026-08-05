#!/bin/bash
#
# Copyright (c) 2025 Red Hat, Inc.
#
# Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
# License. You may obtain a copy of the License at
#
#   http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
# "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
# language governing permissions and limitations under the License.
#

set -eo pipefail

# Name of the project:
name="osac-cli"

# Directory containing this script:
here="$(dirname "$(readlink -f "$0")")"

# This component's own directory, and the mono-repo root above it:
component_dir="$(readlink -f "${here}/..")"
repo_root="$(readlink -f "${here}/../..")"

# Output directory:
outdir="${outdir:-$here}"

# Install git if not available:
if ! command -v git >/dev/null 2>&1; then
  dnf install -y git
fi

# Calculate the version from git. This uses git describe to get a version string based on the most recent tag matching
# this component's own scoped tag prefix -- since the mono-repo's tags aren't namespaced per directory, an unscoped
# `git describe --tags` would happily describe against the nearest tag from *any* component (e.g. osac-operator/vX.Y.Z),
# giving fulfillment-service a nonsensical version. If there are commits after the tag, the version will include the
# commit count and short hash. The component prefix and any leading 'v' are stripped, and the format is converted to
# use RPM's caret notation for post-release versions (e.g., 0.0.20^29.gfa27fa8).
version=$(
  git -C "${component_dir}" describe --tags --always --match 'fulfillment-service/v*' 2>/dev/null |
  sed \
    -e 's#^fulfillment-service/v##' \
    -e 's/-\([0-9]*\)-g/^\1.g/'
)

# Calculate the date for the changelog entry in the format required by RPM:
date=$(date +'%a %b %d %Y')

# Create the tarball. `git archive` has no pathspec-based way to scope to a subdirectory *and* flatten it to the
# archive root in one step -- `HEAD:fulfillment-service` (a subtree, not a full-tree pathspec) gives the scoping,
# archiving fulfillment-service/ as if it were the repo root, so %build/%install's bare `cmd/osac`, `go.mod` etc.
# paths resolve correctly instead of everything (including every other component) landing in the tarball. But
# fulfillment-service/LICENSE was deleted as a redundant duplicate when its root files were consolidated into the
# mono-repo's own LICENSE (see OSAC-1733), so that subtree archive alone would be missing the %license file the spec
# requires -- assemble the tarball in a scratch directory instead, adding the root LICENSE back in.
workdir="$(mktemp -d)"
trap 'rm -rf "${workdir}"' EXIT
extract_dir="${workdir}/${name}-${version}"
mkdir -p "${extract_dir}"
git -C "${repo_root}" archive "HEAD:fulfillment-service" | tar -x -C "${extract_dir}"
cp "${repo_root}/LICENSE" "${extract_dir}/LICENSE"
tar -czf "${outdir}/${name}-${version}.tar.gz" -C "${workdir}" "${name}-${version}"

# Create the spec file:
sed \
  -e "s/@version@/${version}/g" \
  -e "s/@date@/${date}/g" \
  "${here}/${name}.spec.in" > "${here}/${name}.spec"

# Build the SRPM:
rpmbuild \
  --define "_srcrpmdir ${outdir}" \
  --define "_sourcedir ${outdir}" \
  -bs "${here}/${name}.spec"
