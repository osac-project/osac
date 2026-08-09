#!/usr/bin/env bash
# OSAC Kind Development Environment Setup
#
# Creates a kind cluster with all prerequisites needed to run OSAC services.
# Designed to get developers up and running quickly without OpenShift.
#
# Usage:
#   ./setup.sh                    # Full setup
#   ./setup.sh --skip-osac        # Infrastructure only (cert-manager, envoy, keycloak, postgres)
#   ./setup.sh --cluster-only     # Kind cluster only (with CoreDNS *.localhost rewrite)
#
# Prerequisites:
#   - Docker (macOS) or podman (Linux/rootful — see below)
#   - kind >= v0.20
#   - helm >= v3.10
#   - kubectl
#   - openssl
#   - python3 with requests module (for AWX configuration)
#   - inotify max_user_instances >= 256 (Linux only)
#
# Container runtime:
#   - macOS: Docker Desktop (auto-detected)
#   - Linux: podman (rootful — see below)
#   - Override: export KIND_EXPERIMENTAL_PROVIDER=docker (or podman)
#
# Rootful podman setup (Linux):
#   - Host:      sudo is used directly (no extra setup needed)
#   - Distrobox: install the systemd socket override on the host:
#       sudo install -d /etc/systemd/system/podman.socket.d
#       sudo install -m 0644 kind-dev/podman-socket-rootful.conf \
#         /etc/systemd/system/podman.socket.d/rootful-group.conf
#       sudo chgrp wheel /run/podman && sudo chmod 710 /run/podman
#       sudo systemctl daemon-reload && sudo systemctl restart podman.socket
#
# Environment variables:
#   CLUSTER_NAME                   Kind cluster name (default: osac-dev)
#   OSAC_NAMESPACE                 Namespace for OSAC services (default: osac)
#   KEYCLOAK_NAMESPACE             Namespace for Keycloak (default: keycloak)
#   KIND_EXPERIMENTAL_PROVIDER     Container runtime: docker or podman (auto-detected)
#   FULFILLMENT_IMAGE              Override fulfillment-service image (e.g. quay.io/user/fulfillment-service:dev)

set -euo pipefail

SCRIPT_DIR="$(realpath "$(dirname "${BASH_SOURCE[0]}")")"
REPO_ROOT="$(realpath "${SCRIPT_DIR}/..")"

# Configuration
CLUSTER_NAME="${CLUSTER_NAME:-osac-dev}"
OSAC_NAMESPACE="${OSAC_NAMESPACE:-osac}"
KEYCLOAK_NAMESPACE="${KEYCLOAK_NAMESPACE:-keycloak}"
SKIP_OSAC=false
CLUSTER_ONLY=false

# Component versions (aligned with fulfillment-service IT)
CERT_MANAGER_VERSION="v1.20.0"
TRUST_MANAGER_VERSION="v0.22.0"
ENVOY_GATEWAY_VERSION="v1.6.5"
AUTHORINO_VERSION="v0.23.1"

# Networking — services are accessed as <service>.<namespace>.localhost
EXTERNAL_INGRESS_PORT=8443
INTERNAL_INGRESS_NODE_PORT=30443
INTERNAL_HTTP_NODE_PORT=30080

# Auto-detect container runtime (prefer Docker on Mac, podman elsewhere)
if [[ "$(uname -s)" == "Darwin" ]] && command -v docker >/dev/null 2>&1; then
  KIND_PROVIDER="${KIND_EXPERIMENTAL_PROVIDER:-docker}"
else
  KIND_PROVIDER="${KIND_EXPERIMENTAL_PROVIDER:-podman}"
fi

# Detect distrobox: podman is a host-exec wrapper, sudo can't reach it.
# On the host: use sudo for rootful podman (separate socket/namespace).
if grep -qsw distrobox-host-exec "$(command -v podman 2>/dev/null)"; then
  IN_DISTROBOX=true
else
  IN_DISTROBOX=false
fi

ROOTFUL_SOCKET="/run/podman/podman.sock"

detect_podman_mode() {
  if [[ "$IN_DISTROBOX" == "true" ]]; then
    # Check if the rootful socket is reachable from the host
    if distrobox-host-exec env CONTAINER_HOST="unix://${ROOTFUL_SOCKET}" \
         podman info --format '{{.Host.Security.Rootless}}' 2>/dev/null | grep -q false; then
      export PODMAN_ROOTFUL=1
      info "Using rootful podman via ${ROOTFUL_SOCKET}"
    else
      export PODMAN_ROOTFUL=0
      warn "Rootful podman socket not available — using rootless"
      warn "For rootful mode, run on the host:"
      warn "  sudo install -m 0644 kind-dev/podman-socket-rootful.conf \\"
      warn "    /etc/systemd/system/podman.socket.d/rootful-group.conf"
      warn "  sudo systemctl daemon-reload && sudo systemctl restart podman.socket"
    fi
  fi
}

kind_cmd() {
  if [[ "$KIND_PROVIDER" == "docker" ]]; then
    # Docker mode — no sudo needed on Mac
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
    podman "$@"   # wrapper handles PODMAN_ROOTFUL
  else
    sudo podman "$@"
  fi
}

# Legacy alias for backward compatibility
podman_cmd() {
  container_cmd "$@"
}

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'

log()  { echo -e "${GREEN}[+]${NC} $*"; }
warn() { echo -e "${YELLOW}[!]${NC} $*"; }
err()  { echo -e "${RED}[x]${NC} $*" >&2; }
info() { echo -e "${BLUE}[i]${NC} $*"; }

for arg in "$@"; do
  case "$arg" in
    --skip-osac)    SKIP_OSAC=true ;;
    --cluster-only) CLUSTER_ONLY=true ;;
    --help|-h)
      sed -n '2,/^$/p' "$0" | sed 's/^# \?//'
      exit 0
      ;;
  esac
done

# ── Prerequisites ──────────────────────────────────────────────────────────────

check_prerequisites() {
  local missing=()
  for cmd in kind helm kubectl openssl; do
    command -v "$cmd" >/dev/null 2>&1 || missing+=("$cmd")
  done

  # Check for container runtime
  if [[ "$KIND_PROVIDER" == "docker" ]]; then
    if ! command -v docker >/dev/null 2>&1; then
      missing+=("docker")
    fi
  else
    if ! command -v podman >/dev/null 2>&1; then
      missing+=("podman")
    fi
  fi

  if [[ ${#missing[@]} -gt 0 ]]; then
    err "Missing required tools: ${missing[*]}"
    exit 1
  fi

  # Runtime-specific checks
  if [[ "$KIND_PROVIDER" == "podman" ]]; then
    if ! podman info >/dev/null 2>&1; then
      err "Podman is not reachable. Ensure the podman socket is active."
      err "  Host: systemctl --user start podman.socket"
      err "  Distrobox: the podman wrapper should delegate to the host"
      exit 1
    fi
    detect_podman_mode
  else
    if ! docker info >/dev/null 2>&1; then
      err "Docker is not running. Start Docker Desktop or the Docker daemon."
      exit 1
    fi
  fi

  # inotify check (Linux only)
  if [[ -f /proc/sys/fs/inotify/max_user_instances ]]; then
    local max_instances
    max_instances=$(cat /proc/sys/fs/inotify/max_user_instances 2>/dev/null || echo 0)
    if [[ "$max_instances" -lt 256 ]]; then
      err "inotify max_user_instances is ${max_instances} (need >= 256)"
      err "Fix: sudo sysctl fs.inotify.max_user_instances=512"
      err "Persist: echo 'fs.inotify.max_user_instances=512' | sudo tee /etc/sysctl.d/99-kind-inotify.conf"
      exit 1
    fi
  fi

  # VPN route conflict check (Linux + rootful podman only)
  # VPNs often add a broad 10.0.0.0/8 route via tun0 in a policy-routing table
  # that takes precedence over the main table. If the podman bridge subnet
  # (e.g. 10.89.x.0/24) falls within that range, container traffic is sent
  # through the VPN instead of the local bridge — making kind unreachable.
  if [[ "$KIND_PROVIDER" == "podman" && "$(uname -s)" == "Linux" ]]; then
    local vpn_table
    vpn_table=$(ip rule show 2>/dev/null | awk '/proto static/ && /lookup [0-9]/ {for(i=1;i<=NF;i++) if($i=="lookup") {print $(i+1); exit}}' || true)
    if [[ -n "$vpn_table" ]]; then
      local vpn_catch_all
      vpn_catch_all=$(ip route show table "$vpn_table" 2>/dev/null | grep -E '^10\.' | head -1 || true)
      if [[ -n "$vpn_catch_all" ]]; then
        local vpn_prio
        vpn_prio=$(ip rule show 2>/dev/null | awk "/lookup ${vpn_table}/"'{gsub(/:/, "", $1); print $1; exit}')
        if [[ -n "$vpn_prio" ]]; then
          local bypass_prio=$(( vpn_prio - 1 ))
          if ! ip rule show 2>/dev/null | grep -q "to 10\.89\.0\.0/16 lookup main"; then
            warn "VPN route table ${vpn_table} covers 10.0.0.0/8 — adding bypass for podman subnets"
            sudo ip rule add to 10.89.0.0/16 lookup main priority "$bypass_prio" 2>/dev/null || true
            log "Added ip rule: to 10.89.0.0/16 lookup main priority ${bypass_prio}"
          fi
        fi
      fi
    fi
  fi

  # Check for Python requests module (needed for AWX configuration)
  if ! python3 -c "import requests" 2>/dev/null; then
    err "Python 'requests' module not found (needed for AWX configuration)"
    err "Install: python3 -m pip install requests"
    exit 1
  fi

  log "All prerequisites met (using ${KIND_PROVIDER})"
}

# ── Helpers ────────────────────────────────────────────────────────────────────

wait_for_crd() {
  local crd="$1" timeout="${2:-60}" start
  start=$(date +%s)
  while ! kubectl get crd "$crd" >/dev/null 2>&1; do
    if (( $(date +%s) - start > timeout )); then
      err "Timed out waiting for CRD: $crd"
      return 1
    fi
    sleep 2
  done
}

wait_for_secret() {
  local ns="$1" name="$2" timeout="${3:-60}" start
  start=$(date +%s)
  while ! kubectl -n "$ns" get secret "$name" >/dev/null 2>&1; do
    if (( $(date +%s) - start > timeout )); then
      err "Timed out waiting for secret ${ns}/${name}"
      return 1
    fi
    sleep 2
  done
}

# ── Cluster ────────────────────────────────────────────────────────────────────

create_cluster() {
  if kind_cmd get clusters 2>/dev/null | grep -q "^${CLUSTER_NAME}$"; then
    log "Kind cluster '${CLUSTER_NAME}' already exists, reusing it"
    return 0
  fi

  if [[ "$KIND_PROVIDER" == "docker" ]]; then
    log "Creating kind cluster '${CLUSTER_NAME}' (Docker)..."
  elif [[ "$IN_DISTROBOX" != "true" ]] || [[ "${PODMAN_ROOTFUL:-0}" == "1" ]]; then
    log "Creating kind cluster '${CLUSTER_NAME}' (rootful podman)..."
  else
    log "Creating kind cluster '${CLUSTER_NAME}' (rootless podman)..."
  fi
  kind_cmd create cluster \
    --name "${CLUSTER_NAME}" \
    --config "${SCRIPT_DIR}/kind-config.yaml" \
    --wait 60s

  # Podman defaults pids_limit to 2048 per container. KubeVirt's install-strategy
  # jobs need more threads than that — raise the limit on all Kind node containers.
  if [[ "$KIND_PROVIDER" == "podman" ]]; then
    log "Raising cgroup PID limit on Kind node containers (podman default is too low for KubeVirt)..."
    for node in $(kind_cmd get nodes --name "${CLUSTER_NAME}" 2>/dev/null); do
      container_cmd update --pids-limit 4096 "${node}" >/dev/null 2>&1 || \
        warn "Could not raise PID limit on ${node} — KubeVirt may fail to install"
    done
  fi

  log "Kind cluster created"
}

setup_kubeconfig() {
  local kc_file
  kc_file="${HOME}/.kube/${CLUSTER_NAME}-kind.kubeconfig"
  mkdir -p "${HOME}/.kube"

  kind_cmd get kubeconfig --name "${CLUSTER_NAME}" 2>/dev/null > "${kc_file}"
  chmod 600 "${kc_file}"
  export KUBECONFIG="${kc_file}"
  log "Kubeconfig: ${KUBECONFIG}"
}

# ── CoreDNS *.localhost rewrite ────────────────────────────────────────────────
# Adds a generic rewrite rule so that <svc>.<ns>.localhost resolves to
# <svc>.<ns>.svc.cluster.local inside pods. This lets pods and your laptop
# use the same hostnames (*.localhost resolves to 127.0.0.1 on the host
# via systemd-resolved, and to the correct ClusterIP inside the cluster).

patch_coredns_localhost_rewrite() {
  local corefile
  corefile=$(kubectl -n kube-system get configmap coredns -o jsonpath='{.data.Corefile}')

  if echo "$corefile" | grep -q 'name regex .*.localhost'; then
    log "CoreDNS *.localhost rewrite already configured"
    return 0
  fi

  log "Patching CoreDNS with generic *.localhost rewrite rule..."

  local new_corefile
  # Use awk for cross-platform compatibility (BSD/GNU sed differ on -i behavior)
  new_corefile=$(echo "$corefile" | awk '/kubernetes cluster.local/ {
    print "    rewrite name keycloak.osac.localhost keycloak-external.keycloak.svc.cluster.local"
    print "    rewrite stop {"
    print "        name regex (.+)\\.(.+)\\.localhost {1}.{2}.svc.cluster.local"
    print "        answer name (.+)\\.(.+)\\.svc\\.cluster\\.local {1}.{2}.localhost"
    print "    }"
  }
  {print}')

  kubectl -n kube-system create configmap coredns \
    --from-literal=Corefile="$new_corefile" \
    --dry-run=client -o yaml | kubectl apply -f -

  kubectl -n kube-system rollout restart deployment coredns
  kubectl -n kube-system rollout status deployment coredns --timeout=60s

  log "CoreDNS patched — <service>.<namespace>.localhost works inside pods"
}

# ── Infrastructure ─────────────────────────────────────────────────────────────

install_cert_manager() {
  log "Installing cert-manager ${CERT_MANAGER_VERSION}..."
  helm upgrade --install cert-manager \
    oci://quay.io/jetstack/charts/cert-manager \
    --version "${CERT_MANAGER_VERSION}" \
    --namespace cert-manager \
    --create-namespace \
    --set crds.enabled=true \
    --wait --timeout 5m

  wait_for_crd "clusterissuers.cert-manager.io"
  wait_for_crd "certificates.cert-manager.io"
  log "cert-manager installed"
}

install_trust_manager() {
  log "Installing trust-manager ${TRUST_MANAGER_VERSION}..."
  helm upgrade --install trust-manager \
    oci://quay.io/jetstack/charts/trust-manager \
    --version "${TRUST_MANAGER_VERSION}" \
    --namespace cert-manager \
    --set defaultPackage.enabled=false \
    --wait --timeout 5m

  wait_for_crd "bundles.trust.cert-manager.io"

  # Wait for trust-manager webhook to be ready
  log "Waiting for trust-manager webhook..."
  kubectl -n cert-manager wait --for=condition=Available deployment/trust-manager --timeout=60s
  sleep 5  # Extra buffer for webhook to register

  log "trust-manager installed"
}

install_ca() {
  log "Creating self-signed CA..."

  local tmpdir
  tmpdir=$(mktemp -d)
  trap "rm -f '${tmpdir}/ca.key' '${tmpdir}/ca.crt'; rmdir '${tmpdir}' 2>/dev/null || true" RETURN

  openssl req -x509 -newkey rsa:2048 -nodes \
    -keyout "${tmpdir}/ca.key" \
    -out "${tmpdir}/ca.crt" \
    -subj "/CN=OSAC Dev CA" \
    -days 365 2>/dev/null

  kubectl -n cert-manager create secret tls default-ca \
    --cert="${tmpdir}/ca.crt" \
    --key="${tmpdir}/ca.key" \
    --dry-run=client -o yaml | kubectl apply -f -

  kubectl apply -f - <<'EOF'
apiVersion: cert-manager.io/v1
kind: ClusterIssuer
metadata:
  name: default-ca
spec:
  ca:
    secretName: default-ca
EOF

  kubectl apply -f - <<'EOF'
apiVersion: trust.cert-manager.io/v1alpha1
kind: Bundle
metadata:
  name: ca-bundle
spec:
  sources:
    - secret:
        name: default-ca
        key: tls.crt
  target:
    configMap:
      key: bundle.pem
    namespaceSelector:
      matchExpressions:
        - key: kubernetes.io/metadata.name
          operator: NotIn
          values:
            - kube-node-lease
            - kube-public
            - kube-system
            - local-path-storage
            - cert-manager
            - envoy-gateway
EOF

  log "CA and trust bundle configured"
}

install_envoy_gateway() {
  log "Installing Envoy Gateway ${ENVOY_GATEWAY_VERSION}..."
  helm upgrade --install envoy-gateway \
    oci://docker.io/envoyproxy/gateway-helm \
    --version "${ENVOY_GATEWAY_VERSION}" \
    --namespace envoy-gateway \
    --create-namespace \
    --wait --timeout 5m

  wait_for_crd "envoyproxies.gateway.envoyproxy.io"
  wait_for_crd "gatewayclasses.gateway.networking.k8s.io"
  wait_for_crd "gateways.gateway.networking.k8s.io"

  kubectl apply -f - <<EOF
apiVersion: gateway.envoyproxy.io/v1alpha1
kind: EnvoyProxy
metadata:
  name: default
  namespace: envoy-gateway
spec:
  provider:
    type: Kubernetes
    kubernetes:
      envoyService:
        type: NodePort
        patch:
          type: StrategicMerge
          value:
            spec:
              ports:
                - name: http
                  port: 80
                  nodePort: ${INTERNAL_HTTP_NODE_PORT}
                - name: https
                  port: 443
                  nodePort: ${INTERNAL_INGRESS_NODE_PORT}
---
apiVersion: gateway.networking.k8s.io/v1
kind: GatewayClass
metadata:
  name: default
spec:
  controllerName: gateway.envoyproxy.io/gatewayclass-controller
  parametersRef:
    group: gateway.envoyproxy.io
    kind: EnvoyProxy
    namespace: envoy-gateway
    name: default
---
apiVersion: gateway.networking.k8s.io/v1
kind: Gateway
metadata:
  name: default
  namespace: envoy-gateway
spec:
  gatewayClassName: default
  listeners:
    - name: http
      protocol: HTTP
      port: 80
      allowedRoutes:
        namespaces:
          from: All
    - name: tls
      protocol: TLS
      port: 443
      tls:
        mode: Passthrough
      allowedRoutes:
        namespaces:
          from: All
EOF

  log "Envoy Gateway configured with HTTP on NodePort ${INTERNAL_HTTP_NODE_PORT} and TLS passthrough on NodePort ${INTERNAL_INGRESS_NODE_PORT}"
}

install_authorino() {
  log "Installing Authorino ${AUTHORINO_VERSION}..."
  kubectl apply -f \
    "https://raw.githubusercontent.com/Kuadrant/authorino-operator/refs/heads/release-${AUTHORINO_VERSION}/config/deploy/manifests.yaml"
  wait_for_crd "authorinos.operator.authorino.kuadrant.io" 120
  wait_for_crd "authconfigs.authorino.kuadrant.io" 120
  log "Authorino installed"
}

# ── Data Services ──────────────────────────────────────────────────────────────

install_postgres() {
  local chart_dir="${REPO_ROOT}/fulfillment-service/it/charts/postgres"
  if [[ ! -d "$chart_dir" ]]; then
    err "PostgreSQL chart not found at ${chart_dir}"
    return 1
  fi

  log "Installing PostgreSQL..."
  kubectl create namespace "${OSAC_NAMESPACE}" 2>/dev/null || true

  helm upgrade --install postgres \
    "${chart_dir}" \
    --namespace "${OSAC_NAMESPACE}" \
    --set "certs.issuerRef.name=default-ca" \
    --set "certs.caBundle.configMap=ca-bundle" \
    --set "databases[0].name=service" \
    --set "databases[0].user=service" \
    --set "databases[1].name=keycloak" \
    --set "databases[1].user=keycloak" \
    --wait --timeout 5m

  log "PostgreSQL installed with 'service' and 'keycloak' databases"
}

create_database_resources() {
  log "Creating database resources..."

  # Fulfillment service database client cert + config
  kubectl apply -f - <<EOF
apiVersion: cert-manager.io/v1
kind: Certificate
metadata:
  name: fulfillment-database-client
  namespace: ${OSAC_NAMESPACE}
spec:
  issuerRef:
    kind: ClusterIssuer
    name: default-ca
  commonName: service
  usages: [client auth]
  secretName: fulfillment-database-client-cert
  privateKey:
    rotationPolicy: Always
EOF

  kubectl -n "${OSAC_NAMESPACE}" create configmap fulfillment-database-config \
    --from-literal=url="postgres://service@postgres.${OSAC_NAMESPACE}.svc.cluster.local:5432/service" \
    --from-literal=sslmode="verify-full" \
    --dry-run=client -o yaml | kubectl apply -f -

  # Keycloak database client cert (DER format required by JDBC driver) + config
  kubectl create namespace "${KEYCLOAK_NAMESPACE}" 2>/dev/null || true

  kubectl apply -f - <<EOF
apiVersion: cert-manager.io/v1
kind: Certificate
metadata:
  name: keycloak-database-client
  namespace: ${KEYCLOAK_NAMESPACE}
spec:
  issuerRef:
    kind: ClusterIssuer
    name: default-ca
  commonName: keycloak
  usages: [client auth]
  secretName: keycloak-database-client-cert
  privateKey:
    encoding: PKCS8
    rotationPolicy: Always
  additionalOutputFormats:
    - type: DER
EOF

  kubectl -n "${KEYCLOAK_NAMESPACE}" create configmap keycloak-database-config \
    --from-literal=url="postgres://keycloak@postgres.${OSAC_NAMESPACE}.svc.cluster.local:5432/keycloak" \
    --from-literal=user="keycloak" \
    --from-literal=password="" \
    --from-literal=sslmode="require" \
    --dry-run=client -o yaml | kubectl apply -f -

  wait_for_secret "${OSAC_NAMESPACE}" "fulfillment-database-client-cert"
  wait_for_secret "${KEYCLOAK_NAMESPACE}" "keycloak-database-client-cert"

  log "Database resources created"
}

install_keycloak() {
  local chart_dir="${REPO_ROOT}/fulfillment-service/it/charts/keycloak"
  if [[ ! -d "$chart_dir" ]]; then
    err "Keycloak chart not found at ${chart_dir}"
    return 1
  fi

  log "Installing Keycloak..."
  helm upgrade --install keycloak \
    "${chart_dir}" \
    --namespace "${KEYCLOAK_NAMESPACE}" \
    --create-namespace \
    --values "${SCRIPT_DIR}/keycloak-values.yaml" \
    --wait --timeout 10m

  # The chart hardcodes hostname-port=8000 but we need 8443 (the external port).
  # Override via KC_HOSTNAME_PORT env var by re-rendering the pod template.
  log "Patching Keycloak hostname-port to ${EXTERNAL_INGRESS_PORT}..."
  kubectl -n "${KEYCLOAK_NAMESPACE}" delete pod keycloak-service --wait 2>/dev/null || true

  helm template keycloak "${chart_dir}" \
    --namespace "${KEYCLOAK_NAMESPACE}" \
    --values "${SCRIPT_DIR}/keycloak-values.yaml" \
    -s templates/pod.yaml | \
    sed "/KC_BOOTSTRAP_ADMIN_PASSWORD/,/value:/{
      /value:/a\\
    - name: KC_HOSTNAME_PORT\\
      value: \"${EXTERNAL_INGRESS_PORT}\"
    }" | kubectl apply -f -

  log "Waiting for Keycloak to be ready..."
  kubectl -n "${KEYCLOAK_NAMESPACE}" wait --for=condition=Ready pod/keycloak-service --timeout=300s

  # Create a service on port 8443 so pods can reach keycloak.osac.localhost:8443
  # (CoreDNS rewrites the hostname, this service maps the external port to the pod port)
  kubectl apply -f - <<EOF
apiVersion: v1
kind: Service
metadata:
  name: keycloak-external
  namespace: ${KEYCLOAK_NAMESPACE}
spec:
  selector:
    app: keycloak-service
  ports:
  - port: ${EXTERNAL_INGRESS_PORT}
    targetPort: 8000
    protocol: TCP
    name: https
  type: ClusterIP
EOF

  log "Keycloak installed — admin UI: https://keycloak.${OSAC_NAMESPACE}.localhost:${EXTERNAL_INGRESS_PORT}/admin"
}

create_external_tlsroutes() {
  log "Creating external TLSRoutes for *.osac.localhost..."

  kubectl apply -f - <<EOF
apiVersion: gateway.networking.k8s.io/v1alpha3
kind: TLSRoute
metadata:
  name: fulfillment-api-external
  namespace: ${OSAC_NAMESPACE}
spec:
  parentRefs:
  - group: gateway.networking.k8s.io
    kind: Gateway
    namespace: envoy-gateway
    name: default
    sectionName: tls
  hostnames:
  - api.${OSAC_NAMESPACE}.localhost
  rules:
  - backendRefs:
    - name: fulfillment-api
      port: 8000
---
apiVersion: gateway.networking.k8s.io/v1alpha3
kind: TLSRoute
metadata:
  name: fulfillment-internal-api-external
  namespace: ${OSAC_NAMESPACE}
spec:
  parentRefs:
  - group: gateway.networking.k8s.io
    kind: Gateway
    namespace: envoy-gateway
    name: default
    sectionName: tls
  hostnames:
  - internal-api.${OSAC_NAMESPACE}.localhost
  rules:
  - backendRefs:
    - name: fulfillment-internal-api
      port: 8001
---
apiVersion: gateway.networking.k8s.io/v1alpha3
kind: TLSRoute
metadata:
  name: keycloak-external
  namespace: ${KEYCLOAK_NAMESPACE}
spec:
  parentRefs:
  - group: gateway.networking.k8s.io
    kind: Gateway
    namespace: envoy-gateway
    name: default
    sectionName: tls
  hostnames:
  - keycloak.${KEYCLOAK_NAMESPACE}.localhost
  rules:
  - backendRefs:
    - name: keycloak
      port: 8000
EOF

  log "External TLSRoutes created"
}

install_openbao() {
  local chart_dir="${REPO_ROOT}/fulfillment-service/it/charts/openbao"
  if [[ ! -d "$chart_dir" ]]; then
    err "OpenBao chart not found at ${chart_dir}"
    return 1
  fi

  log "Installing OpenBao..."
  helm upgrade --install openbao \
    "${chart_dir}" \
    --namespace openbao \
    --create-namespace \
    --set "dev.rootToken=dev-root-token" \
    --set "caBundle.configMap=ca-bundle" \
    --wait --timeout 2m

  log "OpenBao installed"
}

configure_openbao() {
  log "Configuring OpenBao (creating parent namespace)..."

  local openbao_token="dev-root-token"

  kubectl port-forward svc/openbao 8200:8200 -n openbao &
  local pf_pid=$!

  cleanup_port_forward() {
    kill "${pf_pid}" 2>/dev/null || true
    wait "${pf_pid}" 2>/dev/null || true
  }

  local attempts=0
  while ! curl -sf http://localhost:8200/v1/sys/health >/dev/null 2>&1; do
    attempts=$((attempts + 1))
    if [[ $attempts -ge 30 ]]; then
      err "OpenBao port-forward did not become reachable"
      cleanup_port_forward
      return 1
    fi
    sleep 1
  done

  local status
  status=$(curl -s -o /dev/null -w '%{http_code}' -X POST \
    http://localhost:8200/v1/sys/namespaces/osac \
    -H "X-Vault-Token: ${openbao_token}")

  case "$status" in
    200|204) log "Created 'osac' namespace in OpenBao" ;;
    400)     log "OpenBao 'osac' namespace already exists (idempotent)" ;;
    *)       err "Failed to create 'osac' namespace in OpenBao (HTTP ${status})"
             cleanup_port_forward
             return 1 ;;
  esac

  cleanup_port_forward

  log "OpenBao configured"
}

create_controller_credentials() {
  log "Creating controller credentials..."

  kubectl -n "${OSAC_NAMESPACE}" create secret generic fulfillment-controller-credentials \
    --from-literal=client-id=osac-controller \
    --from-literal=client-secret=password \
    --dry-run=client -o yaml | kubectl apply -f -

  log "Controller credentials created"
}

# ── OSAC Services (umbrella chart) ─────────────────────────────────────────────

install_fake_crds() {
  local fakes_dir="${REPO_ROOT}/osac-operator/config/crd/fakes"
  if [[ ! -d "$fakes_dir" ]]; then
    warn "Fake CRDs not found at ${fakes_dir} — skipping"
    return 0
  fi

  log "Installing fake CRDs (HyperShift, KubeVirt, OVN-K)..."
  for f in "${fakes_dir}"/*.yaml; do
    local base
    base=$(basename "$f")
    [[ "$base" == "kustomization.yaml" ]] && continue
    # Skip CRDs managed by the umbrella chart
    [[ "$base" == *"osac.openshift.io"* ]] && continue
    kubectl apply -f "$f" 2>/dev/null || true
  done
  # ClusterUserDefinedNetwork CRD — needed by the cudn_net Ansible role for
  # subnet provisioning. OVN-Kubernetes is not installed on kind, but the CRD
  # must exist so the k8s module can create CUDN resources without errors.
  kubectl apply -f - <<'CRDEOF'
apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: clusteruserdefinednetworks.k8s.ovn.org
spec:
  group: k8s.ovn.org
  versions:
    - name: v1
      served: true
      storage: true
      schema:
        openAPIV3Schema:
          type: object
          properties:
            spec:
              type: object
              x-kubernetes-preserve-unknown-fields: true
            status:
              type: object
              x-kubernetes-preserve-unknown-fields: true
  scope: Cluster
  names:
    plural: clusteruserdefinednetworks
    singular: clusteruserdefinednetwork
    kind: ClusterUserDefinedNetwork
    shortNames:
      - cudn
CRDEOF

  log "Fake CRDs installed"
}

deploy_osac() {
  local installer_dir="${REPO_ROOT}/osac-installer"
  local chart_dir="${installer_dir}/charts/osac"

  if [[ ! -d "$chart_dir" ]]; then
    err "Umbrella chart not found at ${chart_dir}"
    return 1
  fi

  log "Building umbrella chart dependencies..."
  helm dependency update "${chart_dir}" 2>&1 | tail -3

  log "Deploying OSAC via umbrella chart..."
  local helm_args=(
    upgrade --install osac
    "${chart_dir}"
    --namespace "${OSAC_NAMESPACE}"
    --create-namespace
    --values "${SCRIPT_DIR}/values-kind.yaml"
  )
  if [[ -n "${FULFILLMENT_IMAGE:-}" ]]; then
    helm_args+=(--set "service.images.service=${FULFILLMENT_IMAGE}")
    log "Using custom fulfillment image: ${FULFILLMENT_IMAGE}"
  fi
  helm "${helm_args[@]}"

  # Workaround: Remove console-proxy readiness probe (TLS verification fails with self-signed certs in kind)
  log "Waiting for deployments to be ready..."
  sleep 5  # Give helm time to create resources

  if kubectl get deployment -n "${OSAC_NAMESPACE}" fulfillment-console-proxy >/dev/null 2>&1; then
    kubectl patch deployment -n "${OSAC_NAMESPACE}" fulfillment-console-proxy \
      --type=json \
      -p='[{"op": "remove", "path": "/spec/template/spec/containers/0/readinessProbe"}]' 2>/dev/null || true
  fi

  # Wait for all deployments except we already patched console-proxy
  kubectl wait --for=condition=Available --timeout=10m \
    -n "${OSAC_NAMESPACE}" \
    deployment --all 2>/dev/null || warn "Some deployments may not be ready"

  log "OSAC deployed via umbrella chart"
}

# ── OSAC UI ───────────────────────────────────────────────────────────────────

deploy_osac_ui() {
  log "Deploying OSAC UI..."
  kubectl apply -f "${SCRIPT_DIR}/osac-ui-manifests.yaml"
  kubectl apply -f "${SCRIPT_DIR}/httproute-ui.yaml"
  kubectl -n "${OSAC_NAMESPACE}" rollout status deployment osac-ui --timeout=120s
  log "OSAC UI deployed — http://ui.${OSAC_NAMESPACE}.localhost:8080"
}

register_hub() {
  if ! command -v grpcurl >/dev/null 2>&1; then
    warn "grpcurl not found — skipping automatic hub registration"
    warn "Install grpcurl: brew install grpcurl (macOS) or go install github.com/fullstorydev/grpcurl/cmd/grpcurl@latest"
    warn "Then register manually or re-run setup.sh"
    return 0
  fi

  log "Registering kind cluster as hub..."

  local hub_token
  hub_token=$(kubectl -n "${OSAC_NAMESPACE}" create token admin --duration=87600h)

  kubectl create clusterrolebinding fulfillment-controller-admin \
    --clusterrole=cluster-admin \
    --serviceaccount="${OSAC_NAMESPACE}:admin" \
    --dry-run=client -o yaml | kubectl apply -f -

  local ca_data
  ca_data=$(kubectl config view --raw -o jsonpath='{.clusters[0].cluster.certificate-authority-data}')

  local kubeconfig
  # base64 without line breaks (cross-platform: use tr -d '\n' instead of -w0)
  kubeconfig=$(printf '{
    "apiVersion": "v1",
    "kind": "Config",
    "clusters": [{"name": "kind", "cluster": {"server": "https://kubernetes.default.svc.cluster.local:443", "certificate-authority-data": "%s"}}],
    "users": [{"name": "admin", "user": {"token": "%s"}}],
    "contexts": [{"name": "kind", "context": {"cluster": "kind", "user": "admin", "namespace": "%s"}}],
    "current-context": "kind"
  }' "${ca_data}" "${hub_token}" "${OSAC_NAMESPACE}" | base64 | tr -d '\n')

  local admin_token
  admin_token=$(kubectl -n "${OSAC_NAMESPACE}" create token admin)

  if grpcurl -insecure -H "Authorization: Bearer ${admin_token}" \
    -d "{\"object\":{\"metadata\":{\"name\":\"kind-dev\"},\"spec\":{\"kubeconfig\":\"${kubeconfig}\",\"namespace\":\"${OSAC_NAMESPACE}\"}}}" \
    internal-api."${OSAC_NAMESPACE}".localhost:8443 osac.private.v1.Hubs/Create >/dev/null 2>&1; then
    log "Hub registered — networking resources will now reconcile to CRs"
  else
    warn "Hub registration failed — see 'Open items' in summary for manual registration"
  fi
}

# ── KubeVirt ───────────────────────────────────────────────────────────────────

install_multus() {
  log "Installing Multus CNI..."

  kubectl apply -f https://raw.githubusercontent.com/k8snetworkplumbingwg/multus-cni/master/deployments/multus-daemonset.yml 2>&1 | tail -3
  kubectl -n kube-system wait --for=condition=Ready pods -l app=multus --timeout=120s

  log "Installing bridge CNI plugin into kind node..."
  local node_name="${CLUSTER_NAME}-control-plane"
  container_cmd exec "${node_name}" bash -c \
    'curl -sL https://github.com/containernetworking/plugins/releases/download/v1.6.2/cni-plugins-linux-amd64-v1.6.2.tgz | tar -C /opt/cni/bin -xz'

  log "Multus installed"
}

install_kubevirt() {
  log "Installing KubeVirt..."

  # Remove fake KubeVirt CRDs — they conflict with the real operator
  log "Removing fake KubeVirt CRDs..."
  kubectl delete crd virtualmachines.kubevirt.io virtualmachineinstances.kubevirt.io 2>/dev/null || true

  local version
  version=$(curl -s https://storage.googleapis.com/kubevirt-prow/release/kubevirt/kubevirt/stable.txt)
  log "KubeVirt version: ${version}"

  kubectl apply -f "https://github.com/kubevirt/kubevirt/releases/download/${version}/kubevirt-operator.yaml" 2>&1 | tail -3
  kubectl wait --for=condition=available --timeout=120s -n kubevirt deployments -l kubevirt.io

  kubectl apply -f "https://github.com/kubevirt/kubevirt/releases/download/${version}/kubevirt-cr.yaml"
  kubectl -n kubevirt wait kv kubevirt --for condition=Available --timeout=300s

  # Register l2bridge network binding plugin (replicates what HCO does on OpenShift).
  # The osac-aap ocp_virt_vm role builds VM specs with "binding: name: l2bridge".
  # managedTap creates a tap device wired through a bridge to the pod interface.
  log "Registering l2bridge network binding plugin..."
  kubectl patch kubevirts -n kubevirt kubevirt --type=merge \
    -p='{"spec":{"configuration":{"network":{"binding":{"l2bridge":{"domainAttachmentType":"managedTap"}}}}}}'

  log "KubeVirt installed"
}

install_cdi() {
  log "Installing CDI (Containerized Data Importer)..."

  local version
  version=$(curl -s https://api.github.com/repos/kubevirt/containerized-data-importer/releases/latest | python3 -c "import json,sys; print(json.load(sys.stdin)['tag_name'])")
  log "CDI version: ${version}"

  kubectl apply -f "https://github.com/kubevirt/containerized-data-importer/releases/download/${version}/cdi-operator.yaml" 2>&1 | tail -3
  kubectl apply -f "https://github.com/kubevirt/containerized-data-importer/releases/download/${version}/cdi-cr.yaml"
  kubectl wait --for=condition=available --timeout=120s -n cdi deployments -l cdi.kubevirt.io

  # Disable HonorWaitForFirstConsumer — local-path-provisioner deadlocks with
  # WaitForFirstConsumer when CDI tries to import a disk image (no consumer pod
  # exists yet to trigger provisioning). Removing the feature gate lets CDI
  # create an importer pod immediately, which triggers the provisioner.
  kubectl patch cdi cdi --type=json \
    -p '[{"op":"replace","path":"/spec/config/featureGates","value":["WebhookPvcRendering"]}]'

  log "CDI installed"
}

# ── AWX ────────────────────────────────────────────────────────────────────────

install_awx() {
  log "Installing AWX operator..."

  helm repo add awx-operator https://ansible-community.github.io/awx-operator-helm/ 2>/dev/null || true
  helm upgrade --install awx-operator awx-operator/awx-operator -n awx --create-namespace --wait --timeout 3m 2>&1 | tail -2

  log "Creating AWX instance..."
  kubectl apply -f "${SCRIPT_DIR}/awx/awx-instance.yaml"

  log "Waiting for AWX pods (this takes ~10 minutes)..."
  for i in $(seq 1 60); do
    local task_ready
    task_ready=$(kubectl -n awx get pods -l app.kubernetes.io/component=awx-task --no-headers 2>/dev/null | grep -c "4/4" || true)
    if [[ "$task_ready" -ge 1 ]]; then
      break
    fi
    sleep 10
  done

  kubectl -n awx get pods 2>/dev/null | grep -v Completed
  log "AWX installed"
}

configure_awx() {
  log "Configuring AWX for OSAC..."

  # Add HTTP listener to gateway and HTTPRoute for AWX web UI
  kubectl apply -f "${SCRIPT_DIR}/awx/gateway-with-http.yaml"
  kubectl apply -f "${SCRIPT_DIR}/awx/httproute.yaml"

  local admin_pass awx_token project_id inv_id
  admin_pass=$(kubectl -n awx get secret awx-admin-password -o jsonpath='{.data.password}' | base64 -d)

  # Kill any existing port-forward on 8052
  lsof -ti:8052 | xargs kill -9 2>/dev/null || true
  sleep 1

  # Port-forward for API access
  kubectl -n awx port-forward svc/awx-service 8052:80 >/dev/null 2>&1 &
  local pf_pid=$!
  sleep 3

  # Create OAuth token
  awx_token=$(curl -s -X POST http://localhost:8052/api/v2/tokens/ \
    -u "admin:${admin_pass}" \
    -H "Content-Type: application/json" \
    -d '{"scope": "write"}' | python3 -c "import json,sys; data=json.load(sys.stdin); print(data.get('token',''))")

  if [[ -z "$awx_token" ]]; then
    warn "Failed to create AWX token - AWX may not be ready yet"
    kill $pf_pid 2>/dev/null || true
    wait $pf_pid 2>/dev/null || true
    return 1
  fi

  log "AWX token created"

  # Create inventory (or get existing)
  local inv_response
  inv_response=$(curl -s -X POST http://localhost:8052/api/v2/inventories/ \
    -H "Authorization: Bearer ${awx_token}" \
    -H "Content-Type: application/json" \
    -d '{"name": "OSAC Dev", "organization": 1}')

  inv_id=$(echo "$inv_response" | python3 -c "import json,sys; data=json.load(sys.stdin); print(data.get('id', ''))" 2>/dev/null)

  # If creation failed (inventory exists), get it by name
  if [[ -z "$inv_id" ]]; then
    inv_id=$(curl -s -H "Authorization: Bearer ${awx_token}" \
      "http://localhost:8052/api/v2/inventories/?name=OSAC+Dev" | \
      python3 -c "import json,sys; data=json.load(sys.stdin); print(data['results'][0]['id'] if data.get('results') else '')")
  fi

  # Add localhost host (ignore if already exists)
  curl -s -X POST "http://localhost:8052/api/v2/inventories/${inv_id}/hosts/" \
    -H "Authorization: Bearer ${awx_token}" \
    -H "Content-Type: application/json" \
    -d '{"name": "localhost", "variables": "ansible_connection: local"}' >/dev/null 2>&1 || true

  # Disable collection sync (ansible.platform not available in open-source AWX)
  curl -s -X PATCH http://localhost:8052/api/v2/settings/jobs/ \
    -H "Authorization: Bearer ${awx_token}" \
    -H "Content-Type: application/json" \
    -d '{"AWX_COLLECTIONS_ENABLED": false, "AWX_ROLES_ENABLED": false}' >/dev/null

  # Create project from the osac mono-repo (or get existing). osac-aap's playbooks
  # now live in the osac-aap/ subdirectory of osac-project/osac — AWX's Project API
  # has no sparse-checkout/subdirectory field (it always clones the whole repo), so
  # each job template's "playbook" path below is prefixed with osac-aap/ instead.
  local project_response
  project_response=$(curl -s -X POST http://localhost:8052/api/v2/projects/ \
    -H "Authorization: Bearer ${awx_token}" \
    -H "Content-Type: application/json" \
    -d '{
      "name": "osac-aap",
      "organization": 1,
      "scm_type": "git",
      "scm_url": "https://github.com/osac-project/osac.git",
      "scm_branch": "main",
      "scm_update_on_launch": false
    }')

  # Extract project ID (handle both new creation and existing project)
  project_id=$(echo "$project_response" | python3 -c "import json,sys; data=json.load(sys.stdin); print(data.get('id', ''))" 2>/dev/null)

  # If creation failed (project exists), get it by name. A project surviving
  # from a pre-mono-repo run of this script still has the old standalone
  # osac-aap scm_url, so patch it to the current one and trigger a resync —
  # otherwise the reused project keeps cloning the wrong (now-merged) repo.
  if [[ -z "$project_id" ]]; then
    project_id=$(curl -s -H "Authorization: Bearer ${awx_token}" \
      "http://localhost:8052/api/v2/projects/?name=osac-aap" | \
      python3 -c "import json,sys; data=json.load(sys.stdin); print(data['results'][0]['id'] if data.get('results') else '')")

    if [[ -n "$project_id" ]]; then
      curl -s -X PATCH "http://localhost:8052/api/v2/projects/${project_id}/" \
        -H "Authorization: Bearer ${awx_token}" \
        -H "Content-Type: application/json" \
        -d '{
          "scm_url": "https://github.com/osac-project/osac.git",
          "scm_branch": "main"
        }' >/dev/null
      curl -s -X POST "http://localhost:8052/api/v2/projects/${project_id}/update/" \
        -H "Authorization: Bearer ${awx_token}" >/dev/null
    fi
  fi

  # Wait for project sync
  local proj_status="unknown"
  for i in $(seq 1 20); do
    proj_status=$(curl -s -H "Authorization: Bearer ${awx_token}" \
      "http://localhost:8052/api/v2/projects/${project_id}/" | \
      python3 -c "import json,sys; data=json.load(sys.stdin); print(data.get('status','unknown'))" 2>/dev/null || echo "unknown")
    if [[ "$proj_status" == "successful" || "$proj_status" == "failed" ]]; then break; fi
    sleep 5
  done
  log "AWX project synced: ${proj_status}"

  # Create compute instance job templates (real playbooks)
  local compute_extra_vars
  compute_extra_vars="tenant_target_namespace: ${OSAC_NAMESPACE}
compute_instance_target_namespace: ${OSAC_NAMESPACE}
tenant_storage_classes:
  - name: standard
    tier: default"

  local compute_templates=(
    "osac-create-compute-instance:osac-aap/playbook_osac_create_compute_instance.yml"
    "osac-delete-compute-instance:osac-aap/playbook_osac_delete_compute_instance.yml"
  )

  for entry in "${compute_templates[@]}"; do
    local name="${entry%%:*}" playbook="${entry##*:}"
    curl -s -X POST http://localhost:8052/api/v2/job_templates/ \
      -H "Authorization: Bearer ${awx_token}" \
      -H "Content-Type: application/json" \
      -d "{
        \"name\": \"${name}\",
        \"organization\": 1,
        \"inventory\": ${inv_id},
        \"project\": ${project_id},
        \"playbook\": \"${playbook}\",
        \"ask_variables_on_launch\": true,
        \"extra_vars\": $(echo "${compute_extra_vars}" | jq -Rs .)
      }" >/dev/null
    log "  template: ${name}"
  done

  # Create networking job templates (use real playbooks — they are effective no-ops
  # on kind because there is no matching implementation strategy / fabric manager)
  local network_templates=(
    "osac-create-virtual-network:osac-aap/playbook_osac_create_virtual_network.yml"
    "osac-delete-virtual-network:osac-aap/playbook_osac_delete_virtual_network.yml"
    "osac-create-subnet:osac-aap/playbook_osac_create_subnet.yml"
    "osac-delete-subnet:osac-aap/playbook_osac_delete_subnet.yml"
    "osac-create-security-group:osac-aap/playbook_osac_create_security_group.yml"
    "osac-delete-security-group:osac-aap/playbook_osac_delete_security_group.yml"
  )

  for entry in "${network_templates[@]}"; do
    local name="${entry%%:*}" playbook="${entry##*:}"
    curl -s -X POST http://localhost:8052/api/v2/job_templates/ \
      -H "Authorization: Bearer ${awx_token}" \
      -H "Content-Type: application/json" \
      -d "{
        \"name\": \"${name}\",
        \"organization\": 1,
        \"inventory\": ${inv_id},
        \"project\": ${project_id},
        \"playbook\": \"${playbook}\",
        \"ask_variables_on_launch\": true
      }" >/dev/null
    log "  template: ${name}"
  done

  # Create Kubernetes credential for AWX
  kubectl -n "${OSAC_NAMESPACE}" create serviceaccount awx-runner 2>/dev/null || true
  kubectl create clusterrolebinding awx-runner-admin --clusterrole=cluster-admin --serviceaccount="${OSAC_NAMESPACE}:awx-runner" 2>/dev/null || true

  local awx_runner_token cluster_ca
  awx_runner_token=$(kubectl -n "${OSAC_NAMESPACE}" create token awx-runner --duration=87600h)
  cluster_ca=$(kubectl config view --raw -o jsonpath='{.clusters[0].cluster.certificate-authority-data}' | base64 -d)

  # Create Kubernetes credential
  local cred_response cred_id
  cred_response=$(curl -s -X POST http://localhost:8052/api/v2/credentials/ \
    -H "Authorization: Bearer ${awx_token}" \
    -H "Content-Type: application/json" \
    -d "{
      \"name\": \"kind-cluster\",
      \"organization\": 1,
      \"credential_type\": 17,
      \"inputs\": {
        \"host\": \"https://kubernetes.default.svc.cluster.local:443\",
        \"bearer_token\": \"${awx_runner_token}\",
        \"verify_ssl\": true,
        \"ssl_ca_cert\": $(echo "${cluster_ca}" | jq -Rs .)
      }
    }")
  cred_id=$(echo "${cred_response}" | python3 -c "import json,sys; print(json.load(sys.stdin).get('id',''))")

  # Attach credential to all job templates
  local templates
  templates=$(curl -s -H "Authorization: Bearer ${awx_token}" \
    http://localhost:8052/api/v2/job_templates/ | \
    python3 -c "import json,sys; print(' '.join(str(t['id']) for t in json.load(sys.stdin)['results']))")

  for jt_id in ${templates}; do
    curl -s -X POST "http://localhost:8052/api/v2/job_templates/${jt_id}/credentials/" \
      -H "Authorization: Bearer ${awx_token}" \
      -H "Content-Type: application/json" \
      -d "{\"id\": ${cred_id}}" >/dev/null
  done

  log "Credential ${cred_id} attached to all templates"

  kill $pf_pid 2>/dev/null || true
  wait $pf_pid 2>/dev/null || true

  # Store AWX token as K8s secret for the operator
  kubectl -n "${OSAC_NAMESPACE}" create secret generic awx-token \
    --from-literal=token="${awx_token}" \
    --dry-run=client -o yaml | kubectl apply -f -

  log "AWX configured for OSAC"
}

# ── Catalog Seeding ───────────────────────────────────────────────────────────

seed_catalog() {
  log "Seeding compute instance template and instance types..."

  local admin_token api_base
  admin_token=$(kubectl -n "${OSAC_NAMESPACE}" create token admin)
  api_base="https://internal-api.${OSAC_NAMESPACE}.localhost:${EXTERNAL_INGRESS_PORT}"

  # Seed ComputeInstance template (osac.templates.ocp_virt_vm)
  local tpl_response
  tpl_response=$(curl -sk -X POST "${api_base}/api/private/v1/compute_instance_templates" \
    -H "Authorization: Bearer ${admin_token}" \
    -H "Content-Type: application/json" \
    -d '{
      "id": "osac.templates.ocp_virt_vm",
      "title": "Virtual Machine Template (Linux and Windows)",
      "description": "VM template for OpenShift Virtualization supporting Linux and Windows guests.",
      "spec_defaults": {
        "cores": 2,
        "memory_gib": 2,
        "boot_disk": {"size_gib": 10},
        "image": {"source_type": "registry", "source_ref": "quay.io/containerdisks/fedora:latest"},
        "run_strategy": "Always"
      },
      "parameters": [
        {
          "name": "exposed_ports",
          "title": "Exposed Ports",
          "description": "Ports to expose (e.g. 22/tcp,80/tcp)",
          "type": "string",
          "required": false
        }
      ]
    }')

  if echo "$tpl_response" | python3 -c "import json,sys; d=json.load(sys.stdin); sys.exit(0 if 'id' in d else 1)" 2>/dev/null; then
    log "  template: osac.templates.ocp_virt_vm"
  else
    warn "  template creation failed (may already exist)"
  fi

  # Seed InstanceTypes
  local instance_types=(
    "u1-small:2:4:2 cores, 4 GiB RAM"
    "u1-medium:4:8:4 cores, 8 GiB RAM"
    "u1-large:8:16:8 cores, 16 GiB RAM"
  )

  for entry in "${instance_types[@]}"; do
    local name cores mem desc
    name="${entry%%:*}"; entry="${entry#*:}"
    cores="${entry%%:*}"; entry="${entry#*:}"
    mem="${entry%%:*}"; desc="${entry#*:}"

    local it_response
    it_response=$(curl -sk -X POST "${api_base}/api/private/v1/instance_types" \
      -H "Authorization: Bearer ${admin_token}" \
      -H "Content-Type: application/json" \
      -d "{
        \"metadata\": {\"name\": \"${name}\"},
        \"spec\": {\"cores\": ${cores}, \"memory_gib\": ${mem}, \"description\": \"${desc}\", \"state\": \"INSTANCE_TYPE_STATE_ACTIVE\"}
      }")

    if echo "$it_response" | python3 -c "import json,sys; d=json.load(sys.stdin); sys.exit(0 if 'id' in d else 1)" 2>/dev/null; then
      log "  instance-type: ${name} (${desc})"
    else
      warn "  instance-type ${name} creation failed (may already exist)"
    fi
  done

  # Seed catalog items (visible in UI)
  local catalog_items=(
    'linux-vm:Linux Virtual Machine:Fedora-based virtual machine with KVM acceleration. Default: 2 cores, 2 GiB RAM, 10 GiB disk.'
  )

  for entry in "${catalog_items[@]}"; do
    local ci_name ci_title ci_desc
    ci_name="${entry%%:*}"; entry="${entry#*:}"
    ci_title="${entry%%:*}"; ci_desc="${entry#*:}"

    local ci_response
    ci_response=$(curl -sk -X POST "${api_base}/api/private/v1/compute_instance_catalog_items" \
      -H "Authorization: Bearer ${admin_token}" \
      -H "Content-Type: application/json" \
      -d "{
        \"metadata\": {\"name\": \"${ci_name}\"},
        \"title\": \"${ci_title}\",
        \"description\": \"${ci_desc}\",
        \"template\": \"osac.templates.ocp_virt_vm\",
        \"published\": true,
        \"tenant\": \"\"
      }")

    if echo "$ci_response" | python3 -c "import json,sys; d=json.load(sys.stdin); sys.exit(0 if 'id' in d else 1)" 2>/dev/null; then
      log "  catalog-item: ${ci_name} (${ci_title})"
    else
      warn "  catalog-item ${ci_name} creation failed (may already exist)"
    fi
  done

  # Seed networking resources (NetworkClass → VirtualNetwork → Subnet)
  log "Seeding networking resources..."

  local nc_response
  nc_response=$(curl -sk -X POST "${api_base}/api/private/v1/network_classes" \
    -H "Authorization: Bearer ${admin_token}" \
    -H "Content-Type: application/json" \
    -d '{
      "metadata": {"name": "pod-network"},
      "title": "Pod Network (kind)",
      "description": "Default network for kind dev. Uses cudn_net role — creates namespace but no real L2.",
      "implementation_strategy": "cudn_net",
      "fabric_manager": "noop",
      "is_default": true,
      "capabilities": {"supports_ipv4": true}
    }')

  if echo "$nc_response" | python3 -c "import json,sys; d=json.load(sys.stdin); sys.exit(0 if 'id' in d else 1)" 2>/dev/null; then
    log "  network-class: pod-network (default)"
  else
    warn "  network-class creation failed (may already exist)"
  fi

  local vn_response vn_id
  vn_response=$(curl -sk -X POST "${api_base}/api/private/v1/virtual_networks" \
    -H "Authorization: Bearer ${admin_token}" \
    -H "Content-Type: application/json" \
    -d '{
      "metadata": {"name": "default"},
      "spec": {
        "region": "kind",
        "ipv4_cidr": "10.100.0.0/16"
      }
    }')

  vn_id=$(echo "$vn_response" | python3 -c "import json,sys; print(json.load(sys.stdin).get('id',''))" 2>/dev/null)
  if [[ -n "$vn_id" ]]; then
    log "  virtual-network: default (id=${vn_id})"
  else
    warn "  virtual-network creation failed (may already exist)"
    vn_id=$(curl -sk -H "Authorization: Bearer ${admin_token}" \
      "${api_base}/api/private/v1/virtual_networks" 2>/dev/null | \
      python3 -c "import json,sys; items=json.load(sys.stdin).get('items',[]); print(next((i['id'] for i in items if i.get('metadata',{}).get('name')=='default'), ''))" 2>/dev/null)
  fi

  # Wait for VirtualNetwork to reach READY (operator runs AWX no-op job)
  if [[ -n "$vn_id" ]]; then
    log "  waiting for virtual-network to become READY..."
    local vn_state="unknown"
    for i in $(seq 1 30); do
      vn_state=$(curl -sk -H "Authorization: Bearer ${admin_token}" \
        "${api_base}/api/private/v1/virtual_networks/${vn_id}" 2>/dev/null | \
        python3 -c "import json,sys; print(json.load(sys.stdin).get('status',{}).get('state','unknown'))" 2>/dev/null)
      if [[ "$vn_state" == "VIRTUAL_NETWORK_STATE_READY" ]]; then break; fi
      sleep 5
    done

    if [[ "$vn_state" == "VIRTUAL_NETWORK_STATE_READY" ]]; then
      log "  virtual-network: READY"

      local sn_response
      sn_response=$(curl -sk -X POST "${api_base}/api/private/v1/subnets" \
        -H "Authorization: Bearer ${admin_token}" \
        -H "Content-Type: application/json" \
        -d "{
          \"metadata\": {\"name\": \"default\"},
          \"spec\": {
            \"virtual_network\": \"${vn_id}\",
            \"ipv4_cidr\": \"10.100.0.0/24\"
          }
        }")

      if echo "$sn_response" | python3 -c "import json,sys; d=json.load(sys.stdin); sys.exit(0 if 'id' in d else 1)" 2>/dev/null; then
        log "  subnet: default (10.100.0.0/24)"
      else
        warn "  subnet creation failed: $(echo "$sn_response" | python3 -c "import json,sys; print(json.load(sys.stdin).get('message','unknown'))" 2>/dev/null)"
      fi

      # Security group — allow SSH inbound, all outbound
      local sg_response
      sg_response=$(curl -sk -X POST "${api_base}/api/private/v1/security_groups" \
        -H "Authorization: Bearer ${admin_token}" \
        -H "Content-Type: application/json" \
        -d "{
          \"metadata\": {\"name\": \"default\"},
          \"spec\": {
            \"virtual_network\": \"${vn_id}\",
            \"ingress\": [{\"protocol\": \"PROTOCOL_TCP\", \"port_from\": 22, \"port_to\": 22, \"ipv4_cidr\": \"0.0.0.0/0\"}],
            \"egress\": [{\"protocol\": \"PROTOCOL_ALL\", \"ipv4_cidr\": \"0.0.0.0/0\"}]
          }
        }")

      if echo "$sg_response" | python3 -c "import json,sys; d=json.load(sys.stdin); sys.exit(0 if 'id' in d else 1)" 2>/dev/null; then
        log "  security-group: default (SSH in, all out)"
      else
        warn "  security-group creation failed (may already exist)"
      fi
    else
      warn "  virtual-network did not reach READY (state=${vn_state}) — skipping subnet/security-group creation"
      warn "  Create them manually once the VN is ready"
    fi
  fi

  log "Catalog seeded — ready to create compute instances"
}

# ── Summary ────────────────────────────────────────────────────────────────────

print_summary() {
  echo ""
  echo "============================================="
  echo "  OSAC Kind Development Environment Ready"
  echo "============================================="
  echo ""
  info "Cluster:    ${CLUSTER_NAME}"
  info "Kubeconfig: export KUBECONFIG=${KUBECONFIG}"
  echo ""

  if [[ "$CLUSTER_ONLY" == "true" ]]; then
    info "Cluster created with CoreDNS *.localhost rewrite."
    info "Use <service>.<namespace>.localhost from your laptop and from inside pods."
    return
  fi

  if [[ "$SKIP_OSAC" == "true" ]]; then
    info "Infrastructure deployed (cert-manager, envoy, keycloak, postgres)."
    info "Run again without --skip-osac to deploy OSAC services."
    echo ""
  fi

  if [[ "$(uname -s)" == "Darwin" ]]; then
    # Test if *.localhost resolves automatically (macOS 14+ / Sonoma+)
    if ! ping -c 1 -W 1 test.localhost >/dev/null 2>&1; then
      warn "macOS Ventura and earlier require /etc/hosts entries for *.localhost domains:"
      echo "  sudo tee -a /etc/hosts <<EOF"
      echo "127.0.0.1 ui.${OSAC_NAMESPACE}.localhost api.${OSAC_NAMESPACE}.localhost internal-api.${OSAC_NAMESPACE}.localhost keycloak.${OSAC_NAMESPACE}.localhost"
      echo "EOF"
      echo ""
    fi
  fi

  info "Access:"
  echo "  OSAC UI:          http://ui.${OSAC_NAMESPACE}.localhost:8080"
  echo "  OSAC API:         https://api.${OSAC_NAMESPACE}.localhost:${EXTERNAL_INGRESS_PORT}"
  echo "  OSAC Internal:    https://internal-api.${OSAC_NAMESPACE}.localhost:${EXTERNAL_INGRESS_PORT}"
  echo "  Keycloak Admin:   https://keycloak.${OSAC_NAMESPACE}.localhost:${EXTERNAL_INGRESS_PORT}/admin  (admin/password)"
  echo ""
  info "CLI quickstart:"
  echo "  cd fulfillment-service && go build -o osac ./cmd/osac"
  echo "  TOKEN=\$(kubectl -n ${OSAC_NAMESPACE} create token admin --duration=1h)"
  echo "  ./osac login https://api.${OSAC_NAMESPACE}.localhost:${EXTERNAL_INGRESS_PORT} --token \"\$TOKEN\" --insecure"
  echo "  ./osac get tenants"
  echo ""
  info "Teardown:"
  echo "  ${SCRIPT_DIR}/teardown.sh"
  echo ""
  info "AWX admin:"
  echo "  URL:      http://awx.awx.localhost:8080 (after adding HTTPRoute)"
  echo "  Password: kubectl -n awx get secret awx-admin-password -o jsonpath='{.data.password}' | base64 -d"
  echo ""
  info "Open items:"
  echo "  - Hub registration (osac create hub) for full operator reconciliation"
  echo ""
}

# ── Main ───────────────────────────────────────────────────────────────────────

main() {
  echo ""
  log "Setting up OSAC Kind development environment"
  echo ""

  check_prerequisites

  # Step 1: Create kind cluster and configure kubeconfig
  create_cluster
  setup_kubeconfig

  # Step 2: Patch CoreDNS for *.localhost resolution inside pods
  patch_coredns_localhost_rewrite

  if [[ "$CLUSTER_ONLY" == "true" ]]; then
    print_summary
    return 0
  fi

  # Step 3: Install infrastructure
  install_cert_manager
  install_trust_manager
  install_ca
  install_envoy_gateway
  install_authorino

  # Step 4: Install data services
  install_postgres
  create_database_resources
  install_keycloak
  install_openbao
  configure_openbao
  create_controller_credentials

  if [[ "$SKIP_OSAC" == "true" ]]; then
    print_summary
    return 0
  fi

  # Step 5: Install OSAC via umbrella chart
  install_fake_crds
  deploy_osac
  create_external_tlsroutes
  deploy_osac_ui
  register_hub

  # Step 6: Install KubeVirt + CDI + Multus
  install_multus
  install_kubevirt
  install_cdi

  # Step 7: Install and configure AWX
  install_awx
  configure_awx

  # Step 8: Seed catalog (templates + instance types)
  seed_catalog

  print_summary
}

# Guard: skip main when sourced by other scripts (e.g. teardown.sh)
if [[ "${BASH_SOURCE[0]}" == "${0}" ]]; then
  main "$@"
fi
