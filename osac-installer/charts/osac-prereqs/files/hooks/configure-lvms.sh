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
  # lvms-vg1 pre-exists (e.g. MOC, where it was installed by cluster admins).
  # Skip both LVMCluster creation AND the default-class annotation: on shared clusters
  # another StorageClass (e.g. Ceph) is already the intended default, and annotating
  # lvms-vg1 here would silently override that.
  echo "lvms-vg1 already exists, skipping LVMCluster creation and annotation."
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

  echo "Setting lvms-vg1 as default StorageClass..."
  oc annotate sc lvms-vg1 storageclass.kubernetes.io/is-default-class=true --overwrite
fi

echo "LVMS configuration complete."
