#!/usr/bin/env bash
set -euo pipefail

echo "Waiting for keycloak-service deployment..."
oc wait --for=condition=Available deploy/keycloak-service -n keycloak --timeout=600s

echo "Keycloak is ready."
