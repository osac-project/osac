#!/usr/bin/env bash
###############################################################################
# run-ui-e2e.sh — Configure and run OSAC UI E2E flows
#
# Run prepare-setup-for-ui-run.sh first. This script only validates the prepared
# checkout, configures the requested flow, and runs Playwright.
###############################################################################
set -euo pipefail

OSAC_UI_BASE_URL="${OSAC_UI_BASE_URL:-}"
OSAC_API_BASE_URL="${OSAC_API_BASE_URL:-}"
OSAC_ADMIN_USERNAME="${OSAC_ADMIN_USERNAME:-admin}"
OSAC_ADMIN_PASSWORD="${OSAC_ADMIN_PASSWORD:-}"
OSAC_ADMIN_PASSWORD_FILE="${OSAC_ADMIN_PASSWORD_FILE:-}"
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
ADMIN_PASSWORD_SET=false

if [[ "$OSAC_CLI_PATH" == osac ]] && ! command -v osac >/dev/null 2>&1 &&
  [[ -x "${HOME}/.local/bin/osac" ]]; then
  OSAC_CLI_PATH="${HOME}/.local/bin/osac"
fi

while (($# > 0)); do
  arg="$1"
  shift
  case $arg in
    --osac-ui-dir=*)    OSAC_UI_DIR="${arg#*=}" ;;
    --ignore-tls)       export IGNORE_HTTPS_ERRORS=true ;;
    --admin-user=*)     OSAC_ADMIN_USERNAME="${arg#*=}" ;;
    --admin-password=*|--admin-password)
      echo "--admin-password is not accepted; use OSAC_ADMIN_PASSWORD or --admin-password-file=PATH." >&2
      exit 1
      ;;
    --admin-password-file=*) OSAC_ADMIN_PASSWORD_FILE="${arg#*=}" ;;
    --admin-password-file)
      if (($# == 0)); then
        echo "--admin-password-file requires a path." >&2
        exit 1
      fi
      OSAC_ADMIN_PASSWORD_FILE="$1"
      shift
      ;;
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
  vmaas_all                                 all VMaaS flows (happy flow for now)
  vmaas-full-happy-basic-flow              complete VMaaS happy flow
  create_pre-resources, delete_pre-resources, full_pre-resources
                                            infrastructure resource group
  create_disk_image, delete_disk_image, full_disk_image
                                            disk image lifecycle
  create_storage-tier, delete_storage-tier, full_storage-tier
                                            storage tier lifecycle
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
  --admin-password-file=PATH
                          readable file containing the admin password
  --headed               run with a headed browser using the prepared Xvfb
  --run-smoke            run the setup and smoke projects
  --list                 list discovered tests without executing them
  --no-deps              do not run Playwright project dependencies
  --allow-root-e2e       explicitly allow credentialed tests as root
  --osac-ui-dir=PATH     prepared osac-ui directory; auto-detected when unset
  --ignore-tls            accept untrusted TLS certificates
  --help, -h             print this help

Examples:
  bash $0 --tag=create_disk_image
  bash $0 --tag=delete_disk_image
  bash $0 --tag=full_disk_image
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

if [[ -n "$OSAC_ADMIN_PASSWORD" && -n "$OSAC_ADMIN_PASSWORD_FILE" ]]; then
  fail "Provide the admin password through the environment or a file, not both."
fi
if [[ -n "$OSAC_ADMIN_PASSWORD_FILE" ]]; then
  [[ -f "$OSAC_ADMIN_PASSWORD_FILE" && -r "$OSAC_ADMIN_PASSWORD_FILE" ]] ||
    fail "Admin password file is not readable."
  OSAC_ADMIN_PASSWORD="$(< "$OSAC_ADMIN_PASSWORD_FILE")"
fi
[[ -n "$OSAC_ADMIN_PASSWORD" ]] && ADMIN_PASSWORD_SET=true

cleanup() {
  if [[ -n ${xvfb_pid:-} ]]; then
    kill "$xvfb_pid" 2>/dev/null || true
  fi
  [[ -n ${xauth_file:-} ]] && rm -f -- "$xauth_file"
  [[ -n ${xvfb_log:-} ]] && rm -f -- "$xvfb_log"
  [[ -n ${cli_password_file:-} ]] && rm -f -- "$cli_password_file"
  [[ -n ${cli_config_dir:-} ]] && rm -rf -- "$cli_config_dir"
  [[ -n ${cli_cache_dir:-} ]] && rm -rf -- "$cli_cache_dir"
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
  [vmaas_all]='vmaas-all-full-flows|full||false'
  [vmaas-full-happy-basic-flow]='vmaas-full-happy-basic-flow|full||false'
  [create_pre-resources]='vmaas-full-flow-pre-resource|create||false'
  [delete_pre-resources]='vmaas-full-flow-pre-resource|delete||false'
  [full_pre-resources]='vmaas-full-flow-pre-resource|full||false'
  [create_disk_image]='disk-image|create||false'
  [delete_disk_image]='disk-image|delete||false'
  [full_disk_image]='disk-image|full||false'
  [create_storage-tier]='storage-tier|create||false'
  [delete_storage-tier]='storage-tier|delete||false'
  [full_storage-tier]='storage-tier|full||false'
  [create_instance_type]='instance-type|create||false'
  [delete_instance_type]='instance-type|delete||false'
  [full_instance_type]='instance-type|full||false'
  [create_tenant]='tenant|create||false'
  [delete_tenant]='tenant|delete||false'
  [full_tenant]='tenant|full||false'
  [create_catalog]='catalog|create||false'
  [delete_catalog]='catalog|delete||false'
  [full_catalog]='catalog|full||false'
  [create_vm]='vm-flow|create||false'
  [delete_vm]='vm-flow|delete||false'
  [full_vm]='vm-flow|full||false'
)

if [[ -n "$FLOW_TAG" ]]; then
  [[ "$FLOW_TAG" =~ ^[a-z0-9_-]+$ ]] ||
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

route_host=""
api_route_host=""
if command -v oc >/dev/null 2>&1; then
  kubeconfig_candidates=()
  [[ -n "$KUBECONFIG_PATH" ]] && kubeconfig_candidates+=("$KUBECONFIG_PATH")
  kubeconfig_candidates+=(
    /home/kni/clusterconfigs/auth/kubeconfig
    /root/kubeconfig
    /root/dev-scripts/ocp/ostest/auth/kubeconfig
    /root/.kube/config
  )
  KUBECONFIG_PATH=""
  route_host=""
  for candidate in "${kubeconfig_candidates[@]}"; do
    [[ -f "$candidate" ]] || continue
    route_host=$(KUBECONFIG="$candidate" oc --request-timeout=10s \
      -n osac get route osac-ui -o jsonpath='{.spec.host}' 2>/dev/null || true)
    api_route_host=$(KUBECONFIG="$candidate" oc --request-timeout=10s \
      -n osac get route fulfillment-api -o jsonpath='{.spec.host}' 2>/dev/null || true)
    if [[ -n "$route_host" && ( "$needs_vmaas" != true || -n "$api_route_host" ) ]]; then
      KUBECONFIG_PATH="$candidate"
      info "Using a reachable KUBECONFIG with an OSAC UI route."
      break
    fi
  done
fi

if { [[ -z "$OSAC_UI_BASE_URL" ]] || placeholder_url "$OSAC_UI_BASE_URL"; } &&
  [[ -n "$route_host" ]]; then
  OSAC_UI_BASE_URL="https://${route_host}"
elif { [[ -z "$OSAC_UI_BASE_URL" ]] || placeholder_url "$OSAC_UI_BASE_URL"; }; then
  fail "Set OSAC_UI_BASE_URL, or provide a kubeconfig that can read the osac-ui route."
fi

if [[ -z "$OSAC_API_BASE_URL" && -n "$api_route_host" ]]; then
  OSAC_API_BASE_URL="https://${api_route_host}"
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

if [[ "$need_login" == true ]]; then
  [[ -n "$OSAC_ADMIN_USERNAME" ]] && ! placeholder_username "$OSAC_ADMIN_USERNAME" ||
    fail "Set OSAC_ADMIN_USERNAME to a real Keycloak test user before running tests."
  [[ "$ADMIN_PASSWORD_SET" == true && -n "$OSAC_ADMIN_PASSWORD" ]] ||
    fail "Set OSAC_ADMIN_PASSWORD or pass --admin-password-file=PATH for credentialed runs."
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
    if ! timeout 20s "$OSAC_CLI_PATH" whoami >/dev/null 2>&1; then
      [[ "$need_login" == true ]] || fail "OSAC CLI is not authenticated."
      [[ -n "$OSAC_API_BASE_URL" ]] || fail \
        "OSAC CLI is not authenticated and no Fulfillment API route was found. Set OSAC_API_BASE_URL."
      cli_password_file=$(mktemp /tmp/osac-ui-e2e-cli-password.XXXXXX)
      chmod 600 "$cli_password_file"
      printf '%s' "$OSAC_ADMIN_PASSWORD" >"$cli_password_file"
      cli_config_dir=$(mktemp -d /tmp/osac-ui-e2e-cli-config.XXXXXX)
      cli_cache_dir=$(mktemp -d /tmp/osac-ui-e2e-cli-cache.XXXXXX)
      export OSAC_CONFIG="$cli_config_dir" OSAC_CACHE="$cli_cache_dir"
      cli_login_flags=(
        --flow=password
        --user="$OSAC_ADMIN_USERNAME"
        --password-file="$cli_password_file"
      )
      [[ "$IGNORE_HTTPS_ERRORS" == true ]] && cli_login_flags+=(--insecure)
      timeout 30s "$OSAC_CLI_PATH" login "$OSAC_API_BASE_URL" "${cli_login_flags[@]}" \
        >/dev/null 2>&1 || fail "OSAC CLI login failed."
      timeout 20s "$OSAC_CLI_PATH" whoami >/dev/null 2>&1 ||
        fail "OSAC CLI login completed, but authentication could not be verified."
      ok "OSAC CLI authenticated for this run"
    fi
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
