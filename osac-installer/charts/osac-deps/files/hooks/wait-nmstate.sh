#!/usr/bin/env bash
set -euo pipefail

NAMESPACE=openshift-nmstate
SUBSCRIPTION=kubernetes-nmstate-operator

echo "Waiting for NMState Subscription to resolve its installed CSV..."
until INSTALLED_CSV=$(oc get subscription "${SUBSCRIPTION}" -n "${NAMESPACE}" \
  -o jsonpath='{.status.installedCSV}' 2>/dev/null) && [[ -n "${INSTALLED_CSV}" ]]; do
  sleep 10
done

echo "Waiting for NMState CSV ${INSTALLED_CSV} to succeed..."
while true; do
  PHASE=$(oc get csv "${INSTALLED_CSV}" -n "${NAMESPACE}" -o jsonpath='{.status.phase}')
  case "${PHASE}" in
    Succeeded) break ;;
    Failed)
      echo "NMState CSV ${INSTALLED_CSV} failed to install." >&2
      exit 1
      ;;
    *) sleep 10 ;;
  esac
done

DEPLOYMENTS=$(oc get csv "${INSTALLED_CSV}" -n "${NAMESPACE}" \
  -o jsonpath='{range .spec.install.spec.deployments[*]}{.name}{"\n"}{end}')
if [[ -z "${DEPLOYMENTS}" ]]; then
  echo "NMState CSV ${INSTALLED_CSV} declares no operator deployments." >&2
  exit 1
fi

while IFS= read -r deployment; do
  [[ -z "${deployment}" ]] && continue
  echo "Waiting for NMState deployment ${deployment}..."
  oc wait --for=condition=Available "deployment/${deployment}" -n "${NAMESPACE}" --timeout=300s
done <<< "${DEPLOYMENTS}"

echo "NMState operator is ready."
