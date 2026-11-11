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
OSAC_EXTRAVARS="${OSAC_EXTRAVARS:-}"
OSAC_CLI_PATH="${OSAC_CLI_PATH:-}"
FULFILLMENT_SERVICE_VERSION="${FULFILLMENT_SERVICE_VERSION:-}"
OSAC_CLI_INSTALL_DIR="${OSAC_CLI_INSTALL_DIR:-}"
NODE_MAJOR="${NODE_MAJOR:-20}"
PNPM_VERSION="${PNPM_VERSION:-9}"
PW_BROWSER="${PW_BROWSER:-chromium}"
LOCAL_PREFIX="${LOCAL_PREFIX:-$HOME/.local}"
KUBECONFIG_PATH="${KUBECONFIG:-}"
DO_CLONE=false

for arg in "$@"; do
  case "$arg" in
    --clone)            DO_CLONE=true ;;
    --branch=*)         OSAC_BRANCH="${arg#*=}" ;;
    --dir=*)            OSAC_CLONE_DIR="${arg#*=}" ;;
    --osac-ui-dir=*)    OSAC_UI_DIR="${arg#*=}" ;;
    --extravars=*)      OSAC_EXTRAVARS="${arg#*=}" ;;
    --osac-cli=*)       OSAC_CLI_PATH="${arg#*=}" ;;
    --osac-cli-version=*) FULFILLMENT_SERVICE_VERSION="${arg#*=}" ;;
    --browser=*)        PW_BROWSER="${arg#*=}" ;;
    --kubeconfig=*)     KUBECONFIG_PATH="${arg#*=}" ;;
    --ignore-tls|--run-smoke|--admin-user=*|--admin-password=*|--test=*|--project=*|--grep=*|--headed|--mode=*|--tag=*|--allow-root-e2e|--list|--no-deps|--all)
      option_name="${arg%%=*}"
      echo "${option_name} is a run option. Use run-ui-e2e.sh for test parameters." >&2
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
  --extravars=PATH     validate a VMaaS extravars.yaml at PATH
  --osac-cli=PATH      use an existing osac CLI binary
  --osac-cli-version=V download the stable CLI for fulfillment-service V
  --browser=BROWSER    browser to install (default: chromium)
  --kubeconfig=PATH    kubeconfig used to verify the deployed OSAC UI route
  --help, -h           print this help

Environment:
  OSAC_REPO            clone URL (default: https://github.com/osac-project/osac.git)
  OSAC_BRANCH          clone branch (default: main)
  OSAC_CLONE_DIR       clone destination (default: ./osac)
  OSAC_UI_DIR          existing checkout path; overridden by --osac-ui-dir
  OSAC_EXTRAVARS       VMaaS extravars.yaml; defaults to the checkout file
  OSAC_CLI_PATH        existing osac CLI binary
  FULFILLMENT_SERVICE_VERSION
                       stable fulfillment-service version for CLI download
  OSAC_CLI_INSTALL_DIR CLI destination (default: ~/.local/bin for downloads)
  NODE_MAJOR           minimum Node.js major version (default: 20)
  PNPM_VERSION         pnpm version to install if absent (default: 9)
  PW_BROWSER           browser to install (default: chromium)
  LOCAL_PREFIX         user-local pnpm install prefix (default: ~/.local)

Examples:
  bash $0 --clone --dir=/tmp/osac
  bash $0 --osac-ui-dir=/work/osac/osac-ui

After preparation, run for example:
  bash run-ui-e2e.sh --osac-ui-dir=/work/osac/osac-ui --tag=create_disk_image
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

verify_cluster_routes() {
  if ! command -v oc >/dev/null 2>&1; then
    info "oc is not installed; provide OSAC_UI_BASE_URL to the test runner."
    return
  fi

  local candidate ui_route_host api_route_host
  local candidates=()
  [[ -n "$KUBECONFIG_PATH" ]] && candidates+=("$KUBECONFIG_PATH")
  candidates+=(
    /home/kni/clusterconfigs/auth/kubeconfig
    /root/kubeconfig
    /root/dev-scripts/ocp/ostest/auth/kubeconfig
    /root/.kube/config
  )

  for candidate in "${candidates[@]}"; do
    [[ -f "$candidate" ]] || continue
    ui_route_host=$(KUBECONFIG="$candidate" oc --request-timeout=10s \
      -n osac get route osac-ui -o jsonpath='{.spec.host}' 2>/dev/null || true)
    api_route_host=$(KUBECONFIG="$candidate" oc --request-timeout=10s \
      -n osac get route fulfillment-api -o jsonpath='{.spec.host}' 2>/dev/null || true)
    if [[ -n "$ui_route_host" && -n "$api_route_host" ]]; then
      KUBECONFIG_PATH="$candidate"
      ok "Found a reachable Kubernetes config and OSAC UI and API routes"
      return
    fi
  done

  info "No reachable OSAC UI and Fulfillment API routes were found from the available kubeconfigs."
  info "Run the test runner with explicit URLs and a kubeconfig that can read namespace osac."
}

install_openshift_cli() {
  if command -v oc >/dev/null 2>&1; then
    ok "OpenShift CLI is available"
    return
  fi

  if command -v dnf >/dev/null 2>&1 &&
    { (( EUID == 0 )) || command -v sudo >/dev/null 2>&1; }; then
    info "OpenShift CLI is missing; attempting to install openshift-clients ..."
    if (( EUID == 0 )); then
      dnf install -y openshift-clients >/dev/null 2>&1 || true
    else
      sudo dnf install -y openshift-clients >/dev/null 2>&1 || true
    fi
  fi

  if command -v oc >/dev/null 2>&1; then
    ok "OpenShift CLI is available"
  else
    info "OpenShift CLI is unavailable; route and capacity checks are skipped."
    info "Install a matching oc client or pass explicit UI/API URLs to the test runner."
  fi
}

install_osac_cli() {
  if [[ -n "$OSAC_CLI_PATH" ]]; then
    [[ -x "$OSAC_CLI_PATH" ]] || fail "OSAC CLI is not executable: ${OSAC_CLI_PATH}"
    ok "OSAC CLI is available at ${OSAC_CLI_PATH}"
    return
  fi

  if command -v osac >/dev/null 2>&1; then
    OSAC_CLI_PATH="$(command -v osac)"
    ok "OSAC CLI is available at ${OSAC_CLI_PATH}"
    return
  fi

  [[ -n "$FULFILLMENT_SERVICE_VERSION" ]] || fail \
    "OSAC CLI is missing. Install it, pass --osac-cli=PATH, or set FULFILLMENT_SERVICE_VERSION."

  local version install_dir destination temporary download_url checksum_temporary checksum_status
  local expected_checksum actual_checksum
  version="${FULFILLMENT_SERVICE_VERSION#v}"
  [[ "$version" =~ ^[0-9]+\.[0-9]+\.[0-9]+([.-][0-9A-Za-z.-]+)?$ ]] || fail \
    "FULFILLMENT_SERVICE_VERSION must be a semantic version."
  install_dir="${OSAC_CLI_INSTALL_DIR:-${LOCAL_PREFIX}/bin}"
  destination="${install_dir}/osac"
  temporary="${destination}.tmp.$$"
  download_url="https://github.com/osac-project/osac/releases/download/fulfillment-service/v${version}/osac_Linux_x86_64"
  checksum_temporary="${temporary}.sha256"
  mkdir -p "$install_dir"
  info "Downloading the OSAC CLI ..."
  if ! curl --fail --location --show-error --silent -o "$temporary" "$download_url"; then
    rm -f -- "$temporary"
    fail "OSAC CLI download failed."
  fi
  checksum_status=$(curl --location --silent --show-error --output "$checksum_temporary" \
    --write-out '%{http_code}' "${download_url}.sha256" 2>/dev/null || true)
  case "$checksum_status" in
    2[0-9][0-9])
      command -v sha256sum >/dev/null 2>&1 || {
        rm -f -- "$temporary" "$checksum_temporary"
        fail "sha256sum is required to verify the OSAC CLI download."
      }
      expected_checksum=$(awk 'NF { print $1; exit }' "$checksum_temporary" | tr '[:upper:]' '[:lower:]')
      [[ "$expected_checksum" =~ ^[[:xdigit:]]{64}$ ]] || {
        rm -f -- "$temporary" "$checksum_temporary"
        fail "The OSAC CLI checksum file is invalid."
      }
      actual_checksum=$(sha256sum "$temporary" | awk '{print $1}')
      if [[ "$actual_checksum" != "$expected_checksum" ]]; then
        rm -f -- "$temporary" "$checksum_temporary"
        fail "The OSAC CLI checksum did not match."
      fi
      rm -f -- "$checksum_temporary"
      ;;
    404) rm -f -- "$checksum_temporary" ;;
    *)
      rm -f -- "$temporary" "$checksum_temporary"
      fail "The OSAC CLI checksum could not be retrieved."
      ;;
  esac
  chmod 0755 "$temporary"
  mv -f -- "$temporary" "$destination"
  OSAC_CLI_PATH="$destination"
  ok "OSAC CLI installed at ${OSAC_CLI_PATH}"
}

verify_vmaas_capacity() {
  local extravars_path="$1"
  [[ -n "$KUBECONFIG_PATH" ]] || {
    info "No kubeconfig was verified; VMaaS capacity preflight is skipped."
    return
  }

  local nodes_file pods_file
  nodes_file=$(mktemp /tmp/osac-ui-e2e-nodes.XXXXXX)
  pods_file=$(mktemp /tmp/osac-ui-e2e-pods.XXXXXX)
  chmod 600 "$nodes_file" "$pods_file"
  if ! KUBECONFIG="$KUBECONFIG_PATH" oc get nodes -o json >"$nodes_file" 2>/dev/null ||
    ! KUBECONFIG="$KUBECONFIG_PATH" oc get pods --all-namespaces -o json >"$pods_file" 2>/dev/null; then
    rm -f -- "$nodes_file" "$pods_file"
    info "The selected kubeconfig cannot inspect cluster capacity; capacity preflight is skipped."
    return
  fi

  local result
  if EXTRAVARS_PATH="$extravars_path" NODES_PATH="$nodes_file" PODS_PATH="$pods_file" \
    node --input-type=module <<'NODE'
import { readFileSync } from 'node:fs';
import { parse } from 'yaml';

const parseQuantity = (value, kind) => {
  if (typeof value !== 'string') return 0;
  const match = value.trim().match(/^([0-9]+(?:\.[0-9]+)?)([a-zA-Z]*)$/);
  if (!match) return 0;
  const number = Number(match[1]);
  if (kind === 'cpu') return match[2] === 'm' ? number : number * 1000;
  const multipliers = {
    k: 10 ** 3,
    M: 10 ** 6,
    G: 10 ** 9,
    T: 10 ** 12,
    P: 10 ** 15,
    Ki: 2 ** 10,
    Mi: 2 ** 20,
    Gi: 2 ** 30,
    Ti: 2 ** 40,
  };
  return number * (multipliers[match[2]] ?? 1);
};

const sumRequests = (pod, resource, kind) =>
  (pod.spec?.containers ?? []).reduce(
    (total, container) => total + parseQuantity(container.resources?.requests?.[resource], kind),
    0,
  );

const extravars = parse(readFileSync(process.env.EXTRAVARS_PATH, 'utf8'));
const nodes = JSON.parse(readFileSync(process.env.NODES_PATH, 'utf8'));
const pods = JSON.parse(readFileSync(process.env.PODS_PATH, 'utf8'));
const readyNodes = nodes.items.filter(
  (node) =>
    node.spec?.unschedulable !== true &&
    node.status?.conditions?.some((condition) => condition.type === 'Ready' && condition.status === 'True'),
);

if (readyNodes.length === 0) throw new Error('No schedulable Ready nodes were found.');

const allocatable = readyNodes.reduce(
  (total, node) => ({
    memory: total.memory + parseQuantity(node.status.allocatable?.memory, 'memory'),
    cpu: total.cpu + parseQuantity(node.status.allocatable?.cpu, 'cpu'),
  }),
  { memory: 0, cpu: 0 },
);
const requested = pods.items
  .filter((pod) => !['Succeeded', 'Failed'].includes(pod.status?.phase))
  .reduce(
    (total, pod) => ({
      memory: total.memory + sumRequests(pod, 'memory', 'memory'),
      cpu: total.cpu + sumRequests(pod, 'cpu', 'cpu'),
    }),
    { memory: 0, cpu: 0 },
  );
const vmMemory = Number(extravars.instance_type?.memory_gib ?? 0) * 2 ** 30;
const vmCpu = Number(extravars.instance_type?.vcpus ?? 0) * 1000;
const availableMemory = allocatable.memory - requested.memory;
const availableCpu = allocatable.cpu - requested.cpu;
const requiredMemory = vmMemory + 512 * 2 ** 20;
const requiredCpu = vmCpu + 500;
const gib = (value) => (value / 2 ** 30).toFixed(2);

if (availableMemory < requiredMemory || availableCpu < requiredCpu) {
  throw new Error(
    `Insufficient schedulable capacity for the configured VM: ` +
      `${gib(Math.max(availableMemory, 0))} GiB memory and ` +
      `${Math.max(availableCpu, 0)} millicores available, ` +
      `but at least ${gib(requiredMemory)} GiB memory and ${requiredCpu} millicores are required. ` +
      'Increase node capacity or lower instance_type.memory_gib/vcpus in extravars.yaml.',
  );
}

console.log(
  `VMaaS capacity is sufficient (${gib(availableMemory)} GiB memory, ` +
    `${Math.floor(availableCpu)} millicores available).`,
);
NODE
  then
    result=0
  else
    result=$?
  fi
  rm -f -- "$nodes_file" "$pods_file"
  (( result == 0 )) || fail "VMaaS capacity preflight failed."
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

section "Checking the OpenShift CLI"
install_openshift_cli

section "Checking the deployed OSAC UI"
verify_cluster_routes

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
OSAC_EXTRAVARS="${OSAC_EXTRAVARS:-${OSAC_UI_DIR}/apps/e2e-tests/extravars.yaml}"
[[ -f "$OSAC_EXTRAVARS" ]] ||
  fail "Missing VMaaS extravars: ${OSAC_EXTRAVARS}. Provide --extravars=PATH."
OSAC_EXTRAVARS="$(cd "$(dirname "$OSAC_EXTRAVARS")" && pwd)/$(basename "$OSAC_EXTRAVARS")"
install_osac_cli
pushd "$OSAC_UI_DIR" >/dev/null
ok "Using osac-ui at ${OSAC_UI_DIR}"

section "Installing JavaScript dependencies"
pnpm install --frozen-lockfile --ignore-scripts
ok "Dependencies installed"

section "Checking VMaaS cluster capacity"
verify_vmaas_capacity "$OSAC_EXTRAVARS"

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
      libxkbcommon libxcb libdrm mesa-libEGL mesa-libgbm \
      alsa-lib cairo cups-libs dbus-libs fontconfig freetype gtk3 pango
    # Headless runs do not need these, and UBI 9 repositories do not ship
    # them, so install whichever are available. run-ui-e2e.sh --headed checks
    # for Xvfb and xauth before it starts.
    as_root dnf install -y --setopt=strict=0 \
      libxkbcommon-x11 xorg-x11-server-Xvfb xorg-x11-xauth
    command -v Xvfb >/dev/null 2>&1 ||
      info "Xvfb is unavailable; only headless runs are possible."
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
  Kubeconfig        : ${KUBECONFIG_PATH:-not verified}
  OSAC CLI          : ${OSAC_CLI_PATH}
  VMaaS extravars   : ${OSAC_EXTRAVARS}

Run a flow separately, for example:
  OSAC_CLI_PATH=${OSAC_CLI_PATH} \
    bash ${OSAC_UI_DIR}/apps/e2e-tests/run_scripts/run-ui-e2e.sh \
    --osac-ui-dir=${OSAC_UI_DIR} --extravars=${OSAC_EXTRAVARS} --tag=create_disk_image

EOF
popd >/dev/null
