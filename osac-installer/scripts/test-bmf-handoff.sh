#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/lib.sh"

: "${PLATFORM:?PLATFORM is required}"
: "${PROFILE:?PROFILE is required}"
: "${NS:?NS is required}"

# Use a per-run Secret so this suite never overwrites or deletes another
# deployment's AAP credential when NS points at a reused namespace.
token_secret="osac-aap-api-token-it-$(python3 -c 'import secrets; print(secrets.token_hex(8))')"

cleanup_bmf_aap_recorder() {
    local original_status=$? recorder_cleanup_status token_cleanup_status
    trap - EXIT
    set +e
    kubectl delete pod,service,configmap,serviceaccount,role,rolebinding osac-aap-recorder \
        -n "${NS}" --ignore-not-found --wait=false
    recorder_cleanup_status=$?
    kubectl delete secret "${token_secret}" -n "${NS}" --ignore-not-found
    token_cleanup_status=$?
    if [[ "${recorder_cleanup_status}" -ne 0 || "${token_cleanup_status}" -ne 0 ]]; then
        echo "BMF test cleanup failed; see kubectl errors above" >&2
        [[ "${original_status}" -ne 0 ]] || original_status=1
    fi
    exit "${original_status}"
}
trap cleanup_bmf_aap_recorder EXIT

token="$(python3 -c 'import secrets; print(secrets.token_urlsafe(32))')"
printf '%s' "${token}" | kubectl create secret generic "${token_secret}" -n "${NS}" \
    --from-file=token=/dev/stdin --dry-run=client -o yaml | kubectl create -f - >/dev/null
unset token

handoff_helm_args=(
    --set global.services.bmaas.enabled=true
    --set bmf.replicaCount=1
    --set bmf.image.repository=localhost/bmf-operator
    --set bmf.image.tag=it
    --set bmf.image.pullPolicy=Never
    --set bmf.secretAPI.url=
    --set bmf.env.aapUrl="http://osac-aap-recorder.${NS}.svc.cluster.local:8080/api"
    --set "bmf.env.aapTokenSecretName=${token_secret}"
    --set operator.enabled=true
    --set operator.image.repository=localhost/osac-operator
    --set operator.image.tag=it
    --set operator.image.pullPolicy=Never
    --set "operator.aap.tokenSecret.name=${token_secret}"
    --set-string "operator.aap.url=http://osac-aap-recorder.${NS}.svc.cluster.local:8080/api"
    --set operator.aap.statusPollInterval=1s
    --set operator.fulfillment.serverAddress=
    --set service.enabled=false
    --set hubAccess.enabled=false
    --set validation.enabled=false
    --set global.fulfillmentTrust.enabled=false
    --set operator.controllers.tenant=false
    --set operator.controllers.storage=false
    --set operator.controllers.volume=false
    --set operator.controllers.networking=true
    --set operator.controllers.computeInstance=false
    --set operator.controllers.clusterOrder=false
    --set operator.networkManagers.enabled=true
)
handoff_helm_args="${handoff_helm_args[*]} ${METERING_IT_IMAGE_ARGS:-}"

enabled_report_args=()
disabled_report_args=()
if [[ -n "${JUNIT_REPORT_DIR:-}" ]]; then
    enabled_report_args+=("--junit-report=${JUNIT_REPORT_DIR}/bmaas-networking-enabled.xml")
    disabled_report_args+=("--junit-report=${JUNIT_REPORT_DIR}/bmaas-networking-disabled.xml")
fi

make install-osac PLATFORM="${PLATFORM}" PROFILE="${PROFILE}" NS="${NS}" \
    EXTRA_HELM_ARGS="${handoff_helm_args} --set global.networking.provisioningEnabled=true"
(cd ../osac-operator && IT_TEST_SUITE=bmf ginkgo run "${enabled_report_args[@]+${enabled_report_args[@]}}" \
    --timeout 30m -v --label-filter 'bmaas-networking' test/integration)

make install-osac PLATFORM="${PLATFORM}" PROFILE="${PROFILE}" NS="${NS}" \
    EXTRA_HELM_ARGS="${handoff_helm_args} --set global.networking.provisioningEnabled=false"
(cd ../osac-operator && IT_TEST_SUITE=bmf ginkgo run "${disabled_report_args[@]+${disabled_report_args[@]}}" \
    --timeout 15m -v --label-filter 'bmaas-networking-disabled' test/integration)
