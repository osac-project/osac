#!/usr/bin/env bash
# Regression test for Keycloak credential handling. Verifies that production
# credentials are required inputs, dev credentials are generated, explicit
# protected inputs propagate to the Secret, and realm substitution is safe.
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
TMP_DIR=$(mktemp -d)
trap 'rm -rf "${TMP_DIR}"' EXIT

TEST_ADMIN_PASSWORD="$(python3 -c 'import secrets; print(secrets.token_hex(24))')"
TEST_DEFAULT_USER_PASSWORD="$(python3 -c 'import secrets; print(secrets.token_hex(24))')"
SPECIAL_REALM_PASSWORD="test-$(python3 -c 'import secrets; print(secrets.token_hex(4))')@#&with\\slash/chars"
SPECIAL_REALM_PASSWORD+=$'\tand\ttabs\nand\nnewlines\n'
printf '%s' "${TEST_ADMIN_PASSWORD}" >"${TMP_DIR}/admin-password"
printf '%s' "${TEST_DEFAULT_USER_PASSWORD}" >"${TMP_DIR}/default-user-password"
chmod 600 "${TMP_DIR}/admin-password" "${TMP_DIR}/default-user-password"

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
# into OR'd single-line alternatives -- see the static manifest test below).
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

assert_container_security_context() {
    local render="$1" kind="$2" resource_name="$3" container_name="$4" description="$5" read_only="${6:-false}"
    python3 -c "
import sys, yaml
kind, resource_name, container_name, read_only = sys.argv[1:5]
for doc in yaml.safe_load_all(sys.stdin.read()):
    if not doc or doc.get('kind') != kind or doc.get('metadata', {}).get('name') != resource_name:
        continue
    pod = doc.get('spec', {}).get('template', {}).get('spec', {})
    for container in pod.get('containers', []) + pod.get('initContainers', []):
        if container.get('name') != container_name:
            continue
        context = container.get('securityContext', {})
        secure = (
            context.get('runAsNonRoot') is True
            and context.get('allowPrivilegeEscalation') is False
            and 'ALL' in context.get('capabilities', {}).get('drop', [])
            and context.get('seccompProfile', {}).get('type') == 'RuntimeDefault'
        )
        if read_only == 'true':
            secure = secure and context.get('readOnlyRootFilesystem') is True
        sys.exit(0 if secure else 1)
sys.exit(1)
" "${kind}" "${resource_name}" "${container_name}" "${read_only}" <<<"${render}" || fail "${description}"
}

assert_pod_security_context() {
    local render="$1" kind="$2" resource_name="$3" kind_profile="$4" description="$5"
    python3 -c "
import sys, yaml
kind, resource_name, kind_profile = sys.argv[1:4]
for doc in yaml.safe_load_all(sys.stdin.read()):
    if not doc or doc.get('kind') != kind or doc.get('metadata', {}).get('name') != resource_name:
        continue
    context = doc.get('spec', {}).get('template', {}).get('spec', {}).get('securityContext', {})
    valid = context.get('runAsNonRoot') is True
    if kind_profile == 'true':
        valid = valid and context.get('fsGroup') == 1000
        if kind == 'StatefulSet' and resource_name == 'keycloak-database':
            valid = valid and 'runAsUser' not in context
        else:
            valid = valid and context.get('runAsUser') == 1000
    else:
        valid = valid and 'runAsUser' not in context
    sys.exit(0 if valid else 1)
sys.exit(1)
" "${kind}" "${resource_name}" "${kind_profile}" <<<"${render}" || fail "${description}"
}

assert_secret_role_scope() {
    local render="$1" role_name="$2" readable_secret="$3" description="$4"
    python3 -c "
import sys, yaml
role_name, readable_secret = sys.argv[1:3]
for doc in yaml.safe_load_all(sys.stdin.read()):
    if not doc or doc.get('kind') != 'Role' or doc.get('metadata', {}).get('name') != role_name:
        continue
    rules = doc.get('rules', [])
    get_rules = [rule for rule in rules if 'get' in rule.get('verbs', [])]
    create_rules = [rule for rule in rules if 'create' in rule.get('verbs', [])]
    scoped_get = (
        len(get_rules) == 1
        and get_rules[0].get('apiGroups') == ['']
        and get_rules[0].get('resources') == ['secrets']
        and get_rules[0].get('resourceNames') == [readable_secret]
        and get_rules[0].get('verbs') == ['get']
    )
    create_secret = (
        len(create_rules) == 1
        and create_rules[0].get('apiGroups') == ['']
        and create_rules[0].get('resources') == ['secrets']
        and create_rules[0].get('verbs') == ['create']
        and 'resourceNames' not in create_rules[0]
    )
    sys.exit(0 if scoped_get and create_secret else 1)
sys.exit(1)
" "${role_name}" "${readable_secret}" <<<"${render}" || fail "${description}"
}

assert_keycloak_ca_mount() {
    local render="$1" description="$2"
    python3 -c "
import sys, yaml
for doc in yaml.safe_load_all(sys.stdin.read()):
    if not doc or doc.get('kind') != 'Job' or doc.get('metadata', {}).get('name') != 'keycloak-set-passwords':
        continue
    pod = doc.get('spec', {}).get('template', {}).get('spec', {})
    ca_volume = next((volume for volume in pod.get('volumes', []) if volume.get('name') == 'keycloak-ca'), {})
    secret = ca_volume.get('secret', {})
    items = secret.get('items', [])
    ca_mount = next((mount for mount in pod.get('containers', [{}])[0].get('volumeMounts', []) if mount.get('name') == 'keycloak-ca'), {})
    valid = (
        secret.get('secretName') == 'keycloak-tls-cert'
        and items == [{'key': 'ca.crt', 'path': 'ca.crt'}]
        and ca_mount.get('mountPath') == '/etc/keycloak/ca'
        and ca_mount.get('readOnly') is True
    )
    sys.exit(0 if valid else 1)
sys.exit(1)
" <<<"${render}" || fail "${description}"
}

assert_keycloak_tls_certificate_contract() {
    local render="$1" description="$2"
    python3 -c "
import sys, yaml
docs = [doc for doc in yaml.safe_load_all(sys.stdin.read()) if isinstance(doc, dict)]
certificate = next((doc for doc in docs if doc.get('kind') == 'Certificate' and doc.get('metadata', {}).get('name') == 'keycloak-tls'), None)
issuer = next((doc for doc in docs if doc.get('kind') == 'ClusterIssuer' and doc.get('metadata', {}).get('name') == 'default-ca'), None)
if certificate is None or issuer is None:
    sys.exit(1)
spec = certificate.get('spec', {})
valid = (
    spec.get('secretName') == 'keycloak-tls-cert'
    and 'keycloak' in spec.get('dnsNames', [])
    and spec.get('issuerRef', {}).get('kind') == 'ClusterIssuer'
    and spec.get('issuerRef', {}).get('name') == 'default-ca'
    and issuer.get('spec', {}).get('ca', {}).get('secretName') == 'default-ca'
)
sys.exit(0 if valid else 1)
" <<<"${render}" || fail "${description}"
}

secret_string_data() {
    local render="$1" key="$2"
    python3 -c '
import sys, yaml
key = sys.argv[1]
for doc in yaml.safe_load_all(sys.stdin.read()):
    if doc and doc.get("kind") == "Secret" and doc.get("metadata", {}).get("name") == "keycloak-admin-credentials":
        value = doc.get("stringData", {}).get(key)
        if isinstance(value, str):
            sys.stdout.write(value)
            sys.exit(0)
sys.exit(1)
' "${key}" <<<"${render}"
}

echo "=== Test 1: Keycloak admin password is required by default ==="
if DEFAULT_ERROR=$(helm template "${CHART_DIR}" 2>&1); then
    fail "Keycloak chart rendered without an admin password"
elif ! grep -qF "keycloak.adminPassword is required when keycloak.enabled is true" <<<"${DEFAULT_ERROR}"; then
    fail "Missing admin password did not produce the required-value error"
fi

echo "=== Test 2: dev fixtures generate random credentials when omitted ==="
GENERATED_RENDER_A=$(helm template "${CHART_DIR}" --set keycloak.devFixtures.enabled=true)
GENERATED_RENDER_B=$(helm template "${CHART_DIR}" --set keycloak.devFixtures.enabled=true)
GENERATED_ADMIN_A=$(secret_string_data "${GENERATED_RENDER_A}" admin-password) || fail "Generated admin password is missing from the Secret"
GENERATED_ADMIN_B=$(secret_string_data "${GENERATED_RENDER_B}" admin-password) || fail "Second generated admin password is missing from the Secret"
GENERATED_USER_A=$(secret_string_data "${GENERATED_RENDER_A}" default-user-password) || fail "Generated test-user password is missing from the Secret"
GENERATED_USER_B=$(secret_string_data "${GENERATED_RENDER_B}" default-user-password) || fail "Second generated test-user password is missing from the Secret"
[[ ${#GENERATED_ADMIN_A} -ge 32 ]] || fail "Generated admin password is unexpectedly short"
[[ ${#GENERATED_USER_A} -ge 32 ]] || fail "Generated test-user password is unexpectedly short"
[[ "${GENERATED_ADMIN_A}" != "${GENERATED_ADMIN_B}" ]] || fail "Separate dev renders reused the same generated admin password"
[[ "${GENERATED_USER_A}" != "${GENERATED_USER_B}" ]] || fail "Separate dev renders reused the same generated test-user password"
assert_contains "$(<"${CHART_DIR}/templates/keycloak/resources.yaml")" \
    'lookup "v1" "Secret" "keycloak" "keycloak-admin-credentials"' \
    "Credential template must look up the existing Secret for upgrade stability"
assert_contains "$(<"${CHART_DIR}/templates/keycloak/resources.yaml")" \
    'get $existingCredentialData "admin-password"' \
    "Credential template must reuse the existing admin password"
assert_contains "$(<"${CHART_DIR}/templates/keycloak/resources.yaml")" \
    'get $existingCredentialData "default-user-password"' \
    "Credential template must reuse the existing dev-fixture password"

echo "=== Test 3: Keycloak installs without demo users when only admin input is provided ==="
BASIC_RENDER=$(helm template "${CHART_DIR}" \
    --set-file "keycloak.adminPassword=${TMP_DIR}/admin-password")
assert_contains "${BASIC_RENDER}" "admin-password: \"${TEST_ADMIN_PASSWORD}\"" "Admin password input must populate the Secret"
assert_not_contains "${BASIC_RENDER}" 'default-user-password:' "A non-fixture install must not render a demo-user password"

echo "=== Test 4: protected --set-file inputs populate the Secret ==="
OVERRIDE_RENDER=$(helm template "${CHART_DIR}" \
    --set keycloak.devFixtures.enabled=true \
    --set keycloak.adminUsername=demo-admin \
    --set-file "keycloak.adminPassword=${TMP_DIR}/admin-password" \
    --set-file "keycloak.defaultUserPassword=${TMP_DIR}/default-user-password")
assert_contains "${OVERRIDE_RENDER}" 'admin-username: "demo-admin"' "Overridden admin-username in Secret"
assert_contains "${OVERRIDE_RENDER}" "admin-password: \"${TEST_ADMIN_PASSWORD}\"" "Overridden admin-password in Secret"
assert_contains "${OVERRIDE_RENDER}" "default-user-password: \"${TEST_DEFAULT_USER_PASSWORD}\"" "Overridden default-user-password in Secret"
assert_secret_key_ref "${OVERRIDE_RENDER}" Deployment keycloak-service keycloak KEYCLOAK_ADMIN keycloak-admin-credentials admin-username "Deployment's KEYCLOAK_ADMIN must source from keycloak-admin-credentials/admin-username"
assert_secret_key_ref "${OVERRIDE_RENDER}" Deployment keycloak-service keycloak KEYCLOAK_ADMIN_PASSWORD keycloak-admin-credentials admin-password "Deployment's KEYCLOAK_ADMIN_PASSWORD must source from keycloak-admin-credentials/admin-password"
assert_secret_key_ref "${OVERRIDE_RENDER}" Deployment keycloak-service resolve-realm-secrets REALM_ADMIN_USERNAME keycloak-admin-credentials admin-username "resolve-realm-secrets init container's REALM_ADMIN_USERNAME must source from the Secret"
assert_secret_key_ref "${OVERRIDE_RENDER}" Deployment keycloak-service resolve-realm-secrets REALM_ADMIN_PASSWORD keycloak-admin-credentials admin-password "resolve-realm-secrets init container's REALM_ADMIN_PASSWORD must source from the Secret"
assert_secret_key_ref "${OVERRIDE_RENDER}" Job keycloak-set-passwords set-passwords ADMIN_USERNAME keycloak-admin-credentials admin-username "keycloak-set-passwords Job's ADMIN_USERNAME must source from the Secret"
assert_secret_key_ref "${OVERRIDE_RENDER}" Job keycloak-set-passwords set-passwords ADMIN_PASSWORD keycloak-admin-credentials admin-password "keycloak-set-passwords Job's ADMIN_PASSWORD must source from the Secret"
assert_secret_key_ref "${OVERRIDE_RENDER}" Job keycloak-set-passwords set-passwords DEFAULT_USER_PASSWORD keycloak-admin-credentials default-user-password "keycloak-set-passwords Job's DEFAULT_USER_PASSWORD must source from the Secret"
assert_secret_role_scope "${OVERRIDE_RENDER}" keycloak-database keycloak-database-password "Database Secret reads must be name-scoped; create remains required for bootstrap"
assert_secret_role_scope "${OVERRIDE_RENDER}" keycloak-service keycloak-client-secrets "Keycloak client Secret reads must remain name-scoped"
assert_keycloak_tls_certificate_contract "${OVERRIDE_RENDER}" "Keycloak internal TLS must use the default CA and include the keycloak service DNS name"
assert_container_security_context "${OVERRIDE_RENDER}" StatefulSet keycloak-database password-generator "Database password generator must use restricted container settings"
assert_container_security_context "${OVERRIDE_RENDER}" StatefulSet keycloak-database server "Keycloak database must run non-root with restricted container settings"
assert_container_security_context "${OVERRIDE_RENDER}" Deployment keycloak-service wait-for-database "Database wait container must use restricted container settings"
assert_container_security_context "${OVERRIDE_RENDER}" Deployment keycloak-service resolve-realm-secrets "Realm initialization must use restricted container settings"
assert_container_security_context "${OVERRIDE_RENDER}" Deployment keycloak-service keycloak "Keycloak must run non-root with restricted container settings"
assert_container_security_context "${OVERRIDE_RENDER}" Job keycloak-set-passwords set-passwords "Password setup job must use restricted container settings" true
for workload in "StatefulSet:keycloak-database" "Deployment:keycloak-service" "Job:keycloak-set-passwords"; do
    workload_kind=${workload%%:*}
    workload_name=${workload#*:}
    assert_pod_security_context "${OVERRIDE_RENDER}" "${workload_kind}" "${workload_name}" false \
        "${workload_kind}/${workload_name} must leave UID allocation to OpenShift SCC"
done

KIND_RENDER=$(helm template "${CHART_DIR}" --values "${SCRIPT_DIR}/../values/dev/kind-infra.yaml")
for workload in "StatefulSet:keycloak-database" "Deployment:keycloak-service" "Job:keycloak-set-passwords"; do
    workload_kind=${workload%%:*}
    workload_name=${workload#*:}
    description="Kind ${workload_kind}/${workload_name} must use the configured non-root UID and volume group"
    if [[ "${workload}" == "StatefulSet:keycloak-database" ]]; then
        description="Kind keycloak database must preserve the image UID and configured volume group"
    fi
    assert_pod_security_context "${KIND_RENDER}" "${workload_kind}" "${workload_name}" true \
        "${description}"
done

echo "=== Test 4b: pod specs never carry credentials as plaintext, only via secretKeyRef ==="
assert_not_contains "${OVERRIDE_RENDER}" '          value: "demo-admin"' "Overridden render must have no plaintext admin-username literal in any pod spec env"
assert_not_contains "${OVERRIDE_RENDER}" "          value: \"${TEST_ADMIN_PASSWORD}\"" "Overridden render must have no plaintext admin-password literal in any pod spec env"
assert_not_contains "${OVERRIDE_RENDER}" "          value: \"${TEST_DEFAULT_USER_PASSWORD}\"" "Overridden render must have no plaintext default-user-password literal in any pod spec env"

echo "=== Test 5: realm.json placeholders present, no baked-in credential hash ==="
assert_contains "${OVERRIDE_RENDER}" '__OSAC_REALM_ADMIN_USERNAME__' "realm.json admin username placeholder"
assert_contains "${OVERRIDE_RENDER}" '__OSAC_REALM_ADMIN_PASSWORD__' "realm.json admin password placeholder"
assert_not_contains "${OVERRIDE_RENDER}" 'ETe90wgj32P' "Static argon2 password hash must be removed from realm.json"

echo "=== Test 6: resolve-realm-secrets.sh substitution survives special characters ==="

cat >"${TMP_DIR}/realm-raw.json" <<'EOF'
{"username": "__OSAC_REALM_ADMIN_USERNAME__", "credentials": [{"type": "password", "value": "__OSAC_REALM_ADMIN_PASSWORD__", "temporary": false}]}
EOF

# Stub `oc` so the hook script's client-secret bootstrap path is exercised
# without a real cluster: existence check reports "not found" once (forcing
# the generate branch), then jsonpath lookups return fixed base64 values for
# all four client secrets.
mkdir -p "${TMP_DIR}/bin"
cat >"${TMP_DIR}/bin/oc" <<'EOF'
#!/usr/bin/env bash
args="$*"
case "${args}" in
  *"-o jsonpath="*osac-controller*) printf '%s' "$(printf 'controller-secret' | base64)" ;;
  *"-o jsonpath="*osac-admin*)      printf '%s' "$(printf 'admin-secret' | base64)" ;;
  *"-o jsonpath="*osac-csi-driver*) printf '%s' "$(printf 'csi-driver-secret' | base64)" ;;
  *"-o jsonpath="*osac-ui-backend*) printf '%s' "$(printf 'ui-backend-secret' | base64)" ;;
  *"create secret"*)                exit 0 ;;
  *"get secret"*)                   exit 1 ;;  # existence check: force the "generate" branch
  *)                                exit 0 ;;
esac
EOF
chmod +x "${TMP_DIR}/bin/oc"
cat >"${TMP_DIR}/bin/openssl" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
[[ "$*" == "rand -base64 18" ]] || exit 2
printf '%s' 'dGVzdC1jbGllbnQtc2VjcmV0'
EOF
chmod +x "${TMP_DIR}/bin/openssl"

PATH="${TMP_DIR}/bin:${PATH}" \
REALM_RAW_PATH="${TMP_DIR}/realm-raw.json" \
REALM_OUTPUT_PATH="${TMP_DIR}/realm-resolved.json" \
REALM_ADMIN_USERNAME="demo-admin" \
REALM_ADMIN_PASSWORD="${SPECIAL_REALM_PASSWORD}" \
OSAC_UI_URL="https://osac-ui-osac.apps.example.com" \
    bash "${CHART_DIR}/files/hooks/resolve-realm-secrets.sh" >/dev/null 2>&1 || {
        fail "resolve-realm-secrets.sh exited non-zero"
    }

if [[ -f "${TMP_DIR}/realm-resolved.json" ]]; then
    RESOLVED=$(cat "${TMP_DIR}/realm-resolved.json")
    # Compare byte-for-byte inside Python via the environment, not through a
    # $(...) capture -- command substitution silently strips trailing
    # newlines, which would hide a real bug for passwords ending in "\n".
    EXPECTED_REALM_PASSWORD="${SPECIAL_REALM_PASSWORD}" \
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

echo "=== Test 7: dev fixture requests verify TLS and reset-password JSON handles special chars ==="
# Extracts and executes the ACTUAL embedded set-passwords.sh scripts (not a
# reimplementation) against a stubbed curl, so a future edit to either
# script's escaping logic is caught here rather than silently regressing.
DEMO_PASSWORD="test-$(python3 -c 'import secrets; print(secrets.token_hex(8))')\"with\\backslash"

mkdir -p "${TMP_DIR}/curlbin"
CAPTURE_FILE="${TMP_DIR}/captured_reset_password_payload.json"
cat >"${TMP_DIR}/curlbin/curl" <<EOF
#!/usr/bin/env bash
has_cacert=false
previous=""
for arg in "\$@"; do
  if [[ "\${previous}" == "--cacert" && "\${arg}" == "/etc/keycloak/ca/ca.crt" ]]; then
    has_cacert=true
  fi
  if [[ "\${arg}" == "-k" || "\${arg}" == "--insecure" || ( "\${arg}" == -* && "\${arg}" == *k* ) ]]; then
    exit 94
  fi
  previous="\${arg}"
done
[[ "\${has_cacert}" == true ]] || exit 95
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
    ADMIN_PASSWORD="${TEST_ADMIN_PASSWORD}" \
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
" <<<"${OVERRIDE_RENDER}")
[[ -n "${CHART_SCRIPT}" ]] || fail "Could not extract set-passwords.sh from the rendered chart manifest"
assert_keycloak_ca_mount "${OVERRIDE_RENDER}" "Chart password-setup job must mount only keycloak-tls-cert/ca.crt"
assert_contains "${CHART_SCRIPT}" 'curl --cacert "${KEYCLOAK_CA_FILE}"' "Chart password-setup script must verify Keycloak with the mounted CA"
assert_not_contains "${CHART_SCRIPT}" 'curl -k' "Chart password-setup script must not disable TLS verification"
assert_not_contains "${CHART_SCRIPT}" '--insecure' "Chart password-setup script must not disable TLS verification"

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
STATIC_PASSWORD_JOB=$(<"${SCRIPT_DIR}/../prerequisites/keycloak/service/password-setup-job.yaml")
STATIC_TLS_CERT=$(<"${SCRIPT_DIR}/../prerequisites/keycloak/service/certificate.yaml")
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
assert_keycloak_ca_mount "${STATIC_PASSWORD_JOB}" "Static password-setup job must mount only keycloak-tls-cert/ca.crt"
assert_contains "${STATIC_TLS_CERT}" 'name: default-ca' "Static Keycloak TLS certificate must use the internal default CA"
assert_contains "${STATIC_TLS_CERT}" '- keycloak' "Static Keycloak TLS certificate must include the internal service DNS name"
assert_contains "${STATIC_SCRIPT}" 'curl --cacert "${KEYCLOAK_CA_FILE}"' "Static password-setup script must verify Keycloak with the mounted CA"
assert_not_contains "${STATIC_SCRIPT}" 'curl -k' "Static password-setup script must not disable TLS verification"
assert_not_contains "${STATIC_SCRIPT}" '--insecure' "Static password-setup script must not disable TLS verification"

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

# Test 7 above only proves the extracted script's logic works when given
# credentials via env vars -- it doesn't prove the Job manifest actually
# wires those env vars to the Secret. Check that structurally too.
assert_secret_key_ref "${STATIC_PASSWORD_JOB}" Job keycloak-set-passwords set-passwords ADMIN_USERNAME keycloak-admin-credentials admin-username "Static password-setup-job.yaml's ADMIN_USERNAME must source from keycloak-admin-credentials/admin-username"
assert_secret_key_ref "${STATIC_PASSWORD_JOB}" Job keycloak-set-passwords set-passwords ADMIN_PASSWORD keycloak-admin-credentials admin-password "Static password-setup-job.yaml's ADMIN_PASSWORD must source from keycloak-admin-credentials/admin-password"
assert_secret_key_ref "${STATIC_PASSWORD_JOB}" Job keycloak-set-passwords set-passwords DEFAULT_USER_PASSWORD keycloak-admin-credentials default-user-password "Static password-setup-job.yaml's DEFAULT_USER_PASSWORD must source from keycloak-admin-credentials/default-user-password"
assert_container_security_context "${STATIC_PASSWORD_JOB}" Job keycloak-set-passwords set-passwords "Static password setup job must use restricted container settings" true

STATIC_DATABASE_RBAC=$(<"${SCRIPT_DIR}/../prerequisites/keycloak/database/rbac.yaml")
STATIC_SERVICE_RBAC=$(<"${SCRIPT_DIR}/../prerequisites/keycloak/service/rbac.yaml")
assert_secret_role_scope "${STATIC_DATABASE_RBAC}" keycloak-database keycloak-database-password "Static database Role must scope Secret reads to its generated password"
assert_secret_role_scope "${STATIC_SERVICE_RBAC}" keycloak-service keycloak-client-secrets "Static service Role must scope Secret reads to its generated client credentials"

STATIC_DATABASE_STATEFULSET=$(<"${SCRIPT_DIR}/../prerequisites/keycloak/database/statefulset.yaml")
assert_container_security_context "${STATIC_DATABASE_STATEFULSET}" StatefulSet keycloak-database password-generator "Static database password generator must use restricted container settings"
assert_container_security_context "${STATIC_DATABASE_STATEFULSET}" StatefulSet keycloak-database server "Static database must run non-root with restricted container settings"
assert_pod_security_context "${STATIC_DATABASE_STATEFULSET}" StatefulSet keycloak-database false "Static database pod must leave UID allocation to OpenShift SCC"

echo "=== Test 8: static reference manifest's realm.json and resolve-realm-secrets coverage ==="
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
    REALM_ADMIN_PASSWORD="${SPECIAL_REALM_PASSWORD}" \
    OSAC_UI_URL="https://osac-ui-osac.apps.example.com" \
        bash "${TMP_DIR}/extracted-static-resolve.sh" >/dev/null 2>&1 || {
            fail "Static reference manifest's resolve-realm-secrets init container script exited non-zero"
        }

    if [[ -f "${TMP_DIR}/static-realm-resolved.json" ]]; then
        # See the chart resolver test above for why this avoids a $(...)
        # capture: it would silently strip a trailing "\n" from the password.
        EXPECTED_REALM_PASSWORD="${SPECIAL_REALM_PASSWORD}" \
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

echo "=== Test 9: static deployment.yaml sources credentials via secretKeyRef, not literals ==="
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
assert_container_security_context "${STATIC_DEPLOYMENT}" Deployment keycloak-service wait-for-database "Static database wait container must use restricted container settings"
assert_container_security_context "${STATIC_DEPLOYMENT}" Deployment keycloak-service resolve-realm-secrets "Static realm initializer must use restricted container settings"
assert_container_security_context "${STATIC_DEPLOYMENT}" Deployment keycloak-service keycloak "Static Keycloak server must run non-root with restricted container settings"
assert_pod_security_context "${STATIC_DEPLOYMENT}" Deployment keycloak-service false "Static Keycloak pod must leave UID allocation to OpenShift SCC"
assert_pod_security_context "${STATIC_PASSWORD_JOB}" Job keycloak-set-passwords false "Static password setup job must leave UID allocation to OpenShift SCC"

echo
if [[ "${FAILURES}" -gt 0 ]]; then
    echo "${FAILURES} check(s) failed."
    exit 1
fi
echo "All Keycloak credential parameterization checks passed."
