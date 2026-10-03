#!/usr/bin/env bash
set -euo pipefail

namespace="${1:?usage: patch-grpc-probes.sh NAMESPACE BASELINE_HOOK_JOBS HELM_STATUS_FILE}"
baseline_hook_jobs="${2-}"
helm_status_file="${3:?usage: patch-grpc-probes.sh NAMESPACE BASELINE_HOOK_JOBS HELM_STATUS_FILE}"

new_post_install_hook_exists() {
  local current_jobs name uid hook baseline_uid
  current_jobs="$(kubectl -n "$namespace" get jobs \
    -l app.kubernetes.io/instance=osac \
    -o jsonpath='{range .items[*]}{.metadata.name}{"|"}{.metadata.uid}{"|"}{.metadata.annotations.helm\.sh/hook}{"\n"}{end}' 2>/dev/null)" || return 1

  while IFS='|' read -r name uid hook; do
    [[ "$hook" == *post-install* || "$hook" == *post-upgrade* ]] || continue
    baseline_uid="$(printf '%s\n' "$baseline_hook_jobs" | awk -F'|' -v target="$name" '$1 == target { print $2; exit }')"
    if [[ "$baseline_uid" != "$uid" ]]; then
      return 0
    fi
  done <<< "$current_jobs"
  return 1
}

# Helm starts post-install hooks after applying the Deployments, then waits for
# hook Jobs. Watch for a new hook while Helm is waiting so the dev-full-only
# probe override can unblock hooks that wait on fulfillment health.
deadline=$((SECONDS + 2400))
while ! new_post_install_hook_exists; do
  if [[ -s "$helm_status_file" ]]; then
    echo "Helm finished before creating a post-install hook; checking for fulfillment Deployments to patch."
    break
  fi
  if (( SECONDS >= deadline )); then
    echo "Timed out waiting for a new OSAC post-install hook Job." >&2
    exit 1
  fi
  sleep 2
done

# The operator-only installer test disables the fulfillment service.
controller="$(kubectl -n "$namespace" get deployment fulfillment-controller --ignore-not-found -o name)"
if [[ -z "$controller" ]]; then
  exit 0
fi

for deployment in fulfillment-controller fulfillment-event-publisher fulfillment-grpc-server fulfillment-rest-gateway fulfillment-console-proxy; do
  for attempt in $(seq 1 30); do
    if kubectl -n "$namespace" get deployment "$deployment" >/dev/null 2>&1; then
      break
    fi
    sleep 2
  done
  kubectl -n "$namespace" get deployment "$deployment" >/dev/null
done

# The published service chart hard-codes a 1s gRPC health-check deadline. Give
# dev-full's single Kind node enough time to answer while keeping this override
# out of the chart defaults used by other profiles.
grpc_probe_patch='[
  {"op":"replace","path":"/spec/template/spec/containers/0/readinessProbe/exec/command/5","value":"--grpc-server-timeout=10s"},
  {"op":"replace","path":"/spec/template/spec/containers/0/readinessProbe/timeoutSeconds","value":15},
  {"op":"replace","path":"/spec/template/spec/containers/0/livenessProbe/exec/command/5","value":"--grpc-server-timeout=10s"},
  {"op":"replace","path":"/spec/template/spec/containers/0/livenessProbe/timeoutSeconds","value":15}
]'

for deployment in fulfillment-controller fulfillment-event-publisher fulfillment-grpc-server; do
  kubectl -n "$namespace" patch deployment "$deployment" \
    --type=json --patch="$grpc_probe_patch"
done

# In Kind, *.localhost resolves to loopback inside pods. Route the console
# proxy's JWKS request back through the fulfillment API Service instead.
api_service_ip="$(kubectl -n "$namespace" get service fulfillment-api -o jsonpath='{.spec.clusterIP}')"
kubectl -n "$namespace" patch deployment fulfillment-console-proxy --type=json --patch="[
  {\"op\":\"replace\",\"path\":\"/spec/template/spec/containers/0/readinessProbe/exec/command/5\",\"value\":\"--grpc-server-timeout=10s\"},
  {\"op\":\"replace\",\"path\":\"/spec/template/spec/containers/0/readinessProbe/timeoutSeconds\",\"value\":15},
  {\"op\":\"add\",\"path\":\"/spec/template/spec/hostAliases\",\"value\":[{\"ip\":\"$api_service_ip\",\"hostnames\":[\"fulfillment-api.osac.localhost\"]}]}
]"

for deployment in fulfillment-controller fulfillment-event-publisher fulfillment-grpc-server fulfillment-rest-gateway fulfillment-console-proxy; do
  kubectl -n "$namespace" rollout status "deployment/$deployment" --timeout=40m
done
