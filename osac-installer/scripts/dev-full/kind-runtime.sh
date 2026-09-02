#!/usr/bin/env bash
# Cross-platform Kind container-runtime wrapper for PROFILE=dev-full.
#
# KubeVirt (installed by the dev-full profile) needs *rootful* podman on Linux —
# rootless user namespaces deny virt-handler the chown of /dev/kvm. This script
# encapsulates the runtime detection the old kind-dev/setup.sh did:
#   - macOS  : Docker Desktop (auto-detected)
#   - Linux host     : rootful podman via sudo
#   - Linux Distrobox: rootful podman via the host socket (/run/podman/podman.sock)
#   - Override: KIND_EXPERIMENTAL_PROVIDER=docker|podman
#
# It is BOTH:
#   - sourceable   — defines kind_cmd / container_cmd / check_prerequisites
#   - an executable — subcommands used by the Makefile / dev-full scripts:
#       kind-runtime.sh check
#       kind-runtime.sh create-cluster <name> <config> <kubeconfig>
#       kind-runtime.sh delete-cluster <name>
#       kind-runtime.sh container <args...>     # run the container tool
#       kind-runtime.sh <kind args...>          # run kind
#
# All diagnostics go to stderr so stdout stays clean for `kind get ...` parsing.

set -euo pipefail

# ── Configuration ────────────────────────────────────────────────────────────
ROOTFUL_SOCKET="${ROOTFUL_SOCKET:-/run/podman/podman.sock}"

# Auto-detect container runtime (prefer Docker on Mac, podman elsewhere).
if [[ "$(uname -s)" == "Darwin" ]] && command -v docker >/dev/null 2>&1; then
  KIND_PROVIDER="${KIND_EXPERIMENTAL_PROVIDER:-docker}"
else
  KIND_PROVIDER="${KIND_EXPERIMENTAL_PROVIDER:-podman}"
fi

# Detect distrobox: podman is a host-exec wrapper, sudo can't reach it.
if grep -qsw distrobox-host-exec "$(command -v podman 2>/dev/null)"; then
  IN_DISTROBOX=true
else
  IN_DISTROBOX=false
fi

# ── Logging (stderr only) ────────────────────────────────────────────────────
RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'; BLUE='\033[0;34m'; NC='\033[0m'
log()  { echo -e "${GREEN}[+]${NC} $*" >&2; }
warn() { echo -e "${YELLOW}[!]${NC} $*" >&2; }
err()  { echo -e "${RED}[x]${NC} $*" >&2; }
info() { echo -e "${BLUE}[i]${NC} $*" >&2; }

# ── Runtime mode detection ───────────────────────────────────────────────────
detect_podman_mode() {
  if [[ "$IN_DISTROBOX" == "true" ]]; then
    if distrobox-host-exec env CONTAINER_HOST="unix://${ROOTFUL_SOCKET}" \
         podman info --format '{{.Host.Security.Rootless}}' 2>/dev/null | grep -q false; then
      export PODMAN_ROOTFUL=1
      info "Using rootful podman via ${ROOTFUL_SOCKET}"
    else
      export PODMAN_ROOTFUL=0
      warn "Rootful podman socket not available — using rootless (KubeVirt will fail)."
      warn "Install the host socket override (see scripts/dev-full/manifests/podman-socket-rootful.conf):"
      warn "  sudo install -m 0644 podman-socket-rootful.conf \\"
      warn "    /etc/systemd/system/podman.socket.d/rootful-group.conf"
      warn "  sudo systemctl daemon-reload && sudo systemctl restart podman.socket"
    fi
  fi
}

kind_cmd() {
  if [[ "$KIND_PROVIDER" == "docker" ]]; then
    KIND_EXPERIMENTAL_PROVIDER="${KIND_PROVIDER}" kind "$@"
  elif [[ "$IN_DISTROBOX" == "true" ]]; then
    if [[ "${PODMAN_ROOTFUL:-0}" == "1" ]]; then
      systemd-run --scope --user \
        env KIND_EXPERIMENTAL_PROVIDER="${KIND_PROVIDER}" \
        CONTAINER_HOST="unix://${ROOTFUL_SOCKET}" \
        kind "$@"
    else
      systemd-run --scope --user \
        env KIND_EXPERIMENTAL_PROVIDER="${KIND_PROVIDER}" \
        kind "$@"
    fi
  else
    sudo KIND_EXPERIMENTAL_PROVIDER="${KIND_PROVIDER}" kind "$@"
  fi
}

container_cmd() {
  if [[ "$KIND_PROVIDER" == "docker" ]]; then
    docker "$@"
  elif [[ "$IN_DISTROBOX" == "true" ]]; then
    podman "$@"   # the distrobox wrapper honours PODMAN_ROOTFUL / CONTAINER_HOST
  else
    sudo podman "$@"
  fi
}

# ── Prerequisites ────────────────────────────────────────────────────────────
check_prerequisites() {
  local missing=()
  for cmd in kind helm kubectl jq curl openssl python3; do
    command -v "$cmd" >/dev/null 2>&1 || missing+=("$cmd")
  done
  if [[ "$KIND_PROVIDER" == "docker" ]]; then
    command -v docker >/dev/null 2>&1 || missing+=("docker")
  else
    command -v podman >/dev/null 2>&1 || missing+=("podman")
  fi
  if [[ ${#missing[@]} -gt 0 ]]; then
    err "Missing required tools: ${missing[*]}"
    exit 1
  fi

  # Container runtime reachability.
  if [[ "$KIND_PROVIDER" == "podman" ]]; then
    if ! container_cmd info >/dev/null 2>&1; then
      err "Podman is not reachable. Ensure the podman socket is active."
      err "  Host:      systemctl --user start podman.socket (or use rootful, see below)"
      err "  Distrobox: install the host rootful socket override (manifests/podman-socket-rootful.conf)"
      exit 1
    fi
    detect_podman_mode
  else
    if ! docker info >/dev/null 2>&1; then
      err "Docker is not running. Start Docker Desktop or the Docker daemon."
      exit 1
    fi
  fi

  # inotify (Linux only) — kind nodes need many watchers.
  if [[ -f /proc/sys/fs/inotify/max_user_instances ]]; then
    local max_instances
    max_instances=$(cat /proc/sys/fs/inotify/max_user_instances 2>/dev/null || echo 0)
    if [[ "$max_instances" -lt 256 ]]; then
      err "inotify max_user_instances is ${max_instances} (need >= 256)"
      err "Fix:     sudo sysctl fs.inotify.max_user_instances=512"
      err "Persist: echo 'fs.inotify.max_user_instances=512' | sudo tee /etc/sysctl.d/99-kind-inotify.conf"
      exit 1
    fi
  fi

  # /dev/kvm — required by KubeVirt for hardware-accelerated VMs (Linux).
  if [[ "$(uname -s)" == "Linux" ]]; then
    if [[ ! -e /dev/kvm ]]; then
      err "/dev/kvm not found — KubeVirt requires KVM (Intel VT-x / AMD-V)."
      err "Check: ls /dev/kvm && grep -c -E 'vmx|svm' /proc/cpuinfo"
      exit 1
    fi
  else
    warn "Non-Linux host: KubeVirt VM acceleration depends on the container runtime's nested-virt support."
  fi

  # VPN route-conflict bypass (Linux + rootful podman): a VPN 10.0.0.0/8 route
  # can shadow the podman bridge subnet and make kind unreachable.
  if [[ "$KIND_PROVIDER" == "podman" && "$(uname -s)" == "Linux" ]]; then
    local vpn_table
    vpn_table=$(ip rule show 2>/dev/null | awk '/proto static/ && /lookup [0-9]/ {for(i=1;i<=NF;i++) if($i=="lookup") {print $(i+1); exit}}' || true)
    if [[ -n "$vpn_table" ]]; then
      local vpn_catch_all
      vpn_catch_all=$(ip route show table "$vpn_table" 2>/dev/null | grep -E '^10\.' | head -1 || true)
      if [[ -n "$vpn_catch_all" ]]; then
        local vpn_prio
        vpn_prio=$(ip rule show 2>/dev/null | awk "/lookup ${vpn_table}/"'{gsub(/:/, "", $1); print $1; exit}')
        if [[ -n "$vpn_prio" ]] && ! ip rule show 2>/dev/null | grep -q "to 10\.89\.0\.0/16 lookup main"; then
          local bypass_prio=$(( vpn_prio - 1 ))
          warn "VPN route table ${vpn_table} covers 10.0.0.0/8 — adding bypass for podman subnets"
          sudo ip rule add to 10.89.0.0/16 lookup main priority "$bypass_prio" 2>/dev/null || true
          log "Added ip rule: to 10.89.0.0/16 lookup main priority ${bypass_prio}"
        fi
      fi
    fi
  fi

  log "All prerequisites met (using ${KIND_PROVIDER})"
}

# ── Cluster lifecycle ────────────────────────────────────────────────────────
create_cluster() {
  local name="$1" config="$2" kubeconfig="$3"

  if [[ "$KIND_PROVIDER" == "podman" ]]; then
    detect_podman_mode
  fi

  if kind_cmd get clusters 2>/dev/null | grep -q "^${name}$"; then
    log "Kind cluster '${name}' already exists, reusing it"
  else
    log "Creating kind cluster '${name}' (${KIND_PROVIDER})..."
    kind_cmd create cluster --name "${name}" --config "${config}" --wait 60s

    # podman defaults pids_limit to 2048/container; KubeVirt install jobs need more.
    if [[ "$KIND_PROVIDER" == "podman" ]]; then
      log "Raising cgroup PID limit on Kind node containers (podman default is too low for KubeVirt)..."
      local node
      for node in $(kind_cmd get nodes --name "${name}" 2>/dev/null); do
        container_cmd update --pids-limit 4096 "${node}" >/dev/null 2>&1 || \
          warn "Could not raise PID limit on ${node} — KubeVirt may fail to install"
      done
    fi
  fi

  # Write kubeconfig via redirect so it is owned by the invoking user (not root,
  # even when kind ran under sudo).
  mkdir -p "$(dirname "${kubeconfig}")"
  kind_cmd get kubeconfig --name "${name}" > "${kubeconfig}"
  chmod 600 "${kubeconfig}"
  log "Kubeconfig written to ${kubeconfig}"
}

delete_cluster() {
  local name="$1"
  if [[ "$KIND_PROVIDER" == "podman" ]]; then
    detect_podman_mode
  fi
  kind_cmd delete cluster --name "${name}"
}

# ── Subcommand dispatch (only when executed, not sourced) ─────────────────────
if [[ "${BASH_SOURCE[0]}" == "${0}" ]]; then
  case "${1:-}" in
    check)          check_prerequisites ;;
    create-cluster) shift; create_cluster "$@" ;;
    delete-cluster) shift; delete_cluster "$@" ;;
    container)      shift; container_cmd "$@" ;;
    "")             err "usage: kind-runtime.sh {check|create-cluster|delete-cluster|container|<kind args>}"; exit 2 ;;
    *)              kind_cmd "$@" ;;
  esac
fi
