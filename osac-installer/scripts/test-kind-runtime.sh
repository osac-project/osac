#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=osac-installer/scripts/dev-full/kind-runtime.sh
source "${script_dir}/dev-full/kind-runtime.sh"

test_dir="$(mktemp -d)"
trap 'rm -rf "${test_dir}"' EXIT
runtime_script="${script_dir}/dev-full/kind-runtime.sh"
KIND_PROVIDER=docker
KIND_MODE=missing

kind_cmd() {
  case "$1 $2" in
    "get nodes") printf '%s\n' 'osac-dev-control-plane' ;;
    "get kubeconfig")
      case "${KIND_MODE}" in
        missing) return 1 ;;
        empty) return 0 ;;
        ready) printf '%s\n' 'new kubeconfig' ;;
      esac
      ;;
    *) printf 'Unexpected Kind command: %s\n' "$*" >&2; return 1 ;;
  esac
}

kubeconfig="${test_dir}/kind.kubeconfig"
printf '%s\n' 'previous kubeconfig' > "${kubeconfig}"
if create_cluster osac-dev unused-config "${kubeconfig}" 2> "${test_dir}/error"; then
  echo 'Expected an incomplete Kind cluster to fail' >&2
  exit 1
fi
[[ "$(cat "${kubeconfig}")" == 'previous kubeconfig' ]]
[[ "$(cat "${test_dir}/error")" == *'has no usable kubeconfig'* ]]
[[ "$(cat "${test_dir}/error")" == *"${runtime_script} delete-cluster osac-dev"* ]]
[[ -z "$(find "${test_dir}" -name 'kind.kubeconfig.tmp.*' -print)" ]]

KIND_MODE=empty
if create_cluster osac-dev unused-config "${kubeconfig}" 2> "${test_dir}/error"; then
  echo 'Expected an empty Kind kubeconfig to fail' >&2
  exit 1
fi
[[ "$(cat "${kubeconfig}")" == 'previous kubeconfig' ]]

KIND_MODE=ready
create_cluster osac-dev unused-config "${kubeconfig}" 2> "${test_dir}/success"
[[ "$(cat "${kubeconfig}")" == 'new kubeconfig' ]]

echo 'Kind runtime kubeconfig tests passed'
