#!/bin/bash
# Wait for the CIO-managed GatewayClass to be accepted by OpenShift.
#
# Creating the GatewayClass makes OpenShift install istiod and the gateway
# data plane (embedded Sail). That takes a while; this hook blocks until the
# GatewayClass reports Accepted and its controller is installed, so the
# platform chart install that follows never races an unready gateway.
# Also fails fast if a Service Mesh subscription is present: the CIO
# gateway and Red Hat OpenShift Service Mesh mutually exclude each other,
# and the failure mode later is opaque.
set -euo pipefail

GATEWAYCLASS_NAME="${GATEWAYCLASS_NAME:?GATEWAYCLASS_NAME must be set}"
TIMEOUT_SECONDS="${TIMEOUT_SECONDS:-900}"

if oc get subscription -A -o jsonpath='{range .items[*]}{.metadata.name}{"\n"}{end}' 2>/dev/null \
  | grep -qx 'servicemesh'; then
  echo "ERROR: a 'servicemesh' Subscription exists on this cluster."
  echo "The CIO-managed Gateway API and OpenShift Service Mesh are mutually"
  echo "exclusive; remove Service Mesh or keep the envoy ingress driver."
  exit 1
fi

deadline=$(( SECONDS + TIMEOUT_SECONDS ))
while :; do
  conditions=$(kubectl get gatewayclass "${GATEWAYCLASS_NAME}" \
    -o jsonpath='{range .status.conditions[*]}{.type}={.status}{" "}{end}' 2>/dev/null || true)
  echo "GatewayClass ${GATEWAYCLASS_NAME}: ${conditions:-not found yet}"
  if [[ "${conditions}" == *"Accepted=True"* ]] \
    && [[ "${conditions}" == *"ControllerInstalled=True"* ]]; then
    echo "GatewayClass ${GATEWAYCLASS_NAME} is Accepted and its controller is installed."
    exit 0
  fi
  if (( SECONDS >= deadline )); then
    echo "ERROR: GatewayClass ${GATEWAYCLASS_NAME} not ready after ${TIMEOUT_SECONDS}s"
    echo "last conditions: ${conditions:-<absent>}"
    exit 1
  fi
  sleep 10
done
