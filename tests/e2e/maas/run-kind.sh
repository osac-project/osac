#!/usr/bin/env bash
set -Eeuo pipefail

# Run the MaaS WorkloadCluster E2E against a local Kind environment.
# The pytest test remains infrastructure-agnostic; this script supplies the
# same environment that the CI E2E runners publish for a deployed environment.
# Use --setup-only to stop after installing OSAC, without registering a Hub or
# running the scenario test. This also keeps the environment.

usage() {
    cat <<'EOF'
Usage: run-kind.sh [--setup-only|--cleanup-only]

  --setup-only    Create and configure the demo environment, then leave it running.
  --cleanup-only  Delete the configured demo clusters and temporary files.

EOF
}

repo_root=$(git rev-parse --show-toplevel)
installer_dir="${repo_root}/osac-installer"
management_cluster="${OSAC_KIND_CLUSTER_NAME:-osac-wc-e2e}"
source_cluster="${OSAC_SOURCE_HUB_CLUSTER_NAME:-osac-wc-e2e-hub}"
management_kubeconfig="${OSAC_KIND_KUBECONFIG:-${TMPDIR:-/tmp}/${management_cluster}.kubeconfig}"
source_kubeconfig="${OSAC_SOURCE_HUB_KUBECONFIG:-${TMPDIR:-/tmp}/${source_cluster}.kubeconfig}"
controller_source_kubeconfig="${TMPDIR:-/tmp}/${source_cluster}-controller.kubeconfig"
ownership_marker="${TMPDIR:-/tmp}/${management_cluster}.osac-wc-e2e-owner"
source_ownership_marker="${TMPDIR:-/tmp}/${source_cluster}.osac-wc-e2e-owner"
artifact_marker="${TMPDIR:-/tmp}/osac-wc-e2e-artifacts-owner"
namespace="${OSAC_NAMESPACE:-osac}"
private_api_port="${OSAC_E2E_PRIVATE_API_PORT:-18001}"
keycloak_port="${OSAC_E2E_KEYCLOAK_PORT:-18443}"
container_provider="${CONTAINER_TOOL:-${KIND_EXPERIMENTAL_PROVIDER:-podman}}"
fulfillment_image="${OSAC_FULFILLMENT_IMAGE:-localhost/fulfillment-service:e2e}"
cli_binary="${TMPDIR:-/tmp}/osac-wc-e2e-cli"
cli_config="${TMPDIR:-/tmp}/osac-wc-e2e-cli-config"
lifecycle_lock_file="${TMPDIR:-/tmp}/osac-wc-e2e-lifecycle.lock"
setup_only="false"
cleanup_only="false"
preserve_ownership_marker="false"
created_ownership_marker="false"
created_source_ownership_marker="false"
created_artifact_marker="false"
validated_ownership_marker="false"

for argument in "$@"; do
    case "${argument}" in
        --setup-only)
            setup_only="true"
            ;;
        --cleanup-only)
            cleanup_only="true"
            ;;
        --help|-h)
            usage
            exit 0
            ;;
        *)
            echo "unknown argument: ${argument}" >&2
            usage >&2
            exit 2
            ;;
    esac
done

if [[ "${setup_only}" == "true" && "${cleanup_only}" == "true" ]]; then
    echo "--setup-only and --cleanup-only cannot be used together" >&2
    exit 2
fi
forward_pids=()
created_management=false
created_source=false
created_cli_binary=false
created_cli_config=false
forward_log_dir=""
lifecycle_lock_acquired=false
lifecycle_lock_fd=-1

release_lifecycle_lock() {
    if [[ "${lifecycle_lock_acquired}" == true ]]; then
        flock -u "${lifecycle_lock_fd}" 2>/dev/null || true
        exec {lifecycle_lock_fd}>&-
        lifecycle_lock_acquired=false
    fi
}

cleanup() {
    local status=$?
    set +e
    local cleanup_failed=false

    if [[ "${setup_only}" == "true" && "${status}" -eq 0 ]]; then
        release_lifecycle_lock
        echo "Keeping setup-only demo environment."
        echo "  management kubeconfig: ${management_kubeconfig}"
        echo "  source Hub kubeconfig: ${source_kubeconfig}"
        exit "${status}"
    fi

    if [[ ${#forward_pids[@]} -gt 0 ]]; then
        for forward_pid in "${forward_pids[@]}"; do
            kill "${forward_pid}" 2>/dev/null
            wait "${forward_pid}" 2>/dev/null
        done
    fi

    if [[ "${created_source}" == true ]]; then
        if ! KIND_EXPERIMENTAL_PROVIDER="${container_provider}" \
            kind delete cluster --name "${source_cluster}" >/dev/null 2>&1; then
            cleanup_failed=true
            echo "failed to delete source Hub cluster: ${source_cluster}" >&2
        else
            rm -f "${source_kubeconfig}" "${controller_source_kubeconfig}"
        fi
    fi
    if [[ "${created_management}" == true ]]; then
        if ! KIND_EXPERIMENTAL_PROVIDER="${container_provider}" \
            kind delete cluster --name "${management_cluster}" >/dev/null 2>&1; then
            cleanup_failed=true
            echo "failed to delete management cluster: ${management_cluster}" >&2
        else
            rm -f "${management_kubeconfig}"
        fi
    fi

    if [[ "${created_cli_config}" == true ]]; then
        rm -rf "${cli_config}"
    fi
    if [[ "${created_cli_binary}" == true ]]; then
        rm -f "${cli_binary}"
    fi
    if [[ -n "${forward_log_dir}" ]]; then
        rm -rf "${forward_log_dir}"
    fi
	if [[ "${cleanup_failed}" == false && "${preserve_ownership_marker}" == false ]]; then
		if [[ "${created_ownership_marker}" == true || "${validated_ownership_marker}" == true ]]; then
			rm -f "${ownership_marker}"
		fi
		if [[ "${created_source_ownership_marker}" == true || "${validated_ownership_marker}" == true ]]; then
			rm -f "${source_ownership_marker}"
		fi
		if [[ "${created_artifact_marker}" == true || "${validated_ownership_marker}" == true ]]; then
			rm -f "${artifact_marker}"
		fi
    fi

    if [[ "${cleanup_failed}" == true && "${status}" -eq 0 ]]; then
        status=1
    fi
    release_lifecycle_lock
    exit "${status}"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

require_command() {
    command -v "$1" >/dev/null 2>&1 || {
        echo "required command not found: $1" >&2
        exit 2
    }
}

require_command kind
require_command flock

exec {lifecycle_lock_fd}>"${lifecycle_lock_file}"
if ! flock -n "${lifecycle_lock_fd}"; then
    exec {lifecycle_lock_fd}>&-
    echo "another MaaS demo setup or cleanup is already in progress" >&2
    exit 2
fi
lifecycle_lock_acquired=true

if [[ "${cleanup_only}" == "true" ]]; then
    expected_ownership="${management_cluster}|${source_cluster}|${container_provider}|${management_kubeconfig}|${source_kubeconfig}|${controller_source_kubeconfig}|${cli_binary}|${cli_config}"
	if [[ ! -f "${ownership_marker}" ]] || [[ "$(<"${ownership_marker}")" != "${expected_ownership}" ]] ||
		[[ ! -f "${source_ownership_marker}" ]] || [[ "$(<"${source_ownership_marker}")" != "${expected_ownership}" ]] ||
		[[ ! -f "${artifact_marker}" ]] || [[ "$(<"${artifact_marker}")" != "${expected_ownership}" ]]; then
        preserve_ownership_marker=true
        echo "no matching demo ownership record; refusing cleanup" >&2
        exit 1
    fi
    validated_ownership_marker=true
    # Keep the ownership records while teardown is in progress. If this
    # process is interrupted, a later cleanup-only invocation can recover the
    # remaining clusters and artifacts.
    preserve_ownership_marker=true
    cleanup_failed=false
    if ! KIND_EXPERIMENTAL_PROVIDER="${container_provider}" \
        kind delete cluster --name "${source_cluster}" >/dev/null 2>&1; then
        cleanup_failed=true
        echo "failed to delete source Hub cluster: ${source_cluster}" >&2
    else
        rm -f "${source_kubeconfig}" "${controller_source_kubeconfig}"
    fi
    if ! KIND_EXPERIMENTAL_PROVIDER="${container_provider}" \
        kind delete cluster --name "${management_cluster}" >/dev/null 2>&1; then
        cleanup_failed=true
        echo "failed to delete management cluster: ${management_cluster}" >&2
    else
        rm -f "${management_kubeconfig}"
    fi
    rm -rf "${cli_config}"
    rm -f "${cli_binary}"
    if [[ "${cleanup_failed}" == true ]]; then
        echo "Demo environment cleanup failed; retaining recovery files for failed deletions." >&2
        exit 1
    fi
    preserve_ownership_marker=false
    echo "Demo environment cleanup complete."
    exit 0
fi

if [[ -e "${cli_binary}" || -e "${cli_config}" ]]; then
    echo "refusing to reuse existing CLI artifacts; remove the disposable files first:" >&2
    echo "  ${cli_binary}" >&2
    echo "  ${cli_config}" >&2
    exit 2
fi

ownership_record="${management_cluster}|${source_cluster}|${container_provider}|${management_kubeconfig}|${source_kubeconfig}|${controller_source_kubeconfig}|${cli_binary}|${cli_config}"
if ! (set -o noclobber; printf '%s\n' "${ownership_record}" >"${artifact_marker}"); then
    echo "shared demo artifacts are in use; refusing to run concurrently" >&2
    exit 2
fi
created_artifact_marker=true

for command in kubectl helm go uv curl make sed grep; do
    require_command "${command}"
done
require_command "${container_provider}"

if [[ -e "${management_kubeconfig}" || -e "${source_kubeconfig}" || -e "${controller_source_kubeconfig}" || -e "${cli_binary}" || -e "${cli_config}" || -e "${ownership_marker}" || -e "${source_ownership_marker}" ]]; then
    echo "refusing to reuse existing kubeconfig; remove the disposable files first:" >&2
    echo "  ${management_kubeconfig}" >&2
    echo "  ${source_kubeconfig}" >&2
    echo "  ${controller_source_kubeconfig}" >&2
    echo "  ${cli_binary}" >&2
    echo "  ${cli_config}" >&2
    echo "  ${ownership_marker}" >&2
	echo "  ${source_ownership_marker}" >&2
    exit 2
fi

existing_clusters=$(KIND_EXPERIMENTAL_PROVIDER="${container_provider}" kind get clusters)
if grep -Fxq "${management_cluster}" <<<"${existing_clusters}"; then
    echo "management cluster already exists; refusing to manage it: ${management_cluster}" >&2
    exit 2
fi
if grep -Fxq "${source_cluster}" <<<"${existing_clusters}"; then
    echo "source Hub cluster already exists; refusing to manage it: ${source_cluster}" >&2
    exit 2
fi

if ! (set -o noclobber; printf '%s\n' "${ownership_record}" >"${source_ownership_marker}"); then
    echo "source-cluster demo ownership marker was claimed concurrently; refusing to manage the clusters" >&2
    exit 2
fi
created_source_ownership_marker=true
if ! (set -o noclobber; printf '%s\n' "${ownership_record}" >"${ownership_marker}"); then
    echo "demo ownership marker was claimed concurrently; refusing to manage the clusters" >&2
    exit 2
fi
created_ownership_marker=true

forward_log_dir=$(mktemp -d "${TMPDIR:-/tmp}/osac-wc-e2e.XXXXXX")
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

created_management=true
KIND_EXPERIMENTAL_PROVIDER="${container_provider}" \
    kind create cluster --name "${management_cluster}" --kubeconfig "${management_kubeconfig}" --wait 60s

created_source=true
KIND_EXPERIMENTAL_PROVIDER="${container_provider}" \
    kind create cluster --name "${source_cluster}" --kubeconfig "${source_kubeconfig}" --wait 60s

KUBECONFIG="${source_kubeconfig}" kubectl apply -f - <<'EOF'
apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: managedclusters.cluster.open-cluster-management.io
spec:
  group: cluster.open-cluster-management.io
  scope: Cluster
  names:
    plural: managedclusters
    singular: managedcluster
    kind: ManagedCluster
    shortNames: [mcl]
  versions:
    - name: v1
      served: true
      storage: true
      subresources:
        status: {}
      schema:
        openAPIV3Schema:
          type: object
          x-kubernetes-preserve-unknown-fields: true
EOF

KUBECONFIG="${source_kubeconfig}" kubectl wait \
    --for=condition=Established \
    crd/managedclusters.cluster.open-cluster-management.io \
    --timeout=60s

# The kubeconfig generated by Kind points at 127.0.0.1, which is the host and
# not the fulfillment-controller pod. Both Kind nodes share the container
# network, so register the source API through its control-plane container IP.
source_node_ip="$(${container_provider} inspect -f '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' "${source_cluster}-control-plane")"
if [[ -z "${source_node_ip}" ]]; then
    echo "could not determine source Hub control-plane container IP" >&2
    exit 2
fi
(umask 077 && sed -E "s#https://127\\.0\\.0\\.1:[0-9]+#https://${source_node_ip}:6443#" "${source_kubeconfig}" >"${controller_source_kubeconfig}")

make -C "${repo_root}/fulfillment-service" image-build \
    IMG="${fulfillment_image}" CONTAINER_TOOL="${container_provider}"
KIND_EXPERIMENTAL_PROVIDER="${container_provider}" \
    kind load docker-image "${fulfillment_image}" --name "${management_cluster}"

KUBECONFIG="${management_kubeconfig}" make -C "${installer_dir}" install \
    PLATFORM=kind PROFILE=dev NS="${namespace}" KIND_CLUSTER_NAME="${management_cluster}" \
    INFRA_HELM_ARGS="--set bundledPostgres.database.name=osac_service --set bundledPostgres.database.user=osac_service" \
    EXTRA_HELM_ARGS="--set global.services.maas.enabled=true --set service.images.service.repository=${fulfillment_image%:*} --set service.images.service.tag=${fulfillment_image##*:} --set service.images.service.pullPolicy=Never"

KUBECONFIG="${management_kubeconfig}" kubectl -n "${namespace}" port-forward \
    --address 127.0.0.1 service/fulfillment-grpc-server "${private_api_port}:8000" \
    >"${forward_log_dir}/private-api-port-forward.log" 2>&1 &
forward_pids+=("$!")
KUBECONFIG="${management_kubeconfig}" kubectl -n keycloak port-forward \
    --address 127.0.0.1 service/keycloak "${keycloak_port}:8443" \
    >"${forward_log_dir}/keycloak-port-forward.log" 2>&1 &
forward_pids+=("$!")

forward_ready=false
for _ in {1..60}; do
    if kill -0 "${forward_pids[0]}" 2>/dev/null \
        && kill -0 "${forward_pids[1]}" 2>/dev/null \
        && grep -q "Forwarding from 127.0.0.1:${private_api_port}" "${forward_log_dir}/private-api-port-forward.log" \
        && curl -ksS --max-time 2 "https://127.0.0.1:${keycloak_port}/realms/master" >/dev/null 2>&1; then
        forward_ready=true
        break
    fi
    sleep 2
done
if [[ "${forward_ready}" != true ]]; then
    echo "timed out waiting for API and Keycloak port-forwards" >&2
    echo "private API port-forward log:" >&2
    sed -n '1,80p' "${forward_log_dir}/private-api-port-forward.log" >&2 || true
    echo "Keycloak port-forward log:" >&2
    sed -n '1,80p' "${forward_log_dir}/keycloak-port-forward.log" >&2 || true
    exit 1
fi

created_cli_binary=true
go build -o "${cli_binary}" "${repo_root}/fulfillment-service/cmd/osac"

token_script="KUBECONFIG=$(printf '%q' "${management_kubeconfig}") kubectl create token admin -n $(printf '%q' "${namespace}") --duration 4h"
created_cli_config=true
OSAC_CONFIG="${cli_config}" "${cli_binary}" login "127.0.0.1:${private_api_port}" \
    --insecure --private --token-script "${token_script}"

if [[ "${setup_only}" == "true" ]]; then
    echo "Setup complete; Hub registration left for the demo."
    echo "  management kubeconfig: ${management_kubeconfig}"
    echo "  source Hub kubeconfig: ${source_kubeconfig}"
    echo "  API port-forward: 127.0.0.1:${private_api_port} -> service/fulfillment-grpc-server:8000"
    echo "  CLI: ${cli_binary}"
    echo "  CLI config: ${cli_config}"
    exit 0
fi

hub_output=$(OSAC_CONFIG="${cli_config}" "${cli_binary}" create hub \
    --name "${source_cluster}" \
    --kubeconfig "${controller_source_kubeconfig}" \
    --namespace "${namespace}")
source_hub_id=$(sed -nE 's/.*`([^`]+)`.*/\1/p' <<<"${hub_output}")
[[ -n "${source_hub_id}" ]] || { echo "failed to parse source Hub ID" >&2; exit 1; }

export KUBECONFIG="${management_kubeconfig}"
export OSAC_NAMESPACE="${namespace}"
export OSAC_CLUSTER_DOMAIN="local"
export OSAC_CLI_PATH="${cli_binary}"
export OSAC_SERVICE_ACCOUNT="admin"
export OSAC_TOKEN_IMPERSONATE=""
export OSAC_FULFILLMENT_ADDRESS="127.0.0.1:${private_api_port}"
export OSAC_FULFILLMENT_PRIVATE_ADDRESS="127.0.0.1:${private_api_port}"
export OSAC_KEYCLOAK_URL="https://127.0.0.1:${keycloak_port}"
export OSAC_KEYCLOAK_ADMIN_PASSWORD="${OSAC_KEYCLOAK_ADMIN_PASSWORD:-admin}"
export OSAC_SOURCE_HUB_KUBECONFIG="${source_kubeconfig}"
export OSAC_SOURCE_HUB_ID="${source_hub_id}"
export OSAC_SKIP_DEFAULT_SUBNET_WAIT="true"
export OSAC_SKIP_CLUSTER_STORAGE_WAIT="true"

echo "Running WorkloadCluster E2E against Hub ${source_hub_id}"
cd "${repo_root}"
XDG_CACHE_HOME="${XDG_CACHE_HOME:-/tmp/osac-uv-cache}" \
    uv run pytest -n 0 -q tests/e2e/maas/test_workload_cluster_import.py
