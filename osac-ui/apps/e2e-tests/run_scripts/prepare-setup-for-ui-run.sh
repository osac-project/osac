#!/usr/bin/env bash
###############################################################################
# prepare-setup-for-ui-run.sh — Prepare an OSAC UI E2E checkout
#
# This script installs the host and JavaScript prerequisites only. Configure
# and run a flow with run-ui-e2e.sh after preparation finishes.
###############################################################################
set -euo pipefail

OSAC_REPO="${OSAC_REPO:-https://github.com/osac-project/osac.git}"
OSAC_BRANCH="${OSAC_BRANCH:-main}"
OSAC_CLONE_DIR="${OSAC_CLONE_DIR:-./osac}"
OSAC_UI_DIR="${OSAC_UI_DIR:-}"
NODE_MAJOR="${NODE_MAJOR:-20}"
PNPM_VERSION="${PNPM_VERSION:-9}"
PW_BROWSER="${PW_BROWSER:-chromium}"
LOCAL_PREFIX="${LOCAL_PREFIX:-$HOME/.local}"
DO_CLONE=false

for arg in "$@"; do
  case "$arg" in
    --clone)            DO_CLONE=true ;;
    --branch=*)         OSAC_BRANCH="${arg#*=}" ;;
    --dir=*)            OSAC_CLONE_DIR="${arg#*=}" ;;
    --osac-ui-dir=*)    OSAC_UI_DIR="${arg#*=}" ;;
    --browser=*)        PW_BROWSER="${arg#*=}" ;;
    --ignore-tls|--run-smoke|--admin-user=*|--admin-password=*|--test=*|--project=*|--grep=*|--headed|--mode=*|--extravars=*|--tag=*|--allow-root-e2e|--list|--no-deps|--all)
      echo "${arg} is a run option. Use run-ui-e2e.sh for test parameters." >&2
      exit 1
      ;;
    --help|-h)
      cat <<EOF
Usage: $0 [OPTIONS]

Prepare an OSAC UI checkout for Playwright tests. This script installs host
packages, Node.js, pnpm, JavaScript dependencies, browser dependencies, and
the Playwright browser. It does not configure credentials or run tests.

Preparation options:
  --clone              clone the OSAC monorepo into --dir
  --branch=BRANCH      branch used with --clone (default: main)
  --dir=DIR            clone destination (default: ./osac)
  --osac-ui-dir=PATH   use an existing osac-ui directory
  --browser=BROWSER    browser to install (default: chromium)
  --help, -h           print this help

Environment:
  OSAC_REPO            clone URL (default: https://github.com/osac-project/osac.git)
  OSAC_BRANCH          clone branch (default: main)
  OSAC_CLONE_DIR       clone destination (default: ./osac)
  OSAC_UI_DIR          existing checkout path; overridden by --osac-ui-dir
  NODE_MAJOR           minimum Node.js major version (default: 20)
  PNPM_VERSION         pnpm version to install if absent (default: 9)
  PW_BROWSER           browser to install (default: chromium)
  LOCAL_PREFIX         user-local pnpm install prefix (default: ~/.local)

Examples:
  bash $0 --clone --dir=/tmp/osac
  bash $0 --osac-ui-dir=/work/osac/osac-ui

After preparation, run for example:
  bash run-ui-e2e.sh --osac-ui-dir=/work/osac/osac-ui --tag=create_image
EOF
      exit 0
      ;;
    *)
      echo "Unknown option: $arg (use --help)" >&2
      exit 1
      ;;
  esac
done

section() { printf '\n\e[1m== %s\e[0m\n' "$1"; }
ok()      { printf '  \e[32m✓\e[0m %s\n' "$1"; }
info()    { printf '  → %s\n' "$1"; }
fail()    { printf '  \e[31m✗\e[0m %s\n' "$1" >&2; exit 1; }

as_root() {
  if (( EUID == 0 )); then
    "$@"
  elif command -v sudo >/dev/null 2>&1; then
    sudo env PATH=/usr/sbin:/usr/bin:/sbin:/bin "$@"
  else
    fail "Installing OS packages requires root privileges or sudo."
  fi
}

section "Checking system prerequisites"
if ! command -v git >/dev/null 2>&1 || ! command -v curl >/dev/null 2>&1; then
  if command -v dnf >/dev/null 2>&1; then
    missing_packages=()
    command -v git >/dev/null 2>&1 || missing_packages+=(git)
    command -v curl >/dev/null 2>&1 || missing_packages+=(curl-minimal)
    as_root dnf install -y "${missing_packages[@]}"
  elif command -v apt-get >/dev/null 2>&1; then
    as_root apt-get update
    as_root apt-get install -y git curl
  else
    fail "git and curl are required; no supported package manager was found."
  fi
fi
command -v git >/dev/null 2>&1 || fail "git is not available after installation."
command -v curl >/dev/null 2>&1 || fail "curl is not available after installation."
ok "git $(git --version | awk '{print $3}') and curl are available"

section "Checking Node.js (>= ${NODE_MAJOR})"
need_node=false
if command -v node >/dev/null 2>&1 && command -v npm >/dev/null 2>&1; then
  current_major=$(node -v | sed 's/v//' | cut -d. -f1)
  if [[ "$current_major" -ge "$NODE_MAJOR" ]]; then
    ok "Node.js $(node -v) and npm $(npm -v) are available"
  else
    info "Node.js $(node -v) is too old; installing a newer version"
    need_node=true
  fi
else
  need_node=true
fi

if [[ "$need_node" == true ]]; then
  if command -v dnf >/dev/null 2>&1; then
    as_root dnf remove -y nodejs npm 2>/dev/null || true
    if dnf module list nodejs >/dev/null 2>&1; then
      as_root dnf module reset -y nodejs
      as_root dnf module enable -y "nodejs:${NODE_MAJOR}"
    fi
    as_root dnf install -y nodejs
  elif command -v apt-get >/dev/null 2>&1; then
    as_root apt-get update
    as_root apt-get install -y nodejs npm
  else
    fail "Unsupported package manager; install Node.js >= ${NODE_MAJOR} manually."
  fi
  installed_major=$(node -v | sed 's/v//' | cut -d. -f1)
  [[ "$installed_major" -ge "$NODE_MAJOR" ]] ||
    fail "Node.js installation provided $(node -v), not >= ${NODE_MAJOR}."
  command -v npm >/dev/null 2>&1 || fail "npm is missing after Node.js installation."
  ok "Node.js $(node -v) and npm $(npm -v) installed"
fi

section "Checking pnpm (>= ${PNPM_VERSION})"
export PATH="${LOCAL_PREFIX}/bin:$PATH"
pnpm_needs_install=false
if command -v pnpm >/dev/null 2>&1; then
  current_pnpm_major=$(pnpm -v | cut -d. -f1)
  if [[ "$current_pnpm_major" =~ ^[0-9]+$ && "$current_pnpm_major" -ge "$PNPM_VERSION" ]]; then
    ok "pnpm $(pnpm -v) is available"
  else
    pnpm_needs_install=true
  fi
else
  pnpm_needs_install=true
fi
if [[ "$pnpm_needs_install" == true ]]; then
  npm install --global --prefix "${LOCAL_PREFIX}" "pnpm@${PNPM_VERSION}"
  ok "pnpm $(pnpm -v) installed at ${LOCAL_PREFIX}/bin/pnpm"
fi

section "Locating osac-ui"
if [[ -n "$OSAC_UI_DIR" ]]; then
  info "Using explicit path: ${OSAC_UI_DIR}"
elif [[ "$DO_CLONE" == true ]]; then
  if [[ -d "$OSAC_CLONE_DIR" ]] &&
    find "$OSAC_CLONE_DIR" -mindepth 1 -maxdepth 1 -print -quit | grep -q .; then
    fail "Clone destination is not empty: ${OSAC_CLONE_DIR}. Choose a new --dir."
  fi
  info "Cloning OSAC monorepo (branch: ${OSAC_BRANCH}) ..."
  git clone --depth 1 --branch "$OSAC_BRANCH" "$OSAC_REPO" "$OSAC_CLONE_DIR"
  OSAC_UI_DIR="${OSAC_CLONE_DIR}/osac-ui"
  ok "osac @ $(cd "$OSAC_CLONE_DIR" && git rev-parse --short HEAD)"
elif [[ -d ./osac-ui/apps/playwright ]]; then
  OSAC_UI_DIR=./osac-ui
  info "Auto-detected osac-ui/ in the current directory"
else
  fail "Cannot find osac-ui. Use --clone or --osac-ui-dir=PATH."
fi

[[ -d "$OSAC_UI_DIR/apps/playwright" ]] ||
  fail "${OSAC_UI_DIR}/apps/playwright/ not found."
OSAC_UI_DIR="$(cd "$OSAC_UI_DIR" && pwd)"
pushd "$OSAC_UI_DIR" >/dev/null
ok "Using osac-ui at ${OSAC_UI_DIR}"

section "Installing JavaScript dependencies"
pnpm install --frozen-lockfile --ignore-scripts
ok "Dependencies installed"

section "Installing Playwright browser (${PW_BROWSER})"
pushd apps/playwright >/dev/null
if [[ "$(uname)" == Linux ]]; then
  . /etc/os-release
  os_major=${VERSION_ID:-}
  os_major=${os_major%%.*}
  if [[ "$os_major" == 9 ]] &&
    [[ "${ID:-}" == rhel || "${ID:-}" == centos || "${ID:-}" == rocky || "${ID:-}" == almalinux ]]; then
    [[ "$PW_BROWSER" == chromium ]] || fail "RHEL 9 setup supports Chromium only."
    info "Installing Chromium system dependencies with dnf ..."
    as_root dnf install -y \
      nspr nss atk at-spi2-atk at-spi2-core libX11 libXcomposite libXdamage \
      libXext libXfixes libXrandr libX11-xcb libXScrnSaver libXtst \
      libxkbcommon libxkbcommon-x11 libxcb libdrm mesa-libEGL mesa-libgbm \
      alsa-lib cairo cups-libs dbus-libs fontconfig freetype gtk3 pango \
      xorg-x11-server-Xvfb xorg-x11-xauth
  else
    info "Installing Playwright system dependencies ..."
    as_root npx playwright install-deps "$PW_BROWSER"
  fi
fi
npx playwright install "$PW_BROWSER"
PW_BROWSER="$PW_BROWSER" node -e '
  const browser = require("@playwright/test")[process.env.PW_BROWSER];
  if (!browser) throw new Error("Unsupported browser: " + process.env.PW_BROWSER);
  browser.launch().then(instance => instance.close()).catch(error => {
    console.error(error);
    process.exitCode = 1;
  });
' || fail "Playwright ${PW_BROWSER} installed, but the browser cannot launch."
ok "Playwright ${PW_BROWSER} installed and launched"
popd >/dev/null

section "Preparation complete"
cat <<EOF

  osac-ui directory : ${OSAC_UI_DIR}
  osac-ui commit    : $(git rev-parse --short HEAD 2>/dev/null || echo unknown)
  Node.js           : $(node -v)
  pnpm              : $(pnpm -v)
  Playwright        : $(cd apps/playwright && npx playwright --version 2>/dev/null)
  Browser           : ${PW_BROWSER}

Run a flow separately, for example:
  bash ${OSAC_UI_DIR}/apps/e2e-tests/run_scripts/run-ui-e2e.sh \
    --osac-ui-dir=${OSAC_UI_DIR} --tag=create_image

EOF
popd >/dev/null
