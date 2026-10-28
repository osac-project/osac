#!/usr/bin/env bash
set -euo pipefail

fail() {
    printf 'Operator integration preflight: %s\n' "$1" >&2
    exit 2
}

# The suite's own BeforeSuite waits five minutes for the controller-manager pod
# (integration_suite_test.go). Failing as soon as the pod is absent aborted
# normal runs started right after a Helm install, before Ginkgo's retry window
# could apply, so this preflight now waits for the same window instead.
timeout=${OPERATOR_PREFLIGHT_TIMEOUT:-300}
interval=${OPERATOR_PREFLIGHT_INTERVAL:-5}

case $timeout in
    ''|*[!0-9]*) fail 'OPERATOR_PREFLIGHT_TIMEOUT must be a whole number of seconds.' ;;
esac
case $interval in
    ''|*[!0-9]*) fail 'OPERATOR_PREFLIGHT_INTERVAL must be a whole number of seconds.' ;;
esac

if [[ -n ${OPENSHELL_SANDBOX:-} ]]; then
    [[ ${KUBECONFIG:-} == /sandbox/kube/config && -r ${KUBECONFIG:-} ]] ||
        fail 'KUBECONFIG must point to the mounted /sandbox/kube/config. See the Carbonite KiND integration guide.'
elif [[ -n ${KUBECONFIG:-} ]]; then
    # kubectl accepts a colon-separated list; require every entry to be readable.
    IFS=: read -ra kubeconfigs <<< "$KUBECONFIG"
    for path in "${kubeconfigs[@]}"; do
        [[ -z $path || -r $path ]] || fail 'KUBECONFIG is not readable.'
    done
fi

command -v kubectl >/dev/null 2>&1 || fail 'kubectl is unavailable.'
kubectl --request-timeout=5s get --raw=/readyz >/dev/null 2>&1 ||
    fail 'Kubernetes API is unreachable or authentication failed. Check the kubeconfig and sandbox API policy.'

# Access failures stay fast: only an absent pod waits, and only for the window
# the suite itself tolerates.
deadline=$((SECONDS + timeout))
announced=
while :; do
    pods=$(kubectl --request-timeout=5s -n osac get pods \
        -l control-plane=controller-manager,app.kubernetes.io/name=operator \
        -o name 2>/dev/null) ||
        fail 'Cannot list operator pods in namespace osac. Check the scoped identity and deployment.'
    [[ -z $pods ]] || exit 0
    [[ $SECONDS -lt $deadline ]] || break
    if [[ -z $announced ]]; then
        printf 'Waiting up to %ss for the operator pod to appear...\n' "$timeout"
        announced=1
    fi
    sleep "$interval"
done

fail "No operator pod found in namespace osac after ${timeout}s. Deploy the operator on the host and retry."
