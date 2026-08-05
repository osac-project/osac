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

echo "Computing subnet from node IP..."
NODE_IP=$(oc get nodes -o 'jsonpath={.items[0].status.addresses[?(@.type=="InternalIP")].address}')
SUBNET_PREFIX="${NODE_IP%.*}"
echo "Patching MetalLB address pool: ${SUBNET_PREFIX}.240-${SUBNET_PREFIX}.250"
oc patch ipaddresspool caas-address-pool -n metallb-system --type=merge \
  -p "{\"spec\":{\"addresses\":[\"${SUBNET_PREFIX}.240-${SUBNET_PREFIX}.250\"]}}"

echo "MetalLB configuration complete."
