#!/usr/bin/env bash
set -euo pipefail

echo "Waiting for MCE CSV to appear..."
until oc get csv --no-headers -n multicluster-engine | grep -q multicluster-engine; do
  sleep 10
done
MCE_CSV=$(oc get csv --no-headers -n multicluster-engine | awk '/multicluster-engine/ { print $1 }' | tail -1)

echo "Waiting for CSV ${MCE_CSV} to succeed..."
until [[ "$(oc get csv "${MCE_CSV}" -n multicluster-engine -o jsonpath='{.status.phase}')" == "Succeeded" ]]; do
  sleep 10
done

echo "Creating MultiClusterEngine singleton..."
set +e
existing=$(oc get multiclusterengine --no-headers)
rc=$?
set -e
if [[ ${rc} -ne 0 ]] || [[ -z "${existing}" ]]; then
  oc apply -f - <<'EOF'
apiVersion: multicluster.openshift.io/v1
kind: MultiClusterEngine
metadata:
  name: multiclusterengine
spec: {}
EOF
else
  echo "MultiClusterEngine already exists, skipping creation."
fi

echo "Waiting for MultiClusterEngine to be Available..."
until [[ "$(oc get multiclusterengine multiclusterengine -o jsonpath='{.status.phase}')" == "Available" ]]; do
  sleep 10
done

echo "Applying AgentServiceConfig..."
until oc apply -f /config/config.yaml; do
  echo "Retrying AgentServiceConfig apply (webhooks may not be ready)..."
  sleep 5
done

echo "Waiting for assisted-service deployment..."
oc wait --for=condition=Available deploy/assisted-service -n multicluster-engine --timeout=600s

echo "MCE configuration complete."
