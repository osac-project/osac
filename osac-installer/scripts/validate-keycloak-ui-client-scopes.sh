#!/usr/bin/env bash
# Regression test for OSAC-3887: the Keycloak osac-ui client must be assigned
# every OIDC scope the OSAC UI requests. Keycloak's OAuth2 scope validation
# rejects an authorization request naming any scope the client does not have,
# so a missing scope sends the browser back to /callback with
# error=invalid_scope and the UI never establishes a session.
#
# The osac-ui client sets its own defaultClientScopes, which fully overrides
# the realm-level defaultDefaultClientScopes -- so profile/email being realm
# defaults does not help, they have to be listed on the client.
#
# No CI job drives the osac-ui browser authorization-code flow (CI leaves
# Keycloak in-cluster and the e2e suites authenticate through the osac-cli
# localhost grant), so this file-level check is the guard against the client's
# scopes drifting away from the UI build again. It needs no cluster.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CHART_DIR="${SCRIPT_DIR}/../charts/osac-infra"
PREREQ_DIR="${SCRIPT_DIR}/../prerequisites/keycloak/service"
FAILURES=0

fail() {
    echo "FAIL: $1" >&2
    FAILURES=$((FAILURES + 1))
}

# Scopes the shipped osac-ui image asks for, minus "openid" (which is the OIDC
# marker, not a client scope). "organization" is only required when the realm
# has organizations enabled. Keep this list in step with the UI's OIDC config.
UI_REQUIRED_SCOPES="profile email"
UI_REQUIRED_SCOPES_WHEN_ORGS="organization"

assert_realm_scopes() {
    local realm_file="$1" description="$2" errors
    errors=$(REALM_FILE="${realm_file}" \
        REQUIRED="${UI_REQUIRED_SCOPES}" \
        REQUIRED_WHEN_ORGS="${UI_REQUIRED_SCOPES_WHEN_ORGS}" \
        python3 -c "
import json, os, sys

realm = json.load(open(os.environ['REALM_FILE']))
client = next((c for c in realm['clients'] if c['clientId'] == 'osac-ui'), None)
if client is None:
    sys.exit('realm.json has no osac-ui client')

default_scopes = client.get('defaultClientScopes') or []
optional_scopes = client.get('optionalClientScopes') or []
assigned = set(default_scopes) | set(optional_scopes)
defined = {s['name'] for s in realm.get('clientScopes', [])}

required = set(os.environ['REQUIRED'].split())
if realm.get('organizationsEnabled'):
    required |= set(os.environ['REQUIRED_WHEN_ORGS'].split())

errors = []
missing = sorted(required - assigned)
if missing:
    errors.append(
        'osac-ui requests scopes it is not assigned: %s '
        '(assigned: %s) -- Keycloak will answer the authorization request '
        'with invalid_scope' % (', '.join(missing), ', '.join(sorted(assigned)))
    )

# A scope the client references but the realm does not define is dropped
# silently at import time, which reproduces the same login failure.
undefined = sorted(assigned - defined)
if undefined:
    errors.append(
        'osac-ui references client scopes not defined in the realm: %s'
        % ', '.join(undefined)
    )

if errors:
    sys.exit('; '.join(errors))
" 2>&1) || fail "${description} -- ${errors}"
}

echo "=== Test 1: committed realm.json assigns the osac-ui client every scope the UI requests ==="
assert_realm_scopes "${CHART_DIR}/files/realm.json" "Chart realm.json"
assert_realm_scopes "${PREREQ_DIR}/files/realm.json" "Static reference realm.json"

echo "=== Test 2: the rendered chart ships that same realm to Keycloak ==="
# Guards against the chart's realm ConfigMap drifting from files/realm.json
# (e.g. a template that stops embedding it, or embeds a different file).
TMP_DIR=$(mktemp -d)
trap 'rm -rf "${TMP_DIR}"' EXIT

helm template "${CHART_DIR}" | python3 -c "
import yaml, sys
for d in yaml.safe_load_all(sys.stdin.read()):
    if d and d.get('kind') == 'ConfigMap' and d.get('metadata', {}).get('name') == 'keycloak-realm':
        sys.stdout.write(d['data']['realm.json'])
        sys.exit(0)
sys.exit(1)
" >"${TMP_DIR}/rendered-realm.json" || fail "Could not extract realm.json from the rendered keycloak-realm ConfigMap"

if [[ -s "${TMP_DIR}/rendered-realm.json" ]]; then
    assert_realm_scopes "${TMP_DIR}/rendered-realm.json" "Rendered keycloak-realm ConfigMap"
fi

echo
if [[ "${FAILURES}" -gt 0 ]]; then
    echo "${FAILURES} check(s) failed."
    exit 1
fi
echo "All Keycloak osac-ui client scope checks passed."
