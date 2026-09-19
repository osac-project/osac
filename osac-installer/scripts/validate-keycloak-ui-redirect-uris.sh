#!/usr/bin/env bash
# Regression test for OSAC-3884: the Keycloak osac-ui client must end up with
# absolute redirectUris that match the browser-facing UI URL. Keycloak does not
# resolve a relative redirectUri (e.g. "/*") against the Route hostname, so
# shipping one makes browser login fail with "Invalid parameter: redirect_uri".
#
# No CI job drives the osac-ui browser authorization-code flow (CI leaves
# Keycloak in-cluster and the e2e suites authenticate through the osac-cli
# localhost grant), so this is the guard that keeps the client config from
# drifting away from the UI Route again. It needs neither a cluster nor a
# browser: it asserts on the rendered chart and then actually runs the
# resolve-realm-secrets.sh hook against the committed realm.json.
set -euo pipefail

python3 -c "import yaml" 2>/dev/null || {
    echo "ERROR: PyYAML is required to run this script (used to extract the" >&2
    echo "realm ConfigMap and container env from rendered/static manifests)." >&2
    echo "Install it with: pip install pyyaml" >&2
    exit 1
}

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CHART_DIR="${SCRIPT_DIR}/../charts/osac-infra"
PREREQ_DIR="${SCRIPT_DIR}/../prerequisites/keycloak/service"
TEST_UI_URL="https://osac-ui-osac.apps.example.com"
FAILURES=0

fail() {
    echo "FAIL: $1" >&2
    FAILURES=$((FAILURES + 1))
}

TMP_DIR=$(mktemp -d)
trap 'rm -rf "${TMP_DIR}"' EXIT

# Stub `oc` so the hook's client-secret bootstrap runs without a cluster:
# the existence check reports "not found" (forcing the generate branch), then
# each jsonpath lookup returns a fixed base64 value.
mkdir -p "${TMP_DIR}/bin"
cat >"${TMP_DIR}/bin/oc" <<'EOF'
#!/usr/bin/env bash
args="$*"
case "${args}" in
  *"-o jsonpath="*osac-controller*) printf '%s' "$(printf 'controller-secret' | base64)" ;;
  *"-o jsonpath="*osac-admin*)      printf '%s' "$(printf 'admin-secret' | base64)" ;;
  *"-o jsonpath="*osac-csi-driver*) printf '%s' "$(printf 'csi-driver-secret' | base64)" ;;
  *"create secret"*)                exit 0 ;;
  *"get secret"*)                   exit 1 ;;  # existence check: force the "generate" branch
  *)                                exit 0 ;;
esac
EOF
chmod +x "${TMP_DIR}/bin/oc"

# Asserts the osac-ui client in a *resolved* realm.json is browser-usable:
# every redirect/origin is absolute and rooted at the expected UI URL, and no
# placeholder survived substitution.
assert_resolved_ui_client() {
    local realm_file="$1" expected_url="$2" description="$3" errors
    errors=$(EXPECTED_UI_URL="${expected_url}" REALM_FILE="${realm_file}" python3 -c "
import json, os, sys
url = os.environ['EXPECTED_UI_URL']
try:
    realm = json.load(open(os.environ['REALM_FILE']))
except Exception as exc:
    sys.exit(f'resolved realm.json is not valid JSON: {exc}')
client = next((c for c in realm['clients'] if c['clientId'] == 'osac-ui'), None)
if client is None:
    sys.exit('realm.json has no osac-ui client')
errors = []
if client.get('rootUrl') != url:
    errors.append(f'rootUrl is {client.get(\"rootUrl\")!r}, expected {url!r}')
redirects = client.get('redirectUris') or []
if not redirects:
    errors.append('redirectUris is empty')
for uri in redirects:
    if not uri.startswith(url + '/'):
        errors.append(f'redirectUri {uri!r} is not absolute under {url!r}')
if f'{url}/callback' not in redirects:
    errors.append(f'redirectUris is missing the UI callback {url}/callback')
if client.get('webOrigins') != [url]:
    errors.append(f'webOrigins is {client.get(\"webOrigins\")!r}, expected [{url!r}]')
if '__OSAC_UI_URL__' in json.dumps(realm):
    errors.append('__OSAC_UI_URL__ placeholder survived substitution')
if errors:
    sys.exit('; '.join(errors))
" 2>&1) || fail "${description} -- ${errors}"
}

echo "=== Test 1: committed realm.json templates the UI URL instead of hardcoding it ==="
for realm in "${CHART_DIR}/files/realm.json" "${PREREQ_DIR}/files/realm.json"; do
    python3 -c "
import json, sys
client = next(c for c in json.load(open('${realm}'))['clients'] if c['clientId'] == 'osac-ui')
bad = [u for u in (client.get('redirectUris') or []) if not u.startswith('__OSAC_UI_URL__/')]
if bad or client.get('rootUrl') != '__OSAC_UI_URL__' or client.get('webOrigins') != ['__OSAC_UI_URL__']:
    sys.exit(1)
" || fail "${realm}: osac-ui rootUrl/redirectUris/webOrigins must all be built from the __OSAC_UI_URL__ placeholder"
done

echo "=== Test 2: the rendered chart feeds a non-empty UI URL into the resolver ==="
DEFAULT_RENDER=$(helm template "${CHART_DIR}")
render_ui_url() {
    python3 -c "
import yaml, sys
for d in yaml.safe_load_all(sys.stdin.read()):
    if not d or d.get('kind') != 'Deployment' or d.get('metadata', {}).get('name') != 'keycloak-service':
        continue
    for c in d['spec']['template']['spec'].get('initContainers', []):
        if c.get('name') != 'resolve-realm-secrets':
            continue
        for e in c.get('env', []):
            if e.get('name') == 'OSAC_UI_URL':
                print(e.get('value', ''))
                sys.exit(0)
sys.exit(1)
"
}
DEFAULT_UI_URL=$(render_ui_url <<<"${DEFAULT_RENDER}") \
    || fail "resolve-realm-secrets init container has no OSAC_UI_URL env var"
[[ "${DEFAULT_UI_URL}" =~ ^https?://[A-Za-z0-9._-]+(:[0-9]+)?$ ]] \
    || fail "Default keycloak.uiUrl must render as an absolute scheme://host[:port] URL, got '${DEFAULT_UI_URL}'"

OVERRIDE_UI_URL=$(helm template "${CHART_DIR}" --set "keycloak.uiUrl=${TEST_UI_URL}" | render_ui_url) \
    || fail "resolve-realm-secrets init container has no OSAC_UI_URL env var when keycloak.uiUrl is overridden"
[[ "${OVERRIDE_UI_URL}" == "${TEST_UI_URL}" ]] \
    || fail "keycloak.uiUrl override must reach OSAC_UI_URL, got '${OVERRIDE_UI_URL}'"

echo "=== Test 3: the chart's resolver hook produces absolute osac-ui redirect URIs ==="
PATH="${TMP_DIR}/bin:${PATH}" \
REALM_RAW_PATH="${CHART_DIR}/files/realm.json" \
REALM_OUTPUT_PATH="${TMP_DIR}/chart-realm-resolved.json" \
REALM_ADMIN_USERNAME="admin" \
REALM_ADMIN_PASSWORD="admin" \
OSAC_UI_URL="${TEST_UI_URL}" \
    bash "${CHART_DIR}/files/hooks/resolve-realm-secrets.sh" >/dev/null 2>&1 \
    || fail "resolve-realm-secrets.sh exited non-zero with a valid OSAC_UI_URL"

if [[ -f "${TMP_DIR}/chart-realm-resolved.json" ]]; then
    assert_resolved_ui_client "${TMP_DIR}/chart-realm-resolved.json" "${TEST_UI_URL}" \
        "Chart resolver output"
else
    fail "resolve-realm-secrets.sh did not produce a resolved realm.json"
fi

echo "=== Test 4: the resolver rejects a relative or missing UI URL ==="
run_resolver_expecting_failure() {
    local description="$1"
    shift
    if env "$@" \
        PATH="${TMP_DIR}/bin:${PATH}" \
        REALM_RAW_PATH="${CHART_DIR}/files/realm.json" \
        REALM_OUTPUT_PATH="${TMP_DIR}/rejected-realm.json" \
        REALM_ADMIN_USERNAME="admin" \
        REALM_ADMIN_PASSWORD="admin" \
        bash "${CHART_DIR}/files/hooks/resolve-realm-secrets.sh" >/dev/null 2>&1; then
        fail "${description}"
    fi
}
run_resolver_expecting_failure "resolve-realm-secrets.sh must reject an unset OSAC_UI_URL" OSAC_UI_URL=
run_resolver_expecting_failure "resolve-realm-secrets.sh must reject a relative OSAC_UI_URL" OSAC_UI_URL=/
run_resolver_expecting_failure "resolve-realm-secrets.sh must reject a scheme-less OSAC_UI_URL" OSAC_UI_URL=osac-ui.apps.example.com

echo "=== Test 5: the static reference manifest resolves the UI URL the same way ==="
# prerequisites/keycloak/service/deployment.yaml carries its own hand-maintained
# copy of the resolver (no shared script file to reference), so it needs direct
# coverage or it can silently diverge from the chart's hook.
STATIC_RESOLVE_SCRIPT=$(python3 -c "
import yaml
with open('${PREREQ_DIR}/deployment.yaml') as f:
    for d in yaml.safe_load_all(f):
        if d and d.get('kind') == 'Deployment':
            for c in d['spec']['template']['spec']['initContainers']:
                if c.get('name') == 'resolve-realm-secrets':
                    print(c['command'][2])
                    break
            break
")
[[ -n "${STATIC_RESOLVE_SCRIPT}" ]] || fail "Could not extract resolve-realm-secrets from the static Deployment manifest"

if [[ -n "${STATIC_RESOLVE_SCRIPT}" ]]; then
    # The static copy hardcodes /realm-raw and /realm paths; redirect them into
    # TMP_DIR for this run only. The committed file is never touched.
    printf '%s' "${STATIC_RESOLVE_SCRIPT}" | sed \
        -e "s#/realm-raw/realm\.json#${PREREQ_DIR}/files/realm.json#g" \
        -e "s#/realm/realm\.json#${TMP_DIR}/static-realm-resolved.json#g" \
        >"${TMP_DIR}/extracted-static-resolve.sh"

    PATH="${TMP_DIR}/bin:${PATH}" \
    REALM_ADMIN_USERNAME="admin" \
    REALM_ADMIN_PASSWORD="admin" \
    OSAC_UI_URL="${TEST_UI_URL}" \
        bash "${TMP_DIR}/extracted-static-resolve.sh" >/dev/null 2>&1 \
        || fail "Static reference manifest's resolver exited non-zero with a valid OSAC_UI_URL"

    if [[ -f "${TMP_DIR}/static-realm-resolved.json" ]]; then
        assert_resolved_ui_client "${TMP_DIR}/static-realm-resolved.json" "${TEST_UI_URL}" \
            "Static reference manifest resolver output"
    else
        fail "Static reference manifest's resolver did not produce a resolved realm.json"
    fi

    # The manifest must also actually set OSAC_UI_URL -- Test 5 only proves the
    # extracted logic works when the env var is supplied by hand.
    python3 -c "
import yaml, sys
with open('${PREREQ_DIR}/deployment.yaml') as f:
    for d in yaml.safe_load_all(f):
        if d and d.get('kind') == 'Deployment':
            for c in d['spec']['template']['spec']['initContainers']:
                if c.get('name') == 'resolve-realm-secrets':
                    for e in c.get('env', []):
                        if e.get('name') == 'OSAC_UI_URL' and e.get('value'):
                            sys.exit(0)
sys.exit(1)
" || fail "Static deployment.yaml's resolve-realm-secrets container must set OSAC_UI_URL"
fi

echo
if [[ "${FAILURES}" -gt 0 ]]; then
    echo "${FAILURES} check(s) failed."
    exit 1
fi
echo "All Keycloak osac-ui redirect URI checks passed."
