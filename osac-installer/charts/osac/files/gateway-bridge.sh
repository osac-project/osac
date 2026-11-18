#!/bin/bash
# Resolve the CIO gateway Service ClusterIP and bridge the selectorless
# fulfillment-api / fulfillment-internal-api Services to it.
#
# In gatewayapi mode those Services have no selector, so nothing populates
# their endpoints. In-cluster callers (UI, operator, CLI pods) reach them at
# the well-known service DNS names, so the bridge wires each Service to the
# gateway Service ClusterIP via EndpointSlices. The ClusterIP is not known at
# render time and is stable across gateway pod replacement, which is why this
# runs as a post-install/post-upgrade hook instead of a chart resource.
set -euo pipefail

GATEWAY_NAMESPACE="${GATEWAY_NAMESPACE:?GATEWAY_NAMESPACE must be set}"
GATEWAY_SERVICE="${GATEWAY_SERVICE:?GATEWAY_SERVICE must be set}"
RELEASE_NAMESPACE="${RELEASE_NAMESPACE:?RELEASE_NAMESPACE must be set}"
TIMEOUT_SECONDS="${TIMEOUT_SECONDS:-900}"

deadline=$(( SECONDS + TIMEOUT_SECONDS ))
gateway_ip=""
while :; do
  gateway_ip=$(kubectl get service -n "${GATEWAY_NAMESPACE}" "${GATEWAY_SERVICE}" \
    -o jsonpath='{.spec.clusterIPs\[0\]}' 2>/dev/null || true)
  if [[ -n "${gateway_ip}" ]]; then
    echo "Gateway Service ${GATEWAY_NAMESPACE}/${GATEWAY_SERVICE} ClusterIP: ${gateway_ip}"
    break
  fi
  if (( SECONDS >= deadline )); then
    echo "ERROR: gateway Service ${GATEWAY_NAMESPACE}/${GATEWAY_SERVICE} has no ClusterIP after ${TIMEOUT_SECONDS}s"
    exit 1
  fi
  echo "  waiting for gateway Service ${GATEWAY_NAMESPACE}/${GATEWAY_SERVICE}..."
  sleep 10
done

bridge() {
  local service="$1" port_name="$2"
  kubectl apply -n "${RELEASE_NAMESPACE}" -f - <<EOF
apiVersion: discovery.k8s.io/v1
kind: EndpointSlice
metadata:
  name: ${service}-gateway
  labels:
    kubernetes.io/service-name: ${service}
addressType: IPv4
endpoints:
- addresses:
  - ${gateway_ip}
ports:
- name: ${port_name}
  port: 443
  protocol: TCP
EOF
}

bridge fulfillment-api external-api
bridge fulfillment-internal-api internal-api

echo "Bridge EndpointSlices applied in ${RELEASE_NAMESPACE}."
