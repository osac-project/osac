#!/bin/bash
# Offline behavioral tests for the gatewayapi ingress driver wiring
# (OSAC-7866): make install-osac / uninstall-osac run against oc and helm
# STUBS only. No cluster credentials are used (KUBECONFIG is unset) and the
# stubs fail closed on unexpected subcommands.
#
# Usage: scripts/test-ingress-driver-offline.sh   (from osac-installer/)
#
# Scenarios:
#  1  helm failure        -> make install-osac fails
#  2  bridge failure      -> make install-osac fails with the bridge error
#                            even though helm succeeded
#  3  --set driver        -> rejected (INGRESS_DRIVER is the only source)
#  4  gatewayAddress      -> rejected with the installer bridge enabled
#  5  GatewayClass missing-> fails fast with the install-infra pointer,
#                            helm never runs
#  6  custom gateway      -> preflight, helm args and bridge discovery all
#                            use the custom name/class/namespace
#  7  envoy uninstall     -> succeeds without oc on PATH
#  8  gatewayapi uninstll -> fails loudly without oc, with manual steps
#  9  gatewayapi uninstll -> removes the bridge slices when oc exists
# 10  envoy install       -> no bridge, no GatewayClass preflight
set -uo pipefail

ROOT=$(cd "$(dirname "$0")/.." && pwd)
cd "$ROOT"

WORK=$(mktemp -d /tmp/gw-driver-test.XXXXXX)
trap 'rm -rf "$WORK"' EXIT
unset KUBECONFIG

failures=0
pass()   { echo "PASS: $1"; }
fail()   { echo "FAIL: $1"; failures=$((failures + 1)); }

# ---------------------------------------------------------------- stubs
mkdir -p "$WORK/bin" "$WORK/bin-nooc"

cat > "$WORK/bin/oc" <<STUB
#!/bin/bash
echo "\$*" >> "\$GW_LOG_DIR/oc.log"
case "\$1 \$2" in
  "get ingresses.config/cluster") echo "apps.stub.cluster"; exit 0 ;;
  "get clusterversion") echo "4.22.14"; exit 0 ;;
  "get gatewayclass")
    if [ "\${OC_GWCLASS_MISSING:-}" = "1" ]; then
      echo 'Error from server (NotFound): not found' >&2; exit 1
    fi
    exit 0 ;;
esac
case "\$1" in
  get)
    if [[ "\$*" == *"endpointslices"* ]]; then echo "1.2.3.4"; fi
    exit 0 ;;
  apply)
    input=\$(cat)
    if [ "\${OC_APPLY_FAIL:-}" = "1" ] && printf '%s' "\$input" | grep -q EndpointSlice; then
      echo "stub: endpointslice apply rejected" >> "\$GW_LOG_DIR/oc.log"
      exit 1
    fi
    exit 0 ;;
  delete)
    if [[ "\$*" == *"endpointslice"* ]]; then exit 0; fi
    exit 0 ;;
  create|label) exit 0 ;;
esac
echo "UNEXPECTED oc call: \$*" >&2
exit 99
STUB

cat > "$WORK/bin/helm" <<STUB
#!/bin/bash
echo "\$*" >> "\$GW_LOG_DIR/helm.log"
case "\$1" in
  version) echo "v3.17.0"; exit 0 ;;
  upgrade)
    if [ "\${HELM_EXIT:-0}" = "1" ]; then echo "stub: helm upgrade failed" >&2; exit 1; fi
    exit 0 ;;
  uninstall|template|dependency) exit 0 ;;
  get)
    if [ "\$2" = "values" ]; then
      if [ -n "\${HELM_VALUES_ERROR:-}" ]; then echo "\$HELM_VALUES_ERROR" >&2; exit 1; fi
      printf '%s\n' "\${HELM_VALUES_JSON:-\{\}}"
    fi
    exit 0 ;;
esac
echo "UNEXPECTED helm call: \$*" >&2
exit 99
STUB

chmod +x "$WORK/bin/oc" "$WORK/bin/helm"
bash -n "$WORK/bin/oc" && bash -n "$WORK/bin/helm" || { echo "stub syntax error"; exit 1; }
cp "$WORK/bin/helm" "$WORK/bin-nooc/helm"
# A controlled PATH for the no-oc scenarios: only the tools the recipes
# actually need, symlinked from the current PATH, never including a real
# oc (which may live in /usr/bin on some machines and defeat the scenario).
mkdir -p "$WORK/essentials"
# type -P, not command -v: an interactive alias (grep -> ugrep here) would
# otherwise produce a broken relative symlink and break the scenario.
for tool in bash sh env grep sed awk cat cut head tail printf sleep mkdir chmod sort make dirname basename uname date readlink ls tr; do
  tool_path=$(type -P "$tool" 2>/dev/null) && ln -sf "$tool_path" "$WORK/essentials/$tool"
done
NO_OC_PATH="$WORK/bin-nooc:$WORK/essentials"

# A digest-pinned dummy chart ref skips helm-osac-deps (whose recipe clears
# charts/osac/charts) so the test cannot disturb the working tree's built
# dependencies.
REF="stub-chart@sha256:0000000000000000000000000000000000000000000000000000000000000000"
COMMON=(PLATFORM=openshift PROFILE=vmaas-ci NS=gwtest OSAC_CHART_OCI_REF="$REF")
LICENSE="$WORK/license.zip"; echo stub-license > "$LICENSE"

# ------------------------------------------------------------- runner
# run <scenario-dir> [env VAR=...] [VAR=value...] -- make <args...>
run_make() {
  local dir="$1"; shift
  local -a envs=()
  while [ "$1" != "--" ]; do envs+=("$1"); shift; done
  shift
  mkdir -p "$dir"
  local logdir="$dir/logs"
  mkdir -p "$logdir"
  env -u KUBECONFIG "${envs[@]}" \
    GW_LOG_DIR="$logdir" AAP_LICENSE_FILE="$LICENSE" \
    PATH="$WORK/bin:/usr/bin:/bin" \
    make "${COMMON[@]}" "$@" > "$dir/out.log" 2>&1
  echo $?
}

ok()      { [ "$1" -eq 0 ]; }
log_has() { grep -q "$2" "$1/out.log"; }
oc_has()  { grep -q "$2" "$1/logs/oc.log"; }
helm_has(){ grep -q "$2" "$1/logs/helm.log"; }
helm_not(){ ! grep -q "$2" "$1/logs/helm.log"; }

# ---------------------------------------------------------------- 1
rc=$(run_make "$WORK/s1" HELM_EXIT=1 -- install-osac INGRESS_DRIVER=gatewayapi)
if ! ok "$rc" && log_has "$WORK/s1" "stub: helm upgrade failed" && helm_has "$WORK/s1" "upgrade --install"; then
  pass "1 helm failure fails the install"
else fail "1 helm failure fails the install (rc=$rc)"; fi

# ---------------------------------------------------------------- 2
rc=$(run_make "$WORK/s2" OC_APPLY_FAIL=1 -- install-osac INGRESS_DRIVER=gatewayapi)
if ! ok "$rc" && helm_has "$WORK/s2" "upgrade --install" \
   && log_has "$WORK/s2" "the gateway bridge failed" \
   && oc_has "$WORK/s2" "endpointslice apply rejected"; then
  pass "2 bridge failure fails the install despite helm success"
else fail "2 bridge failure fails the install despite helm success (rc=$rc)"; fi

# ---------------------------------------------------------------- 3
rc=$(run_make "$WORK/s3" -- install-osac EXTRA_HELM_ARGS="--set service.ingress.driver=gatewayapi")
if ! ok "$rc" && log_has "$WORK/s3" "set the ingress driver with INGRESS_DRIVER" \
   && { [ ! -f "$WORK/s3/logs/helm.log" ] || helm_not "$WORK/s3" "upgrade --install"; }; then
  pass "3 EXTRA_HELM_ARGS driver override rejected"
else fail "3 EXTRA_HELM_ARGS driver override rejected (rc=$rc)"; fi

# ---------------------------------------------------------------- 3b
rc=$(run_make "$WORK/s3b" -- install-osac EXTRA_HELM_ARGS="--set service.httpCorsAllowedOrigins[0]=https://x,service.ingress.driver=gatewayapi")
if ! ok "$rc" && log_has "$WORK/s3b" "set the ingress driver with INGRESS_DRIVER" \
   && { [ ! -f "$WORK/s3b/logs/helm.log" ] || helm_not "$WORK/s3b" "upgrade --install"; }; then
  pass "3b comma-joined driver override rejected"
else fail "3b comma-joined driver override rejected (rc=$rc)"; fi

# ---------------------------------------------------------------- 4
rc=$(run_make "$WORK/s4" -- install-osac INGRESS_DRIVER=gatewayapi EXTRA_HELM_ARGS="--set service.ingress.gatewayapi.gatewayAddress=1.2.3.4")
if ! ok "$rc" && log_has "$WORK/s4" "gatewayAddress"; then
  pass "4 gatewayAddress with installer bridge rejected"
else fail "4 gatewayAddress with installer bridge rejected (rc=$rc)"; fi

# ---------------------------------------------------------------- 5
rc=$(run_make "$WORK/s5" OC_GWCLASS_MISSING=1 -- install-osac INGRESS_DRIVER=gatewayapi)
if ! ok "$rc" && log_has "$WORK/s5" "run install-infra with gatewayapi.enabled=true" \
   && helm_not "$WORK/s5" "upgrade --install"; then
  pass "5 missing GatewayClass fails fast, helm never runs"
else fail "5 missing GatewayClass fails fast, helm never runs (rc=$rc)"; fi

# ---------------------------------------------------------------- 6
rc=$(run_make "$WORK/s6" -- install-osac INGRESS_DRIVER=gatewayapi \
      GATEWAY_NAME=custom-gw GATEWAY_CLASS_NAME=custom-class GATEWAY_NAMESPACE=custom-ns)
if ok "$rc" && oc_has "$WORK/s6" "get gatewayclass custom-class" \
   && helm_has "$WORK/s6" "gatewayName=custom-gw" \
   && helm_has "$WORK/s6" "gatewayClassName=custom-class" \
   && helm_has "$WORK/s6" "gatewayNamespace=custom-ns" \
   && oc_has "$WORK/s6" "service-name=custom-gw-custom-class" \
   && oc_has "$WORK/s6" "get endpointslices -n custom-ns"; then
  pass "6 custom gateway settings reach preflight, helm and the bridge"
else fail "6 custom gateway settings reach preflight, helm and the bridge (rc=$rc)"; fi

# ---------------------------------------------------------------- 7-9
uninstall_rc() { # <dir> <values-json> <path>
  local dir="$1" values="$2" path="$3"
  mkdir -p "$dir/logs"
  env -u KUBECONFIG GW_LOG_DIR="$dir/logs" HELM_VALUES_JSON="$values" \
    HELM_VALUES_ERROR="${HELM_VALUES_ERROR:-}" \
    PATH="$path" make uninstall-osac PLATFORM=openshift PROFILE=vmaas-ci NS=gwtest \
    > "$dir/out.log" 2>&1
  echo $?
}

rc=$(uninstall_rc "$WORK/s7" '{"ingress":{"driver":"envoy"}}' "$NO_OC_PATH")
if ok "$rc"; then pass "7 envoy uninstall succeeds without oc"; else fail "7 envoy uninstall succeeds without oc (rc=$rc)"; fi

rc=$(uninstall_rc "$WORK/s8" '{"ingress":{"driver":"gatewayapi"}}' "$NO_OC_PATH")
if ! ok "$rc" && grep -q "oc is not available" "$WORK/s8/out.log" \
   && grep -q "oc delete endpointslice" "$WORK/s8/out.log"; then
  pass "8 gatewayapi uninstall without oc fails with manual steps"
else fail "8 gatewayapi uninstall without oc fails with manual steps (rc=$rc)"; fi

rc=$(uninstall_rc "$WORK/s9" '{"ingress":{"driver":"gatewayapi"}}' "$WORK/bin:/usr/bin:/bin")
if ok "$rc" && grep -q "delete endpointslice.*fulfillment-api-gateway" "$WORK/s9/logs/oc.log"; then
  pass "9 gatewayapi uninstall removes bridge slices with oc"
else fail "9 gatewayapi uninstall removes bridge slices with oc (rc=$rc)"; fi

# ---------------------------------------------------------------- 9b
export HELM_VALUES_ERROR="Error: Kubernetes cluster unreachable"
rc=$(uninstall_rc "$WORK/s9b" '' "$WORK/bin:/usr/bin:/bin")
unset HELM_VALUES_ERROR
if ! ok "$rc"; then pass "9b uninstall fails on a values lookup error"; else fail "9b uninstall fails on a values lookup error (rc=$rc)"; fi

# ---------------------------------------------------------------- 10
rc=$(run_make "$WORK/s10" -- install-osac)
if ok "$rc" && ! grep -q "get endpointslices" "$WORK/s10/logs/oc.log" \
   && ! grep -q "get gatewayclass" "$WORK/s10/logs/oc.log" \
   && ! grep -q "Bridge EndpointSlices" "$WORK/s10/out.log"; then
  pass "10 default envoy install runs no bridge or preflight"
else fail "10 default envoy install runs no bridge or preflight (rc=$rc)"; fi

echo
if [ "$failures" -eq 0 ]; then
  echo "ALL SCENARIOS PASSED"
  exit 0
fi
echo "$failures SCENARIO(S) FAILED"
exit 1
