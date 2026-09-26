#!/usr/bin/env bash
set -euo pipefail

# Reconciliation loop: watch for changes to default-ca Secret and update Route destinationCACertificate
LAST_CA_CERT=""

# Idempotency guard: skip if the route already has the correct CA cert.
# Uses || true so a re-run with broken RBAC does not fail when the cert
# is already correct — it simply falls through to the reconciliation loop.
CURRENT_DEST_CA=$(oc get route keycloak -n keycloak -o jsonpath='{.spec.tls.destinationCACertificate}' 2>/dev/null || true)
EXPECTED_CA=$(oc get secret default-ca -n cert-manager -o jsonpath='{.data.ca\.crt}' 2>/dev/null | base64 -d 2>/dev/null || true)
if [[ -n "$CURRENT_DEST_CA" && -n "$EXPECTED_CA" && "$CURRENT_DEST_CA" == "$EXPECTED_CA" ]]; then
  echo "Route already has correct CA cert, nothing to do."
  exit 0
fi

reconcile() {
  # Check if Secret exists
  if ! err=$(oc get secret default-ca -n cert-manager 2>&1 >/dev/null); then
    echo "default-ca secret not found (${err}), waiting..."
    return 1
  fi

  # Read current CA cert from Secret
  CA_CERT=$(oc get secret default-ca -n cert-manager -o jsonpath='{.data.ca\.crt}' | base64 -d)

  # Skip if cert hasn't changed
  if [[ "$CA_CERT" == "$LAST_CA_CERT" ]]; then
    return 0
  fi

  echo "CA certificate changed, patching keycloak Route..."

  # Check if Route exists
  if ! err=$(oc get route keycloak -n keycloak 2>&1 >/dev/null); then
    echo "keycloak Route not found (${err}), waiting..."
    return 1
  fi

  # Read current Route TLS termination mode
  TERMINATION=$(oc get route keycloak -n keycloak -o jsonpath='{.spec.tls.termination}')

  # Only patch if termination is reencrypt (publicIngress mode)
  if [[ "$TERMINATION" != "reencrypt" ]]; then
    echo "Route termination is $TERMINATION (not reencrypt), skipping patch"
    return 0
  fi

  # Patch Route with new CA cert (use python3 for JSON escaping, available in all hook images)
  CA_CERT_JSON=$(python3 -c "import json,sys; print(json.dumps(sys.stdin.read()))" <<< "$CA_CERT")
  if patch_err=$(oc patch route keycloak -n keycloak --type=json -p "[{
    \"op\": \"replace\",
    \"path\": \"/spec/tls/destinationCACertificate\",
    \"value\": $CA_CERT_JSON
  }]" 2>&1); then
    echo "Successfully patched keycloak Route with updated destinationCACertificate"
  else
    # If replace fails, try add (first time)
    echo "Replace failed (${patch_err}), trying add..."
    if patch_err=$(oc patch route keycloak -n keycloak --type=json -p "[{
      \"op\": \"add\",
      \"path\": \"/spec/tls/destinationCACertificate\",
      \"value\": $CA_CERT_JSON
    }]" 2>&1); then
      echo "Added destinationCACertificate to keycloak Route"
    else
      echo "ERROR: Failed to patch keycloak Route (${patch_err})"
      return 1
    fi
  fi

  # Post-patch verification: read back the route and confirm the cert matches
  ACTUAL_CA=$(oc get route keycloak -n keycloak -o jsonpath='{.spec.tls.destinationCACertificate}')
  if [[ "$ACTUAL_CA" == "$CA_CERT" ]]; then
    echo "Verified: Route destinationCACertificate matches expected CA cert"
    LAST_CA_CERT="$CA_CERT"
  else
    echo "ERROR: Post-patch verification failed — destinationCACertificate does not match expected CA cert"
    return 1
  fi
}

# Initial reconciliation with retries
echo "Starting Route patch reconciliation..."
for i in $(seq 1 60); do
  if reconcile; then
    echo "Route patch reconciliation complete"
    exit 0
  fi
  [[ $i -eq 60 ]] && { echo "ERROR: Route patch reconciliation failed after 300s"; exit 1; }
  sleep 5
done
