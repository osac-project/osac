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
KEYCLOAK_NAMESPACE="${KEYCLOAK_NAMESPACE:-}"
BUNDLE_SYNC_TOKEN="${BUNDLE_SYNC_TOKEN:-}"
USE_DEFAULT_CAS="${USE_DEFAULT_CAS:-false}"
CA_BUNDLE_SYNC_TIMEOUT_SECONDS="${CA_BUNDLE_SYNC_TIMEOUT_SECONDS:-300}"
KEYCLOAK_ROLLOUT_TIMEOUT_SECONDS="${KEYCLOAK_ROLLOUT_TIMEOUT_SECONDS:-300}"
BUNDLE_SYNC_ANNOTATION="osac.openshift.io/ca-bundle-sync-token"
KEYCLOAK_CHECKSUM_ANNOTATION="checksum/keycloak-oidc-idp-ca-bundle"
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

# Wait for trust-manager to publish this hook's Bundle generation. The sync
# token is copied atomically with bundle.pem into each target ConfigMap, so an
# existing ConfigMap containing the previous bundle cannot satisfy the wait.
wait_for_keycloak_bundle() {
  local snapshot="$1" deadline=$((SECONDS + CA_BUNDLE_SYNC_TIMEOUT_SECONDS))
  local observed_token

  echo "Waiting for ConfigMap/${CA_BUNDLE_NAME} in ${KEYCLOAK_NAMESPACE} to sync bundle ${BUNDLE_SYNC_TOKEN}..."
  while (( SECONDS < deadline )); do
    if oc_run get configmap "${CA_BUNDLE_NAME}" -n "${KEYCLOAK_NAMESPACE}" \
      -o 'go-template={{ index .metadata.annotations "osac.openshift.io/ca-bundle-sync-token" }}{{ "\n" }}{{ index .data "bundle.pem" }}' \
      >"${snapshot}" 2>/dev/null; then
      IFS= read -r observed_token <"${snapshot}" || true
      if [[ "${observed_token}" == "${BUNDLE_SYNC_TOKEN}" ]]; then
        echo "  ConfigMap/${CA_BUNDLE_NAME} contains the current bundle"
        return 0
      fi
    fi
    if (( SECONDS < deadline )); then
      sleep 5
    fi
  done

  echo "ERROR: timed out after ${CA_BUNDLE_SYNC_TIMEOUT_SECONDS}s waiting for ConfigMap/${CA_BUNDLE_NAME} to sync bundle ${BUNDLE_SYNC_TOKEN}" >&2
  return 1
}

update_keycloak_truststore_checksum() {
  local bundle_file="$1" checksum current_checksum patch
  checksum=$(sha256sum "${bundle_file}" | awk '{print $1}')

  if ! current_checksum=$(oc_run get deployment keycloak-service -n "${KEYCLOAK_NAMESPACE}" \
    -o 'jsonpath={.spec.template.metadata.annotations.checksum\/keycloak-oidc-idp-ca-bundle}' 2>&1); then
    echo "ERROR: failed to read deployment/keycloak-service checksum: ${current_checksum}" >&2
    return 1
  fi
  if [[ "${current_checksum}" == "${checksum}" ]]; then
    echo "Keycloak truststore checksum is unchanged; no rollout needed"
    return 0
  fi

  patch="{\"spec\":{\"template\":{\"metadata\":{\"annotations\":{\"${KEYCLOAK_CHECKSUM_ANNOTATION}\":\"${checksum}\"}}}}}"
  echo "Updating Keycloak truststore checksum to ${checksum}..."
  if ! oc_run patch deployment keycloak-service -n "${KEYCLOAK_NAMESPACE}" \
    --type=merge -p "${patch}"; then
    echo "ERROR: failed to patch deployment/keycloak-service truststore checksum" >&2
    return 1
  fi

  wait_for_keycloak_rollout
}

wait_for_keycloak_rollout() {
  local deadline=$((SECONDS + KEYCLOAK_ROLLOUT_TIMEOUT_SECONDS))
  local status generation observed desired updated replicas available unavailable

  echo "Waiting up to ${KEYCLOAK_ROLLOUT_TIMEOUT_SECONDS}s for deployment/keycloak-service rollout..."
  while (( SECONDS < deadline )); do
    if status=$(oc_run get deployment keycloak-service -n "${KEYCLOAK_NAMESPACE}" \
      -o 'jsonpath={.metadata.generation}{"|"}{.status.observedGeneration}{"|"}{.spec.replicas}{"|"}{.status.updatedReplicas}{"|"}{.status.replicas}{"|"}{.status.availableReplicas}{"|"}{.status.unavailableReplicas}' \
      2>/dev/null); then
      IFS='|' read -r generation observed desired updated replicas available unavailable <<<"${status}"
      desired=${desired:-1}
      updated=${updated:-0}
      replicas=${replicas:-0}
      available=${available:-0}
      unavailable=${unavailable:-0}
      if [[ "${generation}" =~ ^[0-9]+$ && "${observed}" == "${generation}" \
        && "${updated}" == "${desired}" && "${replicas}" == "${desired}" \
        && "${available}" == "${desired}" && "${unavailable}" == "0" ]]; then
        echo "  deployment/keycloak-service rollout complete"
        return 0
      fi
    fi
    if (( SECONDS < deadline )); then
      sleep 5
    fi
  done

  echo "ERROR: timed out after ${KEYCLOAK_ROLLOUT_TIMEOUT_SECONDS}s waiting for deployment/keycloak-service rollout" >&2
  return 1
}

echo "apply-ca-bundle: polling trust-manager in ${CERT_MANAGER_NAMESPACE}"
wait_for_deploy trust-manager "${CERT_MANAGER_NAMESPACE}" 24
wait_for_endpoints trust-manager "${CERT_MANAGER_NAMESPACE}" 24

if [[ "${WAIT_DEFAULT_CA}" == "true" ]]; then
  wait_for_certificate default-ca-cert "${CERT_MANAGER_NAMESPACE}" 36
fi

# When publicIngress is enabled, the OpenShift ingress router presents the
# cluster's default ingress certificate. In CI this certificate is self-signed
# and not in the system CA bundle, so components connecting to Keycloak via
# the external route get "x509: certificate signed by unknown authority".
# Extract the ingress CA from the router-ca secret and add it as a Bundle
# source so trust-manager includes it alongside the system CAs.
INGRESS_CA_SOURCE=""
if [[ "${USE_DEFAULT_CAS}" == "true" ]]; then
  echo "Extracting OpenShift ingress CA from openshift-ingress-operator/router-ca..."
  if ! INGRESS_CA_DATA=$(oc_run get secret router-ca \
    -n openshift-ingress-operator \
    -o jsonpath='{.data.tls\.crt}' 2>&1); then
    if echo "${INGRESS_CA_DATA}" | grep -qiE "not found|forbidden"; then
      echo "  router-ca secret not accessible — skipping ingress CA (non-OpenShift cluster or missing RBAC)"
      INGRESS_CA_DATA=""
    else
      echo "ERROR: failed to read router-ca secret: ${INGRESS_CA_DATA}" >&2
      exit 1
    fi
  fi
  if [[ -n "${INGRESS_CA_DATA}" ]]; then
    echo "  creating ingress-ca secret in ${CERT_MANAGER_NAMESPACE}"
    cat <<EOF | oc_run apply -f -
apiVersion: v1
kind: Secret
metadata:
  name: ingress-ca
  namespace: ${CERT_MANAGER_NAMESPACE}
type: Opaque
data:
  ca.crt: ${INGRESS_CA_DATA}
EOF
    INGRESS_CA_SOURCE="yes"
    echo "  ingress-ca secret created"
  else
    echo "  router-ca data is empty — skipping ingress CA"
  fi
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
  # Trust-manager watches matching Secrets in cert-manager, so operators can
  # add or remove extra roots without changing this Bundle or upgrading Helm.
  - secret:
      selector:
        matchLabels:
          osac.openshift.io/ca-bundle: additional
      key: "ca.crt"
EOF
  if [[ "${USE_DEFAULT_CAS}" == "true" ]]; then
    cat <<'EOF'
  - useDefaultCAs: true
EOF
  fi
  if [[ -n "${INGRESS_CA_SOURCE}" ]]; then
    cat <<'EOF'
  - secret:
      name: "ingress-ca"
      key: "ca.crt"
EOF
  fi
  cat <<EOF
  target:
    configMap:
      key: bundle.pem
EOF
  if [[ -n "${KEYCLOAK_NAMESPACE}" ]]; then
    if [[ -z "${BUNDLE_SYNC_TOKEN}" ]]; then
      echo "ERROR: BUNDLE_SYNC_TOKEN is required when Keycloak is enabled" >&2
      exit 1
    fi
    cat <<EOF
      metadata:
        annotations:
          ${BUNDLE_SYNC_ANNOTATION}: "${BUNDLE_SYNC_TOKEN}"
EOF
  fi
  cat <<EOF
    namespaceSelector:
      matchExpressions:
      - key: kubernetes.io/metadata.name
        operator: In
        values:
        - "${RELEASE_NAMESPACE}"
        - "${OSAC_NAMESPACE}"
EOF
  if [[ -n "${KEYCLOAK_NAMESPACE}" ]]; then
    cat <<EOF
        - "${KEYCLOAK_NAMESPACE}"
EOF
  fi
} | oc_run apply -f -

echo "CA Bundle applied."

if [[ -n "${KEYCLOAK_NAMESPACE}" ]]; then
  bundle_snapshot=$(mktemp "${TMPDIR:-/tmp}/ca-bundle-final.XXXXXX")
  bundle_file=$(mktemp "${TMPDIR:-/tmp}/ca-bundle-data.XXXXXX")
  trap 'rm -f "${bundle_snapshot}" "${bundle_file}"' EXIT
  wait_for_keycloak_bundle "${bundle_snapshot}"
  # Extract exact data bytes from the same ConfigMap read that carried the
  # sync token, preserving the bundle's trailing newline for SHA256.
  tail -n +2 "${bundle_snapshot}" >"${bundle_file}"
  if [[ ! -s "${bundle_file}" ]]; then
    echo "ERROR: ConfigMap/${CA_BUNDLE_NAME} has empty data.bundle.pem after sync" >&2
    exit 1
  fi
  update_keycloak_truststore_checksum "${bundle_file}"
fi
