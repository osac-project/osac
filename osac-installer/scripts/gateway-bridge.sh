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
  # IPv4 only: the bridge slice is addressType IPv4, and a dual-stack gateway
  # Service has one slice per family, so an unfiltered query would mix them.
  # IPv6 gateways are not bridged (v1 constraint; the chart's static
  # gatewayAddress path handles both families).
  gateway_ips=$(oc get endpointslices -n "${GATEWAY_NAMESPACE}" \
    -l "kubernetes.io/service-name=${GATEWAY_SERVICE}" \
    -o jsonpath='{range .items[?(@.addressType=="IPv4")].endpoints[?(@.conditions.ready==true)]}{.addresses[*]}{" "}{end}' 2>/dev/null || true)
  # A terminating endpoint can still report ready=true; snapshotting it
  # would freeze a dead Pod IP into the bridge (nothing reconciles the
  # bridge afterwards). Wait until no endpoint is terminating.
  terminating=$(oc get endpointslices -n "${GATEWAY_NAMESPACE}" \
    -l "kubernetes.io/service-name=${GATEWAY_SERVICE}" \
    -o jsonpath='{.items[?(@.addressType=="IPv4")].endpoints[*].conditions.terminating}' 2>/dev/null || true)
  if [[ -n "${gateway_ips// /}" && "${terminating}" != *"true"* ]]; then
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

# Wait for the release to make the Services selectorless before touching
# their slices: this script starts alongside helm, and on a driver switch
# the gateway may already be ready while the Services still carry their
# Envoy selector - deleting slices then would let the EndpointSlice
# controller recreate them. A missing Service (clean install, helm not
# that far yet) also keeps waiting.
for service in fulfillment-api fulfillment-internal-api; do
  until selector=$(oc get service -n "${NS}" "${service}" \
      -o jsonpath='{.spec.selector}' 2>/dev/null) && [ -z "${selector}" ]; do
    if (( SECONDS >= deadline )); then
      echo "ERROR: Service ${NS}/${service} did not become selectorless after ${TIMEOUT_SECONDS}s" >&2
      exit 1
    fi
    echo "  waiting for ${NS}/${service} to become selectorless..."
    sleep 5
  done
done

# Remove the Envoy-era leftovers of the two Services at their source. The
# Services are selectorless now, so the endpoints controllers no longer
# reconcile them - but the legacy Endpoints object and the slices the
# mirroring controller keeps deriving from it survive a driver switch, and
# deleting slices alone is futile: the mirroring controller recreates them
# from the Endpoints object (observed live). Delete the Endpoints object
# first, then every slice that is not a bridge slice.
for service in fulfillment-api fulfillment-internal-api; do
  oc delete endpoints -n "${NS}" "${service}" --ignore-not-found
  oc get endpointslices -n "${NS}" -l "kubernetes.io/service-name=${service}" \
    -o jsonpath='{range .items[*]}{.metadata.name}{"\n"}{end}' 2>/dev/null \
    | { grep -v -- '-gateway$' || true; } \
    | while read -r stale; do
        [ -n "${stale}" ] || continue
        echo "removing stale EndpointSlice ${NS}/${stale} (service ${service})"
        oc delete endpointslice -n "${NS}" "${stale}"
      done
done

bridge fulfillment-api external-api
bridge fulfillment-internal-api internal-api

# Post-apply sweep: verify nothing foreign reappeared behind the bridge.
# With the Endpoints objects gone the mirroring controller has no source,
# so any survivor here means an unexpected reconciler - fail loudly
# instead of leaving consumers with split traffic.
sleep 10
for service in fulfillment-api fulfillment-internal-api; do
  foreign=$(oc get endpointslices -n "${NS}" -l "kubernetes.io/service-name=${service}" \
    -o jsonpath='{range .items[*]}{.metadata.name}{" "}{end}' 2>/dev/null \
    | tr ' ' '\n' | { grep -v -- '-gateway$' || true; } | grep -c . || true)
  if [ "${foreign:-0}" -gt 0 ]; then
    echo "ERROR: foreign EndpointSlices remain for ${service} after the bridge was applied" >&2
    exit 1
  fi
done

echo "Bridge EndpointSlices applied in ${NS}."
