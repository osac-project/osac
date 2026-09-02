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
# (dev-full is single-instance on Kind). NS is accepted for parity but must be osac.
NS="${1:-${NS:-osac}}"

echo "[+] Deploying OSAC UI (namespace 'osac')..."
kubectl apply -f "${SCRIPT_DIR}/manifests/osac-ui.yaml"
kubectl -n osac rollout status deployment osac-ui --timeout=120s
echo "[+] OSAC UI deployed — http://ui.osac.localhost:8080"
