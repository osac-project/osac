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

echo "Applying LVMCluster configuration..."
oc apply -f /config/config.yaml

echo "Waiting for lvms-vg1 StorageClass..."
until [[ -n "$(oc get sc --ignore-not-found lvms-vg1 -o name)" ]]; do
  sleep 5
done

echo "Setting lvms-vg1 as default StorageClass..."
oc annotate sc lvms-vg1 storageclass.kubernetes.io/is-default-class=true --overwrite

echo "LVMS configuration complete."
