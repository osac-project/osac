#!/usr/bin/env bash
###############################################################################
# run-ui-e2e.sh — Configure and run OSAC UI E2E flows
#
# Run prepare-setup-for-ui-run.sh first. This script only validates the prepared
# checkout, configures the requested flow, and runs Playwright.
###############################################################################
set -euo pipefail

OSAC_UI_BASE_URL="${OSAC_UI_BASE_URL:-}"
OSAC_ADMIN_USERNAME="${OSAC_ADMIN_USERNAME:-admin}"
OSAC_ADMIN_PASSWORD="${OSAC_ADMIN_PASSWORD:-}"
OSAC_EXTRAVARS="${OSAC_EXTRAVARS:-}"
OSAC_CLI_PATH="${OSAC_CLI_PATH:-osac}"
OSAC_VM_FLOW_MODE="${OSAC_VM_FLOW_MODE:-full}"
FLOW_TAG="${OSAC_FLOW_TAG:-}"
OSAC_ALLOW_ROOT_E2E="${OSAC_ALLOW_ROOT_E2E:-false}"
OSAC_UI_DIR="${OSAC_UI_DIR:-}"
KUBECONFIG_PATH="${KUBECONFIG:-}"
unset KUBECONFIG

export IGNORE_HTTPS_ERRORS="${IGNORE_HTTPS_ERRORS:-false}"

RUN_SMOKE=false
SETUP_ONLY=true
PW_PROJECT=""
PW_TEST_PATH=""
PW_GREP=""
PW_HEADED=""
PW_LIST=false
NO_DEPS=false
MODE_FLAG_SET=false
PROMPT_PASSWORD=false

for arg in "$@"; do
  case $arg in
    --osac-ui-dir=*)    OSAC_UI_DIR="${arg#*=}" ;;
    --ignore-tls)       export IGNORE_HTTPS_ERRORS=true ;;
    --admin-user=*)     OSAC_ADMIN_USERNAME="${arg#*=}" ;;
    --admin-password=*)
      echo "Do not pass passwords on the command line. Set OSAC_ADMIN_PASSWORD or use --prompt-password." >&2
      exit 1
      ;;
    --prompt-password)  PROMPT_PASSWORD=true ;;
    --run-smoke)        RUN_SMOKE=true; SETUP_ONLY=false ;;
    --test=*)           PW_TEST_PATH="${arg#*=}"; SETUP_ONLY=false ;;
    --project=*)        PW_PROJECT="${arg#*=}"; SETUP_ONLY=false ;;
    --grep=*)           PW_GREP="${arg#*=}" ;;
    --headed)           PW_HEADED="--headed"; SETUP_ONLY=false ;;
    --mode=*)           OSAC_VM_FLOW_MODE="${arg#*=}"; MODE_FLAG_SET=true ;;
    --extravars=*)      OSAC_EXTRAVARS="${arg#*=}" ;;
    --tag=*)            FLOW_TAG="${arg#*=}"; SETUP_ONLY=false ;;
    --allow-root-e2e)   OSAC_ALLOW_ROOT_E2E=true ;;
    --list)             PW_LIST=true; SETUP_ONLY=false ;;
    --no-deps)          NO_DEPS=true ;;
    --all)              PW_TEST_PATH="e2e-tests"; SETUP_ONLY=false ;;
    --help|-h)
      cat <<EOF
Usage: $0 [OPTIONS]

Run prepared OSAC UI Playwright flows. Run prepare-setup-for-ui-run.sh first.

Flow tags:
  all, happy_basic                         complete VMaaS flow
  create_resources, delete_resources       infrastructure resource group
  create_image, delete_image, full_image   disk image lifecycle
  create_storage, delete_storage, full_storage
  create_instance_type, delete_instance_type, full_instance_type
  create_tenant, delete_tenant, full_tenant
  create_catalog, delete_catalog, full_catalog
  create_vm, delete_vm, full_vm

create_* leaves a resource for delete_*; full_* runs the configured teardown.

Options:
  --tag=TAG              select a flow tag
  --test=PATH            pass a test-path filter to Playwright
  --project=PROJECT      select a Playwright project
  --grep=PATTERN         filter test titles
  --mode=MODE            set OSAC_VM_FLOW_MODE to create, delete, or full
  --extravars=PATH       temporarily use a VMaaS extravars.yaml
  --admin-user=USER      Keycloak admin user (default: admin)
  --prompt-password      read the Keycloak admin password without echoing it
  --headed               run with a headed browser using the prepared Xvfb
  --run-smoke            run the setup and smoke projects
  --list                 list discovered tests without executing them
  --no-deps              do not run Playwright project dependencies
  --allow-root-e2e       explicitly allow credentialed tests as root
  --osac-ui-dir=PATH     prepared osac-ui directory; auto-detected when unset
  --ignore-tls            accept untrusted TLS certificates
  --help, -h             print this help

Examples:
  bash $0 --tag=create_image
  bash $0 --tag=delete_image
  bash $0 --tag=full_image
EOF
      exit 0
      ;;
    *) echo "Unknown option: $arg (use --help)"; exit 1 ;;
  esac
done

section() { printf '\n\e[1m== %s\e[0m\n' "$1"; }
ok()      { printf '  \e[32m✓\e[0m %s\n' "$1"; }
fail()    { printf '  \e[31m✗\e[0m %s\n' "$1" >&2; exit 1; }
info()    { printf '  → %s\n' "$1"; }

cleanup() {
  if [[ -n ${xvfb_pid:-} ]]; then
    kill "$xvfb_pid" 2>/dev/null || true
  fi
  [[ -n ${xauth_file:-} ]] && rm -f -- "$xauth_file"
  [[ -n ${xvfb_log:-} ]] && rm -f -- "$xvfb_log"
  if [[ ${extravars_linked:-false} == true ]]; then
    rm -f -- "$extravars_destination"
  fi
  if [[ -n ${extravars_backup:-} ]]; then
    mv -- "$extravars_backup" "$extravars_destination"
  elif [[ ${extravars_linked:-false} == true ]]; then
    rm -f -- "$extravars_destination"
  fi
}
trap cleanup EXIT

export_test_environment() {
  export OSAC_UI_BASE_URL OSAC_ADMIN_USERNAME OSAC_ADMIN_PASSWORD
  export OSAC_CLI_PATH OSAC_VM_FLOW_MODE
}

placeholder_url() {
  case "$1" in
    *example.com*|*your-cluster*) return 0 ;;
    *) return 1 ;;
  esac
}

placeholder_username() {
  case "$1" in
    your-*|*example*|placeholder|changeme) return 0 ;;
    *) return 1 ;;
  esac
}

declare -A FLOW_CONFIG=(
  [all]='vmaas-full-flow-delete|full||false'
  [happy_basic]='vmaas-full-flow-delete|full||false'
  [create_resources]='vmaas-full-flow-resources|create||false'
  [resources_create]='vmaas-full-flow-resources|create||false'
  [delete_resources]='vmaas-full-flow-delete|delete||false'
  [resources_delete]='vmaas-full-flow-delete|delete||false'
  [create_image]='vmaas-full-flow-resources|create|creates disk image|false'
  [delete_image]='vmaas-disk-image-delete|delete||false'
  [full_image]='vmaas-disk-image|full||false'
  [create_storage]='vmaas-full-flow-resources|create|creates a storage tier|false'
  [delete_storage]='vmaas-storage-tier-delete|delete||false'
  [full_storage]='vmaas-storage-tier|full||false'
  [create_instance_type]='vmaas-full-flow-resources|create|creates an instance type|false'
  [delete_instance_type]='vmaas-instance-type-delete|delete||false'
  [full_instance_type]='vmaas-instance-type|full||false'
  [create_tenant]='vmaas|create||false'
  [delete_tenant]='vmaas-delete|delete||true'
  [full_tenant]='vmaas-delete|full||false'
  [create_catalog]='vmaas-full-flow-catalog|create||false'
  [delete_catalog]='vmaas-catalog-delete|delete||false'
  [full_catalog]='vmaas-catalog|full||false'
  [create_vm]='vmaas-full-flow|create||false'
  [delete_vm]='vmaas-full-flow-vm-delete|delete||false'
  [full_vm]='vmaas-full-flow-vm-delete|full||false'
)

if [[ -n "$FLOW_TAG" ]]; then
  [[ "$FLOW_TAG" =~ ^[a-z_]+$ ]] ||
    fail "Unknown flow tag '${FLOW_TAG}'. See --help for available tags."
  [[ -n "${FLOW_CONFIG[$FLOW_TAG]+set}" ]] ||
    fail "Unknown flow tag '${FLOW_TAG}'. See --help for available tags."
  IFS='|' read -r flow_project flow_mode flow_grep flow_no_deps <<<"${FLOW_CONFIG[$FLOW_TAG]}"
  [[ -n "$PW_TEST_PATH" ]] || PW_TEST_PATH="e2e-tests/src/vmaas"
  [[ -n "$PW_PROJECT" ]] || PW_PROJECT="$flow_project"
  [[ -n "$PW_GREP" ]] || PW_GREP="$flow_grep"
  [[ "$MODE_FLAG_SET" == true ]] || OSAC_VM_FLOW_MODE="$flow_mode"
  [[ "$flow_no_deps" == true ]] && NO_DEPS=true
fi

[[ "$SETUP_ONLY" == false ]] || fail "Select a flow with --tag, --test, --project, or --run-smoke."

case "$OSAC_VM_FLOW_MODE" in
  create|delete|full) ;;
  *) fail "OSAC_VM_FLOW_MODE/--mode must be create, delete, or full." ;;
esac

needs_vmaas=false
case "${PW_TEST_PATH}:${PW_PROJECT}" in
  *vmaas*|e2e-tests:*|*:vmaas*) needs_vmaas=true ;;
esac

need_login=true
[[ "$PW_LIST" == true && "$RUN_SMOKE" == false ]] && need_login=false

if [[ -z "$KUBECONFIG_PATH" ]]; then
  for candidate in \
    /home/kni/clusterconfigs/auth/kubeconfig \
    /root/kubeconfig \
    /root/dev-scripts/ocp/ostest/auth/kubeconfig \
    /root/.kube/config; do
    if [[ -f "$candidate" ]]; then
      if command -v oc >/dev/null 2>&1; then
        timeout 10s env KUBECONFIG="$candidate" oc --request-timeout=5s \
          get --raw=/version >/dev/null 2>&1 || continue
      fi
      KUBECONFIG_PATH="$candidate"
      info "Using detected reachable KUBECONFIG: ${KUBECONFIG_PATH}"
      break
    fi
  done
fi

if { [[ -z "$OSAC_UI_BASE_URL" ]] || placeholder_url "$OSAC_UI_BASE_URL"; } &&
  command -v oc >/dev/null 2>&1 && [[ -n "$KUBECONFIG_PATH" ]]; then
  route_host=$(KUBECONFIG="$KUBECONFIG_PATH" oc --request-timeout=10s \
    -n osac get route osac-ui -o jsonpath='{.spec.host}' 2>/dev/null || true)
  if [[ -n "$route_host" ]]; then
    OSAC_UI_BASE_URL="https://${route_host}"
  elif timeout 10s env KUBECONFIG="$KUBECONFIG_PATH" \
    oc --request-timeout=5s get --raw=/version >/dev/null 2>&1; then
    fail "Kubernetes is reachable, but namespace osac has no osac-ui route. Deploy OSAC before running UI E2E tests."
  fi
fi

[[ -n "$OSAC_UI_BASE_URL" ]] && ! placeholder_url "$OSAC_UI_BASE_URL" ||
  fail "Set OSAC_UI_BASE_URL to a real UI route, or provide a working KUBECONFIG with oc installed."

case "$OSAC_UI_BASE_URL" in
  *[[:cntrl:][:space:]]*) fail "OSAC_UI_BASE_URL contains whitespace or control characters." ;;
  http://*://*|https://*://*) fail "OSAC_UI_BASE_URL is malformed." ;;
  http://*\@*|https://*\@*) fail "OSAC_UI_BASE_URL must not contain URL credentials." ;;
  *\?*|*\#*) fail "OSAC_UI_BASE_URL must not contain a query or fragment." ;;
  http://localhost|http://localhost:*|http://127.0.0.1|http://127.0.0.1:*|http://\[::1\]|http://\[::1\]:*) ;;
  http://*) fail "OSAC_UI_BASE_URL must use HTTPS except for loopback URLs." ;;
  https://*) ;;
  *) fail "OSAC_UI_BASE_URL must use an HTTP or HTTPS URL." ;;
esac

if [[ "$PROMPT_PASSWORD" == true ]]; then
  [[ -t 0 ]] || fail "--prompt-password requires an interactive terminal."
  IFS= read -r -s -p "OSAC admin password: " OSAC_ADMIN_PASSWORD ||
    fail "Unable to read OSAC_ADMIN_PASSWORD."
  printf '\n'
fi

if [[ "$need_login" == true ]]; then
  [[ -n "$OSAC_ADMIN_USERNAME" ]] && ! placeholder_username "$OSAC_ADMIN_USERNAME" ||
    fail "Set OSAC_ADMIN_USERNAME to a real Keycloak test user before running tests."
  [[ -n "$OSAC_ADMIN_PASSWORD" ]] ||
    fail "Set OSAC_ADMIN_PASSWORD or use --prompt-password before running tests."
  if (( EUID == 0 )) && [[ "$OSAC_ALLOW_ROOT_E2E" != true ]]; then
    fail "Credentialed tests refuse to run as root. Use a non-root user or --allow-root-e2e."
  fi
fi

export OSAC_UI_BASE_URL

if [[ -z "$OSAC_UI_DIR" ]]; then
  if [[ -d ./apps/playwright ]]; then
    OSAC_UI_DIR=.
  elif [[ -d ./osac-ui/apps/playwright ]]; then
    OSAC_UI_DIR=./osac-ui
  else
    fail "Cannot find prepared osac-ui. Use --osac-ui-dir=PATH."
  fi
fi
[[ -d "$OSAC_UI_DIR/apps/playwright" ]] || fail \
  "${OSAC_UI_DIR}/apps/playwright/ not found. Run prepare-setup-for-ui-run.sh first."
OSAC_UI_DIR="$(cd "$OSAC_UI_DIR" && pwd)"
pushd "$OSAC_UI_DIR" >/dev/null

command -v node >/dev/null 2>&1 || fail "Node.js is missing. Run prepare-setup-for-ui-run.sh."
command -v pnpm >/dev/null 2>&1 || fail "pnpm is missing. Run prepare-setup-for-ui-run.sh."
pnpm --filter @osac/playwright exec playwright --version >/dev/null 2>&1 ||
  fail "Playwright is not prepared. Run prepare-setup-for-ui-run.sh."

if [[ "$needs_vmaas" == true ]]; then
  extravars_destination="${OSAC_UI_DIR}/apps/e2e-tests/extravars.yaml"
  extravars_backup=""
  extravars_linked=false
  if [[ -n "$OSAC_EXTRAVARS" ]]; then
    [[ -f "$OSAC_EXTRAVARS" ]] || fail "OSAC_EXTRAVARS does not exist: ${OSAC_EXTRAVARS}"
    source_extravars="$(cd "$(dirname "$OSAC_EXTRAVARS")" && pwd)/$(basename "$OSAC_EXTRAVARS")"
    if [[ "$source_extravars" != "$extravars_destination" ]]; then
      if [[ -e "$extravars_destination" || -L "$extravars_destination" ]]; then
        [[ -f "$extravars_destination" ]] || fail \
          "The e2e extravars destination is not a regular file: ${extravars_destination}"
        extravars_backup=$(mktemp /tmp/osac-ui-e2e-extravars.XXXXXX)
        cp -p "$extravars_destination" "$extravars_backup"
      fi
      rm -f -- "$extravars_destination"
      ln -s "$source_extravars" "$extravars_destination"
      extravars_linked=true
    fi
  fi
  [[ -f "$extravars_destination" ]] || fail \
    "Missing ${extravars_destination}. Provide --extravars=PATH."
  node --input-type=module -e '
    import { readFileSync } from "node:fs";
    import { parse } from "yaml";
    const data = parse(readFileSync(process.argv[1], "utf8"));
    for (const key of ["catalog", "instance_type", "disk_image", "storage_tier", "lifecycle_actions"]) {
      if (data?.[key] === undefined) throw new Error(`missing top-level key: ${key}`);
    }
  ' "$extravars_destination" || fail "Invalid e2e extravars: ${extravars_destination}"
  if [[ "$OSAC_CLI_PATH" == */* ]]; then
    [[ -x "$OSAC_CLI_PATH" ]] || fail "OSAC CLI is not executable: ${OSAC_CLI_PATH}"
  else
    command -v "$OSAC_CLI_PATH" >/dev/null 2>&1 || fail "OSAC CLI '${OSAC_CLI_PATH}' was not found."
  fi
  "$OSAC_CLI_PATH" version >/dev/null 2>&1 || fail "Unable to execute OSAC CLI '${OSAC_CLI_PATH}'."
  if [[ "$PW_LIST" == false ]]; then
    "$OSAC_CLI_PATH" whoami >/dev/null 2>&1 || fail "OSAC CLI is not authenticated."
  fi
fi

if [[ -n "$PW_HEADED" && -z ${DISPLAY:-} ]]; then
  command -v Xvfb >/dev/null 2>&1 || fail "Xvfb is missing. Run prepare-setup-for-ui-run.sh."
  command -v xauth >/dev/null 2>&1 || fail "xauth is missing. Run prepare-setup-for-ui-run.sh."
  export DISPLAY=:99
  xauth_file=$(mktemp /tmp/osac-ui-e2e-xauth.XXXXXX)
  chmod 600 "$xauth_file"
  xauth -f "$xauth_file" add "$DISPLAY" . \
    "$(od -An -N16 -tx1 /dev/urandom | tr -d '[:space:]')"
  export XAUTHORITY="$xauth_file"
  xvfb_log=$(mktemp /tmp/osac-ui-e2e-xvfb.XXXXXX.log)
  chmod 600 "$xvfb_log"
  Xvfb "$DISPLAY" -screen 0 1920x1080x24 -nolisten tcp \
    -auth "$XAUTHORITY" >"$xvfb_log" 2>&1 &
  xvfb_pid=$!
  display_socket="/tmp/.X11-unix/X${DISPLAY#:}"
  xvfb_ready=false
  for _ in {1..50}; do
    if ! kill -0 "$xvfb_pid" 2>/dev/null; then
      cat "$xvfb_log" >&2
      fail "Xvfb exited before its display became ready."
    fi
    if [[ -S "$display_socket" ]]; then
      xvfb_ready=true
      break
    fi
    sleep 0.1
  done
  if [[ "$xvfb_ready" != true ]]; then
    cat "$xvfb_log" >&2
    fail "Xvfb display was not ready within the startup timeout."
  fi
fi

HTTP_STATUS="$(curl -sk --max-time 15 --proto '=http,https' -o /dev/null \
  -w '%{http_code}' "$OSAC_UI_BASE_URL" 2>/dev/null || true)"
HTTP_STATUS="${HTTP_STATUS: -3}"
[[ "$HTTP_STATUS" =~ ^[23][0-9][0-9]$ ]] || fail "OSAC UI is not reachable."

export_test_environment
if [[ "$RUN_SMOKE" == true ]]; then
  pnpm --filter @osac/playwright exec playwright test --project=setup --project=smoke
fi

if [[ "$RUN_SMOKE" == true && "$PW_LIST" == false && -z "$PW_PROJECT" &&
  -z "$PW_TEST_PATH" ]]; then
  section "Smoke test complete"
  popd >/dev/null
  exit 0
fi

PW_ARGS=()
[[ "$NO_DEPS" == true ]] && PW_ARGS+=(--no-deps)
[[ -n "$PW_TEST_PATH" ]] && PW_ARGS+=("$PW_TEST_PATH")
[[ -n "$PW_PROJECT" ]] && PW_ARGS+=(--project="$PW_PROJECT")
[[ -n "$PW_GREP" ]] && PW_ARGS+=(-g "$PW_GREP")
[[ -n "$PW_HEADED" ]] && PW_ARGS+=(--headed)

section "Running OSAC UI E2E flow"
if [[ "$PW_LIST" == true ]]; then
  pnpm --filter @osac/playwright exec playwright test "${PW_ARGS[@]}" --list
else
  pnpm --filter @osac/playwright exec playwright test "${PW_ARGS[@]}"
fi

popd >/dev/null
