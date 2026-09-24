#!/usr/bin/env bash
# Approve the InstallPlan for the pinned AAP operator CSV.
#
# FIXME(OSAC-5621): remove this script and its Helm hook once the
# credential-type migration lands and the startingCSV / Manual pins
# are removed from the AAP Subscription.
set -euo pipefail

TARGET_CSV="aap-operator.v2.6.0-0.1787258256"
NAMESPACE="ansible-aap"

echo "Waiting for InstallPlan referencing ${TARGET_CSV} in ${NAMESPACE}..."

while true; do
  for plan in $(oc get installplan -n "${NAMESPACE}" --no-headers \
      -o custom-columns=NAME:.metadata.name 2>/dev/null || true); do
    csvs=$(oc get installplan "${plan}" -n "${NAMESPACE}" \
      -o jsonpath='{.spec.clusterServiceVersionNames[*]}' 2>/dev/null || true)
    if echo "${csvs}" | grep -qw "${TARGET_CSV}"; then
      approved=$(oc get installplan "${plan}" -n "${NAMESPACE}" \
        -o jsonpath='{.spec.approved}' 2>/dev/null || true)
      if [[ "${approved}" == "true" ]]; then
        echo "InstallPlan ${plan} for ${TARGET_CSV} is already approved."
        exit 0
      fi
      echo "Approving InstallPlan ${plan}..."
      oc patch installplan "${plan}" -n "${NAMESPACE}" \
        --type merge -p '{"spec":{"approved":true}}'
      echo "InstallPlan ${plan} approved for CSV ${TARGET_CSV}."
      exit 0
    fi
  done
  sleep 10
done
