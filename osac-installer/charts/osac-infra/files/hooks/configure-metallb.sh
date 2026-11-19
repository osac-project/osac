#!/usr/bin/env bash
set -euo pipefail

echo "Waiting for MetalLB CSV to appear..."
until oc get csv --no-headers -n metallb-system | grep -q metallb; do
  sleep 10
done
METALLB_CSV=$(oc get csv --no-headers -n metallb-system | awk '/metallb/ { print $1 }' | tail -1)

echo "Waiting for CSV ${METALLB_CSV} to succeed..."
until [[ "$(oc get csv "${METALLB_CSV}" -n metallb-system -o jsonpath='{.status.phase}')" == "Succeeded" ]]; do
  sleep 10
done

echo "Waiting for metallb-operator-controller-manager deployment..."
oc wait --for=condition=Available deploy/metallb-operator-controller-manager -n metallb-system --timeout=300s

echo "Waiting for metallb-operator-webhook-server deployment..."
oc wait --for=condition=Available deploy/metallb-operator-webhook-server -n metallb-system --timeout=300s

echo "Applying MetalLB configuration..."
oc apply -f /config/config.yaml

derive_pool="${METALLB_DERIVE_POOL_FROM_NODE_IP:-false}"
pool_start="${METALLB_POOL_RANGE_START:-240}"
pool_end="${METALLB_POOL_RANGE_END:-250}"

if [[ "${derive_pool}" == "true" ]]; then
  if (( pool_start >= pool_end )); then
    echo "metallb.poolRangeStart must be less than metallb.poolRangeEnd (got ${pool_start} and ${pool_end})" >&2
    exit 1
  fi

  echo "Computing subnet from node IP..."
  NODE_IP=$(oc get nodes -o 'jsonpath={.items[0].status.addresses[?(@.type=="InternalIP")].address}')
  SUBNET_PREFIX="${NODE_IP%.*}"
  POOL_RANGE="${SUBNET_PREFIX}.${pool_start}-${SUBNET_PREFIX}.${pool_end}"
  echo "Patching MetalLB address pool: ${POOL_RANGE}"
  oc patch ipaddresspool caas-address-pool -n metallb-system --type=merge \
    -p "{\"spec\":{\"addresses\":[\"${POOL_RANGE}\"]}}"
else
  echo "Leaving MetalLB address pool from chart values (derivePoolFromNodeIP=false)."
fi

echo "MetalLB configuration complete."
