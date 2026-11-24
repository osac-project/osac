#!/usr/bin/env bash
# Setup environment variables for running E2E tests against an existing OSAC cluster.
# Source this file: . tests/e2e/setup-env.sh

if [[ -n "${KUBECONFIG:-}" ]]; then
    export KUBECONFIG
else
    unset KUBECONFIG
fi
export OSAC_NAMESPACE="${OSAC_NAMESPACE:-osac}"
export OSAC_SERVICE_ACCOUNT="${OSAC_SERVICE_ACCOUNT:-admin}"
export OSAC_VM_TEMPLATE="${OSAC_VM_TEMPLATE:-ocp-virt-vm}"

# Virt cluster kubeconfig — on SNO/KubeVirt dev clusters this is usually the hub cluster.
export OSAC_VM_KUBECONFIG="${OSAC_VM_KUBECONFIG:-${KUBECONFIG:-${HOME}/.kube/config}}"
export OSAC_INGRESS_IP="${OSAC_INGRESS_IP:-}"

export OSAC_KEYCLOAK_ADMIN_PASSWORD="${OSAC_KEYCLOAK_ADMIN_PASSWORD:-admin}"
: "${OSAC_JWT_PASSWORD:?set OSAC_JWT_PASSWORD before sourcing this file}"
export OSAC_JWT_PASSWORD

if [[ "$OSAC_VM_KUBECONFIG" == *:* ]]; then
    echo "ERROR: set OSAC_VM_KUBECONFIG to one kubeconfig file when KUBECONFIG contains multiple paths" >&2
    return 1 2>/dev/null || exit 1
fi

# Ensure uv is available
if ! command -v uv &>/dev/null; then
    export PATH="$HOME/.local/bin:$PATH"
fi

_REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"

_cluster_domain() {
    kubectl get ingress.config.openshift.io cluster -o jsonpath='{.spec.domain}'
}

_ingress_router_ip() {
    local ip
    if [[ -n "$OSAC_INGRESS_IP" ]]; then
        printf '%s\n' "$OSAC_INGRESS_IP"
        return 0
    fi
    ip="$(kubectl get svc router-default -n openshift-ingress \
        -o jsonpath='{.status.loadBalancer.ingress[0].ip}' 2>/dev/null || true)"
    if [[ -z "$ip" ]]; then
        ip="$(kubectl get svc router-default -n openshift-ingress \
            -o jsonpath='{.spec.externalIPs[0]}' 2>/dev/null || true)"
    fi
    if [[ -n "$ip" ]]; then
        echo "$ip"
        return 0
    fi
    return 1
}

_ensure_route_hosts() {
    local cluster_domain namespace router_ip missing=()
    cluster_domain="$(_cluster_domain)"
    namespace="${OSAC_NAMESPACE}"

    local -a required_hosts=(
        "fulfillment-api-${namespace}.${cluster_domain}"
        "fulfillment-internal-api-${namespace}.${cluster_domain}"
        "keycloak-keycloak.${cluster_domain}"
    )

    for host in "${required_hosts[@]}"; do
        if ! getent ahostsv4 "$host" &>/dev/null; then
            missing+=("$host")
        fi
    done

    if ((${#missing[@]} == 0)); then
        return 0
    fi

    router_ip="$(_ingress_router_ip)" || {
        echo "WARNING: Could not discover the ingress router IP." >&2
        echo "Add these hostnames to /etc/hosts before running E2E tests:" >&2
        printf '  <ingress-ip> %s\n' "${required_hosts[@]}" >&2
        return 0
    }

    local line="${router_ip}"
    for host in "${missing[@]}"; do
        line+=" ${host}"
    done

    if [[ -w /etc/hosts ]]; then
        echo "$line" >> /etc/hosts
        echo "Added missing /etc/hosts entries for: ${missing[*]}"
        return 0
    fi

    if command -v sudo &>/dev/null && sudo -n true 2>/dev/null; then
        echo "$line" | sudo tee -a /etc/hosts >/dev/null
        echo "Added missing /etc/hosts entries for: ${missing[*]}"
        return 0
    fi

    echo "WARNING: These hostnames do not resolve and /etc/hosts is not writable:" >&2
    printf '  %s\n' "${missing[@]}" >&2
    echo "Add this line to /etc/hosts (sudo required):" >&2
    echo "  $line" >&2
}

_ensure_route_hosts

_CLUSTER_DOMAIN="$(_cluster_domain)"
export OSAC_KEYCLOAK_URL="${OSAC_KEYCLOAK_URL:-https://keycloak-keycloak.${_CLUSTER_DOMAIN}}"
export OSAC_FULFILLMENT_ADDRESS="${OSAC_FULFILLMENT_ADDRESS:-fulfillment-api-${OSAC_NAMESPACE}.${_CLUSTER_DOMAIN}:443}"
export OSAC_FULFILLMENT_PRIVATE_ADDRESS="${OSAC_FULFILLMENT_PRIVATE_ADDRESS:-fulfillment-internal-api-${OSAC_NAMESPACE}.${_CLUSTER_DOMAIN}:443}"

# Uncomment to enable k8s_only NetworkClass (needed for default networking tests to run):
# export OSAC_NETWORK_CLASS_K8S_ONLY=true

(cd "$_REPO_ROOT" && uv sync --quiet 2>&1) && echo "Environment ready." || echo "WARNING: uv sync failed — dependencies may be missing."
unset _REPO_ROOT _CLUSTER_DOMAIN
