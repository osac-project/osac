#!/usr/bin/env bash

# The installer must reject invalid inputs before any cluster mutation or
# readiness wait, naming the offending field and an actionable remedy.
#
# Section A proves the wiring with stubbed helm/oc/kubectl/kind binaries: the
# offline render runs first, and a failing render aborts the install with no
# mutating command invoked -- including under `make -j`. It runs against a
# throwaway copy of the installer so chart-dependency recipes cannot touch
# this checkout's build artifacts.
#
# Section B proves the schema rejections with a real Helm render, asserting
# the offending field and the violated constraint so an unrelated rendering
# failure cannot satisfy a negative test.

set -euo pipefail

SCRIPT_DIR=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
INSTALLER_DIR=$(cd -- "${SCRIPT_DIR}/.." && pwd)
OSAC_CHART_DIR="${INSTALLER_DIR}/charts/osac"
INFRA_CHART_DIR="${INSTALLER_DIR}/charts/osac-infra"
HELM_BIN=${HELM_BIN:-helm}
TMP_DIR=$(mktemp -d)
trap 'rm -rf "${TMP_DIR}"' EXIT

# Print an error to stderr and abort the suite with a non-zero status.
fail() {
  echo "ERROR: $*" >&2
  exit 1
}

# ---------------------------------------------------------------------------
# Section A: gate ordering and no-mutation-on-failure (stubbed commands)
# ---------------------------------------------------------------------------

SANDBOX="${TMP_DIR}/installer"
STUB_BIN="${TMP_DIR}/bin"
LOG="${TMP_DIR}/commands.log"
cp -a "${INSTALLER_DIR}" "${SANDBOX}"
mkdir -p "${STUB_BIN}"
: >"${LOG}"

# Write an executable stub binary named $1 whose body is the heredoc on stdin.
write_stub() {
  local name=$1
  {
    echo '#!/usr/bin/env bash'
    cat
  } >"${STUB_BIN}/${name}"
  chmod +x "${STUB_BIN}/${name}"
}

# helm: log every invocation; MOCK_TEMPLATE_FAIL=1 makes offline renders fail
# the way a schema violation would. `version` feeds the Makefile's Helm
# major/minor detection.
write_stub helm <<'STUB'
printf 'helm %s\n' "$*" >>"${MOCK_LOG}"
case "$1" in
  version) echo "v3.18.0+gstub" ;;
  template)
    if [ "${MOCK_TEMPLATE_FAIL:-0}" = 1 ]; then
      echo "Error: values don't meet the specifications of the schema" >&2
      exit 1
    fi
    ;;
esac
exit 0
STUB

# oc: read-only lookups the gate performs before rendering (cluster version
# and ingress domain). Everything else is logged and succeeds.
write_stub oc <<'STUB'
printf 'oc %s\n' "$*" >>"${MOCK_LOG}"
case "$*" in
  *clusterversion*) echo "4.22.14" ;;
  *ingresses.config/cluster*) echo "apps.example.com" ;;
esac
exit 0
STUB

write_stub kubectl <<'STUB'
printf 'kubectl %s\n' "$*" >>"${MOCK_LOG}"
exit 0
STUB

write_stub kind <<'STUB'
printf 'kind %s\n' "$*" >>"${MOCK_LOG}"
exit 0
STUB

mutation_commands='helm upgrade|kind create|kubectl apply|oc create|oc apply'

# The first chart render must precede the first mutating command in every
# supported installation entry point's expansion.
assert_renders_first() {
  local target=$1
  shift
  local plan="${TMP_DIR}/${target//\//_}.plan"
  MOCK_LOG="${LOG}" PATH="${STUB_BIN}:${PATH}" \
    make -C "${SANDBOX}" --no-print-directory -n "${target}" "$@" \
    >"${plan}" 2>"${TMP_DIR}/plan.err" || fail "make -n ${target} $* failed"
  local first_render first_mutation
  first_render=$(grep -n 'helm template' "${plan}" | head -1 | cut -d: -f1)
  first_mutation=$(grep -nE "${mutation_commands}" "${plan}" | head -1 | cut -d: -f1)
  [ -n "${first_render}" ] || fail "${target} ($*): no offline render in the plan"
  if [ -n "${first_mutation}" ] && [ "${first_mutation}" -lt "${first_render}" ]; then
    fail "${target} ($*): a cluster mutation precedes the input validation render"
  fi
}

assert_renders_first install PLATFORM=kind PROFILE=dev NS=osac
assert_renders_first install PLATFORM=openshift PROFILE=dev NS=osac
assert_renders_first install-infra PLATFORM=kind PROFILE=dev NS=osac
assert_renders_first install-infra PLATFORM=openshift PROFILE=dev NS=osac
assert_renders_first install-osac PLATFORM=kind PROFILE=dev NS=osac
assert_renders_first install-osac PLATFORM=openshift PROFILE=dev NS=osac
assert_renders_first install-devstack PLATFORM=kind PROFILE=dev-full NS=osac
# install-multicluster guards its profile and remote kubeconfig at parse time.
REMOTE_KUBECONFIG="${LOG}" assert_renders_first install-multicluster \
  PLATFORM=openshift PROFILE=vmaas-ci NS=osac

# Entry points that never render the devstack chart must not build its
# external (awx-operator) dependencies: the gate adds no network dependency
# the entry point did not already have.
assert_no_devstack_deps() {
  local label=$1
  shift
  local plan="${TMP_DIR}/no-devstack-${label}.plan"
  MOCK_LOG="${LOG}" PATH="${STUB_BIN}:${PATH}" \
    make -C "${SANDBOX}" --no-print-directory -n "$@" \
    >"${plan}" 2>"${TMP_DIR}/no-devstack-${label}.err" \
    || fail "make -n $* failed"
  if grep -Eq 'awx-operator|helm-devstack-deps' "${plan}"; then
    fail "${label}: the gate builds devstack dependencies it never renders"
  fi
}

assert_no_devstack_deps kind-dev install PLATFORM=kind PROFILE=dev NS=osac
assert_no_devstack_deps openshift-osac install-osac PLATFORM=openshift \
  PROFILE=caas-ci NS=osac
assert_no_devstack_deps multicluster install-multicluster \
  PLATFORM=openshift PROFILE=vmaas-ci NS=osac REMOTE_KUBECONFIG="${LOG}"

# install-multicluster's gate must validate the multicluster infra overlay
# too: the recipe only passes it to the recursive install-infra, which runs
# after prepare-remote-cluster has already changed the remote cluster.
# Continuation lines are joined so the render command and its values flags
# match on one line, and only the plan region before the first recursive
# make is checked -- that is the gate, not the sub-make's own renders.
# Join make -n recipe continuation lines (trailing backslash) so a command
# and its flag lines can be matched as one logical line.
join_continuations() {
  awk '/\\$/ { sub(/\\$/, ""); acc = acc $0 " "; next }
       { print acc $0; acc = "" }'
}

mc_plan="${TMP_DIR}/multicluster.plan"
MOCK_LOG="${LOG}" PATH="${STUB_BIN}:${PATH}" \
  REMOTE_KUBECONFIG="${LOG}" \
  make -C "${SANDBOX}" --no-print-directory -n install-multicluster \
    PLATFORM=openshift PROFILE=vmaas-ci NS=osac \
    >"${mc_plan}" 2>/dev/null || fail "make -n install-multicluster failed"
sed -n '1,/prepare-remote-cluster/p' "${mc_plan}" | join_continuations \
  | grep 'helm template osac-infra' | grep -q 'multicluster-infra.yaml' \
  || fail "install-multicluster: the gate does not render the multicluster overlay"

# install-devstack rejects a wrong profile at parse time, before the gate's
# cluster lookups or renders, so the user sees the actual mistake.
if MOCK_LOG="${LOG}" PATH="${STUB_BIN}:${PATH}" \
  make -C "${SANDBOX}" --no-print-directory -n install-devstack \
    PLATFORM=kind PROFILE=dev NS=osac \
    >"${TMP_DIR}/devstack-guard.out" 2>"${TMP_DIR}/devstack-guard.err"; then
  fail "install-devstack accepted PROFILE=dev"
fi
grep -q 'install-devstack requires PROFILE=dev-full' \
  "${TMP_DIR}/devstack-guard.err" \
  || fail "install-devstack did not name the profile requirement"

# An invalid configuration must abort the install before any cluster
# mutation -- serially and in parallel.
assert_no_mutation_on_failure() {
  local label=$1
  shift
  : >"${LOG}"
  local out="${TMP_DIR}/fail-${label}.out" err="${TMP_DIR}/fail-${label}.err"
  if MOCK_LOG="${LOG}" MOCK_TEMPLATE_FAIL=1 PATH="${STUB_BIN}:${PATH}" \
    make -C "${SANDBOX}" --no-print-directory "$@" \
    >"${out}" 2>"${err}"; then
    fail "${label}: install succeeded despite invalid inputs"
  fi
  grep -q 'helm template' "${LOG}" || fail "${label}: the offline render never ran"
  if grep -Eq "${mutation_commands}" "${LOG}"; then
    fail "${label}: a cluster mutation ran despite invalid inputs"
  fi
  grep -q 'no cluster changes were made' "${err}" \
    || fail "${label}: the failure did not state that no cluster changes were made"
}

assert_no_mutation_on_failure serial install PLATFORM=kind PROFILE=dev NS=osac
assert_no_mutation_on_failure parallel install -j4 PLATFORM=kind PROFILE=dev NS=osac
assert_no_mutation_on_failure openshift install PLATFORM=openshift PROFILE=dev NS=osac
assert_no_mutation_on_failure direct-infra install-infra PLATFORM=openshift PROFILE=dev NS=osac
assert_no_mutation_on_failure direct-osac install-osac PLATFORM=kind PROFILE=dev NS=osac

echo "input-validation gate wiring passed"

# ---------------------------------------------------------------------------
# Section B: schema rejections with a real Helm render
# ---------------------------------------------------------------------------

COMMON_ARGS=(template osac "${OSAC_CHART_DIR}"
  --values "${OSAC_CHART_DIR}/ci/default-values.yaml")
INFRA_ARGS=(template osac-infra "${INFRA_CHART_DIR}"
  --values "${INSTALLER_DIR}/values/dev/kind-infra.yaml")

# Render the osac chart with the given extra helm args and expect success.
render_success() {
  local name=$1
  shift
  echo "checking ${name} (expected success)"
  "${HELM_BIN}" "${COMMON_ARGS[@]}" "$@" >/dev/null
}

# Render the osac-infra chart with the given extra helm args; expect success.
render_infra_success() {
  local name=$1
  shift
  echo "checking ${name} (expected success)"
  "${HELM_BIN}" "${INFRA_ARGS[@]}" "$@" >/dev/null
}

# Render the osac chart expecting failure, requiring stderr to match the
# pattern (the offending field and violated constraint) so an unrelated
# rendering failure cannot satisfy a negative test.
render_failure_matching() {
  local name=$1 pattern=$2
  shift 2
  local error="${TMP_DIR}/${name}.stderr"
  echo "checking ${name} (expected failure matching ${pattern@Q})"
  if "${HELM_BIN}" "${COMMON_ARGS[@]}" "$@" >/dev/null 2>"${error}"; then
    echo "ERROR: ${name} unexpectedly rendered successfully" >&2
    exit 1
  fi
  if ! grep -Eiq -- "${pattern}" "${error}"; then
    echo "ERROR: ${name} failed without the expected message pattern" >&2
    cat "${error}" >&2
    exit 1
  fi
}

# Render the osac-infra chart expecting failure with stderr matching the
# pattern; the infra counterpart of render_failure_matching.
render_infra_failure_matching() {
  local name=$1 pattern=$2
  shift 2
  local error="${TMP_DIR}/${name}.stderr"
  echo "checking ${name} (expected failure matching ${pattern@Q})"
  if "${HELM_BIN}" "${INFRA_ARGS[@]}" "$@" >/dev/null 2>"${error}"; then
    echo "ERROR: ${name} unexpectedly rendered successfully" >&2
    exit 1
  fi
  if ! grep -Eiq -- "${pattern}" "${error}"; then
    echo "ERROR: ${name} failed without the expected message pattern" >&2
    cat "${error}" >&2
    exit 1
  fi
}

# A valid configuration still renders end to end (AC: supported valid
# configurations are unaffected by the gate).
render_success valid-defaults
render_infra_success infra-valid-defaults

# Retry counts and POSIX IDs accept zero (no retries, root) but never
# negative values. Minimum-constraint wording differs across Helm
# versions: 3.18 prints "Must be greater than or equal to 0" while 3.22
# (what CI installs) prints "minimum: got -1, want 0"; patterns accept
# both, in the dotted-path (Helm 3) and JSON-pointer (newer) spellings.
render_failure_matching \
  backoff-limit-negative \
  '(aap\.bootstrap\.backoffLimit|/aap/bootstrap/backoffLimit).*(greater than or equal to 0|minimum: got -1, want 0)' \
  --set aap.bootstrap.backoffLimit=-1

render_success \
  backoff-limit-zero \
  --set aap.bootstrap.backoffLimit=0

for field in runAsUser runAsGroup fsGroup; do
  render_infra_failure_matching \
    "vault-${field}-negative" \
    "(bundledVault\\.podSecurityContext\\.${field}|/bundledVault/podSecurityContext/${field}).*(greater than or equal to 0|minimum: got -1, want 0)" \
    --set "bundledVault.podSecurityContext.${field}=-1"
done

render_infra_success \
  vault-runasuser-zero \
  --set bundledVault.podSecurityContext.runAsUser=0

# Chart-owned objects are closed: an unknown key is rejected instead of
# being silently ignored.
render_failure_matching \
  fulfillment-trust-unknown-key \
  '(global\.fulfillmentTrust|/global/fulfillmentTrust).*additional propert(y|ies).*unknownKey.*(is )?not allowed' \
  --set global.fulfillmentTrust.unknownKey=1

echo "input validation passed"
