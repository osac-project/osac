#!/usr/bin/env bash
set -euo pipefail

echo "Waiting for CNV CSV to appear..."
until CNV_CSV=$(oc get csv --no-headers -n openshift-cnv 2>/dev/null | awk '/kubevirt-hyperconverged-operator/ { print $1 }' | tail -1) && [[ -n "${CNV_CSV}" ]]; do
  sleep 10
done

echo "Waiting for CSV ${CNV_CSV} to succeed..."
until [[ "$(oc get csv "${CNV_CSV}" -n openshift-cnv -o jsonpath='{.status.phase}' 2>/dev/null)" == "Succeeded" ]]; do
  sleep 10
done

echo "Checking for stale sub-CRs in Error phase..."
for cr in cdi kubevirt ssp; do
  set +e
  names=$(oc get "${cr}" -n openshift-cnv --no-headers -o name 2>/dev/null)
  rc=$?
  set -e
  if [[ ${rc} -ne 0 ]]; then
    echo "No ${cr} resources found, skipping..."
    continue
  fi
  for name in ${names}; do
    phase=$(oc get "${name}" -n openshift-cnv -o jsonpath='{.status.phase}' 2>/dev/null) || true
    if [[ "${phase}" == "Error" ]]; then
      echo "Deleting stale ${name} in Error phase..."
      if ! oc delete "${name}" -n openshift-cnv --timeout=30s 2>/dev/null; then
        echo "Delete failed, removing finalizers..."
        oc patch "${name}" -n openshift-cnv --type=merge -p '{"metadata":{"finalizers":null}}' || true
      fi
    fi
  done
done

echo "Applying HyperConverged CR..."
until oc apply -f /config/config.yaml; do
  echo "Retrying HyperConverged CR apply (webhooks may not be ready)..."
  sleep 5
done

echo "Waiting for HyperConverged to be Available (up to 15 min)..."
_hc_timeout=900
_hc_start=${SECONDS}
until [[ "$(oc get hyperconverged kubevirt-hyperconverged -n openshift-cnv -o jsonpath='{.status.conditions[?(@.type=="Available")].status}' 2>/dev/null)" == "True" ]]; do
  if (( SECONDS - _hc_start >= _hc_timeout )); then
    echo "ERROR: timed out waiting for HyperConverged to become Available after ${_hc_timeout}s" >&2
    exit 1
  fi
  sleep 10
done

echo "CNV configuration complete."
