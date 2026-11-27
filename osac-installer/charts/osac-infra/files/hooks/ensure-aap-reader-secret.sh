#!/usr/bin/env bash

ensure_aap_reader_secret() {
    local namespace="$1"
    local secret_name=keycloak-client-secrets
    local state resource_version encoded generated

    state=$(oc get secret "${secret_name}" -n "${namespace}" \
        -o jsonpath='{.metadata.resourceVersion}{" "}{.data.osac-aap-secret-reader}') || return 1
    resource_version=${state%% *}
    encoded=${state#* }
    if [[ -z "${encoded}" ]]; then
        generated=$(openssl rand -base64 18) || return 1
        encoded=$(printf '%s' "${generated}" | base64 | tr -d '\n') || return 1
        # The init container and upgrade hook can both run after a rollout.
        # A resourceVersion conflict makes the loser retry with the stored key.
        printf '{"metadata":{"resourceVersion":"%s"},"data":{"osac-aap-secret-reader":"%s"}}' \
            "${resource_version}" "${encoded}" \
            | oc patch secret "${secret_name}" -n "${namespace}" --type=merge --patch-file /dev/stdin >/dev/null || return 1
    fi
    printf '%s' "${encoded}" | base64 -d
}
