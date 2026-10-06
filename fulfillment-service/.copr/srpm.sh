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

# Copr's SCM checkout may not contain release tags that point to generated release commits
# outside the configured branch. Fetch tags so main snapshots can see the latest release.
git -C "${repo_root}" fetch --quiet --tags origin

# Release tags are created on a temporary branch after release metadata is stamped. If the
# tag commit is not in this checkout's history but its parent is, use that parent as the
# release baseline. This keeps main snapshots newer than the latest released RPM instead
# of falling back to an older reachable tag. On the tagged release commit itself, use the
# tag as the baseline so the build keeps the exact release version.
repo_head=$(git -C "${repo_root}" rev-parse HEAD)
version=""
while IFS= read -r tag; do
  release_version="${tag#fulfillment-service/v}"
  tag_commit=$(git -C "${repo_root}" rev-parse "${tag}^{commit}")
  baseline="${tag_commit}"
  baseline_is_tag_commit=true

  if ! git -C "${repo_root}" merge-base --is-ancestor "${baseline}" "${repo_head}"; then
    baseline=$(git -C "${repo_root}" rev-parse "${tag_commit}^" 2>/dev/null || true)
    baseline_is_tag_commit=false
  fi

  if [[ -n "${baseline}" ]] && git -C "${repo_root}" merge-base --is-ancestor "${baseline}" "${repo_head}"; then
    commit_count=$(git -C "${repo_root}" rev-list --count "${baseline}..${repo_head}")
    if (( commit_count > 0 )) || [[ "${baseline_is_tag_commit}" == false ]]; then
      version="${release_version}^${commit_count}.g$(git -C "${repo_root}" rev-parse --short "${repo_head}")"
    else
      version="${release_version}"
    fi
    break
  fi
done < <(git -C "${repo_root}" tag --list 'fulfillment-service/v*' --sort=-version:refname)

# Retain git-describe fallback for checkouts without an applicable release tag.
if [[ -z "${version}" ]]; then
  version=$(
    git -C "${component_dir}" describe --tags --always --match 'fulfillment-service/v*' 2>/dev/null |
    sed \
      -e 's#^fulfillment-service/v##' \
      -e 's/-\([0-9]*\)-g/^\1.g/'
  )
fi

# Calculate the date for the changelog entry in the format required by RPM:
date=$(date +'%a %b %d %Y')

# Create the tarball with the monorepo layout intact. fulfillment-service/go.mod uses local replace directives for
# sibling modules, so the RPM build needs those modules at their original relative paths. Archive only the required
# component and replacement module paths rather than the entire monorepo. fulfillment-service/LICENSE was deleted as
# a redundant duplicate when its root files were consolidated into the monorepo's own LICENSE (see OSAC-1733), so add
# the root LICENSE back for the spec.
workdir="$(mktemp -d)"
trap 'rm -rf "${workdir}"' EXIT
extract_dir="${workdir}/${name}-${version}"
mkdir -p "${extract_dir}"
git -C "${repo_root}" archive HEAD \
  fulfillment-service proto bare-metal-fulfillment-operator osac-operator/api \
  | tar -x -C "${extract_dir}"
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
