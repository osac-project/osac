#!/usr/bin/env bash

set -Eeuo pipefail

module_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
repo_root="$(cd -- "$module_dir/../../.." && pwd)"
script_path="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)/$(basename -- "${BASH_SOURCE[0]}")"
manifest="$module_dir/networking-profiles.json"
profile="${1:-}"
password_file=""

fail() {
	printf 'ERROR: %s\n' "$*" >&2
	exit 2
}

run_deployed_profile() {
	shift
	local -a make_args=("$@")
	if [[ -n "${NETRIS_PASSWORD:-}" ]]; then
		password_file="$(mktemp)"
		chmod 600 "$password_file"
		printf '%s' "$NETRIS_PASSWORD" >"$password_file"
		unset NETRIS_PASSWORD
		make_args+=("NETRIS_PASSWORD_FILE=$password_file")
	fi
	trap 'if [[ -n "$password_file" ]]; then rm -f -- "$password_file"; fi' EXIT

	make -C "$module_dir" _deploy "${make_args[@]}"
	make -C "$module_dir" fixtures "${make_args[@]}"
}

if [[ "$profile" == "__run-deployed" ]]; then
	run_deployed_profile "$@"
	exit 0
fi

[[ -n "$profile" ]] || fail "usage: make -C tests/integration/caas profile PROFILE=<name>"
command -v jq >/dev/null || fail "jq is required to read the checked-in profile matrix"
command -v kind >/dev/null || fail "kind is required to create an isolated profile cluster"
[[ -f "$manifest" ]] || fail "profile matrix is missing: $manifest"

profile_json="$(jq -ce --arg name "$profile" '.profiles[] | select(.name == $name)' "$manifest")" ||
	fail "unknown profile '$profile'; see $manifest"

status="$(jq -r '.status' <<<"$profile_json")"
case "$status" in
	runnable) ;;
	runnable-after-6194)
		[[ -f "$repo_root/osac-aap/tests/integration/fixtures/netris/kind_fixture.py" ]] ||
			fail "$profile requires the unmerged OSAC-6194 Netris fixture (PR #2612)"
		;;
	blocked-by-6195)
		fail "$profile requires the unmerged OSAC-6195 AgentlessNet AWX fixture (PR #2611)"
		;;
	contract-rejected)
		fail "$profile is expected to be rejected by the documented manager contract; it is not a runnable profile"
		;;
	unqualified)
		fail "$profile has no support/rejection evidence and must not be run as a supported profile"
		;;
	*) fail "profile '$profile' has unsupported status '$status'" ;;
esac

overlay="$(jq -er '.overlay' <<<"$profile_json")" || fail "profile '$profile' has no values overlay"
overlay_path="$module_dir/$overlay"
[[ -f "$overlay_path" ]] || fail "profile overlay is missing: $overlay_path"

networking_enabled="$(jq -r '.networkingEnabled' <<<"$profile_json")"
services="$(jq -r '.services | join(",")' <<<"$profile_json")"
fabric_manager="$(jq -r '.fabricManager' <<<"$profile_json")"
k8s_manager="$(jq -r '.k8sManager' <<<"$profile_json")"
fixture="$(jq -r '.fixture' <<<"$profile_json")"

profile_key="${profile//[^a-z0-9]/-}"
run_id="$(python3 -c 'import uuid; print(uuid.uuid4().hex[:8])')"
cluster_name="osac-7925-${profile_key:0:28}-${run_id}"
(( ${#cluster_name} <= 63 )) || fail "generated Kind cluster name is too long"
export KIND_CLUSTER_NAME="$cluster_name"
export KUBECONFIG="${HOME}/.kube/${cluster_name}-kind.kubeconfig"
mkdir -p -- "$(dirname -- "$KUBECONFIG")"
[[ ! -e "$KUBECONFIG" ]] || fail "refusing to overwrite existing kubeconfig $KUBECONFIG"

clusters="$(kind get clusters)" || fail "unable to inspect existing Kind clusters"
if grep -Fqx -- "$cluster_name" <<<"$clusters"; then
	fail "refusing to reuse existing Kind cluster '$cluster_name'"
fi

cleanup_cluster() {
	local result=$?
	trap - EXIT INT TERM
	if kind get clusters 2>/dev/null | grep -Fqx -- "$cluster_name"; then
		if ! kind delete cluster --name "$cluster_name"; then
			printf 'ERROR: failed to remove profile-created Kind cluster %s\n' "$cluster_name" >&2
			result=1
		fi
	fi
	rm -f -- "$KUBECONFIG"
	exit "$result"
}
trap cleanup_cluster EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

make_args=(
	"KIND_CLUSTER_NAME=$cluster_name"
	"KUBECONFIG=$KUBECONFIG"
	"NETWORK_PROFILE_VALUES=$overlay_path"
	"NETWORK_PROFILE_NAME=$profile"
	"NETWORK_PROFILE_SERVICES=$services"
	"EXPECTED_FABRIC_MANAGER=$fabric_manager"
	"EXPECTED_K8S_MANAGER=$k8s_manager"
	"NETWORKING_ENABLED=$networking_enabled"
)

printf 'Running networking profile %s in fresh Kind cluster %s\n' "$profile" "$cluster_name"
printf 'Services: %s; Fabric Manager: %s; Kubernetes Manager: %s\n' \
	"$services" "$fabric_manager" "${k8s_manager:-none}"

make -C "$module_dir" _prepare-platform _ee-image-build "${make_args[@]}"
make -C "$module_dir" _ee-image-load "${make_args[@]}"

if [[ "$fixture" == "netris" || "$fixture" == "netris-and-ovn" ]]; then
	python3 "$repo_root/osac-aap/tests/integration/fixtures/netris/kind_fixture.py" run \
		--kubeconfig "$KUBECONFIG" \
		--namespace osac \
		--image localhost/osac-aap:it \
		--pull-policy Never \
		-- bash "$script_path" __run-deployed "${make_args[@]}"
else
	bash "$script_path" __run-deployed "${make_args[@]}"
fi
