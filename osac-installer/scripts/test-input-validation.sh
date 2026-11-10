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
DEVSTACK_CHART_DIR="${INSTALLER_DIR}/charts/osac-devstack"
HELM_BIN=${HELM_BIN:-helm}
TMP_DIR=$(mktemp -d)
trap 'rm -rf "${TMP_DIR}"' EXIT

python3 "${SCRIPT_DIR}/test-validation-diagnostics.py"

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

# Shared stub logger: append "name arg..." to MOCK_LOG. When MOCK_ARGV_LOG
# is set, also record one "name<TAB>arg" line per argument, so a caller's
# argument grouping can be asserted where the space-joined form cannot
# distinguish one argument from two.
write_stub mock-log <<'STUB'
#!/usr/bin/env bash
set -eu
name=$1
shift
printf '%s %s\n' "${name}" "$*" >>"${MOCK_LOG}"
if [ -n "${MOCK_ARGV_LOG:-}" ]; then
  for arg in "$@"; do
    printf '%s\t%s\n' "${name}" "${arg}" >>"${MOCK_ARGV_LOG}"
  done
fi
STUB

# helm: log every invocation; MOCK_TEMPLATE_FAIL=1 makes offline renders fail
# the way a schema violation would. `version` feeds the Makefile's Helm
# major/minor detection.
write_stub helm <<'STUB'
mock-log helm "$@"
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
mock-log oc "$@"
case "$*" in
  *clusterversion*) echo "4.22.14" ;;
  *ingresses.config/cluster*) echo "apps.example.com" ;;
esac
exit 0
STUB

write_stub kubectl <<'STUB'
mock-log kubectl "$@"
exit 0
STUB

write_stub kind <<'STUB'
mock-log kind "$@"
exit 0
STUB

mutation_commands='helm upgrade|kind create|kubectl apply|oc create|oc apply'

assert_prevalidation_commands() {
  local label=$1 command
  while IFS= read -r command; do
    case "${command}" in
      'helm version '*|'helm dependency build '*|'helm template '*) ;;
      'helm --repository-config /dev/null dependency build charts/osac') ;;
      'helm repo add topolvm https://topolvm.github.io/topolvm/ --force-update') ;;
      'helm repo add netbox https://netbox-community.github.io/netbox-chart/ --force-update') ;;
      'helm repo add awx-operator https://ansible-community.github.io/awx-operator-helm/ --force-update') ;;
      'oc get clusterversion version -o jsonpath={.status.desired.version}') ;;
      'oc get ingresses.config/cluster -o jsonpath={.spec.domain}') ;;
      *) fail "${label}: command not permitted before validation: ${command}" ;;
    esac
  done <"${LOG}"
}

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
  first_render=$(grep -nE 'helm template|render_chart_offline' "${plan}" \
    | head -1 | cut -d: -f1)
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

# The AAP license and the remote API address are static OSAC-install
# inputs: their checks must precede the first cluster mutation in every
# entry point that installs OSAC, including the composed `make install`
# (infra first), while install-infra alone must not require a license.
assert_static_inputs_first() {
  local target=$1
  shift
  local plan="${TMP_DIR}/${target//\//_}-static.plan"
  MOCK_LOG="${LOG}" PATH="${STUB_BIN}:${PATH}" \
    make -C "${SANDBOX}" --no-print-directory -n "${target}" "$@" \
    >"${plan}" 2>"${TMP_DIR}/static-plan.err" \
    || fail "make -n ${target} $* failed"
  local license_line address_line first_mutation
  license_line=$(grep -n 'AAP_LICENSE_FILE is required' "${plan}" | head -1 | cut -d: -f1)
  address_line=$(grep -n 'REMOTE_API_ADDRESS must use https' "${plan}" | head -1 | cut -d: -f1)
  first_mutation=$(grep -nE "${mutation_commands}" "${plan}" | head -1 | cut -d: -f1)
  [ -n "${license_line}" ] \
    || fail "${target} ($*): no license check in the plan"
  if [ -n "${first_mutation}" ] && [ "${license_line}" -gt "${first_mutation}" ]; then
    fail "${target} ($*): the license check follows a cluster mutation"
  fi
  if [ -n "${address_line}" ] && [ -n "${first_mutation}" ] \
    && [ "${address_line}" -gt "${first_mutation}" ]; then
    fail "${target} ($*): the remote address check follows a cluster mutation"
  fi
}

assert_static_inputs_first install PLATFORM=openshift PROFILE=dev NS=osac
assert_static_inputs_first install-osac PLATFORM=openshift PROFILE=dev NS=osac

# install-infra alone must not require the license: the check belongs to
# the OSAC install only.
infra_static_plan="${TMP_DIR}/infra-static.plan"
MOCK_LOG="${LOG}" PATH="${STUB_BIN}:${PATH}" \
  make -C "${SANDBOX}" --no-print-directory -n install-infra \
  PLATFORM=openshift PROFILE=dev NS=osac \
  >"${infra_static_plan}" 2>/dev/null \
  || fail "infra-static: the install-infra plan failed"
grep -q 'AAP_LICENSE_FILE' "${infra_static_plan}" \
  && fail "infra-static: install-infra requires the AAP license"

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
# The values list reaches recipes through the environment, so the plan
# shows the $${INFRA_VALUES_EXTRA} token; the exported value itself (the
# vmaas-ci base plus the multicluster overlay) is asserted from the make
# database, proving the gate's osac-infra render applies the overlay.
sed -n '1,/prepare-remote-cluster/p' "${mc_plan}" | join_continuations \
  | grep -E '(helm template|render_chart_offline) osac-infra' \
  | grep -Fq '${INFRA_VALUES_EXTRA}' \
  || fail "install-multicluster: the gate render omits the values-list token"
mc_db="${TMP_DIR}/multicluster.db"
MOCK_LOG="${LOG}" PATH="${STUB_BIN}:${PATH}" \
  REMOTE_KUBECONFIG="${LOG}" \
  make -C "${SANDBOX}" --no-print-directory -p -n install-multicluster \
    PLATFORM=openshift PROFILE=vmaas-ci NS=osac \
    >"${mc_db}" 2>/dev/null || fail "make -p -n install-multicluster failed"
grep -q '^INFRA_VALUES_EXTRA := .*multicluster-infra.yaml' "${mc_db}" \
  || fail "install-multicluster: the exported values list lacks the overlay"

# Gate/install parity for INFRA_HELM_ARGS: every helm command touching
# osac-infra or osac-deps in the OpenShift install plan -- the gate's
# renders, the EVPN deadline render, and the upgrades -- must apply the
# variable, like the Kind branch already does. Without it, the gate would
# validate a different value set than the install applies (e.g. an overlay
# at an invalid value overridden by INFRA_HELM_ARGS would pass the gate and
# fail the install).
infra_args_plan="${TMP_DIR}/infra-args.plan"
MOCK_LOG="${LOG}" PATH="${STUB_BIN}:${PATH}" \
  make -C "${SANDBOX}" --no-print-directory -n install-infra \
    PLATFORM=openshift PROFILE=caas-ci NS=osac \
    INFRA_HELM_ARGS='--set topolvm.enabled=true' \
    >"${infra_args_plan}" 2>/dev/null || fail "make -n install-infra (openshift) failed"
infra_arg_cmds=$(join_continuations <"${infra_args_plan}" \
  | sed -E 's/(helm (template|upgrade)|render_chart_offline)/\n\1/g' \
  | grep -E '(helm (template|upgrade)|render_chart_offline).* osac-(infra|deps) ' \
  || true)
[ -n "${infra_arg_cmds}" ] \
  || fail "install-infra (openshift): no osac-infra/osac-deps helm commands in the plan"
while IFS= read -r infra_cmd; do
  case "${infra_cmd}" in
    *topolvm.enabled=true*) ;;
    *) fail "install-infra (openshift): INFRA_HELM_ARGS missing from: ${infra_cmd}" ;;
  esac
done <<<"${infra_arg_cmds}"

# Gate/install parity for OIDC_IDP_CA_HELM_ARGS: every helm command
# touching osac-infra in the install plan -- the gate's render, the
# seed-check render, the EVPN deadline render, and the upgrade -- must
# apply the same CA input, so a CA file (or a values-file caPem it
# overrides) is validated identically everywhere and an unreadable or
# schema-breaking input fails before any cluster change.
for ca_platform in kind openshift; do
  ca_plan="${TMP_DIR}/oidc-ca-${ca_platform}.plan"
  MOCK_LOG="${LOG}" PATH="${STUB_BIN}:${PATH}" \
    make -C "${SANDBOX}" --no-print-directory -n install-infra \
    PLATFORM="${ca_platform}" PROFILE=dev NS=osac \
    OIDC_IDP_CA_FILE=/nonexistent-ca.pem \
    >"${ca_plan}" 2>/dev/null || fail "make -n install-infra (${ca_platform}) failed"
  ca_cmds=$(join_continuations <"${ca_plan}" \
    | sed -E 's/(helm (template|upgrade)|render_chart_offline)/\n\1/g' \
    | grep -E '(helm (template|upgrade)|render_chart_offline).* osac-infra ' \
    || true)
  [ -n "${ca_cmds}" ] \
    || fail "install-infra (${ca_platform}): no osac-infra helm commands in the plan"
  while IFS= read -r ca_cmd; do
    case "${ca_cmd}" in
      *--set-file\ keycloak.oidcIdpTrust.caPem*) ;;
      *) fail "install-infra (${ca_platform}): OIDC IdP CA input missing from: ${ca_cmd}" ;;
    esac
  done <<<"${ca_cmds}"
  join_continuations <"${ca_plan}" | grep 'netbox_seed_check osac-infra' \
    | grep -q -- '--set-file keycloak.oidcIdpTrust.caPem' \
    || fail "install-infra (${ca_platform}): the seed-check render omits the OIDC IdP CA input"
done

# Cluster-lookup output must reach Make only through a validating filter:
# the lookups feed $(eval), so a compromised or misbehaving API response
# carrying Make syntax must fail the gate instead of executing. The
# sentinel file proves no injection ran.
echo "checking injection (Make syntax in cluster lookups fails the gate)"
inj_bin="${TMP_DIR}/inj-bin"
cp -a "${STUB_BIN}" "${inj_bin}"
inj_sentinel="${TMP_DIR}/injection-ran"
cat >"${inj_bin}/oc" <<STUB
#!/usr/bin/env bash
case "\$*" in
  *clusterversion*) printf '4\$(shell touch \${INJECTION_SENTINEL}).22.14\n' ;;
  *ingresses.config/cluster*) printf '\$(shell touch \${INJECTION_SENTINEL})apps.example.com\n' ;;
esac
exit 0
STUB
chmod +x "${inj_bin}/oc"
: >"${LOG}"
if INJECTION_SENTINEL="${inj_sentinel}" MOCK_LOG="${LOG}" PATH="${inj_bin}:${PATH}" \
  make -C "${SANDBOX}" --no-print-directory validate-inputs \
  PLATFORM=openshift PROFILE=dev NS=osac \
  >"${TMP_DIR}/inj.out" 2>"${TMP_DIR}/inj.err"; then
  fail "injection: the gate accepted Make-syntax-bearing cluster lookups"
fi
[ ! -e "${inj_sentinel}" ] \
  || fail "injection: cluster-lookup output executed as Make code"
grep -q 'could not determine the OpenShift version' "${TMP_DIR}/inj.err" \
  || fail "injection: the failure did not name the version lookup"
assert_prevalidation_commands injection

# NS is interpolated into recipe command text after the gate, so the gate
# must reject a namespace carrying shell metacharacters before any of that
# text is expanded. The sentinel file proves nothing executed.
echo "checking ns-injection (metacharacters in NS fail the gate)"
ns_sentinel="${TMP_DIR}/ns-injection-ran"
: >"${LOG}"
if MOCK_LOG="${LOG}" PATH="${STUB_BIN}:${PATH}" \
  make -C "${SANDBOX}" --no-print-directory validate-inputs \
  PLATFORM=kind PROFILE=dev "NS=osac;touch ${ns_sentinel}" \
  >"${TMP_DIR}/ns.out" 2>"${TMP_DIR}/ns.err"; then
  fail "ns-injection: the gate accepted a metacharacter-bearing NS"
fi
[ ! -e "${ns_sentinel}" ] \
  || fail "ns-injection: NS executed as shell code"
grep -q 'NS must be a lowercase DNS label' "${TMP_DIR}/ns.err" \
  || fail "ns-injection: the failure did not name the namespace rule"
assert_prevalidation_commands ns-injection

# NS is frozen verbatim before it is exported, so a Make expression in a
# caller value must not expand at parse time -- before the gate can reject
# the value. The sentinel file proves no $(shell ...) side effect ran, and
# the gate must reject the raw expression as an invalid namespace.
echo "checking ns-injection-make (Make expressions in NS stay data)"
: >"${LOG}"
if MOCK_LOG="${LOG}" PATH="${STUB_BIN}:${PATH}" \
  make -C "${SANDBOX}" --no-print-directory validate-inputs \
  PLATFORM=kind PROFILE=dev "NS=osac\$(shell touch ${ns_sentinel}-make)" \
  >"${TMP_DIR}/nsmake.out" 2>"${TMP_DIR}/nsmake.err"; then
  fail "ns-injection-make: the gate accepted a Make-expression-bearing NS"
fi
[ ! -e "${ns_sentinel}-make" ] \
  || fail "ns-injection-make: a Make expression in NS executed"
grep -q 'NS must be a lowercase DNS label' "${TMP_DIR}/nsmake.err" \
  || fail "ns-injection-make: the failure did not name the namespace rule"
assert_prevalidation_commands ns-injection-make

# The NS freeze sits outside the parse-time MAKECMDGOALS gate: make
# auto-exports command-line variables by expanding them, so a goal the
# gate does not cover (install-dry-run) would otherwise run a Make
# expression in an unvalidated NS at recipe-launch time. The sentinel
# proves the expression never executed and the gate rejected the raw
# value instead, before any render ran.
echo "checking ns-injection-ungated (goals outside the parse-time gate)"
: >"${LOG}"
if MOCK_LOG="${LOG}" PATH="${STUB_BIN}:${PATH}" \
  make -C "${SANDBOX}" --no-print-directory install-dry-run \
  PLATFORM=kind PROFILE=dev "NS=osac\$(shell touch ${ns_sentinel}-ungated)" \
  >"${TMP_DIR}/nsung.out" 2>"${TMP_DIR}/nsung.err"; then
  fail "ns-injection-ungated: the gate accepted a Make-expression-bearing NS"
fi
[ ! -e "${ns_sentinel}-ungated" ] \
  || fail "ns-injection-ungated: a Make expression in NS executed"
grep -q 'NS must be a lowercase DNS label' "${TMP_DIR}/nsung.err" \
  || fail "ns-injection-ungated: the failure did not name the namespace rule"
assert_prevalidation_commands ns-injection-ungated

# Kubeconfig paths are caller-supplied data too: REMOTE_KUBECONFIG is
# frozen verbatim before the parse-time remote conditionals and
# REMOTE_CLUSTER_HELM_ARGS expand, and KUBECONFIG is frozen under an origin
# guard before the kind-file default expands it. Both reach recipes through
# the exported environment. The sentinel files prove no $(shell ...) side
# effect ran at parse or export time; the values stay valid inputs.
echo "checking kubeconfig-injection-make (Make expressions in kubeconfigs stay data)"
: >"${LOG}"
if MOCK_LOG="${LOG}" PATH="${STUB_BIN}:${PATH}" \
  make -C "${SANDBOX}" --no-print-directory validate-inputs \
  PLATFORM=openshift PROFILE=vmaas-ci NS=osac \
  "KUBECONFIG=/tmp/hub-$(date +%s)-$$\$(shell touch ${ns_sentinel}-kubeconfig)" \
  "REMOTE_KUBECONFIG=/tmp/remote-$(date +%s)-$$\$(shell touch ${ns_sentinel}-remote-kubeconfig)" \
  >"${TMP_DIR}/kubeconfig.out" 2>"${TMP_DIR}/kubeconfig.err"; then
  :
else
  fail "kubeconfig-injection-make: the gate rejected valid kubeconfig paths"
fi
[ ! -e "${ns_sentinel}-kubeconfig" ] \
  || fail "kubeconfig-injection-make: a Make expression in KUBECONFIG executed"
[ ! -e "${ns_sentinel}-remote-kubeconfig" ] \
  || fail "kubeconfig-injection-make: a Make expression in REMOTE_KUBECONFIG executed"
grep -q 'Installer input validation passed' "${TMP_DIR}/kubeconfig.out" \
  || fail "kubeconfig-injection-make: the gate did not complete"
assert_prevalidation_commands kubeconfig-injection-make

# An empty caller KUBECONFIG is not a path: the Makefile must fall back to
# the kind kubeconfig so helm never silently uses the home kubeconfig. The
# frozen value has origin "override", so the fallback assignment must be an
# override too; a plain assignment would leave the value empty.
echo "checking kubeconfig-empty-fallback (an empty KUBECONFIG uses the kind file)"
if ! KUBECONFIG= make -C "${SANDBOX}" --no-print-directory \
    --eval='print-kubeconfig: ; @echo $(KUBECONFIG)' \
    print-kubeconfig PLATFORM=kind PROFILE=dev NS=osac \
    >"${TMP_DIR}/kubeconfig-empty.out" 2>"${TMP_DIR}/kubeconfig-empty.err"; then
  fail "kubeconfig-empty-fallback: make rejected an empty KUBECONFIG"
fi
grep -q -- '-kind\.kubeconfig$' "${TMP_DIR}/kubeconfig-empty.out" \
  || fail "kubeconfig-empty-fallback: an empty KUBECONFIG did not use the kind file"

# Caller-supplied Helm args are data, never command text: EXTRA_HELM_ARGS
# is frozen verbatim (no Make expression in it expands, including at
# export time) and reaches recipes through the environment, so both Make
# syntax and shell metacharacters in a caller value must reach Helm as
# argument text instead of executing. The sentinel files prove nothing ran.
echo "checking extra-args-injection (metacharacters in EXTRA_HELM_ARGS stay data)"
extra_sentinel="${TMP_DIR}/extra-args-injection-ran"
: >"${LOG}"
if MOCK_LOG="${LOG}" PATH="${STUB_BIN}:${PATH}" \
  REMOTE_KUBECONFIG="${LOG}" \
  make -C "${SANDBOX}" --no-print-directory validate-inputs \
    PLATFORM=openshift PROFILE=vmaas-ci NS=osac \
    EXTRA_HELM_ARGS='--set operator.fulfillment.serverAddress=ok$(touch '"${extra_sentinel}"')-$$(touch '"${extra_sentinel}"'-2)' \
    >"${TMP_DIR}/extra-args.out" 2>"${TMP_DIR}/extra-args.err"; then
  :
else
  fail "extra-args-injection: the gate rejected a valid EXTRA_HELM_ARGS value"
fi
[ ! -e "${extra_sentinel}" ] \
  || fail "extra-args-injection: Make syntax in EXTRA_HELM_ARGS executed"
[ ! -e "${extra_sentinel}-2" ] \
  || fail "extra-args-injection: shell metacharacters in EXTRA_HELM_ARGS executed"
grep -q 'helm template osac ' "${LOG}" \
  || fail "extra-args-injection: the gate render never ran"
assert_prevalidation_commands extra-args-injection

# Caller-supplied Helm args keep shell-style grouping without shell-style
# evaluation: double-quoted, single-quoted, and backslash-escaped
# whitespace must reach Helm inside one argument, not split into a stray
# positional argument. The argv log records each argument on its own line,
# because the space-joined command log cannot show the difference.
echo "checking extra-args-quoting (quoted and escaped whitespace stay grouped)"
extra_argv_log="${TMP_DIR}/extra-args-argv.log"
: >"${LOG}"
: >"${extra_argv_log}"
if MOCK_LOG="${LOG}" MOCK_ARGV_LOG="${extra_argv_log}" PATH="${STUB_BIN}:${PATH}" \
  REMOTE_KUBECONFIG="${LOG}" \
  make -C "${SANDBOX}" --no-print-directory validate-inputs \
    PLATFORM=openshift PROFILE=vmaas-ci NS=osac \
    EXTRA_HELM_ARGS="--set-string operator.networkManagers.fabricManagers.cudn_net.description=\"two words\" --set-string operator.vendorControllers.acme.description=two\ escaped --set-string operator.vendorControllers.other.description='three word value'" \
    >"${TMP_DIR}/extra-quoting.out" 2>"${TMP_DIR}/extra-quoting.err"; then
  :
else
  fail "extra-args-quoting: the gate rejected a valid quoted EXTRA_HELM_ARGS value"
fi
grep -Fxq $'helm\toperator.networkManagers.fabricManagers.cudn_net.description=two words' \
  "${extra_argv_log}" \
  || fail "extra-args-quoting: a double-quoted value did not reach Helm as one argument"
grep -Fxq $'helm\toperator.vendorControllers.acme.description=two escaped' \
  "${extra_argv_log}" \
  || fail "extra-args-quoting: a backslash-escaped value did not reach Helm as one argument"
grep -Fxq $'helm\toperator.vendorControllers.other.description=three word value' \
  "${extra_argv_log}" \
  || fail "extra-args-quoting: a single-quoted value did not reach Helm as one argument"
assert_prevalidation_commands extra-args-quoting

# The dry-run path parses the same input the same way: `make install
# DRY_RUN=1` must not fail on a value that the gate accepted.
echo "checking extra-args-quoting-dry-run (quoted whitespace in the dry-run path)"
extra_dry_argv_log="${TMP_DIR}/extra-args-dry-run-argv.log"
: >"${LOG}"
: >"${extra_dry_argv_log}"
if MOCK_LOG="${LOG}" MOCK_ARGV_LOG="${extra_dry_argv_log}" PATH="${STUB_BIN}:${PATH}" \
  make -C "${SANDBOX}" --no-print-directory install-dry-run \
    PLATFORM=kind PROFILE=dev NS=osac \
    EXTRA_HELM_ARGS='--set-string operator.vendorControllers.acme.description="two words"' \
    >"${TMP_DIR}/extra-dry.out" 2>"${TMP_DIR}/extra-dry.err"; then
  :
else
  fail "extra-args-quoting-dry-run: the dry-run render rejected a quoted EXTRA_HELM_ARGS value"
fi
grep -Fxq $'helm\toperator.vendorControllers.acme.description=two words' \
  "${extra_dry_argv_log}" \
  || fail "extra-args-quoting-dry-run: the value did not reach Helm as one argument"

# An unterminated quote is a caller error with a value-free diagnostic: the
# gate must fail before any render, and the message must not echo the value
# (the argument may carry customer data).
echo "checking extra-args-quoting-error (an unbalanced quote fails safely)"
: >"${LOG}"
if MOCK_LOG="${LOG}" PATH="${STUB_BIN}:${PATH}" \
  make -C "${SANDBOX}" --no-print-directory validate-inputs \
    PLATFORM=kind PROFILE=dev NS=osac \
    EXTRA_HELM_ARGS='--set-string operator.vendorControllers.acme.description="unterminated SECRETVALUE' \
    >"${TMP_DIR}/extra-quote-error.out" 2>"${TMP_DIR}/extra-quote-error.err"; then
  fail "extra-args-quoting-error: the gate accepted an unbalanced quote"
fi
grep -q 'EXTRA_HELM_ARGS has an unterminated quote' "${TMP_DIR}/extra-quote-error.err" \
  || fail "extra-args-quoting-error: the failure did not name the unbalanced quote"
grep -q 'SECRETVALUE' "${TMP_DIR}/extra-quote-error.err" \
  && fail "extra-args-quoting-error: the diagnostic echoed the caller value"
grep -q 'helm template osac ' "${LOG}" \
  && fail "extra-args-quoting-error: the OSAC chart render ran despite the bad input"
assert_prevalidation_commands extra-args-quoting-error

# Caller-supplied values lists are data too: INFRA_VALUES_EXTRA is frozen
# and env-passed, so shell metacharacters in it word-split into Helm
# argument text (visible in the stub log) instead of executing.
echo "checking infra-values-injection (metacharacters in INFRA_VALUES_EXTRA stay data)"
infra_sentinel="${TMP_DIR}/infra-values-injection-ran"
: >"${LOG}"
if MOCK_LOG="${LOG}" PATH="${STUB_BIN}:${PATH}" \
  make -C "${SANDBOX}" --no-print-directory validate-inputs \
    PLATFORM=openshift PROFILE=dev NS=osac \
    "INFRA_VALUES_EXTRA=-f values/dev/infra.yaml; touch ${infra_sentinel}" \
    >"${TMP_DIR}/infra-args.out" 2>"${TMP_DIR}/infra-args.err"; then
  :
else
  fail "infra-values-injection: the gate rejected a valid INFRA_VALUES_EXTRA value"
fi
[ ! -e "${infra_sentinel}" ] \
  || fail "infra-values-injection: shell metacharacters in INFRA_VALUES_EXTRA executed"
grep -q -- '-f values/dev/infra.yaml; touch' "${LOG}" \
  || fail "infra-values-injection: the metacharacter-bearing list did not reach Helm as data"
assert_prevalidation_commands infra-values-injection

# Remote-cluster inputs: the gate must reject invalid static identifiers
# before any cluster mutation -- install-osac's own re-check runs only after
# install-infra and namespace creation have already changed the cluster.
remote_bad_out="${TMP_DIR}/remote-bad.out" remote_bad_err="${TMP_DIR}/remote-bad.err"
: >"${LOG}"
if MOCK_LOG="${LOG}" PATH="${STUB_BIN}:${PATH}" \
  make -C "${SANDBOX}" --no-print-directory install-osac \
    PLATFORM=openshift PROFILE=vmaas-ci NS=osac \
    REMOTE_KUBECONFIG="${LOG}" REMOTE_KUBECONFIG_SECRET_NAME='Bad_Name' \
    >"${remote_bad_out}" 2>"${remote_bad_err}"; then
  fail "remote-identifier: install-osac succeeded with an invalid secret name"
fi
grep -q 'REMOTE_KUBECONFIG_SECRET_NAME must be a DNS subdomain' "${remote_bad_err}" \
  || fail "remote-identifier: the failure did not name the identifier rule"
assert_prevalidation_commands remote-identifier

# The identifiers reach the gate's validator through the environment, not
# through Make expansion into its quoted bash -c string: a quote-bearing
# value is rejected as an invalid DNS subdomain instead of breaking out of
# the quoting and running as shell code. The mutation log doubles as the
# canary: the injected command is an oc create, so execution would show.
: >"${LOG}"
if MOCK_LOG="${LOG}" PATH="${STUB_BIN}:${PATH}" \
  make -C "${SANDBOX}" --no-print-directory install-osac \
    PLATFORM=openshift PROFILE=vmaas-ci NS=osac \
    REMOTE_KUBECONFIG="${LOG}" \
    REMOTE_KUBECONFIG_SECRET_NAME="a'; oc create namespace pwned; '" \
    >"${TMP_DIR}/remote-inj.out" 2>"${TMP_DIR}/remote-inj.err"; then
  fail "remote-injection: install-osac succeeded with a quote-bearing name"
fi
grep -q 'REMOTE_KUBECONFIG_SECRET_NAME must be a DNS subdomain' "${TMP_DIR}/remote-inj.err" \
  || fail "remote-injection: the value did not reach the validator as a literal"
assert_prevalidation_commands remote-injection

# The AAP license is a static input to the OSAC install only: `make
# install` composes install-infra before install-osac, so a missing
# license must be rejected before install-infra creates namespaces and
# waits for readiness.
echo "checking license-before-infra (a missing license fails before install-infra)"
: >"${LOG}"
if MOCK_LOG="${LOG}" PATH="${STUB_BIN}:${PATH}" \
  make -C "${SANDBOX}" --no-print-directory install \
    PLATFORM=openshift PROFILE=dev NS=osac \
    AAP_LICENSE_FILE="${TMP_DIR}/missing-license.zip" \
    >"${TMP_DIR}/license-missing.out" 2>"${TMP_DIR}/license-missing.err"; then
  fail "license-before-infra: make install succeeded with a missing license"
fi
grep -q 'AAP_LICENSE_FILE not found' "${TMP_DIR}/license-missing.err" \
  || fail "license-before-infra: the failure did not name the license"
if grep -Eq "${mutation_commands}" "${LOG}"; then
  fail "license-before-infra: a cluster mutation ran before the license check"
fi

# The remote API address is static too: a non-https value must fail in
# validate-osac-inputs, before install-osac creates the namespace (the
# kubeconfig readability and Secret staging stay at install time).
echo "checking remote-address-scheme (a non-https remote address fails before mutations)"
: >"${LOG}"
: >"${TMP_DIR}/license.zip"
if MOCK_LOG="${LOG}" PATH="${STUB_BIN}:${PATH}" \
  make -C "${SANDBOX}" --no-print-directory install-osac \
    PLATFORM=openshift PROFILE=vmaas-ci NS=osac \
    AAP_LICENSE_FILE="${TMP_DIR}/license.zip" \
    REMOTE_KUBECONFIG="${LOG}" \
    REMOTE_API_ADDRESS="http://insecure.internal.example.com" \
    >"${TMP_DIR}/addr-bad.out" 2>"${TMP_DIR}/addr-bad.err"; then
  fail "remote-address-scheme: install-osac succeeded with a non-https address"
fi
grep -q 'REMOTE_API_ADDRESS must use https' "${TMP_DIR}/addr-bad.err" \
  || fail "remote-address-scheme: the failure did not name the address rule"
if grep -Eq "${mutation_commands}" "${LOG}"; then
  fail "remote-address-scheme: a cluster mutation ran before the address check"
fi
assert_prevalidation_commands remote-address-scheme

# The gate's osac-chart render and install-osac's upgrade share the same
# remote-cluster kubeconfig argument construction (REMOTE_CLUSTER_HELM_ARGS),
# so the gate validates the final merged values the install applies.
remote_plan="${TMP_DIR}/remote-args.plan"
MOCK_LOG="${LOG}" PATH="${STUB_BIN}:${PATH}" \
  REMOTE_KUBECONFIG="${LOG}" \
  make -C "${SANDBOX}" --no-print-directory -n install-osac \
    PLATFORM=openshift PROFILE=vmaas-ci NS=osac \
    >"${remote_plan}" 2>/dev/null || fail "make -n install-osac (remote) failed"
join_continuations <"${remote_plan}" | grep 'render_osac_chart_offline' \
  | grep -q 'operator.remoteClusterKubeconfigSecretName=osac-remote-kubeconfig' \
  || fail "validate-inputs: the gate render omits the remote-cluster args"
join_continuations <"${remote_plan}" | grep 'helm upgrade --install osac ' \
  | grep -q 'operator.remoteClusterKubeconfigSecretName=osac-remote-kubeconfig' \
  || fail "install-osac: the upgrade omits the remote-cluster args"

# The remote-cluster flags must also resolve in the same order everywhere:
# the gate's render applies them after EXTRA_HELM_ARGS like install-osac's
# upgrade does, so a caller overriding a remote-cluster flag through
# EXTRA_HELM_ARGS sees the gate validate exactly what the install applies
# (Helm's last --set-string for a key wins).
remote_order_plan="${TMP_DIR}/remote-order.plan"
MOCK_LOG="${LOG}" PATH="${STUB_BIN}:${PATH}" \
  REMOTE_KUBECONFIG="${LOG}" \
  make -C "${SANDBOX}" --no-print-directory -n install-osac \
    PLATFORM=openshift PROFILE=vmaas-ci NS=osac \
    EXTRA_HELM_ARGS='--set-string operator.remoteClusterKubeconfigSecretName=caller-override' \
    >"${remote_order_plan}" 2>/dev/null || fail "make -n install-osac (remote order) failed"
for remote_cmd in 'render_osac_chart_offline' 'helm upgrade --install osac '; do
  join_continuations <"${remote_order_plan}" | grep "${remote_cmd}" \
    | grep -Eq '\$\{EXTRA_HELM_ARGS\}.*operator\.remoteClusterKubeconfigSecretName=osac-remote-kubeconfig' \
    || fail "remote-flag order: ${remote_cmd} does not apply EXTRA_HELM_ARGS before the remote-cluster flags"
done
# The caller-supplied override itself must flow to Helm at runtime: with
# stubbed Helm the gate succeeds and the render runs with the args in the
# environment (the wiring above pins where the token is applied).
: >"${LOG}"
MOCK_LOG="${LOG}" PATH="${STUB_BIN}:${PATH}" \
  REMOTE_KUBECONFIG="${LOG}" \
  make -C "${SANDBOX}" --no-print-directory validate-inputs \
    PLATFORM=openshift PROFILE=vmaas-ci NS=osac \
    EXTRA_HELM_ARGS='--set-string operator.remoteClusterKubeconfigSecretName=caller-override' \
    >"${TMP_DIR}/remote-order.out" 2>"${TMP_DIR}/remote-order.err" \
    || fail "remote-flag order: the gate rejected a valid EXTRA_HELM_ARGS override"
grep -q 'helm template osac ' "${LOG}" \
  || fail "remote-flag order: the gate render never ran with the override"

# The gate's osac-chart render goes through render_osac_chart_offline with
# the render-only placeholder token: the function applies the placeholder
# only when the merged render has no NetBox token at all, never over a
# caller-supplied value from --set, --set-string, or a values file. The
# bridge is legitimate only when this install's infra values deploy the
# netbox-token-seed hook, so the gate runs netbox_seed_check first, in the
# same recipe line (make recipe lines are separate shells).
join_continuations <"${remote_plan}" | grep 'render_osac_chart_offline' \
  | grep -q 'offline-validation-placeholder' \
  || fail "validate-inputs: the gate render must bridge the NetBox token via render_osac_chart_offline"
join_continuations <"${remote_plan}" | grep 'netbox_seed_check osac-infra' \
  | grep -q 'render_osac_chart_offline' \
  || fail "validate-inputs: the gate must run netbox_seed_check before the bridged render"
# The seed-check render must apply the same cluster-specific overrides as
# the gate's own osac-infra render (and install-infra's upgrade): an
# overlay value the install overrides on the command line -- for example
# an empty bundledVault.keycloakIssuerUrl filled in with the cluster
# domain -- must not fail the seed check alone, which would silently
# disable the token bridge and reject a valid seeded install with a
# misleading missing-token error. Make expands the cluster lookups into
# the printed recipe (the stubbed oc answers 4.22.14 and
# apps.example.com), so the expected values are the expanded ones.
for seed_override in \
  '--set lvms.channel=stable-4.22' \
  '--set keycloak.hostname=https://keycloak-keycloak.apps.example.com' \
  '--set keycloak.route.hostname=keycloak-keycloak.apps.example.com' \
  '--set keycloak.uiUrl=https://osac-ui-osac.apps.example.com' \
  '--set "bundledVault.keycloakIssuerUrl=https://keycloak-keycloak.apps.example.com/realms/osac"'; do
  join_continuations <"${remote_plan}" | grep 'netbox_seed_check osac-infra' \
    | grep -Fq -- "${seed_override}" \
    || fail "validate-inputs: the seed-check render omits ${seed_override}"
done

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
  assert_prevalidation_commands "${label}"
  grep -q 'no cluster changes were made' "${err}" \
    || fail "${label}: the failure did not state that no cluster changes were made"
}

assert_no_mutation_on_failure serial install PLATFORM=kind PROFILE=dev NS=osac
assert_no_mutation_on_failure parallel install -j4 PLATFORM=kind PROFILE=dev NS=osac
assert_no_mutation_on_failure openshift install PLATFORM=openshift PROFILE=dev NS=osac
assert_no_mutation_on_failure direct-infra install-infra PLATFORM=openshift PROFILE=dev NS=osac
assert_no_mutation_on_failure direct-osac install-osac PLATFORM=kind PROFILE=dev NS=osac
assert_no_mutation_on_failure multicluster install-multicluster \
  PLATFORM=openshift PROFILE=vmaas-ci NS=osac REMOTE_KUBECONFIG="${LOG}"

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
  echo "checking ${name} (expected failure matching '${pattern}')"
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
  echo "checking ${name} (expected failure matching '${pattern}')"
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

# runAsUser 0 with the default runAsNonRoot true only fails at
# provisioning -- the kubelet refuses to start a root container flagged
# non-root -- so the schema rejects the combination up front. UID 0
# stays allowed when runAsNonRoot is explicitly false. The `not` keyword
# wording differs by Helm version: 3.18 prints "Must not validate the
# schema (not)" with the dotted path while 3.22 (what CI installs)
# prints "'not' failed" with a JSON-pointer path; the pattern accepts
# both, in the dotted-path (Helm 3) and JSON-pointer (newer) spellings.
render_infra_failure_matching \
  vault-runasuser-zero-nonroot \
  '(bundledVault\.podSecurityContext|/bundledVault/podSecurityContext).*(not validate|not. failed)' \
  --set bundledVault.podSecurityContext.runAsUser=0

render_infra_success \
  vault-runasuser-zero-root \
  --set bundledVault.podSecurityContext.runAsUser=0 \
  --set bundledVault.podSecurityContext.runAsNonRoot=false

render_infra_success \
  vault-runasgroup-zero \
  --set bundledVault.podSecurityContext.runAsGroup=0

render_infra_success \
  vault-fsgroup-zero \
  --set bundledVault.podSecurityContext.fsGroup=0

# Chart-owned objects are closed: an unknown key is rejected instead of
# being silently ignored.
render_failure_matching \
  fulfillment-trust-unknown-key \
  '(global\.fulfillmentTrust|/global/fulfillmentTrust).*additional propert(y|ies).*unknownKey.*(is )?not allowed' \
  --set global.fulfillmentTrust.unknownKey=1

# bmaas-netbox-ci is a valid profile the gate must not block: it relies on
# the infra chart's netbox-token-seed hook to create the NetBox token Secret
# during a real install, and offline rendering cannot see cluster Secrets
# (helm template's lookup returns nil), so the raw render fails the
# lookup-dependent branch. The gate bridges exactly that failure through
# render_osac_chart_offline (scripts/lib.sh) with a render-only placeholder
# (NETBOX_OFFLINE_PLACEHOLDER in the Makefile, generated at runtime there;
# the plan assertion above matches only its prefix) that is never passed to
# an installation -- and only when
# netbox_seed_check first saw this install's infra values deploy the seed
# hook. The hostname --sets mirror the OpenShift gate branch -- the
# profile's supported platform; on Kind the install itself rejects the
# profile's empty hostnames, gate and install alike.
NETBOX_PROFILE_ARGS=(
  -f "${INSTALLER_DIR}/values/bmaas-ci/instance.yaml"
  -f "${INSTALLER_DIR}/values/bmaas-netbox-ci/instance.yaml"
  --set service.externalHostname=fulfillment-api-osac.example.com
  --set service.internalHostname=fulfillment-internal-api-osac.example.com
  --set global.clusterDomain=example.com
)

render_failure_matching \
  netbox-profile-raw-render \
  'netbox\.tokenValue is required when Secret' \
  "${NETBOX_PROFILE_ARGS[@]}"

# The gate's osac-chart render goes through render_osac_chart_offline, so
# the tests below exercise the same code path the gate uses.
source "${SCRIPT_DIR}/lib.sh"
netbox_gate_err="${TMP_DIR}/netbox-gate.err"
# The render-only placeholder mirrors the Makefile's runtime-generated
# NETBOX_OFFLINE_PLACEHOLDER; only its prefix is asserted against the
# gate's plan above, so the two values need not match. Generated here so
# no token-shaped literal sits in the script.
offline_token="offline-validation-placeholder-$(date +%s)-$$"

# The unseeded bridge path (a pre-existing token Secret) is tested
# hermetically: stub CLIs stand in for the cluster lookup, so no developer
# cluster or login state can flip the result. The kubectl stub logs every
# invocation and answers "present" only for the exact Secret identity and
# key the lookup asks for; the oc stub never answers, so the kubectl path
# is what runs (lib.sh falls back to oc only when kubectl is unavailable).
netbox_cli_bin="${TMP_DIR}/netbox-cli"
mkdir -p "${netbox_cli_bin}"
netbox_cli_log="${TMP_DIR}/netbox-cli.log"
cat >"${netbox_cli_bin}/kubectl" <<'STUB'
#!/usr/bin/env bash
printf 'kubectl %s\n' "$*" >>"${NETBOX_CLI_LOG}"
[ "${NETBOX_CLI_SECRET_PRESENT:-0}" = "1" ] || exit 0
[ "$1 $2 $3 $4 $5" = "get secret ${NETBOX_CLI_NAME} -n ${NETBOX_CLI_NS}" ] || exit 0
for arg in "$@"; do
  case "${arg}" in
    *"index .data \"${NETBOX_CLI_KEY}\""*) echo present; exit 0 ;;
  esac
done
exit 0
STUB
cat >"${netbox_cli_bin}/oc" <<'STUB'
#!/usr/bin/env bash
printf 'oc %s\n' "$*" >>"${NETBOX_CLI_LOG}"
exit 1
STUB
chmod +x "${netbox_cli_bin}/kubectl" "${netbox_cli_bin}/oc"

# netbox_seed_check decides whether the bridge is legitimate at all: the
# netbox profile's infra values deploy the seed hook, bmaas-ci's do not.
echo "checking netbox-seed-hook-present (the netbox profile's infra deploys it)"
netbox_seed_check osac-infra "${INFRA_CHART_DIR}" \
  --values "${INSTALLER_DIR}/values/bmaas-ci/infra.yaml" \
  --values "${INSTALLER_DIR}/values/bmaas-netbox-ci/infra.yaml" \
  --set osacNamespace=osac
[ "${NETBOX_TOKEN_SEEDED}" = "1" ] \
  || fail "netbox-seed-hook-present: the netbox profile's infra render deploys no netbox-token-seed"
[ "${NETBOX_SEED_SECRET_NAME}" = "netbox-api-token" ] \
  || fail "netbox-seed-hook-present: wrong seed Secret name ${NETBOX_SEED_SECRET_NAME}"
[ "${NETBOX_SEED_SECRET_NS}" = "osac" ] \
  || fail "netbox-seed-hook-present: wrong seed Secret namespace ${NETBOX_SEED_SECRET_NS}"
[ "${NETBOX_SEED_SECRET_KEY}" = "token" ] \
  || fail "netbox-seed-hook-present: wrong seed Secret key ${NETBOX_SEED_SECRET_KEY}"

echo "checking netbox-seed-hook-absent (bmaas-ci infra deploys none)"
netbox_seed_check osac-infra "${INFRA_CHART_DIR}" \
  --values "${INSTALLER_DIR}/values/bmaas-ci/infra.yaml" \
  --set osacNamespace=osac
[ "${NETBOX_TOKEN_SEEDED}" = "0" ] \
  || fail "netbox-seed-hook-absent: bmaas-ci infra unexpectedly seeds the token"

# The bridge: only the specific lookup failure above is retried with the
# placeholder, and only for a seeded install whose seed-hook Secret
# identity matches what the osac chart reads, so the profile renders like
# the gate does. The render runs in the osac namespace like the gate's
# own --namespace $(NS).
echo "checking netbox-profile-gate-placeholder (bridged render)"
netbox_seed_check osac-infra "${INFRA_CHART_DIR}" \
  --values "${INSTALLER_DIR}/values/bmaas-ci/infra.yaml" \
  --values "${INSTALLER_DIR}/values/bmaas-netbox-ci/infra.yaml" \
  --set osacNamespace=osac
render_osac_chart_offline "${offline_token}" \
  osac "${OSAC_CHART_DIR}" --values "${OSAC_CHART_DIR}/ci/default-values.yaml" \
  --namespace osac \
  "${NETBOX_PROFILE_ARGS[@]}" \
  || fail "netbox-profile-gate-placeholder: the bridged render failed"

# The bridge must verify the seed hook's Secret identity: a caller override
# that diverges from the seeded Secret (here the key, via
# bmf.netbox.tokenSecretKey) would pass a bridged render yet fail the real
# install only after install-infra changed the cluster. The gate fails
# instead, naming the differing identity fields. The mismatching values
# are internal-hostname-shaped on purpose: Secret names and keys are
# customer-supplied identifiers, and the gate's errors reach the terminal
# and CI logs, so the values themselves must stay out of the message.
echo "checking netbox-key-mismatch (seeded key differs from the chart's key)"
if render_osac_chart_offline "${offline_token}" \
    osac "${OSAC_CHART_DIR}" --values "${OSAC_CHART_DIR}/ci/default-values.yaml" \
    --namespace osac \
    "${NETBOX_PROFILE_ARGS[@]}" \
    --set bmf.netbox.tokenSecretKey=token.netbox.internal.example.com \
    2>"${netbox_gate_err}"; then
  fail "netbox-key-mismatch: the gate bridged a mismatched Secret key"
fi
grep -q 'identity mismatch' "${netbox_gate_err}" \
  || fail "netbox-key-mismatch: the failure did not name the identity mismatch"
grep -Fq 'differing: key' "${netbox_gate_err}" \
  || fail "netbox-key-mismatch: the failure did not name the differing field"
grep -Fq 'token.netbox.internal.example.com' "${netbox_gate_err}" \
  && fail "netbox-key-mismatch: the failure echoed the divergent key"

echo "checking netbox-name-mismatch (seeded name differs from the chart's name)"
if render_osac_chart_offline "${offline_token}" \
    osac "${OSAC_CHART_DIR}" --values "${OSAC_CHART_DIR}/ci/default-values.yaml" \
    --namespace osac \
    "${NETBOX_PROFILE_ARGS[@]}" \
    --set bmf.secrets.netboxToken=netbox.internal.example.com \
    2>"${netbox_gate_err}"; then
  fail "netbox-name-mismatch: the gate bridged a mismatched Secret name"
fi
grep -q 'identity mismatch' "${netbox_gate_err}" \
  || fail "netbox-name-mismatch: the failure did not name the identity mismatch"
grep -Fq 'differing: name' "${netbox_gate_err}" \
  || fail "netbox-name-mismatch: the failure did not name the differing field"
grep -Fq 'netbox.internal.example.com' "${netbox_gate_err}" \
  && fail "netbox-name-mismatch: the failure echoed the divergent Secret name"

# Without the seed hook nothing creates the token Secret, so the same
# lookup failure is a real input error unless the Secret already exists in
# the cluster: the bridge must not paper over a genuinely missing token,
# or the gate would pass an install that fails only after install-infra
# changed the cluster. The stub CLIs answer nothing here (no Secret). The
# gate names the constraint without the chart's raw error, which embeds
# the customer-supplied Secret name and key.
echo "checking netbox-unseeded-bridge (no seed hook and no Secret: the token error is real)"
if PATH="${netbox_cli_bin}:${PATH}" NETBOX_CLI_LOG="${netbox_cli_log}" \
    NETBOX_TOKEN_SEEDED=0 render_osac_chart_offline "${offline_token}" \
    osac "${OSAC_CHART_DIR}" --values "${OSAC_CHART_DIR}/ci/default-values.yaml" \
    --namespace osac \
    "${NETBOX_PROFILE_ARGS[@]}" \
    --set bmf.secrets.netboxToken=netbox.internal.example.com \
    2>"${netbox_gate_err}"; then
  fail "netbox-unseeded-bridge: the gate bridged a missing token with no seed hook"
fi
grep -Eiq 'netbox\.tokenValue is required when the NetBox token Secret' \
  "${netbox_gate_err}" \
  || fail "netbox-unseeded-bridge: the failure did not name the missing token"
grep -Fq 'netbox.internal.example.com' "${netbox_gate_err}" \
  && fail "netbox-unseeded-bridge: the failure echoed the Secret name"

# A token Secret that already exists in the cluster is the other
# legitimate unseeded source: `helm upgrade` reads it through lookup at
# install time even though `helm template` cannot see it offline, so the
# gate verifies it with a read-only cluster lookup instead of rejecting a
# valid install. The stub answers only for the exact identity the chart
# renders (netbox-api-token in osac, key token), proving the lookup uses
# the effective values, and the log proves it is read-only.
echo "checking netbox-existing-secret (a pre-existing token Secret passes the gate)"
: >"${netbox_cli_log}"
if ! PATH="${netbox_cli_bin}:${PATH}" NETBOX_CLI_LOG="${netbox_cli_log}" \
    NETBOX_CLI_SECRET_PRESENT=1 NETBOX_CLI_NAME=netbox-api-token \
    NETBOX_CLI_NS=osac NETBOX_CLI_KEY=token NETBOX_TOKEN_SEEDED=0 \
    render_osac_chart_offline "${offline_token}" \
    osac "${OSAC_CHART_DIR}" --values "${OSAC_CHART_DIR}/ci/default-values.yaml" \
    --namespace osac \
    "${NETBOX_PROFILE_ARGS[@]}" \
    2>"${netbox_gate_err}"; then
  fail "netbox-existing-secret: the gate rejected an install whose token Secret exists"
fi
grep -q 'kubectl get secret netbox-api-token -n osac' "${netbox_cli_log}" \
  || fail "netbox-existing-secret: the lookup did not use the rendered Secret identity"
grep -q -- '-o go-template' "${netbox_cli_log}" \
  || fail "netbox-existing-secret: the lookup was not the read-only template check"
grep -Eq 'kubectl (apply|create|delete|patch|replace|edit)' "${netbox_cli_log}" \
  && fail "netbox-existing-secret: the gate mutated the cluster"

# A Secret that exists under a different key than the chart reads is still
# a real input error: the caller overrode bmf.netbox.tokenSecretKey, so
# the lookup asks for that key and nothing answers.
echo "checking netbox-existing-secret-wrong-key (a Secret missing the chart's key fails)"
if PATH="${netbox_cli_bin}:${PATH}" NETBOX_CLI_LOG="${netbox_cli_log}" \
    NETBOX_CLI_SECRET_PRESENT=1 NETBOX_CLI_NAME=netbox-api-token \
    NETBOX_CLI_NS=osac NETBOX_CLI_KEY=token NETBOX_TOKEN_SEEDED=0 \
    render_osac_chart_offline "${offline_token}" \
    osac "${OSAC_CHART_DIR}" --values "${OSAC_CHART_DIR}/ci/default-values.yaml" \
    --namespace osac \
    "${NETBOX_PROFILE_ARGS[@]}" \
    --set bmf.netbox.tokenSecretKey=api-token \
    2>"${netbox_gate_err}"; then
  fail "netbox-existing-secret-wrong-key: the gate bridged a Secret without the chart's key"
fi
grep -Eiq 'netbox\.tokenValue is required when the NetBox token Secret' \
  "${netbox_gate_err}" \
  || fail "netbox-existing-secret-wrong-key: the failure did not name the missing token"

# A caller-supplied token must reach the gate's render, never be masked by
# the placeholder: the placeholder applies only when the merged render has
# no token at all, so a token from --set, --set-string, or a values file is
# validated itself. A numeric token fails the schema in the gate, before
# any cluster change, exactly as it would fail the install. Type-error
# wording differs across Helm versions like the minimum constraints above:
# 3.18 prints "Invalid type. Expected: string, given: integer" while 3.22
# (what CI installs) prints "got number, want string".
numeric_token_pattern='(bmf\.netbox\.tokenValue|/bmf/netbox/tokenValue).*(Invalid type|got number, want string)'
echo "checking netbox-caller-numeric-token (expected failure naming the token type)"
if render_osac_chart_offline "${offline_token}" \
    osac "${OSAC_CHART_DIR}" --values "${OSAC_CHART_DIR}/ci/default-values.yaml" \
    "${NETBOX_PROFILE_ARGS[@]}" \
    --set bmf.netbox.tokenValue=123 \
    >"${TMP_DIR}/netbox-set-numeric.out" 2>"${netbox_gate_err}"; then
  fail "netbox-caller-numeric-token: the gate accepted a numeric --set token"
fi
grep -Eiq "${numeric_token_pattern}" "${netbox_gate_err}" \
  || fail "netbox-caller-numeric-token: the failure did not name the token type"

# The same through a values file: a --set placeholder would mask a
# file-supplied token (Helm applies --set after values files), so the
# bridge must render the file's value itself.
printf 'bmf:\n  netbox:\n    tokenValue: 123\n' >"${TMP_DIR}/netbox-numeric.yaml"
echo "checking netbox-file-numeric-token (expected failure naming the token type)"
if render_osac_chart_offline "${offline_token}" \
    osac "${OSAC_CHART_DIR}" --values "${OSAC_CHART_DIR}/ci/default-values.yaml" \
    "${NETBOX_PROFILE_ARGS[@]}" \
    -f "${TMP_DIR}/netbox-numeric.yaml" \
    >"${TMP_DIR}/netbox-file-numeric.out" 2>"${netbox_gate_err}"; then
  fail "netbox-file-numeric-token: the gate accepted a numeric file token"
fi
grep -Eiq "${numeric_token_pattern}" "${netbox_gate_err}" \
  || fail "netbox-file-numeric-token: the failure did not name the token type"

# A valid file-supplied token renders unmasked (the bridge is not taken).
# The token is generated at runtime so no credential-looking literal
# lives in this suite.
file_supplied_token="file-supplied-token-$(date +%s)-$$"
printf 'bmf:\n  netbox:\n    tokenValue: %s\n' "${file_supplied_token}" \
  >"${TMP_DIR}/netbox-token.yaml"
echo "checking netbox-file-string-token (expected success)"
render_osac_chart_offline "${offline_token}" \
  osac "${OSAC_CHART_DIR}" --values "${OSAC_CHART_DIR}/ci/default-values.yaml" \
  "${NETBOX_PROFILE_ARGS[@]}" \
  -f "${TMP_DIR}/netbox-token.yaml" \
  || fail "netbox-file-string-token: a valid file-supplied token must render"

# A duplicate metering reader name must fail the gate's own render (the
# umbrella chart carries the readers into the metering subchart) without
# echoing the name: reader names are customer-supplied infrastructure
# identifiers -- often internal hostnames -- and the gate's error text
# reaches the terminal and CI logs.
echo "checking metering-reader-duplicate (duplicate names fail the gate without the name)"
if render_osac_chart_offline "${offline_token}" \
    osac "${OSAC_CHART_DIR}" --values "${OSAC_CHART_DIR}/ci/default-values.yaml" \
    --namespace osac \
    --set 'metering.readers[0].name=metrics-reader.internal.example.com' \
    --set 'metering.readers[1].name=metrics-reader.internal.example.com' \
    >/dev/null 2>"${netbox_gate_err}"; then
  fail "metering-reader-duplicate: the gate accepted a duplicate reader name"
fi
grep -Eiq 'duplicate metering reader' "${netbox_gate_err}" \
  || fail "metering-reader-duplicate: the failure did not name the constraint"
grep -Fq 'metrics-reader.internal.example.com' "${netbox_gate_err}" \
  && fail "metering-reader-duplicate: the failure echoed the reader name"

echo "input validation passed"
