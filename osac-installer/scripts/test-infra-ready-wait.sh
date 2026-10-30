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

echo "Infra readiness wait checks passed."
