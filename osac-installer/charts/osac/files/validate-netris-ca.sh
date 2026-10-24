#!/bin/sh
set -eu

: "${CA_CONFIGMAP_NAME:?CA_CONFIGMAP_NAME is required}"
: "${AAP_NAMESPACE:?AAP_NAMESPACE is required}"
: "${CA_EXECUTION_IDENTITY:?CA_EXECUTION_IDENTITY is required}"

KUBECTL=${KUBECTL:-kubectl}
CHECK_IDENTITY=${CHECK_IDENTITY:-true}

fail() {
  echo "ERROR: NETRIS_CA_$1: $2" >&2
  exit 1
}

validate_configmap_object() {
  configmap_json=$("$KUBECTL" get configmap "$CA_CONFIGMAP_NAME" -n "$AAP_NAMESPACE" -o json 2>/dev/null) || \
    fail CONFIGMAP_MISSING "ConfigMap ${AAP_NAMESPACE}/${CA_CONFIGMAP_NAME} is missing or cannot be read"

  immutable=$("$KUBECTL" get configmap "$CA_CONFIGMAP_NAME" -n "$AAP_NAMESPACE" -o jsonpath='{.immutable}' 2>/dev/null || true)
  [ "$immutable" = "true" ] || \
    fail CONFIGMAP_MUTABLE "ConfigMap ${AAP_NAMESPACE}/${CA_CONFIGMAP_NAME} must set immutable: true"

  printf '%s' "$configmap_json" | grep -q '"bundle\.pem"' || \
    fail BUNDLE_KEY_MISSING "ConfigMap ${AAP_NAMESPACE}/${CA_CONFIGMAP_NAME} has no data.bundle.pem key"
}

validate_bundle_pem() {
  bundle=$("$KUBECTL" get configmap "$CA_CONFIGMAP_NAME" -n "$AAP_NAMESPACE" -o jsonpath='{.data.bundle\.pem}' 2>/dev/null || true)
  [ -n "$bundle" ] || \
    fail BUNDLE_EMPTY "ConfigMap ${AAP_NAMESPACE}/${CA_CONFIGMAP_NAME} has an empty data.bundle.pem value"

  command -v openssl >/dev/null 2>&1 || \
    fail OPENSSL_MISSING "openssl is required to validate the CA bundle and was not found in cliImage"

  bundle_file=$(mktemp)
  trap 'rm -f "$bundle_file"' EXIT
  printf '%s\n' "$bundle" >"$bundle_file"

  parsed=$(openssl crl2pkcs7 -nocrl -certfile "$bundle_file" 2>/dev/null | openssl pkcs7 -print_certs -noout 2>/dev/null) || \
    fail BUNDLE_INVALID "ConfigMap ${AAP_NAMESPACE}/${CA_CONFIGMAP_NAME} does not contain a parseable PEM certificate bundle"
  printf '%s' "$parsed" | grep -q 'subject=' || \
    fail BUNDLE_INVALID "ConfigMap ${AAP_NAMESPACE}/${CA_CONFIGMAP_NAME} does not contain a parseable PEM certificate bundle"
}

check_execution_identity() {
  result=$("$KUBECTL" auth can-i get "configmaps/${CA_CONFIGMAP_NAME}" -n "$AAP_NAMESPACE" --as="$CA_EXECUTION_IDENTITY" 2>/dev/null || true)
  [ "$result" = "yes" ] || \
    fail IDENTITY_DENIED "${CA_EXECUTION_IDENTITY} cannot read ConfigMap ${AAP_NAMESPACE}/${CA_CONFIGMAP_NAME}"
}

validate_configmap_object
validate_bundle_pem
if [ "$CHECK_IDENTITY" = "true" ]; then
  check_execution_identity
fi

echo "  Netris CA ConfigMap validated: ${AAP_NAMESPACE}/${CA_CONFIGMAP_NAME}"
