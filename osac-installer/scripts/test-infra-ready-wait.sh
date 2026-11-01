#!/usr/bin/env bash
# Checks wait_for_infra_ready (scripts/lib.sh) with a stub kubectl: it waits only
# for resources the profile created, passes the right namespace and condition,
# and fails when a resource does not become ready. No cluster is needed.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
WORK="$(mktemp -d)"
trap 'rm -rf "${WORK}"' EXIT
mkdir -p "${WORK}/bin"
export STUB_LOG="${WORK}/calls.log"

cat >"${WORK}/bin/kubectl" <<'STUB'
#!/usr/bin/env bash
echo "$*" >>"${STUB_LOG}"
case "$1" in
get)
    if [[ -n "${STUB_GET_ERR:-}" ]]; then
        echo "${STUB_GET_ERR}" >&2
        exit "${STUB_GET_RC:-1}"
    fi
    [[ -n "${STUB_GET_WARN:-}" ]] && echo "${STUB_GET_WARN}" >&2
    # Print the resource name only if the case lists it as existing.
    for existing in ${STUB_EXISTS:-}; do
        [[ "$2" == "${existing}" ]] && echo "${existing}"
    done
    exit 0
    ;;
wait)
    exit "${STUB_WAIT_RC:-0}"
    ;;
esac
STUB
chmod +x "${WORK}/bin/kubectl"
export PATH="${WORK}/bin:${PATH}"

# shellcheck source=lib.sh
source "${SCRIPT_DIR}/lib.sh"

fail() { echo "FAIL: $*" >&2; exit 1; }
waits() { grep -c '^wait ' "${STUB_LOG}" || true; }

# 1. Nothing created by the profile: no wait, success.
: >"${STUB_LOG}"
STUB_EXISTS="" wait_for_infra_ready kubectl 5 >/dev/null || fail "returned an error with nothing to wait for"
[[ "$(waits)" == 0 ]] || fail "waited for resources that do not exist: $(cat "${STUB_LOG}")"

# 2. Issuer and PostgreSQL exist, Kafka does not: two waits with their namespaces and conditions.
: >"${STUB_LOG}"
STUB_EXISTS="clusterissuer/default-ca deployment/postgres" wait_for_infra_ready kubectl 7 >/dev/null || fail "returned an error when everything became ready"
[[ "$(waits)" == 2 ]] || fail "expected 2 waits, got $(waits): $(cat "${STUB_LOG}")"
grep -qx 'wait --for=condition=Ready clusterissuer/default-ca --timeout=7s' "${STUB_LOG}" || fail "issuer wait is wrong: $(cat "${STUB_LOG}")"
grep -qx 'wait --for=condition=Available deployment/postgres -n osac-infra --timeout=7s' "${STUB_LOG}" || fail "postgres wait is wrong: $(cat "${STUB_LOG}")"
grep -q 'kafka' <(grep '^wait ' "${STUB_LOG}") && fail "waited for a Kafka that does not exist"

# 3. All three exist: Kafka is waited for in its own namespace.
: >"${STUB_LOG}"
STUB_EXISTS="clusterissuer/default-ca deployment/postgres kafka/osac-kafka" wait_for_infra_ready kubectl 9 >/dev/null || fail "returned an error when everything became ready"
grep -qx 'wait --for=condition=Ready kafka/osac-kafka -n osac-kafka --timeout=9s' "${STUB_LOG}" || fail "kafka wait is wrong: $(cat "${STUB_LOG}")"

# 4. A resource that does not become ready fails the step and says which one.
: >"${STUB_LOG}"
if out="$(STUB_EXISTS="deployment/postgres" STUB_WAIT_RC=1 wait_for_infra_ready kubectl 3 2>&1)"; then
    fail "a resource that never became ready did not fail the step"
fi
grep -q 'deployment/postgres did not reach condition=Available within 3s' <<<"${out}" || fail "error does not name the resource: ${out}"

# 5. Every lookup is bounded by a request timeout.
: >"${STUB_LOG}"
STUB_EXISTS="deployment/postgres" wait_for_infra_ready kubectl 5 >/dev/null || fail "returned an error when everything became ready"
[[ "$(grep -c '^get ' "${STUB_LOG}")" == 3 ]] || fail "expected 3 lookups: $(cat "${STUB_LOG}")"
[[ "$(grep '^get ' "${STUB_LOG}" | grep -c -- '--request-timeout=')" == 3 ]] || fail "a lookup has no request timeout: $(cat "${STUB_LOG}")"

# 6. A CRD that is not installed is an absent resource: skipped, not an error.
: >"${STUB_LOG}"
STUB_GET_ERR='error: the server doesn'"'"'t have a resource type "kafka"' wait_for_infra_ready kubectl 5 >/dev/null 2>&1 || fail "a missing CRD was treated as an error"
[[ "$(waits)" == 0 ]] || fail "waited for a resource whose CRD is missing"

# 7. A failed lookup (forbidden, API unreachable, timed out) is an error, not an absent resource.
for err in 'Error from server (Forbidden): clusterissuers.cert-manager.io "default-ca" is forbidden' \
           'The connection to the server 127.0.0.1:1 was refused' \
           'Unable to connect to the server: net/http: request canceled (Client.Timeout exceeded)'; do
    : >"${STUB_LOG}"
    if out="$(STUB_GET_ERR="${err}" wait_for_infra_ready kubectl 5 2>&1)"; then
        fail "a failed lookup was treated as an absent resource: ${err}"
    fi
    grep -q 'could not look up clusterissuer/default-ca' <<<"${out}" || fail "error does not name the resource: ${out}"
    grep -qF "${err}" <<<"${out}" || fail "error does not carry the cause: ${out}"
    [[ "$(waits)" == 0 ]] || fail "waited after a failed lookup"
done

# 8. A warning on stderr from a successful lookup of an absent object is not a resource.
: >"${STUB_LOG}"
STUB_GET_WARN="W1030 deprecated API" wait_for_infra_ready kubectl 5 >/dev/null 2>&1 || fail "a warning made the lookup fail"
[[ "$(waits)" == 0 ]] || fail "a stderr warning was read as an existing resource"

# 9. The install-infra recipe calls the wait in both branches, with that branch's CLI, after
# the osac-infra release. make -n expands each branch; the stubs answer its parse-time queries.
INSTALLER="$(cd "${SCRIPT_DIR}/.." && pwd)"
for c in oc kind helm; do
    cat >"${WORK}/bin/${c}" <<'STUB'
#!/usr/bin/env bash
echo "$*" >>"${STUB_LOG:-/dev/null}"
case "$*" in
"get clusterversion version -o jsonpath={.status.desired.version}") echo 4.22.5 ;;
"get ingresses.config/cluster -o jsonpath={.spec.domain}") echo apps.example.com ;;
esac
STUB
    chmod +x "${WORK}/bin/${c}"
done
for branch in "kind dev kubectl" "openshift bmaas-ci oc"; do
    read -r platform profile cli <<<"${branch}"
    plan="$(cd "${INSTALLER}" && make -n install-infra PLATFORM="${platform}" PROFILE="${profile}" NS=osac 2>&1)" \
        || fail "make -n install-infra failed for ${platform}: ${plan}"
    helm_line="$({ grep -n 'helm upgrade --install osac-infra' <<<"${plan}" || true; } | tail -1 | cut -d: -f1)"
    wait_line="$({ grep -n 'wait_for_infra_ready' <<<"${plan}" || true; } | cut -d: -f1)"
    [[ -n "${helm_line}" ]] || fail "${platform}: no osac-infra install in the recipe"
    [[ -n "${wait_line}" && "$(wc -w <<<"${wait_line}")" == 1 ]] || fail "${platform}: expected one readiness call, got: ${wait_line}"
    (( wait_line > helm_line )) || fail "${platform}: the readiness call is not after the osac-infra install"
    call="$(sed -n "${wait_line}p" <<<"${plan}")"
    [[ "${call}" == *"wait_for_infra_ready ${cli}'"* ]] || fail "${platform}: wrong CLI in: ${call}"
    # Run the recipe line itself: the CLI must be reached, and with none of the profile's
    # resources present it must succeed without waiting.
    : >"${STUB_LOG}"
    (cd "${INSTALLER}" && eval "${call}") >/dev/null 2>&1 || fail "${platform}: the recipe's readiness call failed"
    grep -q '^get clusterissuer/default-ca' "${STUB_LOG}" || fail "${platform}: the recipe's call never reached ${cli}: $(cat "${STUB_LOG}")"
    [[ "$(waits)" == 0 ]] || fail "${platform}: the recipe's call waited for resources that do not exist"
done

echo "Infra readiness wait checks passed."
