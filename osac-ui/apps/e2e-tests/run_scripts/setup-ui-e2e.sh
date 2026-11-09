#!/usr/bin/env bash
###############################################################################
# setup-ui-e2e.sh — Bootstrap and run OSAC UI E2E tests
#
# Lives in:  osac-project/osac (or osac-project/osac-test-infra)
# Usage:     ./setup-ui-e2e.sh [OPTIONS]
#
# Prerequisites: package repository access and root privileges (or sudo)
###############################################################################
set -euo pipefail

# ─── Configurable variables (override via env) ─────────────────────────────

# A live URL and test account are needed when tests run, not for setup alone.
OSAC_UI_BASE_URL="${OSAC_UI_BASE_URL:-}"
OSAC_ADMIN_USERNAME="${OSAC_ADMIN_USERNAME:-admin}"
OSAC_ADMIN_PASSWORD="${OSAC_ADMIN_PASSWORD:-foobar}"
OSAC_EXTRAVARS="${OSAC_EXTRAVARS:-}"
OSAC_CLI_PATH="${OSAC_CLI_PATH:-osac}"
OSAC_VM_FLOW_MODE="${OSAC_VM_FLOW_MODE:-full}"
FLOW_TAG="${OSAC_FLOW_TAG:-}"
OSAC_ALLOW_ROOT_E2E="${OSAC_ALLOW_ROOT_E2E:-false}"

# osac monorepo (used with --clone)
OSAC_REPO="${OSAC_REPO:-https://github.com/osac-project/osac.git}"
OSAC_BRANCH="${OSAC_BRANCH:-main}"
OSAC_CLONE_DIR="${OSAC_CLONE_DIR:-./osac}"

# Explicit osac-ui path (overrides auto-detection)
OSAC_UI_DIR="${OSAC_UI_DIR:-}"
KUBECONFIG_PATH="${KUBECONFIG:-}"
unset KUBECONFIG

# Runtime
NODE_MAJOR="${NODE_MAJOR:-20}"
PNPM_VERSION="${PNPM_VERSION:-9}"
PW_BROWSER="${PW_BROWSER:-chromium}"
export IGNORE_HTTPS_ERRORS="${IGNORE_HTTPS_ERRORS:-false}"

# Local install prefix (no sudo needed)
LOCAL_PREFIX="${LOCAL_PREFIX:-$HOME/.local}"

# Flags
RUN_SMOKE=false
DO_CLONE=false
SETUP_ONLY=true
PW_PROJECT=""
PW_TEST_PATH=""
PW_GREP=""
PW_HEADED=""
PW_LIST=false

# ─── CLI args ──────────────────────────────────────────────────────────────

for arg in "$@"; do
  case $arg in
    --clone)            DO_CLONE=true ;;
    --run-smoke)        RUN_SMOKE=true ;;
    --branch=*)         OSAC_BRANCH="${arg#*=}" ;;
    --dir=*)            OSAC_CLONE_DIR="${arg#*=}" ;;
    --osac-ui-dir=*)    OSAC_UI_DIR="${arg#*=}" ;;
    --browser=*)        PW_BROWSER="${arg#*=}" ;;
    --ignore-tls)       export IGNORE_HTTPS_ERRORS=true ;;
    --admin-user=*)     OSAC_ADMIN_USERNAME="${arg#*=}" ;;
    --admin-password=*) OSAC_ADMIN_PASSWORD="${arg#*=}" ;;
    --test=*)           PW_TEST_PATH="${arg#*=}"; SETUP_ONLY=false ;;
    --project=*)        PW_PROJECT="${arg#*=}"; SETUP_ONLY=false ;;
    --grep=*)           PW_GREP="${arg#*=}" ;;
    --headed)           PW_HEADED="--headed"; SETUP_ONLY=false ;;
    --mode=*)           OSAC_VM_FLOW_MODE="${arg#*=}" ;;
    --extravars=*)      OSAC_EXTRAVARS="${arg#*=}" ;;
    --tag=*)            FLOW_TAG="${arg#*=}"; SETUP_ONLY=false ;;
    --allow-root-e2e)   OSAC_ALLOW_ROOT_E2E=true ;;
    --list)             PW_LIST=true ;;
    --all)              PW_TEST_PATH="e2e-tests"; SETUP_ONLY=false ;;
    --help|-h)
      cat <<EOF
Usage: $0 [OPTIONS]

Prepare an OSAC UI checkout for Playwright tests. With no test option, the
script performs setup only, in this order:

  1. Install Git and curl if missing, using dnf on RHEL 9 or apt on Debian.
  2. Ensure Node.js major version 20 or newer and npm are installed
     (NODE_MAJOR overrides 20).
     If Node is missing or too old, add the NodeSource package repository
     and install Node.js from it. On RHEL, first remove old nodejs/npm RPMs.
  3. Use an existing pnpm >= 9, or install pnpm 9 (PNPM_VERSION overrides 9)
     with npm under LOCAL_PREFIX, which defaults to ~/.local.
  4. Find the osac-ui checkout, or clone the OSAC monorepo with --clone.
     Run pnpm install --frozen-lockfile --ignore-scripts in osac-ui for its
     workspace packages.
  5. On RHEL 9, install Chromium's RPM libraries and Xvfb with dnf. On other
     Linux distributions, use Playwright install-deps.
  6. Download the Playwright browser (normally to ~/.cache/ms-playwright)
     and launch it once to confirm the browser and OS libraries work together.
  7. Validate the e2e extravars file, OSAC CLI, and cluster access when a
     VMaaS flow is requested.
  8. If a real UI URL was supplied, check that the deployment responds.

Setup-only stops there. --run-smoke logs in through Keycloak and checks the
live UI. --test and --all run tests against the live deployment. Test modes
require a running OSAC UI and a real Keycloak test account.

Run as root in a fresh RHEL 9 container, or as a user with working sudo.
Configured OS package repositories, network access, and a writable checkout
are required. RHEL 9 setup supports Chromium only.

Checkout selection (in priority order):
  --osac-ui-dir=PATH   use an existing osac-ui directory
  --clone              clone OSAC into --dir, then use its osac-ui directory
  no option            use ./osac-ui from the current directory, if present
  --branch=BRANCH      branch used with --clone (default: main)
  --dir=DIR            clone destination (default: ./osac)

  CAUTION: --clone requires an empty existing destination; it never deletes
  files. Choose a new --dir when reusing a checkout.

Test options:
  --run-smoke          authenticate and run Playwright setup + smoke projects
  --admin-user=USER    Keycloak admin user (default: admin)
  --admin-password=PW  Keycloak admin password (default: foobar)
  --test=PATH          pass a test-path filter to Playwright, for example
                       e2e-tests/src/vmaas or src/smoke.spec.ts
  --all                same as --test=e2e-tests; run tests configured under
                       apps/e2e-tests in the selected checkout
  --project=PROJECT    select a project for --test/--list; available projects
                       depend on the checkout (main has setup, smoke,
                       chromium, and VMaaS projects)
  --grep=PATTERN       filter test titles when running --test or --all
  --headed              show the browser when running --test or --all;
                       starts Xvfb when DISPLAY is not set
  --mode=MODE           set OSAC_VM_FLOW_MODE to create, delete, or full
  --extravars=PATH      temporarily use a VMaaS extravars.yaml for the run
  --tag=TAG              select a named flow: all or happy_basic
  --allow-root-e2e      explicitly allow credentialed tests as root
  --list               list discovered tests without executing them

  --run-smoke always uses the setup and smoke projects. VMaaS tests can create
  and delete resources on the live OSAC deployment.

Browser and TLS options:
  --browser=BROWSER    browser to install (default: chromium); RHEL 9 accepts
                       only chromium
  --ignore-tls         let Playwright accept untrusted TLS certificates;
                       use only for a trusted lab cluster
  --help, -h           print this help and exit without installing anything

Environment:
  OSAC_UI_BASE_URL     live http(s) UI URL; needed for tests and --list.
                       If unset or an example URL, the script can discover
                       the osac-ui route in namespace osac when KUBECONFIG
                       points to that cluster and oc is installed.
  OSAC_ADMIN_USERNAME  Keycloak admin user; defaults to admin
  OSAC_ADMIN_PASSWORD  Keycloak admin password; defaults to foobar
  OSAC_EXTRAVARS       path to apps/e2e-tests/extravars.yaml; used by VMaaS flows
  OSAC_CLI_PATH        OSAC CLI binary or command name; defaults to osac
  OSAC_VM_FLOW_MODE    create, delete, or full; defaults to full
  OSAC_FLOW_TAG        named flow selector; equivalent to --tag=TAG
  OSAC_REPO            clone URL (default: https://github.com/osac-project/osac.git)
  OSAC_BRANCH          clone branch (default: main)
  OSAC_CLONE_DIR       clone destination (default: ./osac)
  OSAC_UI_DIR          existing checkout path; overridden by --osac-ui-dir
  NODE_MAJOR           minimum Node.js major version (default: 20)
  PNPM_VERSION         pnpm version to install if absent (default: 9)
  PW_BROWSER           browser to install (default: chromium)
  LOCAL_PREFIX         user-local pnpm install prefix (default: ~/.local)
  IGNORE_HTTPS_ERRORS  true has the same browser effect as --ignore-tls

  URL and credentials are optional for setup only. Example credentials are
  rejected before installation when tests are requested. A real URL, if
  provided during setup, is checked for connectivity. --list needs a URL but
  does not need credentials. The connectivity probe ignores TLS errors;
  --ignore-tls controls Playwright's browser separately.

Examples (assuming this script is mounted at /setup-ui-e2e.sh):
  # Fresh container: install test dependencies without running tests.
  bash /setup-ui-e2e.sh --clone --dir=/tmp/osac-ui-ci

  # Reuse an existing checkout for setup only.
  bash /setup-ui-e2e.sh --osac-ui-dir=/work/osac/osac-ui

  # After exporting a real URL, username, and password, run the smoke test.
  bash /setup-ui-e2e.sh --osac-ui-dir=/work/osac/osac-ui --run-smoke --ignore-tls

  # List smoke tests (URL required; credentials not required).
  bash /setup-ui-e2e.sh --osac-ui-dir=/work/osac/osac-ui --project=smoke --list

  # Run the complete VMaaS creation flow from an existing checkout.
  bash /setup-ui-e2e.sh --osac-ui-dir=/work/osac/osac-ui \
    --test=e2e-tests/src/vmaas --project=vmaas-full-flow --mode=create
EOF
      exit 0
      ;;
    *) echo "Unknown option: $arg (use --help)"; exit 1 ;;
  esac
done

# ─── Helpers ───────────────────────────────────────────────────────────────

section() {
  printf '\n\e[1m== %s\e[0m\n' "$1"
}

ok()   { printf '  \e[32m✓\e[0m %s\n' "$1"; }
fail() { printf '  \e[31m✗\e[0m %s\n' "$1" >&2; exit 1; }
info() { printf '  → %s\n' "$1"; }

as_root() {
  if (( EUID == 0 )); then
    "$@"
  elif command -v sudo >/dev/null 2>&1; then
    sudo env PATH=/usr/sbin:/usr/bin:/sbin:/bin "$@"
  else
    fail "Installing OS packages requires root privileges. Run as root or install sudo for this user."
  fi
}

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

placeholder_login() {
  case "$1" in
    your-*|*example*|placeholder|changeme) return 0 ;;
    *) return 1 ;;
  esac
}

if [[ -n "$FLOW_TAG" ]]; then
  case "$FLOW_TAG" in
    all|happy_basic)
      # Both names currently select the complete VMaaS journey. Keep explicit
      # --test/--project arguments authoritative for future flow definitions.
      [[ -n "$PW_TEST_PATH" ]] || PW_TEST_PATH="e2e-tests/src/vmaas"
      [[ -n "$PW_PROJECT" ]] || PW_PROJECT="vmaas-full-flow-delete"
      ;;
    *)
      fail "Unknown flow tag '${FLOW_TAG}'. Available tags: all, happy_basic."
      ;;
  esac
fi

case "$OSAC_VM_FLOW_MODE" in
  create|delete|full) ;;
  *) fail "OSAC_VM_FLOW_MODE/--mode must be create, delete, or full." ;;
esac

needs_vmaas=false
if [[ $SETUP_ONLY == false ]]; then
  case "${PW_TEST_PATH}:${PW_PROJECT}" in
    *vmaas*|e2e-tests:*|*:vmaas*) needs_vmaas=true ;;
  esac
fi

need_ui_url=false
need_login=false
if [[ $RUN_SMOKE == true ]]; then
  need_ui_url=true
  need_login=true
elif [[ $PW_LIST == true ]]; then
  need_ui_url=true
elif [[ $SETUP_ONLY == false ]]; then
  need_ui_url=true
  need_login=true
fi

# Prefer the kubeconfig created by dev-scripts when the caller did not provide
# one explicitly. A URL can still be supplied directly for non-cluster hosts.
if [[ -z $KUBECONFIG_PATH ]]; then
  for candidate in \
    /root/dev-scripts/ocp/ostest/auth/kubeconfig \
    /home/kni/clusterconfigs/auth/kubeconfig \
    /root/.kube/config; do
    if [[ -f "$candidate" ]]; then
      KUBECONFIG_PATH="$candidate"
      info "Using detected KUBECONFIG: ${KUBECONFIG_PATH}"
      break
    fi
  done
fi

if [[ $need_ui_url == true ]] && { [[ -z $OSAC_UI_BASE_URL ]] || placeholder_url "$OSAC_UI_BASE_URL"; }; then
  if command -v oc >/dev/null 2>&1 && [[ -n $KUBECONFIG_PATH ]]; then
    route_host=$(KUBECONFIG="$KUBECONFIG_PATH" oc --request-timeout=10s \
      -n osac get route osac-ui -o jsonpath='{.spec.host}' 2>/dev/null || true)
    if [[ -n $route_host ]]; then
      OSAC_UI_BASE_URL="https://${route_host}"
      info "Discovered OSAC UI route from KUBECONFIG: ${OSAC_UI_BASE_URL}"
    fi
  fi
fi

if [[ $need_ui_url == true ]]; then
  [[ -n $OSAC_UI_BASE_URL ]] && ! placeholder_url "$OSAC_UI_BASE_URL" ||
    fail "Set OSAC_UI_BASE_URL to a real UI route, or provide a working KUBECONFIG with oc installed."
elif placeholder_url "$OSAC_UI_BASE_URL"; then
  OSAC_UI_BASE_URL=""
fi

if [[ -n $OSAC_UI_BASE_URL ]]; then
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
fi

if [[ $need_login == true ]]; then
  [[ -n $OSAC_ADMIN_USERNAME ]] && ! placeholder_login "$OSAC_ADMIN_USERNAME" ||
    fail "Set OSAC_ADMIN_USERNAME to a real Keycloak test user before running tests."
  [[ -n $OSAC_ADMIN_PASSWORD ]] && ! placeholder_login "$OSAC_ADMIN_PASSWORD" ||
    fail "Set OSAC_ADMIN_PASSWORD to that user's real password before running tests."
  if (( EUID == 0 )) && [[ $OSAC_ALLOW_ROOT_E2E != true ]]; then
    fail "Credentialed tests refuse to run as root. Use a non-root user or --allow-root-e2e."
  fi
else
  if placeholder_login "$OSAC_ADMIN_USERNAME"; then OSAC_ADMIN_USERNAME=""; fi
  if placeholder_login "$OSAC_ADMIN_PASSWORD"; then OSAC_ADMIN_PASSWORD=""; fi
fi

# The URL is needed by Playwright even for --list; credentials stay private
# until a credentialed flow is actually launched.
export OSAC_UI_BASE_URL

# ─── 1. System prerequisites ──────────────────────────────────────────────

section "Checking system prerequisites"

if ! command -v git >/dev/null 2>&1 || ! command -v curl >/dev/null 2>&1; then
  if command -v dnf >/dev/null 2>&1; then
    missing_packages=()
    command -v git >/dev/null 2>&1 || missing_packages+=(git)
    command -v curl >/dev/null 2>&1 || missing_packages+=(curl-minimal)
    info "Installing missing prerequisites: ${missing_packages[*]}"
    as_root dnf install -y "${missing_packages[@]}"
  elif command -v apt-get >/dev/null 2>&1; then
    info "Installing missing git/curl prerequisites ..."
    as_root apt-get update
    as_root apt-get install -y git curl
  else
    fail "git and curl are required; no supported package manager was found."
  fi
fi

command -v git >/dev/null 2>&1 || fail "git installation did not provide the git command."
ok "git $(git --version | awk '{print $3}')"

command -v curl >/dev/null 2>&1 || fail "curl installation did not provide the curl command."
ok "curl found"

# ─── 2. Node.js ───────────────────────────────────────────────────────────

section "Checking Node.js (>= ${NODE_MAJOR})"

need_node=false
if command -v node >/dev/null 2>&1 && command -v npm >/dev/null 2>&1; then
  current_major=$(node -v | sed 's/v//' | cut -d. -f1)
  if [[ "$current_major" -ge "$NODE_MAJOR" ]]; then
    ok "Node.js $(node -v) and npm $(npm -v) already installed"
  else
    info "Node.js $(node -v) is too old (need >= ${NODE_MAJOR})"
    need_node=true
  fi
else
  info "Node.js or npm is missing"
  need_node=true
fi

if [[ "$need_node" == "true" ]]; then
  info "Installing Node.js ${NODE_MAJOR}.x ..."
  if command -v dnf >/dev/null 2>&1; then
    # Prefer the signed RHEL/AppStream module instead of executing a remote
    # repository bootstrap script as root.
    as_root dnf remove -y nodejs npm 2>/dev/null || true
    if dnf module list nodejs >/dev/null 2>&1; then
      as_root dnf module reset -y nodejs
      as_root dnf module enable -y "nodejs:${NODE_MAJOR}"
    fi
    as_root dnf install -y nodejs
  elif command -v apt-get >/dev/null 2>&1; then
    as_root apt-get update
    as_root apt-get install -y nodejs
  else
    fail "Unsupported package manager — install Node.js >= ${NODE_MAJOR} manually"
  fi

  # Verify the install actually gave us the right version
  installed_major=$(node -v | sed 's/v//' | cut -d. -f1)
  if [[ "$installed_major" -lt "$NODE_MAJOR" ]]; then
    fail "Node.js install did not upgrade to >= ${NODE_MAJOR} (got $(node -v)). Install manually."
  fi
  command -v npm >/dev/null 2>&1 || fail "Node.js installation did not provide npm."
  ok "Node.js $(node -v) and npm $(npm -v) installed"
fi

# ─── 3. pnpm (user-local, no sudo) ────────────────────────────────────────

section "Checking pnpm (>= ${PNPM_VERSION})"

# Ensure local bin is on PATH
export PATH="${LOCAL_PREFIX}/bin:$PATH"

pnpm_needs_install=false
if command -v pnpm >/dev/null 2>&1; then
  current_pnpm_major=$(pnpm -v | cut -d. -f1)
  if [[ "$current_pnpm_major" =~ ^[0-9]+$ && "$current_pnpm_major" -ge "$PNPM_VERSION" ]]; then
    ok "pnpm $(pnpm -v) already installed"
  else
    info "pnpm $(pnpm -v) is too old (need >= ${PNPM_VERSION})"
    pnpm_needs_install=true
  fi
else
  pnpm_needs_install=true
fi

if [[ "$pnpm_needs_install" == true ]]; then
  info "Installing pnpm to ${LOCAL_PREFIX} ..."
  npm install --global --prefix "${LOCAL_PREFIX}" "pnpm@${PNPM_VERSION}"
  ok "pnpm $(pnpm -v) installed at ${LOCAL_PREFIX}/bin/pnpm"
fi

# ─── 4. Locate osac-ui ────────────────────────────────────────────────────

section "Locating osac-ui"

# Fallback chain:
#   1. --osac-ui-dir=PATH (explicit)
#   2. --clone (clone monorepo, use <clone-dir>/osac-ui/)
#   3. ./osac-ui/apps/playwright/ exists (auto-detect: we're in monorepo root)
#   4. fail with guidance

if [[ -n "$OSAC_UI_DIR" ]]; then
  # Explicit path provided
  info "Using explicit path: ${OSAC_UI_DIR}"
elif [[ "$DO_CLONE" == "true" ]]; then
  # Clone the osac monorepo
  if [[ -d "${OSAC_CLONE_DIR}" ]]; then
    if find "${OSAC_CLONE_DIR}" -mindepth 1 -maxdepth 1 -print -quit |
      grep -q .; then
      fail "Clone destination is not empty: ${OSAC_CLONE_DIR}. Choose a new --dir."
    fi
  fi
  info "Cloning osac monorepo (branch: ${OSAC_BRANCH}) ..."
  git clone --depth 1 --branch "${OSAC_BRANCH}" "${OSAC_REPO}" "${OSAC_CLONE_DIR}"
  OSAC_UI_DIR="${OSAC_CLONE_DIR}/osac-ui"
  ok "osac @ $(cd "${OSAC_CLONE_DIR}" && git rev-parse --short HEAD)"
elif [[ -d "./osac-ui/apps/playwright" ]]; then
  # Auto-detect: we're in the monorepo root
  OSAC_UI_DIR="./osac-ui"
  info "Auto-detected osac-ui/ in current directory"
else
  fail "Cannot find osac-ui/. Either:
  - run from the osac monorepo root
  - use --clone to fetch the monorepo
  - use --osac-ui-dir=PATH to point at it"
fi

# Verify the path is valid
if [[ ! -d "${OSAC_UI_DIR}/apps/playwright" ]]; then
  fail "${OSAC_UI_DIR}/apps/playwright/ not found. Is this the right osac-ui directory?"
fi

# Resolve to absolute path for clarity in logs
OSAC_UI_DIR="$(cd "${OSAC_UI_DIR}" && pwd)"
pushd "${OSAC_UI_DIR}" > /dev/null

ok "osac-ui @ ${OSAC_UI_DIR} ($(git rev-parse --short HEAD 2>/dev/null || echo 'no git'))"

# ─── 5. Install JS dependencies ───────────────────────────────────────────

section "Installing dependencies (pnpm install)"

pnpm install --frozen-lockfile --ignore-scripts
ok "Dependencies installed"

# VMaaS specs load this fixed path during module initialization. A custom file
# is temporarily linked here and the tracked seed file is restored on exit.
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
      info "Using e2e extravars from ${source_extravars} for this run"
    fi
  fi

  [[ -f "$extravars_destination" ]] || fail \
    "Missing ${extravars_destination}. Provide --extravars=PATH or create it from the e2e README."

  node --input-type=module -e '
    import { readFileSync } from "node:fs";
    import { parse } from "yaml";
    const file = process.argv[1];
    const data = parse(readFileSync(file, "utf8"));
    for (const key of ["catalog", "instance_type", "disk_image", "storage_tier", "lifecycle_actions"]) {
      if (data?.[key] === undefined) throw new Error(`missing top-level key: ${key}`);
    }
  ' "$extravars_destination" || fail "Invalid e2e extravars: ${extravars_destination}"
  ok "VMaaS e2e extravars found and has the required sections"

  if [[ "$OSAC_CLI_PATH" == */* ]]; then
    [[ -x "$OSAC_CLI_PATH" ]] || fail "OSAC CLI is not executable: ${OSAC_CLI_PATH}"
  else
    command -v "$OSAC_CLI_PATH" >/dev/null 2>&1 || fail \
      "OSAC CLI '${OSAC_CLI_PATH}' was not found on PATH. Install it or set OSAC_CLI_PATH."
  fi
  "$OSAC_CLI_PATH" version >/dev/null 2>&1 || fail \
    "Unable to execute OSAC CLI '${OSAC_CLI_PATH}'."
  if [[ "$PW_LIST" == false ]]; then
    "$OSAC_CLI_PATH" whoami >/dev/null 2>&1 || fail \
      "OSAC CLI is not authenticated. Log in with an administrator account before the VMaaS flow."
    ok "OSAC CLI is installed and authenticated"
  else
    ok "OSAC CLI is installed"
  fi
fi

# ─── 6. Playwright browser + OS deps ──────────────────────────────────────

section "Installing Playwright browser (${PW_BROWSER})"

# Playwright is a dependency of apps/playwright, not the monorepo root,
# so we cd there so npx can find the playwright binary.
pushd apps/playwright > /dev/null

# OS-level libraries (fonts, libgbm, etc.) — needed for headless on Linux
if [[ "$(uname)" == "Linux" ]]; then
  . /etc/os-release
  os_major=${VERSION_ID:-}
  os_major=${os_major%%.*}
  if [[ $os_major == 9 ]] && [[ ${ID:-} == rhel || ${ID:-} == centos || ${ID:-} == rocky || ${ID:-} == almalinux ]]; then
    [[ $PW_BROWSER == chromium ]] || fail "RHEL 9 setup currently supports Chromium only."
    info "Installing Chromium system dependencies with dnf ..."
    as_root dnf install -y nspr nss atk at-spi2-atk at-spi2-core \
      libX11 libXcomposite libXdamage libXext libXfixes libXrandr \
      libX11-xcb libXScrnSaver libXtst libxkbcommon libxkbcommon-x11 \
      libxcb libdrm mesa-libEGL mesa-libgbm alsa-lib \
      cairo cups-libs dbus-libs fontconfig freetype gtk3 pango \
      xorg-x11-server-Xvfb xorg-x11-xauth
  else
    info "Installing Playwright system dependencies ..."
    as_root npx playwright install-deps "${PW_BROWSER}"
  fi
fi

npx playwright install "${PW_BROWSER}"
PW_BROWSER="$PW_BROWSER" node -e '
  const browser = require("@playwright/test")[process.env.PW_BROWSER];
  if (!browser) throw new Error("Unsupported browser: " + process.env.PW_BROWSER);
  browser.launch().then(instance => instance.close()).catch(error => {
    console.error(error);
    process.exitCode = 1;
  });
' || fail "Playwright ${PW_BROWSER} installed, but the browser cannot launch."
ok "Playwright ${PW_BROWSER} installed and launched"

popd > /dev/null

if [[ -n "$PW_HEADED" && -z ${DISPLAY:-} ]]; then
  command -v Xvfb >/dev/null 2>&1 || fail \
    "--headed requires DISPLAY or Xvfb. Install xorg-x11-server-Xvfb first."
  command -v xauth >/dev/null 2>&1 || fail \
    "--headed requires xauth. Install xorg-x11-xauth first."
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
  info "Started virtual display ${DISPLAY}; headed browser output is not visible without VNC."
fi

# ─── 7. Connectivity check ────────────────────────────────────────────────

if [[ -n $OSAC_UI_BASE_URL ]]; then
  section "Verifying connectivity to OSAC UI"
  HTTP_STATUS=$(curl -sk --max-time 15 -o /dev/null -w '%{http_code}' "${OSAC_UI_BASE_URL}" 2>/dev/null || true)
  HTTP_STATUS="${HTTP_STATUS: -3}"

  if [[ -z "$HTTP_STATUS" || "$HTTP_STATUS" == "000" ]]; then
    fail "Cannot reach ${OSAC_UI_BASE_URL} — check URL and network."
  elif [[ "$HTTP_STATUS" -ge 200 && "$HTTP_STATUS" -lt 400 ]]; then
    ok "Reachable (HTTP ${HTTP_STATUS})"
  else
    fail "OSAC UI returned HTTP ${HTTP_STATUS}: ${OSAC_UI_BASE_URL}"
  fi
else
  info "No UI URL supplied; connectivity will be checked when tests run."
fi

# ─── 8. Environment summary ───────────────────────────────────────────────

section "Environment summary"

cat <<EOF

  OSAC_UI_BASE_URL     : ${OSAC_UI_BASE_URL:-<not set>}
  OSAC_ADMIN_USERNAME  : ${OSAC_ADMIN_USERNAME:-<not set>}
  OSAC_ADMIN_PASSWORD  : $([[ -n $OSAC_ADMIN_PASSWORD ]] && echo '<set>' || echo '<not set>')
  OSAC_VM_FLOW_MODE    : ${OSAC_VM_FLOW_MODE}
  OSAC_ALLOW_ROOT_E2E  : ${OSAC_ALLOW_ROOT_E2E}
  KUBECONFIG           : ${KUBECONFIG_PATH:-<not set>}
  IGNORE_HTTPS_ERRORS  : ${IGNORE_HTTPS_ERRORS}

  osac-ui directory    : ${OSAC_UI_DIR}
  osac-ui commit       : $(git rev-parse --short HEAD 2>/dev/null || echo 'unknown')
  cloned               : ${DO_CLONE} (branch: ${OSAC_BRANCH})

  Node.js              : $(node -v)
  pnpm                 : $(pnpm -v)
  Playwright           : $(cd apps/playwright && npx playwright --version 2>/dev/null || echo 'unknown')
  Browser              : ${PW_BROWSER}
  OS                   : $(cat /etc/os-release 2>/dev/null | grep PRETTY_NAME | cut -d= -f2 | tr -d '"' || uname -s)

EOF

# ─── 9. Smoke test (optional gate) ────────────────────────────────────────

if [[ "${RUN_SMOKE}" == "true" ]]; then
  section "Running smoke test"

  export_test_environment
  info "Authenticating via Keycloak + verifying app loads ..."
  pnpm --filter @osac/playwright exec playwright test \
    --project=setup --project=smoke

  ok "Smoke test passed — environment is ready!"
else
  info "Smoke test skipped (use --run-smoke to verify the full auth flow)"
fi

# ─── 10. Test execution ───────────────────────────────────────────────────

if [[ "${PW_LIST}" == "true" ]]; then
  section "Listing discovered tests"

  PW_ARGS=()
  [[ -n "$PW_PROJECT" ]] && PW_ARGS+=(--project="${PW_PROJECT}")
  [[ -n "$PW_TEST_PATH" ]] && PW_ARGS+=("${PW_TEST_PATH}")

  pnpm --filter @osac/playwright exec playwright test "${PW_ARGS[@]}" --list
  popd > /dev/null
  exit 0
fi

if [[ "${SETUP_ONLY}" == "false" ]]; then
  section "Running E2E tests"

  export_test_environment

  # Build the playwright command
  PW_ARGS=()
  [[ -n "$PW_TEST_PATH" ]] && PW_ARGS+=("${PW_TEST_PATH}")
  [[ -n "$PW_PROJECT" ]] && PW_ARGS+=(--project="${PW_PROJECT}")
  [[ -n "$PW_GREP" ]] && PW_ARGS+=(-g "${PW_GREP}")
  [[ -n "$PW_HEADED" ]] && PW_ARGS+=(--headed)

  info "Command: pnpm --filter @osac/playwright exec playwright test ${PW_ARGS[*]}"
  echo ""

  if pnpm --filter @osac/playwright exec playwright test "${PW_ARGS[@]}"; then
    ok "All tests passed!"
  else
    fail "Tests failed. Run: cd ${OSAC_UI_DIR} && pnpm exec playwright show-report"
  fi
fi

popd > /dev/null

# ─── Done ──────────────────────────────────────────────────────────────────

section "Done"

if [[ "${SETUP_ONLY}" == "true" ]]; then
  cat <<EOF

  UI test dependencies are installed. Supply a live UI URL and test account
  when running tests if they were not provided during setup.

  To run tests manually:
    cd ${OSAC_UI_DIR}
    pnpm --filter @osac/playwright exec playwright test \\
      e2e-tests/src/vmaas --project=vmaas

  To list available tests:
    pnpm --filter @osac/playwright exec playwright test --project=vmaas --list

  To view a test report:
    cd ${OSAC_UI_DIR} && pnpm exec playwright show-report

EOF
fi
