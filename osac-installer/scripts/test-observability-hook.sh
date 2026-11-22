#!/usr/bin/env bash

# Exercises the observability pre-install prerequisite gate without a cluster.
# The gate script is run from the chart's files/ directory; oc and getent are
# stubbed in a temporary PATH so every abort path and the success path are
# covered locally.

set -euo pipefail

SCRIPT_DIR=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
REPO_ROOT=$(cd -- "${SCRIPT_DIR}/../.." && pwd)
GATE="${REPO_ROOT}/osac-installer/charts/osac/files/validate-observability.sh"
TMP_DIR=$(mktemp -d)
trap 'rm -rf "${TMP_DIR}"' EXIT
STUB_DIR="${TMP_DIR}/bin"
mkdir -p "${STUB_DIR}"

fail() {
  echo "ERROR: $*" >&2
  exit 1
}

# The stubs answer from environment variables so each case states only what it
# changes.
cat >"${STUB_DIR}/oc" <<'STUB'
#!/usr/bin/env bash
set -euo pipefail
case "$*" in
  "get namespace osac-observability -o name")
    case "${STUB_NAMESPACE:-absent}" in
      present)
        echo "namespace/osac-observability"
        ;;
      forbidden)
        echo 'Error from server (Forbidden): namespaces "osac-observability" is forbidden' >&2
        exit 1
        ;;
      *)
        echo 'Error from server (NotFound): namespaces "osac-observability" not found' >&2
        exit 1
        ;;
    esac
    ;;
  "get secret "*" -n osac-observability")
    if [ "${STUB_SECRET:-missing}" != "present" ]; then
      name=$(printf '%s' "$*" | cut -d' ' -f3)
      echo "Error from server (NotFound): secrets \"${name}\" not found" >&2
      exit 1
    fi
    ;;
  "get --raw /apis/cert-manager.io/v1")
    if [ "${STUB_CERTMANAGER:-missing}" != "served" ]; then
      echo 'Error from server (NotFound): the server could not find the requested resource' >&2
      exit 1
    fi
    ;;
  *)
    echo "unexpected oc invocation: $*" >&2
    exit 2
    ;;
esac
STUB
chmod +x "${STUB_DIR}/oc"

cat >"${STUB_DIR}/getent" <<'STUB'
#!/usr/bin/env bash
set -euo pipefail
if [ "$2" = "no-such-host" ]; then
  exit 2
fi
echo "$2"
STUB
chmod +x "${STUB_DIR}/getent"

export PATH="${STUB_DIR}:${PATH}"
export OBS_SECRET_NAME=thanos-object-storage
export OBS_SECRET_NAMESPACE=osac-observability
export OBS_ENDPOINT=https://s3.example.com
export OBSERVABILITY_ENABLED=true

# expect_exit <name> <expected-status> <expected-message> <env assignments...>
expect_exit() {
  local name=$1
  local expected_status=$2
  local expected_message=$3
  shift 3

  local output="${TMP_DIR}/${name}.log"
  local status=0
  # shellcheck disable=SC2086 # the extra arguments are KEY=VALUE assignments
  env "$@" bash "${GATE}" >"${output}" 2>&1 || status=$?
  if [ "${status}" != "${expected_status}" ]; then
    echo "ERROR: ${name} exited ${status}, expected ${expected_status}" >&2
    cat "${output}" >&2
    exit 1
  fi
  if ! grep -Fq -- "${expected_message}" "${output}"; then
    echo "ERROR: ${name} did not report ${expected_message@Q}" >&2
    cat "${output}" >&2
    exit 1
  fi
  echo "ok: ${name}"
}

echo "checking the observability prerequisite gate"

expect_exit empty-secret 1 \
  'global.observability.objectStorage.secretName must name an existing Secret when observability is enabled' \
  OBS_SECRET_NAME=

expect_exit empty-endpoint 1 \
  'global.observability.objectStorage.endpoint must be set when observability is enabled' \
  OBS_ENDPOINT=

expect_exit missing-secret 1 \
  'observability object-storage Secret "thanos-object-storage" was not found in namespace "osac-observability"' \
  STUB_NAMESPACE=present STUB_SECRET=missing

expect_exit forbidden-namespace 1 \
  'could not read namespace "osac-observability"' \
  STUB_NAMESPACE=forbidden

# The first install runs before this release creates osac-observability, so the
# Secret cannot be read yet; the pre-upgrade run repeats the check.
expect_exit first-install 0 \
  'the object-storage Secret check runs on pre-upgrade' \
  STUB_NAMESPACE=absent STUB_CERTMANAGER=served

expect_exit unresolvable-endpoint 1 \
  'observability object-storage endpoint host "no-such-host" does not resolve' \
  STUB_NAMESPACE=present STUB_SECRET=present OBS_ENDPOINT=https://no-such-host/

expect_exit missing-cert-manager 1 \
  'the cert-manager.io/v1 API is not served; install cert-manager before enabling observability' \
  STUB_NAMESPACE=present STUB_SECRET=present STUB_CERTMANAGER=missing

expect_exit validated 0 'observability prerequisites validated' \
  STUB_NAMESPACE=present STUB_SECRET=present STUB_CERTMANAGER=served

expect_exit disabled 0 'observability is disabled; skipping prerequisites' \
  OBSERVABILITY_ENABLED=false

echo 'Observability prerequisite gate checks passed.'
