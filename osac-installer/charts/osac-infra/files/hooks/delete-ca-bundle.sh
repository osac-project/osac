#!/usr/bin/env bash
set -euo pipefail

# pre-delete: drop the oc-applied Bundle (Helm does not own it) and any
# apply-ca-bundle hook leftovers if uninstall races the post-install Job.

CA_BUNDLE_NAME="${CA_BUNDLE_NAME:-ca-bundle}"
RELEASE_NAMESPACE="${RELEASE_NAMESPACE:-}"
OC=(oc --request-timeout=10s)

# Run oc with a hard 15s cap. origin-cli can ignore --request-timeout.
oc_run() {
  if command -v timeout >/dev/null 2>&1; then
    timeout -k 2s 15s "${OC[@]}" "$@"
  else
    "${OC[@]}" "$@"
  fi
}

# Attempt every delete; --ignore-not-found is success. Timeout/auth still fail.
cleanup_failed=0
# Run one oc delete; record failure without aborting remaining deletes.
try_delete() {
  if ! oc_run "$@"; then
    cleanup_failed=1
  fi
}

# Delete the Bundle. Missing object and missing CRD are success.
try_delete_bundle() {
  local out rc=0
  out="$(oc_run delete bundle.trust.cert-manager.io "${CA_BUNDLE_NAME}" --ignore-not-found 2>&1)" || rc=$?
  if [[ "${rc}" -eq 0 ]]; then
    [[ -n "${out}" ]] && echo "${out}"
    return 0
  fi
  if [[ "${out}" == *"doesn't have a resource type"* || "${out}" == *"no matches for kind"* ]]; then
    echo "Bundle CRD absent; skip"
    return 0
  fi
  echo "${out}" >&2
  cleanup_failed=1
}

echo "Deleting Bundle ${CA_BUNDLE_NAME}..."
try_delete_bundle

if [[ -n "${RELEASE_NAMESPACE}" ]]; then
  echo "Deleting leftover apply-ca-bundle hook resources..."
  try_delete delete job,sa "osac-infra-apply-ca-bundle" -n "${RELEASE_NAMESPACE}" --ignore-not-found
fi
try_delete delete clusterrole,clusterrolebinding "osac-infra-apply-ca-bundle" --ignore-not-found
try_delete delete role,rolebinding "osac-infra-apply-ca-bundle" -n cert-manager --ignore-not-found

if [[ "${cleanup_failed}" -ne 0 ]]; then
  echo "ERROR: CA Bundle cleanup incomplete" >&2
  exit 1
fi
echo "CA Bundle cleanup done."
