#!/bin/bash
# Maintain the in-cluster bridge for the gatewayapi ingress driver (OSAC-7866).
#
# In gatewayapi mode the fulfillment-api / fulfillment-internal-api Services
# are selectorless and in-cluster callers (UI, operator, CLI pods) reach them
# via the well-known service DNS names, so each Service must be wired to the
# gateway Pods through EndpointSlices.
#
# This script runs OUTSIDE the cluster, with the installer's admin
# credentials. OpenShift's RestrictedEndpointsAdmission plugin rejects an
# EndpointSlice address inside the pod or service network (172.30.0.0/16,
# 10.128.0.0/14 by default) for non-privileged identities, on both create and
# update, so an in-cluster Job with a ServiceAccount cannot maintain the
# bridge. The address is therefore resolved from the gateway Service's own
# EndpointSlices (the ready gateway Pod IPs) and applied as the installing
# admin. The chart's static ingress.gatewayapi.gatewayAddress path remains
# for controller-based setups.
#
# Usage: gateway-bridge.sh <namespace> [gateway-namespace] [gateway-service]
#                            [timeout-seconds]
#
# The script waits for the gateway Pods to appear, so it can be started
# before or while `helm install --wait` runs; the callers crash-loop until
# the bridge exists, which would otherwise deadlock the wait.
set -euo pipefail

NS="${1:?usage: gateway-bridge.sh <namespace> [gateway-namespace] [gateway-service] [timeout-seconds]}"
GATEWAY_NAMESPACE="${2:-openshift-ingress}"
GATEWAY_SERVICE="${3:-osac-gateway-openshift-default}"
TIMEOUT_SECONDS="${4:-1800}"

deadline=$(( SECONDS + TIMEOUT_SECONDS ))
gateway_ips=""
while :; do
  gateway_ips=$(oc get endpointslices -n "${GATEWAY_NAMESPACE}" \
    -l "kubernetes.io/service-name=${GATEWAY_SERVICE}" \
    -o jsonpath='{range .items[*].endpoints[?(@.conditions.ready)]}{.addresses[*]}{" "}{end}' 2>/dev/null || true)
  if [[ -n "${gateway_ips// /}" ]]; then
    echo "Gateway ${GATEWAY_NAMESPACE}/${GATEWAY_SERVICE} Pod IPs: ${gateway_ips}"
    break
  fi
  if (( SECONDS >= deadline )); then
    echo "ERROR: no ready gateway Pod IPs for ${GATEWAY_NAMESPACE}/${GATEWAY_SERVICE} after ${TIMEOUT_SECONDS}s" >&2
    exit 1
  fi
  echo "  waiting for ready gateway Pod IPs (${GATEWAY_NAMESPACE}/${GATEWAY_SERVICE})..."
  sleep 10
done

bridge() {
  local service="$1" port_name="$2" eps=""
  local ip
  for ip in ${gateway_ips}; do
    eps+="- addresses:"$'\n'"  - ${ip}"$'\n'
  done
  oc apply -n "${NS}" -f - <<EOF
apiVersion: discovery.k8s.io/v1
kind: EndpointSlice
metadata:
  name: ${service}-gateway
  labels:
    kubernetes.io/service-name: ${service}
addressType: IPv4
endpoints:
${eps}ports:
- name: ${port_name}
  port: 443
  protocol: TCP
EOF
}

bridge fulfillment-api external-api
bridge fulfillment-internal-api internal-api

echo "Bridge EndpointSlices applied in ${NS}."
