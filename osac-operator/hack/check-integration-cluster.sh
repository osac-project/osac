#!/usr/bin/env bash
set -euo pipefail

fail() {
    printf 'Operator integration preflight: %s\n' "$1" >&2
    exit 2
}

if [[ -n ${OPENSHELL_SANDBOX:-} ]]; then
    [[ ${KUBECONFIG:-} == /sandbox/kube/config && -r ${KUBECONFIG:-} ]] ||
        fail 'KUBECONFIG must point to the mounted /sandbox/kube/config. See the Carbonite KiND integration guide.'
elif [[ -n ${KUBECONFIG:-} && ! -r ${KUBECONFIG:-} ]]; then
    fail 'KUBECONFIG is not readable.'
fi

command -v kubectl >/dev/null 2>&1 || fail 'kubectl is unavailable.'
kubectl --request-timeout=5s get --raw=/readyz >/dev/null 2>&1 ||
    fail 'Kubernetes API is unreachable or authentication failed. Check the kubeconfig and sandbox API policy.'

pods=$(kubectl --request-timeout=5s -n osac get pods \
    -l control-plane=controller-manager,app.kubernetes.io/name=operator \
    -o name 2>/dev/null) ||
    fail 'Cannot list operator pods in namespace osac. Check the scoped identity and deployment.'
[[ -n $pods ]] || fail 'No operator pod found in namespace osac. Deploy the operator on the host first.'
