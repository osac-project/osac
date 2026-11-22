#!/bin/bash
# Resolve the CIO gateway Pod IPs and bridge the selectorless
# fulfillment-api / fulfillment-internal-api Services to them.
#
# In gatewayapi mode those Services have no selector, so nothing populates
# their endpoints. In-cluster callers (UI, operator, CLI pods) reach them at
# the well-known service DNS names, so the bridge wires each Service to the
# gateway Pods via EndpointSlices.
#
# The bridge targets Pod IPs, not the gateway Service ClusterIP: the API
# server rejects an EndpointSlice address that is another Service's
# ClusterIP for non-privileged identities. The Pod IPs are read from the
# gateway Service's own EndpointSlices and are not known at render time,
# which is why this runs as a chart-resource Job created with the release
# (not a post-install hook: the callers crash-loop without the bridge, so a
# hook gated on the release becoming ready would deadlock the install under
# `helm --wait`). When gateway Pods are replaced the slices go stale until
# the next upgrade re-runs this Job; a controller would close that gap.
set -euo pipefail

GATEWAY_NAMESPACE="${GATEWAY_NAMESPACE:?GATEWAY_NAMESPACE must be set}"
GATEWAY_SERVICE="${GATEWAY_SERVICE:?GATEWAY_SERVICE must be set}"
RELEASE_NAMESPACE="${RELEASE_NAMESPACE:?RELEASE_NAMESPACE must be set}"
TIMEOUT_SECONDS="${TIMEOUT_SECONDS:-900}"

deadline=$(( SECONDS + TIMEOUT_SECONDS ))
gateway_ips=""
while :; do
  gateway_ips=$(kubectl get endpointslices -n "${GATEWAY_NAMESPACE}" \
    -l "kubernetes.io/service-name=${GATEWAY_SERVICE}" \
    -o jsonpath='{range .items[*].endpoints[?(@.conditions.ready)]}{.addresses[*]}{" "}{end}' 2>/dev/null || true)
  if [[ -n "${gateway_ips// /}" ]]; then
    echo "Gateway ${GATEWAY_NAMESPACE}/${GATEWAY_SERVICE} Pod IPs: ${gateway_ips}"
    break
  fi
  if (( SECONDS >= deadline )); then
    echo "ERROR: gateway Service ${GATEWAY_NAMESPACE}/${GATEWAY_SERVICE} has no ready Pod IPs after ${TIMEOUT_SECONDS}s"
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
  kubectl apply -n "${RELEASE_NAMESPACE}" -f - <<EOF
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

echo "Bridge EndpointSlices applied in ${RELEASE_NAMESPACE}."
