#!/usr/bin/env bash
# dev-full: deploy the OSAC UI on Kind.
#
# The osac chart's ui.enabled subchart exposes the UI via an OpenShift Route,
# which does not exist on Kind. Instead we deploy the UI directly and route to it
# through the shared Envoy Gateway HTTP listener (created by the osac-infra chart)
# with a Gateway API HTTPRoute — reachable at http://ui.osac.localhost:8080.
#
# Usage: deploy-ui.sh [namespace]

set -euo pipefail

SCRIPT_DIR="$(CDPATH= cd -- "$(dirname "${BASH_SOURCE[0]}")" >/dev/null && pwd)"
# The manifests are pinned to the 'osac' namespace and the ui.osac.localhost host
# (dev-full is single-instance on Kind). NS is accepted for parity with other
# dev-full scripts but must match osac-infra osacNamespace (default: osac), where
# the create-ui-backend-credentials hook provisions osac-ui-backend-credentials.
NS="${1:-${NS:-osac}}"
if [[ "${NS}" != osac ]]; then
  echo "ERROR: dev-full OSAC UI manifests are pinned to namespace 'osac'. Got '${NS}'" >&2
  exit 1
fi

echo "[+] Waiting for osac-ui-backend-credentials client-secret to be populated..."
for i in $(seq 1 60); do
  secret_val=$(kubectl -n "${NS}" get secret osac-ui-backend-credentials \
      -o jsonpath='{.data.client-secret}' 2>/dev/null | base64 -d 2>/dev/null || true)
  if [[ -n "${secret_val}" && "${secret_val}" != "pending" ]]; then
    echo "[+] osac-ui-backend-credentials ready"
    break
  fi
  [[ $i -lt 60 ]] || {
    echo "ERROR: timed out waiting for osac-ui-backend-credentials client-secret (300s)" >&2
    echo "       Check that the osac-infra-create-ui-backend-creds hook Job completed." >&2
    exit 1
  }
  echo "    still pending, retrying in 5s... (${i}/60)"
  sleep 5
done

echo "[+] Deploying OSAC UI (namespace '${NS}')..."
kubectl apply -f "${SCRIPT_DIR}/manifests/osac-ui.yaml"
kubectl -n "${NS}" rollout status deployment osac-ui --timeout=120s
echo "[+] OSAC UI deployed - http://ui.osac.localhost:8080"
