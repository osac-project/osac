#!/usr/bin/env bash
# Sync image tags in Helm values files to match built component commits.
# osac-operator, fulfillment-service, osac-aap, and
# bare-metal-fulfillment-operator now live in the same mono-repo as
# osac-installer itself and publish SHA-tagged images off that mono-repo's
# own commits -- there's one shared commit for all of them, not four
# independent submodule pins. osac-ui remains a genuinely separate
# repo/submodule and keeps its own commit lookup.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"
MONOREPO_ROOT="$(cd "${REPO_ROOT}/.." && pwd)"

errors=0

osac_tag="sha-$(git -C "${MONOREPO_ROOT}" rev-parse --short=7 HEAD)"
operator_tag="${osac_tag}"
fulfillment_tag="${osac_tag}"
aap_tag="${osac_tag}"
bmf_tag="${osac_tag}"
ui_tag="sha-$(git -C "${REPO_ROOT}" submodule status base/osac-ui | awk '{print $1}' | tr -d ' +-' | cut -c1-7)"

for values_file in "${REPO_ROOT}"/values/*/values.yaml; do
  [[ ! -f "${values_file}" ]] && continue
  name=$(basename "$(dirname "${values_file}")")
  grep -q "sha-" "${values_file}" || continue

  for pair in \
    "osac-operator:tag ${operator_tag}" \
    "fulfillment-service:inline ${fulfillment_tag}" \
    "osac-aap:inline ${aap_tag}" \
    "bare-metal-fulfillment-operator:tag ${bmf_tag}" \
    "osac-ui:inline ${ui_tag}"; do
    component="${pair%%:*}"
    rest="${pair#*:}"
    mode="${rest%% *}"
    expected="${rest#* }"

    if [[ "${mode}" == "tag" ]]; then
      # Skip components not configured in this values file (e.g. BMF disabled in vmaas-ci).
      grep -q "repository: ghcr.io/osac-project/${component}$" "${values_file}" || continue
      current=$(grep -A1 "repository: ghcr.io/osac-project/${component}$" "${values_file}" | grep "tag:" | awk '{print $2}' || true)
      [[ -z "${current}" ]] && continue
      if [[ "${current}" == "${expected}" ]]; then
        echo "${name} ${component}: OK (${expected})"
      elif [[ "${1:-}" == "--fix" ]]; then
        sed -i "/repository: ghcr.io\/osac-project\/${component}$/{n;s|tag: .*|tag: ${expected}|}" "${values_file}"
        echo "${name} ${component}: FIXED ${current} -> ${expected}"
      else
        echo "${name} ${component}: MISMATCH current=${current} expected=${expected}"
        errors=$((errors + 1))
      fi
    else
      current=$(grep -o "${component}:sha-[a-f0-9]\{7\}" "${values_file}" | head -1 | sed "s/${component}://" || true)
      [[ -z "${current}" ]] && continue
      if [[ "${current}" == "${expected}" ]]; then
        echo "${name} ${component}: OK (${expected})"
      elif [[ "${1:-}" == "--fix" ]]; then
        sed -i "s|${component}:sha-[a-f0-9]\{7\}|${component}:${expected}|g" "${values_file}"
        echo "${name} ${component}: FIXED ${current} -> ${expected}"
      else
        echo "${name} ${component}: MISMATCH current=${current} expected=${expected}"
        errors=$((errors + 1))
      fi
    fi
  done

  # projectGitBranch/projectGitUri (osac-aap's config-as-code clone target)
  # intentionally NOT synced here anymore. It's a *runtime* git-clone
  # target consumed by the AAP bootstrap job inside the installed cluster,
  # not a build-time reference -- unlike the image tags above, it can't
  # simply track this mono-repo's own HEAD, because projectGitUri still
  # points at the standalone osac-aap repo, which doesn't contain the
  # mono-repo's commits. Repointing this pair needs the AAP bootstrap job
  # itself to clone the whole mono-repo and cd into osac-aap/, a bigger
  # change than this script's scope. Left unmanaged rather than silently
  # synced to a SHA its current target can't resolve.
done

if [[ ${errors} -gt 0 ]]; then
  echo ""
  echo "Run '$0 --fix' to update the tags automatically."
  exit 1
fi
