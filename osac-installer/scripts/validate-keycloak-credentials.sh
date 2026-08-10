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

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CHART_DIR="${SCRIPT_DIR}/../charts/osac-prereqs"
FAILURES=0

fail() {
    echo "FAIL: $1" >&2
    FAILURES=$((FAILURES + 1))
}

assert_contains() {
    local haystack="$1" needle="$2" description="$3"
    if ! grep -qF -- "${needle}" <<<"${haystack}"; then
        fail "${description} -- expected to find: ${needle}"
    fi
}

assert_not_contains() {
    local haystack="$1" needle="$2" description="$3"
    if grep -qF -- "${needle}" <<<"${haystack}"; then
        fail "${description} -- did not expect to find: ${needle}"
    fi
}

echo "=== Test 1: defaults preserve today's literals ==="
DEFAULT_RENDER=$(helm template "${CHART_DIR}")
assert_contains "${DEFAULT_RENDER}" 'name: KEYCLOAK_ADMIN
          value: "admin"' "Default KEYCLOAK_ADMIN"
assert_contains "${DEFAULT_RENDER}" 'name: KEYCLOAK_ADMIN_PASSWORD
          value: "admin"' "Default KEYCLOAK_ADMIN_PASSWORD"
assert_contains "${DEFAULT_RENDER}" 'name: DEFAULT_USER_PASSWORD
          value: "foobar"' "Default DEFAULT_USER_PASSWORD"

echo "=== Test 2: --set overrides propagate (the actual regression) ==="
OVERRIDE_RENDER=$(helm template "${CHART_DIR}" \
    --set keycloak.adminUsername=demo-admin \
    --set keycloak.adminPassword=SuperSecret123 \
    --set keycloak.defaultUserPassword=DemoUserPass456)
assert_contains "${OVERRIDE_RENDER}" 'name: KEYCLOAK_ADMIN
          value: "demo-admin"' "Overridden KEYCLOAK_ADMIN"
assert_contains "${OVERRIDE_RENDER}" 'name: KEYCLOAK_ADMIN_PASSWORD
          value: "SuperSecret123"' "Overridden KEYCLOAK_ADMIN_PASSWORD"
assert_contains "${OVERRIDE_RENDER}" 'name: DEFAULT_USER_PASSWORD
          value: "DemoUserPass456"' "Overridden DEFAULT_USER_PASSWORD"
assert_not_contains "${OVERRIDE_RENDER}" 'value: "admin"' "Overridden render must not retain hardcoded admin literal"
assert_not_contains "${OVERRIDE_RENDER}" 'value: "foobar"' "Overridden render must not retain hardcoded foobar literal"

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
REALM_ADMIN_PASSWORD=$'test-p@ss#word&with\\slash/chars\tand\ttabs\nand\nnewlines' \
    bash "${CHART_DIR}/files/hooks/resolve-realm-secrets.sh" >/dev/null 2>&1 || {
        fail "resolve-realm-secrets.sh exited non-zero"
    }

if [[ -f "${TMP_DIR}/realm-resolved.json" ]]; then
    RESOLVED=$(cat "${TMP_DIR}/realm-resolved.json")
    DECODED_PASSWORD=$(python3 -c "import json; print(json.load(open('${TMP_DIR}/realm-resolved.json'))['credentials'][0]['value'])" 2>/dev/null) || {
        fail "Resolved realm.json is not valid JSON after substituting a password with sed/JSON metacharacters"
    }
    # Compare the JSON-decoded value, not the raw file text -- valid JSON
    # necessarily re-escapes the backslash as \\, so the raw bytes never
    # contain the password's literal backslash form.
    if [[ "${DECODED_PASSWORD}" != $'test-p@ss#word&with\\slash/chars\tand\ttabs\nand\nnewlines' ]]; then
        fail "Resolved realm.json's decoded password does not match the original"
    fi
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

echo
if [[ "${FAILURES}" -gt 0 ]]; then
    echo "${FAILURES} check(s) failed."
    exit 1
fi
echo "All Keycloak credential parameterization checks passed."
