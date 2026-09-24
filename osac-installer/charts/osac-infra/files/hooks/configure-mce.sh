#!/usr/bin/env bash
set -euo pipefail

MCE_NAMESPACE=multicluster-engine
MCE_NAME=multiclusterengine
IMAGE_OVERRIDES_CONFIGMAP=osac-infra-mce-image-overrides
IMAGE_OVERRIDES_ANNOTATION=installer.multicluster.openshift.io/image-overrides-configmap

echo "Waiting for MCE CSV to appear..."
until oc get csv --no-headers -n "${MCE_NAMESPACE}" | grep -q multicluster-engine; do
  sleep 10
done
MCE_CSV=$(oc get csv --no-headers -n "${MCE_NAMESPACE}" | awk '/multicluster-engine/ { print $1 }' | tail -1)

echo "Waiting for CSV ${MCE_CSV} to succeed..."
until [[ "$(oc get csv "${MCE_CSV}" -n "${MCE_NAMESPACE}" -o jsonpath='{.status.phase}')" == "Succeeded" ]]; do
  sleep 10
done

echo "Creating MultiClusterEngine singleton..."
set +e
existing=$(oc get multiclusterengine --no-headers -n "${MCE_NAMESPACE}")
rc=$?
set -e
if [[ ${rc} -ne 0 ]] || [[ -z "${existing}" ]]; then
  oc apply -n "${MCE_NAMESPACE}" -f - <<EOF
apiVersion: multicluster.openshift.io/v1
kind: MultiClusterEngine
metadata:
  name: ${MCE_NAME}
spec: {}
EOF
else
  echo "MultiClusterEngine already exists, skipping creation."
fi

echo "Waiting for MultiClusterEngine to be Available..."
until [[ "$(oc get multiclusterengine "${MCE_NAME}" -n "${MCE_NAMESPACE}" -o jsonpath='{.status.phase}')" == "Available" ]]; do
  sleep 10
done

if oc get configmap "${IMAGE_OVERRIDES_CONFIGMAP}" -n "${MCE_NAMESPACE}" >/dev/null 2>&1; then
  oc annotate mce "${MCE_NAME}" -n "${MCE_NAMESPACE}" \
    "${IMAGE_OVERRIDES_ANNOTATION}=${IMAGE_OVERRIDES_CONFIGMAP}" --overwrite
else
  oc annotate mce "${MCE_NAME}" -n "${MCE_NAMESPACE}" \
    "${IMAGE_OVERRIDES_ANNOTATION}-" --overwrite 2>/dev/null || true
fi

echo "Applying AgentServiceConfig..."
until oc apply -n "${MCE_NAMESPACE}" -f /config/config.yaml; do
  echo "Retrying AgentServiceConfig apply (webhooks may not be ready)..."
  sleep 5
done

echo "Waiting for assisted-service deployment..."
oc wait --for=condition=Available deploy/assisted-service \
  -n "${MCE_NAMESPACE}" --timeout=600s

echo "MCE configuration complete."
