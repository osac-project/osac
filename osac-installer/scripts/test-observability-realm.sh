#!/usr/bin/env bash

# Verifies the Keycloak realm provisioning carries the thanos-admin client and
# the cloud-provider-admin realm role, and that both the Helm resolver and the
# standalone prerequisite deployment know the client's secret key. The realm is
# imported by Keycloak, so a missing client or role is only discovered at login
# time; this check fails the chart instead.

set -euo pipefail

SCRIPT_DIR=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
REPO_ROOT=$(cd -- "${SCRIPT_DIR}/.." && pwd)
CHART_REALM="${REPO_ROOT}/charts/osac-infra/files/realm.json"
STANDALONE_REALM="${REPO_ROOT}/prerequisites/keycloak/service/files/realm.json"
RESOLVER="${REPO_ROOT}/charts/osac-infra/files/hooks/resolve-realm-secrets.sh"
STANDALONE_DEPLOYMENT="${REPO_ROOT}/prerequisites/keycloak/service/deployment.yaml"

for file in "${CHART_REALM}" "${STANDALONE_REALM}" "${RESOLVER}" "${STANDALONE_DEPLOYMENT}"; do
  if [ ! -f "${file}" ]; then
    echo "ERROR: ${file} does not exist" >&2
    exit 1
  fi
done

echo "checking the realm exports"
python3 - "${CHART_REALM}" "${STANDALONE_REALM}" <<'PY'
import json
import sys

# Client and role names the realm had before this story. Losing one of them
# would break an existing login path, and a duplicate name would make the
# import behave differently depending on which copy Keycloak applied first.
REQUIRED_CLIENTS = [
    "osac-cli",
    "osac-ui",
    "osac-controller",
    "osac-admin",
    "osac-ui-backend",
    "osac-csi-driver",
]
REQUIRED_ROLES = [
    "tenant-admin",
    "tenant-reader",
    "cloud-provider-reader",
    "tenant-user-manager",
    "tenant-idp-manager",
    "osac-csi-driver",
]


def fail(message):
    print(f"ERROR: {message}", file=sys.stderr)
    sys.exit(1)


def names(items, key):
    seen = []
    for item in items:
        name = item.get(key)
        if name in seen:
            fail(f"duplicate {key} {name!r}")
        seen.append(name)
    return seen


for path in sys.argv[1:]:
    with open(path, encoding="utf-8") as handle:
        realm = json.load(handle)

    clients = names(realm.get("clients", []), "clientId")
    roles = names(realm.get("roles", {}).get("realm", []), "name")

    for client in REQUIRED_CLIENTS:
        if client not in clients:
            fail(f"{path}: existing client {client!r} is missing")
    for role in REQUIRED_ROLES:
        if role not in roles:
            fail(f"{path}: existing realm role {role!r} is missing")

    if "thanos-admin" not in clients:
        fail(f"{path}: thanos-admin client missing")
    admin = next(c for c in realm["clients"] if c.get("clientId") == "thanos-admin")
    expected = {
        "enabled": True,
        "publicClient": False,
        "standardFlowEnabled": True,
        "implicitFlowEnabled": False,
        "directAccessGrantsEnabled": False,
        "serviceAccountsEnabled": False,
        "secret": "__THANOS_ADMIN_CLIENT_SECRET__",
        "protocol": "openid-connect",
    }
    for key, value in expected.items():
        if admin.get(key) != value:
            fail(f"{path}: thanos-admin.{key} is {admin.get(key)!r}, expected {value!r}")
    # The Route host is computed at install time from the cluster domain and
    # namespace, so the checked-in realm carries a placeholder that the
    # substitution hook replaces with the exact callback. A wildcard would let
    # any HTTPS origin finish the login and take the authorization code.
    if admin.get("redirectUris") != ["__THANOS_ADMIN_REDIRECT_URI__"]:
        fail(f"{path}: thanos-admin.redirectUris is {admin.get('redirectUris')!r}")

    if "cloud-provider-admin" not in roles:
        fail(f"{path}: cloud-provider-admin realm role missing")
    role = next(r for r in realm["roles"]["realm"] if r.get("name") == "cloud-provider-admin")
    if role.get("clientRole") is not False or role.get("composite") is not False:
        fail(f"{path}: cloud-provider-admin must be a plain realm role")
    if not str(role.get("description", "")).strip():
        fail(f"{path}: cloud-provider-admin needs a description")
    if not role.get("id"):
        fail(f"{path}: cloud-provider-admin needs an id")
PY

for file in "${RESOLVER}" "${STANDALONE_DEPLOYMENT}"; do
  echo "checking ${file} provisions the thanos-admin secret"
  grep -Fq -- '--from-literal=thanos-admin=' "${file}" ||
    { echo "ERROR: ${file} does not generate a thanos-admin client secret" >&2; exit 1; }
  grep -Fq -- '__THANOS_ADMIN_CLIENT_SECRET__' "${file}" ||
    { echo "ERROR: ${file} does not substitute __THANOS_ADMIN_CLIENT_SECRET__" >&2; exit 1; }
  grep -Fq -- '.data.thanos-admin' "${file}" ||
    { echo "ERROR: ${file} does not read the thanos-admin secret key" >&2; exit 1; }
done

# An existing Secret can predate this client, so the resolver has to add the
# key on upgrade instead of failing the import on an empty secret.
grep -Fq -- 'to add thanos-admin key' "${RESOLVER}" ||
  { echo "ERROR: ${RESOLVER} has no upgrade path for the thanos-admin key" >&2; exit 1; }

echo "checking both resolvers write the exact thanos-admin callback"
# The chart resolver computes the callback from the cluster domain and fails
# when the domain is empty; the standalone deployment takes the whole callback
# as an input and fails when it is empty. Both must substitute the placeholder
# rather than ship the checked-in value.
grep -Fq -- '__THANOS_ADMIN_REDIRECT_URI__' "${RESOLVER}" ||
  { echo "ERROR: ${RESOLVER} does not substitute __THANOS_ADMIN_REDIRECT_URI__" >&2; exit 1; }
grep -Fq -- 'thanos-query-admin-${OBS_NAMESPACE}.${CLUSTER_DOMAIN}/oauth2/callback' "${RESOLVER}" ||
  { echo "ERROR: ${RESOLVER} does not build the callback from the cluster domain" >&2; exit 1; }
grep -Fq -- 'CLUSTER_DOMAIN must be set' "${RESOLVER}" ||
  { echo "ERROR: ${RESOLVER} does not fail when the cluster domain is empty" >&2; exit 1; }
grep -Fq -- '__THANOS_ADMIN_REDIRECT_URI__' "${STANDALONE_DEPLOYMENT}" ||
  { echo "ERROR: ${STANDALONE_DEPLOYMENT} does not substitute __THANOS_ADMIN_REDIRECT_URI__" >&2; exit 1; }
grep -Fq -- 'THANOS_ADMIN_REDIRECT_URI must be set' "${STANDALONE_DEPLOYMENT}" ||
  { echo "ERROR: ${STANDALONE_DEPLOYMENT} does not fail when the callback is empty" >&2; exit 1; }

echo "checking the chart resolver writes the callback Keycloak must accept"
TMP_DIR=$(mktemp -d)
trap 'rm -rf "${TMP_DIR}"' EXIT
mkdir -p "${TMP_DIR}/bin"
cat >"${TMP_DIR}/bin/oc" <<'OC'
#!/usr/bin/env bash
# The resolver only reads the Secret; every key already exists, so it never
# creates or patches it.
if [[ "$*" == *"-o jsonpath="* ]]; then
  printf 'dGVzdA=='
fi
exit 0
OC
chmod +x "${TMP_DIR}/bin/oc"
for domain in apps.example.com ""; do
  out="${TMP_DIR}/realm-${domain:-empty}.json"
  status=0
  PATH="${TMP_DIR}/bin:${PATH}" \
    KEYCLOAK_NAMESPACE=keycloak \
    REALM_RAW_PATH="${CHART_REALM}" \
    REALM_OUTPUT_PATH="${out}" \
    OSAC_UI_URL=http://ui.osac.localhost:8080 \
    REALM_ADMIN_PASSWORD=test-password \
    OBS_NAMESPACE=osac-observability \
    CLUSTER_DOMAIN="${domain}" \
    bash "${RESOLVER}" >"${TMP_DIR}/resolver.out" 2>&1 || status=$?
  if [ -z "${domain}" ]; then
    if [ "${status}" -eq 0 ]; then
      echo "ERROR: ${RESOLVER} accepted an empty CLUSTER_DOMAIN" >&2
      exit 1
    fi
    grep -Fq -- 'CLUSTER_DOMAIN must be set' "${TMP_DIR}/resolver.out" ||
      { echo "ERROR: ${RESOLVER} did not explain the empty CLUSTER_DOMAIN" >&2; cat "${TMP_DIR}/resolver.out" >&2; exit 1; }
    continue
  fi
  if [ "${status}" -ne 0 ]; then
    echo "ERROR: ${RESOLVER} failed with CLUSTER_DOMAIN=${domain}" >&2
    cat "${TMP_DIR}/resolver.out" >&2
    exit 1
  fi
  python3 - "${out}" <<'PY'
import json
import sys

expected = "https://thanos-query-admin-osac-observability.apps.example.com/oauth2/callback"
with open(sys.argv[1], encoding="utf-8") as handle:
    realm = json.load(handle)
admin = next(c for c in realm["clients"] if c.get("clientId") == "thanos-admin")
if admin.get("redirectUris") != [expected]:
    print(f"ERROR: resolved thanos-admin.redirectUris is {admin.get('redirectUris')!r}, expected {expected!r}", file=sys.stderr)
    sys.exit(1)
PY
done

echo "Observability realm checks passed."
