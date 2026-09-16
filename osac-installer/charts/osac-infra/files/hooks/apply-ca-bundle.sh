#!/usr/bin/env bash
set -euo pipefail

# Apply the cluster ca-bundle only after trust-manager's webhook has
# endpoints. Helm's install order emits Bundle before Certificate, so a
# regular template hits the webhook while trust-manager-tls is missing.
#
# Bundle YAML is inlined here so the Job only mounts osac-infra-hook-scripts.
# A same-weight hook ConfigMap never appeared for the pod (Kind IT:
# FailedMount / ContainerCreating until helm's 30m).
#
# Do not use `oc wait`: origin-cli 4.20 against kind (k8s 1.35) can ignore
# --timeout. `timeout(1)` bounds each oc call if --request-timeout is ignored.

CERT_MANAGER_NAMESPACE="${CERT_MANAGER_NAMESPACE:-cert-manager}"
WAIT_DEFAULT_CA="${WAIT_DEFAULT_CA:-true}"
CA_BUNDLE_NAME="${CA_BUNDLE_NAME:-ca-bundle}"
RELEASE_NAMESPACE="${RELEASE_NAMESPACE:-}"
OSAC_NAMESPACE="${OSAC_NAMESPACE:-osac}"
USE_DEFAULT_CAS="${USE_DEFAULT_CAS:-false}"
OC=(oc --request-timeout=10s)

# Run oc with a hard 15s cap. origin-cli can ignore --request-timeout.
oc_run() {
  if command -v timeout >/dev/null 2>&1; then
    timeout -k 2s 15s "${OC[@]}" "$@"
  else
    "${OC[@]}" "$@"
  fi
}

# Poll Endpoints until it has at least one address. Args: name ns [attempts]
wait_for_endpoints() {
  local name="$1" ns="$2" attempts="${3:-24}"
  local _attempt _eps
  echo "Waiting for ${name} webhook endpoints..."
  for _attempt in $(seq 1 "${attempts}"); do
    _eps=$(oc_run get endpoints "${name}" -n "${ns}" \
      -o jsonpath='{.subsets[*].addresses[*].ip}' 2>/dev/null || true)
    if [[ -n "${_eps}" ]]; then
      echo "  endpoints ready"
      return 0
    fi
    sleep 5
  done
  echo "ERROR: timed out waiting for ${name} endpoints" >&2
  return 1
}

# Poll Deployment until availableReplicas >= 1. Args: name ns [attempts]
wait_for_deploy() {
  local name="$1" ns="$2" attempts="${3:-24}"
  local _attempt _avail
  echo "Waiting for deploy/${name}..."
  for _attempt in $(seq 1 "${attempts}"); do
    _avail=$(oc_run get deploy "${name}" -n "${ns}" \
      -o jsonpath='{.status.availableReplicas}' 2>/dev/null || true)
    if [[ "${_avail}" =~ ^[1-9][0-9]*$ ]]; then
      echo "  deploy/${name} available"
      return 0
    fi
    sleep 5
  done
  echo "ERROR: timed out waiting for deploy/${name}" >&2
  return 1
}

# Poll Certificate until Ready=True. Args: name ns [attempts]
wait_for_certificate() {
  local name="$1" ns="$2" attempts="${3:-36}"
  local _attempt _ready
  echo "Waiting for certificate/${name}..."
  for _attempt in $(seq 1 "${attempts}"); do
    _ready=$(oc_run get certificate "${name}" -n "${ns}" \
      -o jsonpath='{.status.conditions[?(@.type=="Ready")].status}' 2>/dev/null || true)
    if [[ "${_ready}" == "True" ]]; then
      echo "  certificate/${name} Ready"
      return 0
    fi
    sleep 5
  done
  echo "ERROR: timed out waiting for certificate/${name}" >&2
  return 1
}

echo "apply-ca-bundle: polling trust-manager in ${CERT_MANAGER_NAMESPACE}"
wait_for_deploy trust-manager "${CERT_MANAGER_NAMESPACE}" 24
wait_for_endpoints trust-manager "${CERT_MANAGER_NAMESPACE}" 24

if [[ "${WAIT_DEFAULT_CA}" == "true" ]]; then
  wait_for_certificate default-ca-cert "${CERT_MANAGER_NAMESPACE}" 36
fi

echo "Applying CA Bundle ${CA_BUNDLE_NAME}..."
{
  cat <<EOF
apiVersion: trust.cert-manager.io/v1alpha1
kind: Bundle
metadata:
  name: ${CA_BUNDLE_NAME}
spec:
  sources:
  - secret:
      name: "default-ca"
      key: "ca.crt"
EOF
  if [[ "${USE_DEFAULT_CAS}" == "true" ]]; then
    cat <<'EOF'
  - useDefaultCAs: true
EOF
  fi
  cat <<EOF
  target:
    configMap:
      key: bundle.pem
    namespaceSelector:
      matchExpressions:
      - key: kubernetes.io/metadata.name
        operator: In
        values:
        - "${RELEASE_NAMESPACE}"
        - "${OSAC_NAMESPACE}"
EOF
} | oc_run apply -f -

echo "CA Bundle applied."
