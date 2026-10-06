#!/usr/bin/env bash
# Cross-platform Kind container-runtime wrapper for PROFILE=dev-full.
#
# dev-full needs a *rootful* Podman engine on Linux for KubeVirt's /dev/kvm access
# and the TopoLVM loop device. This script
# encapsulates the runtime detection the old kind-dev/setup.sh did:
#   - macOS  : Docker Desktop or Podman Desktop (auto-detected)
#   - Linux host     : rootful podman via sudo or an accessible rootful socket
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
#       kind-runtime.sh container-build-file <Containerfile> <args...>
#       kind-runtime.sh <kind args...>          # run kind
#
# All diagnostics go to stderr so stdout stays clean for `kind get ...` parsing.

set -euo pipefail

# ── Configuration ────────────────────────────────────────────────────────────
ROOTFUL_SOCKET="${ROOTFUL_SOCKET:-/run/podman/podman.sock}"
SCRIPT_DIR="$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")" >/dev/null && pwd)"
TOPOLVM_RUNTIME_DIR="/var/lib/osac-dev-full/topolvm"
TOPOLVM_LVMD_IMAGE="osac-dev/topolvm-lvmd:0.41.1-container"
TOPOLVM_LVMD_CONTAINER="osac-dev-full-topolvm-lvmd"
TOPOLVM_LVM_DEVICE_ARGS=(
  --security-opt label=disable
  --cap-drop=ALL
  --cap-add=SYS_ADMIN
  --cap-add=MKNOD
  --device-cgroup-rule "b 7:* rwm"
  --device-cgroup-rule "c 10:236 rwm"
  --device-cgroup-rule "c 10:237 rwm"
  --device-cgroup-rule "b 253:* rwm"
)
PODMAN_MODE="unset"

# Auto-detect container runtime (prefer Podman when available, Docker otherwise).
if command -v podman >/dev/null 2>&1; then
  KIND_PROVIDER="${KIND_EXPERIMENTAL_PROVIDER:-podman}"
else
  KIND_PROVIDER="${KIND_EXPERIMENTAL_PROVIDER:-docker}"
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
sudo_cmd() {
  if ! command -v sudo >/dev/null 2>&1; then
    err "Rootful Podman requires sudo or an accessible rootful socket at ${ROOTFUL_SOCKET}."
    exit 1
  fi
  # Let sudo authenticate the actual command, including command-specific
  # NOPASSWD permissions. It reads passwords from the terminal, not stdin.
  sudo "$@"
}

detect_podman_mode() {
  [[ "$KIND_PROVIDER" == "podman" ]] || return 0
  [[ "$(uname -s)" == "Linux" ]] || return 0

  if [[ "$IN_DISTROBOX" == "true" ]]; then
    if [[ -S "$ROOTFUL_SOCKET" ]] && distrobox-host-exec env CONTAINER_HOST="unix://${ROOTFUL_SOCKET}" \
         podman info --format '{{.Host.Security.Rootless}}' 2>/dev/null | grep -q false; then
      PODMAN_MODE="socket"
      info "Using rootful Podman via ${ROOTFUL_SOCKET}"
    else
      PODMAN_MODE="rootless"
    fi
  elif [[ -S "$ROOTFUL_SOCKET" ]] && \
       CONTAINER_HOST="unix://${ROOTFUL_SOCKET}" podman info --format '{{.Host.Security.Rootless}}' 2>/dev/null | grep -q false; then
    PODMAN_MODE="socket"
    info "Using rootful Podman via ${ROOTFUL_SOCKET}"
  elif [[ "$(id -u)" == "0" ]] && \
       podman info --format '{{.Host.Security.Rootless}}' 2>/dev/null | grep -q false; then
    PODMAN_MODE="root"
    info "Using local rootful Podman as root"
  elif sudo -n podman info --format '{{.Host.Security.Rootless}}' 2>/dev/null | grep -q false; then
    PODMAN_MODE="sudo"
    info "Using local rootful Podman via sudo"
  elif [[ "${KIND_PROFILE:-}" == "dev-full" ]]; then
    info "Rootful Podman requires sudo authentication; enter your password if prompted."
    if sudo_cmd podman info --format '{{.Host.Security.Rootless}}' | grep -q false; then
      PODMAN_MODE="sudo"
      info "Using local rootful Podman via sudo"
    else
      PODMAN_MODE="rootless"
    fi
  else
    PODMAN_MODE="rootless"
  fi

  if [[ "${KIND_PROFILE:-}" == "dev-full" && "$PODMAN_MODE" != "socket" && "$PODMAN_MODE" != "sudo" && "$PODMAN_MODE" != "root" ]]; then
    err "Kind dev-full requires a reachable rootful Podman engine for the TopoLVM loop device and KubeVirt."
    err "A rootless Podman engine or the socket started by 'systemctl --user start podman.socket' is not sufficient."
    if [[ "$IN_DISTROBOX" == "true" ]]; then
      err "Ask the host administrator to enable ${ROOTFUL_SOCKET} for your user; see scripts/dev-full/manifests/podman-socket-rootful.conf."
    else
      err "Check your sudo permissions, or ask the host administrator to configure an accessible rootful socket at ${ROOTFUL_SOCKET}."
    fi
    exit 1
  fi
}

kind_cmd() {
  if [[ "$KIND_PROVIDER" == "podman" && "$PODMAN_MODE" == "unset" ]]; then
    detect_podman_mode
  fi
  if [[ "$KIND_PROVIDER" == "docker" || "$(uname -s)" == "Darwin" ]]; then
    KIND_EXPERIMENTAL_PROVIDER="${KIND_PROVIDER}" kind "$@"
  elif [[ "$PODMAN_MODE" == "socket" ]]; then
    if [[ "$IN_DISTROBOX" == "true" ]]; then
      systemd-run --scope --user \
        env KIND_EXPERIMENTAL_PROVIDER="${KIND_PROVIDER}" \
        CONTAINER_HOST="unix://${ROOTFUL_SOCKET}" \
        kind "$@"
    else
      KIND_EXPERIMENTAL_PROVIDER="${KIND_PROVIDER}" \
        CONTAINER_HOST="unix://${ROOTFUL_SOCKET}" kind "$@"
    fi
  elif [[ "$IN_DISTROBOX" == "true" ]]; then
    systemd-run --scope --user \
      env KIND_EXPERIMENTAL_PROVIDER="${KIND_PROVIDER}" \
      kind "$@"
  elif [[ "$PODMAN_MODE" == "sudo" ]]; then
    sudo_cmd KIND_EXPERIMENTAL_PROVIDER="${KIND_PROVIDER}" kind "$@"
  else
    KIND_EXPERIMENTAL_PROVIDER="${KIND_PROVIDER}" kind "$@"
  fi
}

container_cmd() {
  if [[ "$KIND_PROVIDER" == "podman" && "$PODMAN_MODE" == "unset" ]]; then
    detect_podman_mode
  fi
  if [[ "$KIND_PROVIDER" == "docker" ]]; then
    docker "$@"
  elif [[ "$(uname -s)" == "Darwin" ]]; then
    # Podman Desktop exposes the machine connection to the invoking user.
    # sudo would select root's connection instead and cannot reach that socket.
    podman "$@"
  elif [[ "$PODMAN_MODE" == "socket" ]]; then
    if [[ "$IN_DISTROBOX" == "true" ]]; then
      distrobox-host-exec env CONTAINER_HOST="unix://${ROOTFUL_SOCKET}" podman "$@"
    else
      CONTAINER_HOST="unix://${ROOTFUL_SOCKET}" podman "$@"
    fi
  elif [[ "$IN_DISTROBOX" == "true" ]]; then
    podman "$@"
  elif [[ "$PODMAN_MODE" == "sudo" ]]; then
    sudo_cmd podman "$@"
  else
    podman "$@"
  fi
}

container_build_file() {
  local containerfile="$1"
  shift

  if [[ "$KIND_PROVIDER" == "podman" && "$(uname -s)" == "Darwin" ]]; then
    # Podman Desktop runs builds in a remote Linux VM. Stream the Containerfile
    # there and use a remote context; it does not copy any local context files.
    podman machine ssh -- podman build "$@" -f - /tmp < "${containerfile}"
  else
    container_cmd build "$@" -f "${containerfile}" "$(dirname "${containerfile}")"
  fi
}

# ── TopoLVM host runtime for Kind dev-full ───────────────────────────────────
ensure_topolvm_image() {
  if container_cmd image inspect "${TOPOLVM_LVMD_IMAGE}" >/dev/null 2>&1; then
    return
  fi

  log "Building the local TopoLVM lvmd helper image..."
  container_build_file "${SCRIPT_DIR}/Containerfile.topolvm-lvmd" \
    -t "${TOPOLVM_LVMD_IMAGE}" --build-arg TOPOLVM_VERSION=0.41.1
}

ensure_topolvm_runtime_dir() {
  local runtime_dir_relative
  runtime_dir_relative="${TOPOLVM_RUNTIME_DIR#/var/lib/}"

  # Bind mounts need their source directory to exist in the container runtime's
  # Linux filesystem. This also works with remote Docker/Podman Desktop engines,
  # where the path is inside the engine VM rather than on the invoking host.
  container_cmd run --rm \
    --volume /var/lib:/host-var-lib \
    "${TOPOLVM_LVMD_IMAGE}" \
    mkdir -p "/host-var-lib/${runtime_dir_relative}/socket" \
      "/host-var-lib/${runtime_dir_relative}/kubelet"
}

prepare_topolvm_storage() {
  local setup_script
  setup_script='set -euo pipefail
mkdir -p /runtime/socket /runtime/kubelet
disk=/runtime/backing.img
expected_size=21474836480
if [[ -e "$disk" ]]; then
  actual_size=$(stat -c %s "$disk")
  if [[ "$actual_size" != "$expected_size" ]]; then
    echo "ERROR: existing TopoLVM backing file is ${actual_size} bytes; expected ${expected_size}. Remove the dev-full Kind cluster to reset its local storage." >&2
    exit 1
  fi
else
  truncate -s 20G "$disk"
fi

loopdev=$(losetup -j "$disk" | awk -F: "NR == 1 { print \$1 }")
if [[ -z "$loopdev" ]]; then
  loopdev=$(losetup --find --show "$disk")
fi

if vgs vg1 >/dev/null 2>&1; then
  vg_pvs=$(pvs --noheadings -o pv_name -S vg_name=vg1 | xargs)
  if [[ "$vg_pvs" != "$loopdev" ]]; then
    echo "ERROR: volume group vg1 uses ${vg_pvs}, not the dev-full backing device ${loopdev}. Remove the dev-full Kind cluster to reset its local storage." >&2
    exit 1
  fi
else
  if pvs "$loopdev" >/dev/null 2>&1; then
    pv_vg=$(pvs --noheadings -o vg_name "$loopdev" | xargs)
    if [[ -n "$pv_vg" && "$pv_vg" != vg1 ]]; then
      echo "ERROR: dev-full backing device ${loopdev} already belongs to volume group ${pv_vg}." >&2
      exit 1
    fi
  else
    pvcreate --yes "$loopdev"
  fi
  vgcreate vg1 "$loopdev"
fi

cat > /runtime/lvmd.yaml <<"EOF"
socket-name: /run/topolvm/lvmd.sock
# Run LVM in this helper container instead of entering the host namespaces.
lvm-command-prefix:
  - /sbin/lvm
device-classes:
  - name: vg1
    volume-group: vg1
    default: true
    spare-gb: 1
EOF'

  container_cmd run --rm "${TOPOLVM_LVM_DEVICE_ARGS[@]}" \
    --volume "${TOPOLVM_RUNTIME_DIR}:/runtime" \
    --volume /dev:/dev \
    "${TOPOLVM_LVMD_IMAGE}" bash -euo pipefail -c "${setup_script}"
}

start_topolvm_lvmd() {
  local existing_image="" existing_command_matches="false" container_inspect="" running="false" restart_existing="false" attempt
  if container_cmd inspect "${TOPOLVM_LVMD_CONTAINER}" >/dev/null 2>&1; then
    container_inspect=$(container_cmd inspect "${TOPOLVM_LVMD_CONTAINER}")
    existing_image=$(jq -r '.[0].Config.Image // empty' <<<"${container_inspect}")
    existing_command_matches=$(jq -r \
      '.[0].Config.Cmd == ["/usr/local/bin/lvmd", "--config=/etc/topolvm/lvmd.yaml"]' \
      <<<"${container_inspect}")
    running=$(jq -r '.[0].State.Running // false' <<<"${container_inspect}")
    if [[ "${existing_image}" != "${TOPOLVM_LVMD_IMAGE}" || "${existing_command_matches}" != "true" ]]; then
      container_cmd rm --force "${TOPOLVM_LVMD_CONTAINER}" >/dev/null
      running="false"
    elif [[ "${running}" == "true" ]]; then
      restart_existing="true"
    elif [[ "${running}" != "true" ]]; then
      container_cmd start "${TOPOLVM_LVMD_CONTAINER}" >/dev/null
      running="true"
    fi
  fi

  if [[ "${running}" != "true" ]]; then
    container_cmd run --detach --name "${TOPOLVM_LVMD_CONTAINER}" \
      --restart unless-stopped "${TOPOLVM_LVM_DEVICE_ARGS[@]}" \
      --volume /dev:/dev \
      --volume "${TOPOLVM_RUNTIME_DIR}/socket:/run/topolvm" \
      --volume "${TOPOLVM_RUNTIME_DIR}/lvmd.yaml:/etc/topolvm/lvmd.yaml:ro" \
      "${TOPOLVM_LVMD_IMAGE}" \
      /usr/local/bin/lvmd --config=/etc/topolvm/lvmd.yaml >/dev/null
  fi

  if [[ "${restart_existing}" == "true" ]] && ! container_cmd exec "${TOPOLVM_LVMD_CONTAINER}" \
    bash -c 'test -S /run/topolvm/lvmd.sock && vgs vg1 >/dev/null 2>&1' >/dev/null 2>&1; then
    container_cmd restart "${TOPOLVM_LVMD_CONTAINER}" >/dev/null
  fi

  for attempt in $(seq 1 30); do
    if container_cmd exec "${TOPOLVM_LVMD_CONTAINER}" \
      bash -c 'test -S /run/topolvm/lvmd.sock' >/dev/null 2>&1; then
      log "External TopoLVM lvmd is ready (device class vg1)"
      return
    fi
    sleep 1
  done

  container_cmd logs "${TOPOLVM_LVMD_CONTAINER}" >&2 || true
  err "External TopoLVM lvmd did not create ${TOPOLVM_RUNTIME_DIR}/socket/lvmd.sock"
  exit 1
}

ensure_topolvm_runtime() {
  ensure_topolvm_image
  ensure_topolvm_runtime_dir
  prepare_topolvm_storage
  start_topolvm_lvmd
}

reset_topolvm_kubelet_credentials() {
  log "Clearing persisted kubelet PKI before creating a fresh dev-full cluster"
  container_cmd run --rm \
    --security-opt label=disable \
    --cap-drop=ALL \
    --volume "${TOPOLVM_RUNTIME_DIR}/kubelet:/kubelet" \
    "${TOPOLVM_LVMD_IMAGE}" rm -rf /kubelet/pki
}

check_topolvm_cluster_mounts() {
  local name="$1" node mounts_ok nodes namespace="${KIND_OSAC_NAMESPACE:-osac}"
  nodes=$(kind_cmd get nodes --name "${name}")
  if [[ -z "${nodes}" ]]; then
    err "Kind cluster '${name}' has no nodes to validate for dev-full TopoLVM mounts."
    exit 1
  fi
  while IFS= read -r node; do
    [[ -n "${node}" ]] || continue
    mounts_ok=$(container_cmd inspect "${node}" | jq -r \
      --arg socket "${TOPOLVM_RUNTIME_DIR}/socket" \
      --arg kubelet "${TOPOLVM_RUNTIME_DIR}/kubelet" \
      '.[0].Mounts as $m |
       (any($m[]; .Destination == "/run/topolvm" and (.Source | endswith($socket))) and
        any($m[]; .Destination == "/var/lib/kubelet" and (.Source | endswith($kubelet)) and .Propagation == "rshared") and
        any($m[]; .Destination == "/dev" and .Source == "/dev"))')
    if [[ "${mounts_ok}" != "true" ]]; then
      err "Kind cluster '${name}' is missing required dev-full TopoLVM mounts on node '${node}'."
      err "Kind mounts cannot be added to an existing cluster. Recreate it with: make uninstall PLATFORM=kind PROFILE=dev-full NS=${namespace}"
      err "Then rerun your install command with the same PLATFORM, PROFILE, and NS values."
      exit 1
    fi
  done <<< "${nodes}"
}

delete_topolvm_runtime() {
  local had_service="false"
  if container_cmd inspect "${TOPOLVM_LVMD_CONTAINER}" >/dev/null 2>&1; then
    had_service="true"
  fi
  container_cmd rm --force "${TOPOLVM_LVMD_CONTAINER}" >/dev/null 2>&1 || true
  if ! container_cmd image inspect "${TOPOLVM_LVMD_IMAGE}" >/dev/null 2>&1; then
    if [[ "${had_service}" == "true" ]]; then
      err "TopoLVM helper image is unavailable; could not safely detach the backing device in ${TOPOLVM_RUNTIME_DIR}."
      return 1
    fi
    log "No TopoLVM helper image or lvmd container is present; nothing to clean up"
    return 0
  fi

  local cleanup_script
  cleanup_script='set -euo pipefail
disk=/runtime/backing.img
loopdev=""
if [[ -e "$disk" ]]; then
  loopdev=$(losetup -j "$disk" | awk -F: "NR == 1 { print \$1 }")
fi
if [[ -n "$loopdev" ]]; then
  if vgs vg1 >/dev/null 2>&1; then
    vg_pvs=$(pvs --noheadings -o pv_name -S vg_name=vg1 | xargs)
    if [[ "$vg_pvs" == "$loopdev" ]]; then
      vgremove --yes --force --force vg1
    elif [[ " $vg_pvs " == *" $loopdev "* ]]; then
      echo "ERROR: volume group vg1 contains additional physical volumes (${vg_pvs}); refusing to remove it." >&2
      exit 1
    fi
  fi
  if pvs "$loopdev" >/dev/null 2>&1; then
    pv_vg=$(pvs --noheadings -o vg_name "$loopdev" | xargs)
    if [[ -n "$pv_vg" && "$pv_vg" != vg1 ]]; then
      echo "ERROR: backing device ${loopdev} belongs to volume group ${pv_vg}; refusing to remove it." >&2
      exit 1
    fi
    pvremove --yes --force --force "$loopdev"
  fi
  losetup --detach "$loopdev"
fi
rm -rf /runtime/*'
  # Kubelet volumes can contain directories owned by pod users with restrictive
  # permissions or sticky bits (for example, AWX project checkouts).
  container_cmd run --rm "${TOPOLVM_LVM_DEVICE_ARGS[@]}" \
    --cap-add=DAC_OVERRIDE --cap-add=FOWNER \
    --volume "${TOPOLVM_RUNTIME_DIR}:/runtime" \
    --volume /dev:/dev \
    "${TOPOLVM_LVMD_IMAGE}" bash -euo pipefail -c "${cleanup_script}"
  log "Removed dev-full TopoLVM volume group and backing file"
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
    detect_podman_mode
    if ! container_cmd info >/dev/null 2>&1; then
      err "Podman is not reachable through the selected ${PODMAN_MODE} runtime."
      if [[ "${KIND_PROFILE:-}" == "dev-full" ]]; then
        err "Check that the rootful Podman service/socket is running and accessible, then rerun the install."
      elif [[ "$IN_DISTROBOX" == "true" ]]; then
        err "Distrobox: start the host Podman socket or install the rootful socket override (scripts/dev-full/manifests/podman-socket-rootful.conf)."
      else
        err "Host: start Podman with 'systemctl --user start podman.socket' for rootless profiles."
      fi
      exit 1
    fi
    if [[ "${KIND_PROFILE:-}" == "dev-full" && "$(uname -s)" == "Darwin" ]]; then
      local podman_rootless
      podman_rootless=$(container_cmd info --format '{{.Host.Security.Rootless}}' 2>/dev/null || echo unknown)
      if [[ "${podman_rootless}" != "false" ]]; then
        err "Kind dev-full requires a rootful Podman Desktop machine to manage the TopoLVM loop device."
        err "Switch the machine to rootful mode in Podman Desktop, then restart it and retry."
        exit 1
      fi
    fi
  else
    if ! docker info >/dev/null 2>&1; then
      err "Docker is not running. Start Docker Desktop or the Docker daemon."
      exit 1
    fi
    if [[ "${KIND_PROFILE:-}" == "dev-full" ]] && docker info --format '{{json .SecurityOptions}}' 2>/dev/null | grep -qi rootless; then
      err "Kind dev-full requires a rootful Docker daemon to manage the TopoLVM loop device."
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

  # `kind get clusters` is broken with current Podman releases: kind 0.32
  # treats Podman's JSON Labels array as a map while listing clusters. Listing
  # the nodes for one cluster still works and is enough to make this
  # idempotent.
  local nodes
  if ! nodes=$(kind_cmd get nodes --name "${name}"); then
    err "Could not check Kind cluster '${name}'; refusing to reset kubelet credentials or create a cluster."
    return 1
  fi
  if [[ -n "${nodes}" ]]; then
    log "Kind cluster '${name}' already exists, reusing it"
    if [[ "${KIND_PROFILE:-}" == "dev-full" ]]; then
      check_topolvm_cluster_mounts "${name}"
      ensure_topolvm_runtime
    fi
  else
    if [[ "${KIND_PROFILE:-}" == "dev-full" ]]; then
      ensure_topolvm_runtime
      reset_topolvm_kubelet_credentials
    fi
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
  # As in create_cluster, avoid `kind get clusters` with Podman's JSON labels.
  # Stop on lookup errors so storage cleanup cannot run against a live cluster.
  local nodes
  if ! nodes=$(kind_cmd get nodes --name "${name}"); then
    err "Could not check Kind cluster '${name}'; refusing to remove its storage."
    return 1
  fi
  if [[ -n "${nodes}" ]]; then
    if ! kind_cmd delete cluster --name "${name}"; then
      err "Could not delete Kind cluster '${name}'; refusing to remove its storage."
      return 1
    fi
  else
    log "Kind cluster '${name}' does not exist"
  fi
  if [[ "${KIND_PROFILE:-}" == "dev-full" ]]; then
    delete_topolvm_runtime
  fi
}

# ── Subcommand dispatch (only when executed, not sourced) ─────────────────────
if [[ "${BASH_SOURCE[0]}" == "${0}" ]]; then
  case "${1:-}" in
    check)          check_prerequisites ;;
    create-cluster) shift; create_cluster "$@" ;;
    delete-cluster) shift; delete_cluster "$@" ;;
    container)            shift; container_cmd "$@" ;;
    container-build-file) shift; container_build_file "$@" ;;
    "")                   err "usage: kind-runtime.sh {check|create-cluster|delete-cluster|container|container-build-file|<kind args>}"; exit 2 ;;
    *)              kind_cmd "$@" ;;
  esac
fi
