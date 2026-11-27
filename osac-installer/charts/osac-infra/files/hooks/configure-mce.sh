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

# Own only the auto-import entry in the name-keyed variables list, preserving
# MCE's other settings.
# Retry like the AgentServiceConfig apply below: admission webhooks may not be
# ready yet when the MultiClusterEngine phase first reports Available.
policy_attempts=5
until oc apply --server-side --force-conflicts --field-manager=osac-acm-policy \
  -n "${MCE_NAMESPACE}" -f /config/acm-import-policy.yaml; do
  policy_attempts=$((policy_attempts - 1))
  if [[ ${policy_attempts} -le 0 ]]; then
    echo "ACM import policy apply failed after retries" >&2
    exit 1
  fi
  echo "Retrying ACM import policy apply (webhooks may not be ready)..."
  sleep 5
done

echo "Applying AgentServiceConfig..."
until oc apply -n "${MCE_NAMESPACE}" -f /config/config.yaml; do
  echo "Retrying AgentServiceConfig apply (webhooks may not be ready)..."
  sleep 5
done

echo "Waiting for assisted-service deployment..."
oc wait --for=condition=Available deploy/assisted-service \
  -n "${MCE_NAMESPACE}" --timeout=600s

# Check the actual rollout, not just the desired policy. No AAP job needs to
# read this MCE API or list agent Deployments when provisioning a cluster.
agent_namespace=$(oc get addondeploymentconfig hypershift-addon-deploy-config \
  -n "${MCE_NAMESPACE}" --request-timeout=10s -o 'jsonpath={.spec.agentInstallNamespace}')
agent_namespace=${agent_namespace:-open-cluster-management-agent-addon}
agent_deployment=hypershift-addon-agent
# Return only the setting and rollout counters from a single Deployment read.
agent_projection='{range .spec.template.spec.containers[?(@.name=="hypershift-addon-agent")]}{.env[?(@.name=="DISABLE_AUTO_IMPORT")].value}{end}'
agent_projection+='{"|"}{.metadata.generation}{"|"}{.status.observedGeneration}{"|"}{.spec.replicas}'
agent_projection+='{"|"}{.status.replicas}{"|"}{.status.updatedReplicas}{"|"}{.status.availableReplicas}'

printf 'Waiting for %s/%s to roll out with DISABLE_AUTO_IMPORT=true...\n' "${agent_namespace}" "${agent_deployment}"
for ((attempt = 1; attempt <= 60; attempt++)); do
  agent_state=$(oc get deployment "${agent_deployment}" -n "${agent_namespace}" \
    --ignore-not-found --request-timeout=10s -o "jsonpath=${agent_projection}")
  IFS='|' read -r disabled generation observed desired current updated available <<< "${agent_state}"
  if [[ "${agent_state}" != *$'\n'* && "${disabled}" == true && -n "${generation}" && "${observed}" == "${generation}" &&
    "${desired}" =~ ^[1-9][0-9]*$ && "${current}" == "${desired}" && "${updated}" == "${desired}" &&
    "${available}" == "${desired}" ]]; then
    echo "MCE configuration complete."
    exit 0
  fi
  if ((attempt < 60)); then
    sleep 5
  fi
done
printf 'HyperShift agent %s/%s did not roll out with DISABLE_AUTO_IMPORT=true\n' \
  "${agent_namespace}" "${agent_deployment}" >&2
exit 1
