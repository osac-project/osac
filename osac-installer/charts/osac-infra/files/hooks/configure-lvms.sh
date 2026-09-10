#!/usr/bin/env bash
set -euo pipefail

echo "Waiting for LVMS CSV to appear..."
until oc get csv --no-headers -n openshift-storage | grep -q lvms; do
  sleep 10
done
LVMS_CSV=$(oc get csv --no-headers -n openshift-storage | awk '/lvms/ { print $1 }' | tail -1)

echo "Waiting for CSV ${LVMS_CSV} to succeed..."
until [[ "$(oc get csv "${LVMS_CSV}" -n openshift-storage -o jsonpath='{.status.phase}')" == "Succeeded" ]]; do
  sleep 10
done

echo "Waiting for lvms-operator deployment..."
oc wait --for=condition=Available deploy/lvms-operator -n openshift-storage --timeout=900s

_sc_output=$(oc get sc lvms-vg1 --ignore-not-found -o name 2>&1) \
  || { echo "ERROR: failed to query StorageClasses: ${_sc_output}" >&2; exit 1; }
if [[ -n "${_sc_output}" ]]; then
  echo "lvms-vg1 already exists, skipping LVMCluster creation."
else
  echo "Applying LVMCluster configuration..."
  oc apply -f /config/config.yaml

  echo "Waiting for lvms-vg1 StorageClass..."
  for _attempt in $(seq 1 120); do
    _sc_query=$(oc get sc --ignore-not-found lvms-vg1 -o name 2>&1) \
      || { echo "ERROR: oc get StorageClass failed: ${_sc_query}" >&2; exit 1; }
    [[ -n "${_sc_query}" ]] && break
    (( _attempt < 120 )) || { echo "ERROR: timed out waiting for lvms-vg1 StorageClass" >&2; exit 1; }
    sleep 5
  done
fi

# Decide default-class annotation independently of LVMCluster creation.
# If another SC (e.g. Ceph on shared clusters) already holds the default
# annotation, leave it in place. Otherwise, ensure lvms-vg1 is default.
_all_defaults=$(oc get sc \
  -o jsonpath='{range .items[?(@.metadata.annotations.storageclass\.kubernetes\.io/is-default-class=="true")]}{.metadata.name}{"\n"}{end}') \
  || { echo "ERROR: failed to query default StorageClasses" >&2; exit 1; }
_other_default=$(echo "${_all_defaults}" | grep -v '^lvms-vg1$' | grep -v '^$' || true)

if [[ -n "${_other_default}" ]]; then
  echo "Another StorageClass is already default (${_other_default//$'\n'/ }), skipping lvms-vg1 default annotation."
else
  echo "Setting lvms-vg1 as default StorageClass..."
  oc annotate sc lvms-vg1 storageclass.kubernetes.io/is-default-class=true --overwrite
fi

echo "LVMS configuration complete."
