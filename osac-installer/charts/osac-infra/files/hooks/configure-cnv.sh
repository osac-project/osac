#!/usr/bin/env bash
set -euo pipefail

echo "Waiting for CNV CSV to appear..."
until oc get csv --no-headers -n openshift-cnv | grep -q kubevirt-hyperconverged-operator; do
  sleep 10
done
CNV_CSV=$(oc get csv --no-headers -n openshift-cnv | awk '/kubevirt-hyperconverged-operator/ { print $1 }' | tail -1)

echo "Waiting for CSV ${CNV_CSV} to succeed..."
until [[ "$(oc get csv "${CNV_CSV}" -n openshift-cnv -o jsonpath='{.status.phase}')" == "Succeeded" ]]; do
  sleep 10
done

echo "Checking for stale sub-CRs in Error phase..."
for cr in cdi kubevirt ssp; do
  set +e
  names=$(oc get "${cr}" -n openshift-cnv --no-headers -o name)
  rc=$?
  set -e
  if [[ ${rc} -ne 0 ]]; then
    echo "No ${cr} resources found, skipping..."
    continue
  fi
  for name in ${names}; do
    phase=$(oc get "${name}" -n openshift-cnv -o jsonpath='{.status.phase}')
    if [[ "${phase}" == "Error" ]]; then
      echo "Deleting stale ${name} in Error phase..."
      if ! oc delete "${name}" -n openshift-cnv --timeout=30s; then
        echo "Delete failed, removing finalizers..."
        oc patch "${name}" -n openshift-cnv --type=merge -p '{"metadata":{"finalizers":null}}'
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
until [[ "$(oc get hyperconverged kubevirt-hyperconverged -n openshift-cnv -o jsonpath='{.status.conditions[?(@.type=="Available")].status}')" == "True" ]]; do
  sleep 10
done

echo "CNV configuration complete."
