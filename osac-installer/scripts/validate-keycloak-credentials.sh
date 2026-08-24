#!/usr/bin/env bash
# Regression test for OSAC-3610: Keycloak admin/demo-user credentials must be
# overridable via Helm values, not hardcoded. Verifies:
#   1. Defaults render as today's literals (admin/admin/foobar) -- no
#      behavior change for installs that don't override anything.
#   2. --set overrides actually propagate into the rendered manifests --
#      this is the core regression: before the fix, no override existed.
#   3. resolve-realm-secrets.sh's sed substitution handles passwords
#      containing sed/regex metacharacters without corrupting the JSON.
set -euo pipefail

python3 -c "import yaml" 2>/dev/null || {
    echo "ERROR: PyYAML is required to run this script (used to extract embedded" >&2
    echo "scripts and structural fields from rendered/static manifests)." >&2
    echo "Install it with: pip install pyyaml  (already present in the workspace's" >&2
    echo "documented dev Containerfile via python3-pyyaml)." >&2
    exit 1
}

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CHART_DIR="${SCRIPT_DIR}/../charts/osac-infra"
FAILURES=0

fail() {
    echo "FAIL: $1" >&2
    FAILURES=$((FAILURES + 1))
}

assert_contains() {
    local haystack="$1" needle="$2" description="$3"
    if ! grep -qF -- "${needle}" <<<"${haystack}"; then
        fail "${description} -- expected value not found"
    fi
}

assert_not_contains() {
    local haystack="$1" needle="$2" description="$3"
    if grep -qF -- "${needle}" <<<"${haystack}"; then
        fail "${description} -- unexpected value was found"
    fi
}

# Structural check: does the given Deployment/Job's named container/
# initContainer have an env var sourced from the expected Secret name/key
# (not a plaintext `value:`)? Uses PyYAML for a real structural check rather
# than brittle multi-line grep -F (which silently splits multi-line needles
# into OR'd single-line alternatives -- see Test 7's comment below).
assert_secret_key_ref() {
    local render="$1" kind="$2" resource_name="$3" container_name="$4" env_name="$5" expected_secret="$6" expected_key="$7" description="$8"
    python3 -c "
import yaml, sys
docs = list(yaml.safe_load_all(sys.stdin.read()))
for d in docs:
    if not d or d.get('kind') != '${kind}' or d.get('metadata', {}).get('name') != '${resource_name}':
        continue
    spec = d['spec']['template']['spec']
    for c in spec.get('containers', []) + spec.get('initContainers', []):
        if c.get('name') != '${container_name}':
            continue
        for e in c.get('env', []):
            if e.get('name') == '${env_name}':
                ref = e.get('valueFrom', {}).get('secretKeyRef', {})
                sys.exit(0 if ref.get('name') == '${expected_secret}' and ref.get('key') == '${expected_key}' else 1)
sys.exit(1)
" <<<"${render}" || fail "${description}"
}

echo "=== Test 1: defaults preserve today's literals (via the keycloak-admin-credentials Secret) ==="
DEFAULT_RENDER=$(helm template "${CHART_DIR}")
assert_contains "${DEFAULT_RENDER}" 'admin-username: "admin"' "Default admin-username in keycloak-admin-credentials Secret"
assert_contains "${DEFAULT_RENDER}" 'admin-password: "admin"' "Default admin-password in keycloak-admin-credentials Secret"
assert_contains "${DEFAULT_RENDER}" 'default-user-password: "foobar"' "Default default-user-password in keycloak-admin-credentials Secret"
assert_secret_key_ref "${DEFAULT_RENDER}" Deployment keycloak-service keycloak KEYCLOAK_ADMIN keycloak-admin-credentials admin-username "Deployment's KEYCLOAK_ADMIN must source from keycloak-admin-credentials/admin-username"
assert_secret_key_ref "${DEFAULT_RENDER}" Deployment keycloak-service keycloak KEYCLOAK_ADMIN_PASSWORD keycloak-admin-credentials admin-password "Deployment's KEYCLOAK_ADMIN_PASSWORD must source from keycloak-admin-credentials/admin-password"
assert_secret_key_ref "${DEFAULT_RENDER}" Deployment keycloak-service resolve-realm-secrets REALM_ADMIN_USERNAME keycloak-admin-credentials admin-username "resolve-realm-secrets init container's REALM_ADMIN_USERNAME must source from the Secret"
assert_secret_key_ref "${DEFAULT_RENDER}" Deployment keycloak-service resolve-realm-secrets REALM_ADMIN_PASSWORD keycloak-admin-credentials admin-password "resolve-realm-secrets init container's REALM_ADMIN_PASSWORD must source from the Secret"
assert_secret_key_ref "${DEFAULT_RENDER}" Job keycloak-set-passwords set-passwords ADMIN_USERNAME keycloak-admin-credentials admin-username "keycloak-set-passwords Job's ADMIN_USERNAME must source from the Secret"
assert_secret_key_ref "${DEFAULT_RENDER}" Job keycloak-set-passwords set-passwords ADMIN_PASSWORD keycloak-admin-credentials admin-password "keycloak-set-passwords Job's ADMIN_PASSWORD must source from the Secret"
assert_secret_key_ref "${DEFAULT_RENDER}" Job keycloak-set-passwords set-passwords DEFAULT_USER_PASSWORD keycloak-admin-credentials default-user-password "keycloak-set-passwords Job's DEFAULT_USER_PASSWORD must source from the Secret"

echo "=== Test 2: --set overrides propagate into the Secret (the actual regression) ==="
OVERRIDE_RENDER=$(helm template "${CHART_DIR}" \
    --set keycloak.adminUsername=demo-admin \
    --set keycloak.adminPassword=SuperSecret123 \
    --set keycloak.defaultUserPassword=DemoUserPass456)
assert_contains "${OVERRIDE_RENDER}" 'admin-username: "demo-admin"' "Overridden admin-username in Secret"
assert_contains "${OVERRIDE_RENDER}" 'admin-password: "SuperSecret123"' "Overridden admin-password in Secret"
assert_contains "${OVERRIDE_RENDER}" 'default-user-password: "DemoUserPass456"' "Overridden default-user-password in Secret"
assert_not_contains "${OVERRIDE_RENDER}" 'admin-username: "admin"' "Overridden Secret must not retain the hardcoded admin-username default"
assert_not_contains "${OVERRIDE_RENDER}" 'admin-password: "admin"' "Overridden Secret must not retain the hardcoded admin-password default"
assert_not_contains "${OVERRIDE_RENDER}" 'default-user-password: "foobar"' "Overridden Secret must not retain the hardcoded default-user-password default"

echo "=== Test 2b: pod specs never carry credentials as plaintext, only via secretKeyRef ==="
assert_not_contains "${DEFAULT_RENDER}" '          value: "admin"' "Default render must have no plaintext admin literal in any pod spec env"
assert_not_contains "${DEFAULT_RENDER}" '          value: "foobar"' "Default render must have no plaintext foobar literal in any pod spec env"
assert_not_contains "${OVERRIDE_RENDER}" '          value: "demo-admin"' "Overridden render must have no plaintext admin-username literal in any pod spec env"
assert_not_contains "${OVERRIDE_RENDER}" '          value: "SuperSecret123"' "Overridden render must have no plaintext admin-password literal in any pod spec env"
assert_not_contains "${OVERRIDE_RENDER}" '          value: "DemoUserPass456"' "Overridden render must have no plaintext default-user-password literal in any pod spec env"

echo "=== Test 3: realm.json placeholders present, no baked-in credential hash ==="
assert_contains "${DEFAULT_RENDER}" '__OSAC_REALM_ADMIN_USERNAME__' "realm.json admin username placeholder"
assert_contains "${DEFAULT_RENDER}" '__OSAC_REALM_ADMIN_PASSWORD__' "realm.json admin password placeholder"
assert_not_contains "${DEFAULT_RENDER}" 'ETe90wgj32P' "Static argon2 password hash must be removed from realm.json"

echo "=== Test 4: resolve-realm-secrets.sh substitution survives special characters ==="
TMP_DIR=$(mktemp -d)
trap 'rm -rf "${TMP_DIR}"' EXIT

cat >"${TMP_DIR}/realm-raw.json" <<'EOF'
{"username": "__OSAC_REALM_ADMIN_USERNAME__", "credentials": [{"type": "password", "value": "__OSAC_REALM_ADMIN_PASSWORD__", "temporary": false}]}
EOF

# Stub `oc` so the hook script's client-secret bootstrap path is exercised
# without a real cluster: existence check reports "not found" once (forcing
# the generate branch), then jsonpath lookups return fixed base64 values.
mkdir -p "${TMP_DIR}/bin"
cat >"${TMP_DIR}/bin/oc" <<'EOF'
#!/usr/bin/env bash
args="$*"
case "${args}" in
  *"-o jsonpath="*osac-controller*) printf '%s' "$(printf 'controller-secret' | base64)" ;;
  *"-o jsonpath="*osac-admin*)      printf '%s' "$(printf 'admin-secret' | base64)" ;;
  *"create secret"*)                exit 0 ;;
  *"get secret"*)                   exit 1 ;;  # existence check: force the "generate" branch
  *)                                exit 0 ;;
esac
EOF
chmod +x "${TMP_DIR}/bin/oc"

PATH="${TMP_DIR}/bin:${PATH}" \
REALM_RAW_PATH="${TMP_DIR}/realm-raw.json" \
REALM_OUTPUT_PATH="${TMP_DIR}/realm-resolved.json" \
REALM_ADMIN_USERNAME="demo-admin" \
REALM_ADMIN_PASSWORD=$'test-p@ss#word&with\\slash/chars\tand\ttabs\nand\nnewlines\n' \
    bash "${CHART_DIR}/files/hooks/resolve-realm-secrets.sh" >/dev/null 2>&1 || {
        fail "resolve-realm-secrets.sh exited non-zero"
    }

if [[ -f "${TMP_DIR}/realm-resolved.json" ]]; then
    RESOLVED=$(cat "${TMP_DIR}/realm-resolved.json")
    # Compare byte-for-byte inside Python via the environment, not through a
    # $(...) capture -- command substitution silently strips trailing
    # newlines, which would hide a real bug for passwords ending in "\n".
    EXPECTED_REALM_PASSWORD=$'test-p@ss#word&with\\slash/chars\tand\ttabs\nand\nnewlines\n' \
        python3 -c "
import json, os, sys
try:
    decoded = json.load(open('${TMP_DIR}/realm-resolved.json'))['credentials'][0]['value']
except Exception:
    sys.exit(2)
sys.exit(0 if decoded == os.environ['EXPECTED_REALM_PASSWORD'] else 1)
"
    case $? in
        2) fail "Resolved realm.json is not valid JSON after substituting a password with sed/JSON metacharacters" ;;
        1) fail "Resolved realm.json's decoded password does not match the original" ;;
    esac
    assert_not_contains "${RESOLVED}" '__OSAC_REALM_ADMIN_PASSWORD__' "Placeholder must be fully substituted"
else
    fail "resolve-realm-secrets.sh did not produce ${TMP_DIR}/realm-resolved.json"
fi

echo "=== Test 5: demo-user reset-password JSON body handles quote/backslash chars ==="
# Extracts and executes the ACTUAL embedded set-passwords.sh scripts (not a
# reimplementation) against a stubbed curl, so a future edit to either
# script's escaping logic is caught here rather than silently regressing.
DEMO_PASSWORD='pass"with\backslash'

mkdir -p "${TMP_DIR}/curlbin"
CAPTURE_FILE="${TMP_DIR}/captured_reset_password_payload.json"
cat >"${TMP_DIR}/curlbin/curl" <<EOF
#!/usr/bin/env bash
args="\$*"
case "\${args}" in
  *"reset-password"*)
    # Most specific match first: find the argument immediately following -d
    # and capture it, before any less-specific pattern below can match.
    prev=""
    for a in "\$@"; do
      if [[ "\${prev}" == "-d" ]]; then
        printf '%s' "\${a}" > "${CAPTURE_FILE}"
      fi
      prev="\${a}"
    done
    exit 0
    ;;
  *"protocol/openid-connect/token"*) echo '{"access_token":"fake-token"}' ;;
  *"/users?username="*) echo '[{"id":"fake-user-id"}]' ;;
  *) exit 0 ;;  # bare readiness-check GET (https://keycloak:443/realms/osac, no distinguishing flag)
esac
EOF
chmod +x "${TMP_DIR}/curlbin/curl"

run_set_password_script() {
    local script_content="$1"
    rm -f "${CAPTURE_FILE}"
    echo "${script_content}" > "${TMP_DIR}/extracted-set-passwords.sh"
    PATH="${TMP_DIR}/curlbin:${PATH}" \
    ADMIN_USERNAME="demo-admin" \
    ADMIN_PASSWORD="admin-pw" \
    DEFAULT_USER_PASSWORD="${DEMO_PASSWORD}" \
        bash "${TMP_DIR}/extracted-set-passwords.sh" >/dev/null 2>&1 || true
}

# --- Chart Job's actual embedded script, extracted from the real rendered manifest ---
CHART_SCRIPT=$(python3 -c "
import yaml, sys
docs = yaml.safe_load_all(sys.stdin.read())
for d in docs:
    if d and d.get('kind') == 'ConfigMap' and d.get('metadata', {}).get('name') == 'keycloak-password-setup' and d.get('metadata', {}).get('namespace') == 'keycloak':
        print(d['data']['set-passwords.sh'])
        break
" <<<"${DEFAULT_RENDER}")
[[ -n "${CHART_SCRIPT}" ]] || fail "Could not extract set-passwords.sh from the rendered chart manifest"

if [[ -n "${CHART_SCRIPT}" ]]; then
    run_set_password_script "${CHART_SCRIPT}"
    if [[ -f "${CAPTURE_FILE}" ]]; then
        DECODED=$(python3 -c "import json; print(json.load(open('${CAPTURE_FILE}'))['value'])" 2>/dev/null) || {
            fail "Chart Job's actual set-passwords.sh produced an invalid JSON reset-password body for a password with quote/backslash"
        }
        [[ "${DECODED}" == "${DEMO_PASSWORD}" ]] || fail "Chart Job's actual set-passwords.sh reset-password body does not round-trip the password"
    else
        fail "Chart Job's actual set-passwords.sh never reached the reset-password call"
    fi
fi

# --- Static reference manifest's actual embedded script, extracted directly from the YAML file ---
STATIC_SCRIPT=$(python3 -c "
import yaml
with open('${SCRIPT_DIR}/../prerequisites/keycloak/service/password-setup-job.yaml') as f:
    docs = list(yaml.safe_load_all(f))
for d in docs:
    if d and d.get('kind') == 'ConfigMap' and d.get('metadata', {}).get('name') == 'keycloak-password-setup':
        print(d['data']['set-passwords.sh'])
        break
")
[[ -n "${STATIC_SCRIPT}" ]] || fail "Could not extract set-passwords.sh from the static reference manifest"

if [[ -n "${STATIC_SCRIPT}" ]]; then
    run_set_password_script "${STATIC_SCRIPT}"
    if [[ -f "${CAPTURE_FILE}" ]]; then
        DECODED=$(python3 -c "import json; print(json.load(open('${CAPTURE_FILE}'))['value'])" 2>/dev/null) || {
            fail "Static reference manifest's actual set-passwords.sh produced an invalid JSON reset-password body for a password with quote/backslash"
        }
        [[ "${DECODED}" == "${DEMO_PASSWORD}" ]] || fail "Static reference manifest's actual set-passwords.sh reset-password body does not round-trip the password"
    else
        fail "Static reference manifest's actual set-passwords.sh never reached the reset-password call"
    fi
fi

# Test 5 above only proves the extracted script's logic works when given
# credentials via env vars -- it doesn't prove the Job manifest actually
# wires those env vars to the Secret. Check that structurally too.
STATIC_PASSWORD_JOB=$(cat "${SCRIPT_DIR}/../prerequisites/keycloak/service/password-setup-job.yaml")
assert_secret_key_ref "${STATIC_PASSWORD_JOB}" Job keycloak-set-passwords set-passwords ADMIN_USERNAME keycloak-admin-credentials admin-username "Static password-setup-job.yaml's ADMIN_USERNAME must source from keycloak-admin-credentials/admin-username"
assert_secret_key_ref "${STATIC_PASSWORD_JOB}" Job keycloak-set-passwords set-passwords ADMIN_PASSWORD keycloak-admin-credentials admin-password "Static password-setup-job.yaml's ADMIN_PASSWORD must source from keycloak-admin-credentials/admin-password"
assert_secret_key_ref "${STATIC_PASSWORD_JOB}" Job keycloak-set-passwords set-passwords DEFAULT_USER_PASSWORD keycloak-admin-credentials default-user-password "Static password-setup-job.yaml's DEFAULT_USER_PASSWORD must source from keycloak-admin-credentials/default-user-password"

echo "=== Test 6: static reference manifest's realm.json and resolve-realm-secrets coverage ==="
# Tests 3/4 only cover the chart's copies. The static prerequisites/
# manifest has its own hand-maintained duplicates of both realm.json and
# the resolve-realm-secrets logic (deployment.yaml has no shared script
# file to reference), so they need their own direct coverage -- otherwise
# a future edit to the static copy alone could silently diverge unnoticed.

STATIC_REALM_JSON=$(cat "${SCRIPT_DIR}/../prerequisites/keycloak/service/files/realm.json")
assert_contains "${STATIC_REALM_JSON}" '__OSAC_REALM_ADMIN_USERNAME__' "Static realm.json admin username placeholder"
assert_contains "${STATIC_REALM_JSON}" '__OSAC_REALM_ADMIN_PASSWORD__' "Static realm.json admin password placeholder"
assert_not_contains "${STATIC_REALM_JSON}" 'ETe90wgj32P' "Static reference manifest's realm.json must not retain the static argon2 password hash"

STATIC_RESOLVE_SCRIPT=$(python3 -c "
import yaml
with open('${SCRIPT_DIR}/../prerequisites/keycloak/service/deployment.yaml') as f:
    docs = list(yaml.safe_load_all(f))
for d in docs:
    if d and d.get('kind') == 'Deployment':
        for c in d['spec']['template']['spec']['initContainers']:
            if c.get('name') == 'resolve-realm-secrets':
                print(c['command'][2])
                break
        break
")
[[ -n "${STATIC_RESOLVE_SCRIPT}" ]] || fail "Could not extract resolve-realm-secrets init container script from the static Deployment manifest"

if [[ -n "${STATIC_RESOLVE_SCRIPT}" ]]; then
    # Unlike the chart's copy (which takes paths via REALM_RAW_PATH/
    # REALM_OUTPUT_PATH env vars), this script hardcodes /realm-raw/realm.json
    # and /realm/realm.json -- redirect them to TMP_DIR paths for this test
    # run only; the committed file is never touched.
    STATIC_RESOLVE_REDIRECTED=$(printf '%s' "${STATIC_RESOLVE_SCRIPT}" | sed \
        -e "s#/realm-raw/realm\.json#${TMP_DIR}/static-realm-raw.json#g" \
        -e "s#/realm/realm\.json#${TMP_DIR}/static-realm-resolved.json#g")
    cp "${TMP_DIR}/realm-raw.json" "${TMP_DIR}/static-realm-raw.json"
    echo "${STATIC_RESOLVE_REDIRECTED}" > "${TMP_DIR}/extracted-static-resolve.sh"
    rm -f "${TMP_DIR}/static-realm-resolved.json"

    PATH="${TMP_DIR}/bin:${PATH}" \
    REALM_ADMIN_USERNAME="demo-admin" \
    REALM_ADMIN_PASSWORD=$'test-p@ss#word&with\\slash/chars\tand\ttabs\nand\nnewlines\n' \
        bash "${TMP_DIR}/extracted-static-resolve.sh" >/dev/null 2>&1 || {
            fail "Static reference manifest's resolve-realm-secrets init container script exited non-zero"
        }

    if [[ -f "${TMP_DIR}/static-realm-resolved.json" ]]; then
        # See the chart resolver test above for why this avoids a $(...)
        # capture: it would silently strip a trailing "\n" from the password.
        EXPECTED_REALM_PASSWORD=$'test-p@ss#word&with\\slash/chars\tand\ttabs\nand\nnewlines\n' \
            python3 -c "
import json, os, sys
try:
    decoded = json.load(open('${TMP_DIR}/static-realm-resolved.json'))['credentials'][0]['value']
except Exception:
    sys.exit(2)
sys.exit(0 if decoded == os.environ['EXPECTED_REALM_PASSWORD'] else 1)
"
        case $? in
            2) fail "Static reference manifest's resolved realm.json is not valid JSON after substituting a password with sed/JSON metacharacters" ;;
            1) fail "Static reference manifest's resolved realm.json decoded password does not match the original" ;;
        esac
    else
        fail "Static reference manifest's resolve-realm-secrets script did not produce a resolved realm.json"
    fi
fi

echo "=== Test 7: static deployment.yaml sources credentials via secretKeyRef, not literals ==="
# Structural checks per env var (assert_secret_key_ref), not a substring
# search: "name: keycloak-admin-credentials" appearing *somewhere* in the
# file doesn't prove any specific env var points at the right secret/key --
# e.g. KEYCLOAK_ADMIN could be wired to the wrong key and this would have
# still passed under the old string-based check.
STATIC_DEPLOYMENT=$(cat "${SCRIPT_DIR}/../prerequisites/keycloak/service/deployment.yaml")
assert_secret_key_ref "${STATIC_DEPLOYMENT}" Deployment keycloak-service keycloak KEYCLOAK_ADMIN keycloak-admin-credentials admin-username "Static deployment.yaml's KEYCLOAK_ADMIN must source from keycloak-admin-credentials/admin-username"
assert_secret_key_ref "${STATIC_DEPLOYMENT}" Deployment keycloak-service keycloak KEYCLOAK_ADMIN_PASSWORD keycloak-admin-credentials admin-password "Static deployment.yaml's KEYCLOAK_ADMIN_PASSWORD must source from keycloak-admin-credentials/admin-password"
assert_secret_key_ref "${STATIC_DEPLOYMENT}" Deployment keycloak-service resolve-realm-secrets REALM_ADMIN_USERNAME keycloak-admin-credentials admin-username "Static deployment.yaml's REALM_ADMIN_USERNAME must source from keycloak-admin-credentials/admin-username"
assert_secret_key_ref "${STATIC_DEPLOYMENT}" Deployment keycloak-service resolve-realm-secrets REALM_ADMIN_PASSWORD keycloak-admin-credentials admin-password "Static deployment.yaml's REALM_ADMIN_PASSWORD must source from keycloak-admin-credentials/admin-password"

echo
if [[ "${FAILURES}" -gt 0 ]]; then
    echo "${FAILURES} check(s) failed."
    exit 1
fi
echo "All Keycloak credential parameterization checks passed."
