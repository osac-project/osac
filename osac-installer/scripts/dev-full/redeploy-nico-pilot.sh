#!/usr/bin/env bash
# Refresh the existing NICo BMaaS pilot in the VM's dev-full Kind cluster.
# No NICo Instance, Machine, Site, or hub-cluster resource is changed here.
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
installer_dir="$(cd "${script_dir}/../.." && pwd)"
repo_dir="$(cd "${installer_dir}/.." && pwd)"
source "${installer_dir}/scripts/lib.sh"

namespace="${NS:?NS is required}"
kind_cluster="${KIND_CLUSTER_NAME:-osac-dev}"
github_repo="${NICO_PILOT_GITHUB_REPO:-vladikr/osac}"
branch="${NICO_PILOT_BRANCH:-no-issue/nico-bmaas-integration-audit}"
container_tool="${NICO_PILOT_CONTAINER_TOOL:-docker}"

fail() { echo "ERROR: $*" >&2; exit 1; }

[[ "${github_repo}" =~ ^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$ ]] || fail "invalid GitHub owner/repository"
[[ "${branch}" =~ ^[A-Za-z0-9._/-]+$ ]] || fail "invalid pilot branch"
git check-ref-format --branch "${branch}" >/dev/null || fail "invalid Git branch"
[[ "${namespace}" =~ ^[a-z0-9]([-a-z0-9]*[a-z0-9])?$ ]] || fail "invalid namespace"
[[ "${kind_cluster}" =~ ^[a-z0-9]([-a-z0-9]*[a-z0-9])?$ ]] || fail "invalid Kind cluster name"
[[ "${container_tool}" == docker || "${container_tool}" == podman ]] || fail "container tool must be docker or podman"
[[ -n "${KUBECONFIG:-}" && -f "${KUBECONFIG}" ]] || fail "set KUBECONFIG to the VM's Kind kubeconfig"

for command in git kubectl helm make "${container_tool}"; do
  command -v "${command}" >/dev/null || fail "missing command: ${command}"
done
context="$(kubectl config current-context)"
[[ "${context}" == "kind-${kind_cluster}" ]] || fail "Kubernetes context is ${context}; expected kind-${kind_cluster}"
kubectl -n "${namespace}" get node "${kind_cluster}-control-plane" >/dev/null || fail "Kind control-plane node is absent"
kubectl -n "${namespace}" get deployment awx-web >/dev/null || fail "AWX is not installed in ${namespace}"
helm -n "${namespace}" status osac >/dev/null || fail "OSAC Helm release is absent"
helm -n "${namespace}" status osac-devstack >/dev/null || fail "devstack Helm release is absent"

# A reconfiguration must preserve the operator's tenant binding and credentials.
for secret in osac-inventory-config-nico-pilot osac-management-config-nico-pilot awx-token; do
  kubectl -n "${namespace}" get secret "${secret}" >/dev/null || fail "missing Secret ${secret}"
done
kubectl -n "${namespace}" get configmap osac-nico-tenant-bindings >/dev/null || fail "missing NICo tenant bindings"

[[ "$(git -C "${repo_dir}" branch --show-current)" == "${branch}" ]] || fail "check out ${branch} before redeploying"
[[ -z "$(git -C "${repo_dir}" status --porcelain)" ]] || fail "working tree is dirty; commit or move changes first"

repo_url="https://github.com/${github_repo}.git"
git -C "${repo_dir}" fetch --no-tags "${repo_url}" "${branch}"
git -C "${repo_dir}" merge --ff-only FETCH_HEAD
revision="$(git -C "${repo_dir}" rev-parse HEAD)"
tag="${revision:0:12}"
ee_image="ghcr.io/osac-project/osac-aap:${tag}"
bmf_repository="ghcr.io/osac-project/bare-metal-fulfillment-operator"
bmf_image="${bmf_repository}:${tag}"
catalog_url="https://raw.githubusercontent.com/${github_repo}/${revision}/osac-aap/collections/ansible_collections/osac/config_as_code/roles/aap/vars/controller.yml"

echo "Redeploying NICo pilot from ${github_repo}:${branch} at ${revision}"
make -C "${repo_dir}/osac-aap" execution-environment-build IMG="${ee_image}" CONTAINER_TOOL="${container_tool}"
make -C "${repo_dir}/osac-aap" kind-load-image IMG="${ee_image}" CONTAINER_TOOL="${container_tool}" KIND_CLUSTER_NAME="${kind_cluster}" NS="${namespace}"
make -C "${repo_dir}/bare-metal-fulfillment-operator" image-build IMG="${bmf_image}" CONTAINER_TOOL="${container_tool}"
make -C "${repo_dir}/bare-metal-fulfillment-operator" kind-load-image IMG="${bmf_image}" CONTAINER_TOOL="${container_tool}" KIND_CLUSTER_NAME="${kind_cluster}" NS="${namespace}"

pilot_values="-f values/dev/kind-instance-devfull.yaml -f values/dev/kind-instance-nico-pilot.yaml"
devstack_values="${pilot_values} --set-string awx.executionEnvironment.image=${ee_image} --set-string awx.project.scmUrl=${repo_url} --set-string awx.project.scmBranch=${branch} --set-string awx.project.controllerVarsUrl=${catalog_url}"
osac_values="${pilot_values} --set-string bmf.image.repository=${bmf_repository} --set-string bmf.image.tag=${tag}"

make -C "${installer_dir}" install-devstack PLATFORM=kind PROFILE=dev-full NS="${namespace}" KIND_CLUSTER_NAME="${kind_cluster}" CONTAINER_TOOL="${container_tool}" INSTANCE_VALUES_EXTRA="${devstack_values}"
make -C "${installer_dir}" install-osac PLATFORM=kind PROFILE=dev-full NS="${namespace}" KIND_CLUSTER_NAME="${kind_cluster}" CONTAINER_TOOL="${container_tool}" INSTANCE_VALUES_EXTRA="${osac_values}"

mapfile -t bmf_deployments < <(kubectl -n "${namespace}" get deployments -l app.kubernetes.io/name=bare-metal-fulfillment-operator -o name)
[[ "${#bmf_deployments[@]}" -eq 1 ]] || fail "expected one BMF deployment in ${namespace}"
kubectl -n "${namespace}" rollout restart "${bmf_deployments[0]}"
kubectl -n "${namespace}" rollout status "${bmf_deployments[0]}" --timeout=5m
echo "NICo pilot redeployed: AWX project ${revision}, AAP EE ${ee_image}, BMF ${bmf_image}"
