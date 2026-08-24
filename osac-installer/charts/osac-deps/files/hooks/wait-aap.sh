#!/usr/bin/env bash
set -euo pipefail

echo "Waiting for AAP CSV to appear in ansible-aap namespace..."
until oc get csv --no-headers -n ansible-aap | grep -q aap; do
  sleep 10
done
AAP_CSV=$(oc get csv --no-headers -n ansible-aap | awk '/aap/ { print $1 }' | tail -1)

echo "Waiting for CSV ${AAP_CSV} to succeed..."
until [[ "$(oc get csv "${AAP_CSV}" -n ansible-aap -o jsonpath='{.status.phase}')" == "Succeeded" ]]; do
  sleep 10
done

echo "Waiting for AAP operator deployment..."
oc wait --for=condition=Available deploy/automation-controller-operator-controller-manager -n ansible-aap --timeout=300s

echo "AAP operator is ready."
