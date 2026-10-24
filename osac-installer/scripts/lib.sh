#!/usr/bin/env bash

# Sourced by other scripts, all of which already set this themselves before
# sourcing -- set here too so a function like resolve_release_tag() still
# fails loudly instead of silently continuing on an internal error if this
# is ever sourced by a script that forgot to.
set -euo pipefail

# Validate the Kubernetes Secret metadata.name and data key used for the
# remote-cluster kubeconfig before either value reaches a command or manifest.
validate_remote_kubeconfig_secret() {
    local secret_name="$1"
    local secret_key="$2"
    local LC_ALL=C

    if (( ${#secret_name} > 253 )) || \
        [[ ! "${secret_name}" =~ ^[a-z0-9]([-a-z0-9]*[a-z0-9])?(\.[a-z0-9]([-a-z0-9]*[a-z0-9])?)*$ ]]; then
        echo "ERROR: REMOTE_KUBECONFIG_SECRET_NAME must be a DNS subdomain of at most 253 characters" >&2
        return 2
    fi

    if (( ${#secret_key} == 0 || ${#secret_key} > 253 )) || \
        [[ ! "${secret_key}" =~ ^[-._a-zA-Z0-9]+$ || "${secret_key}" == "." || "${secret_key}" == ..* ]]; then
        echo "ERROR: REMOTE_KUBECONFIG_SECRET_KEY must be a valid Secret data key of at most 253 characters" >&2
        return 2
    fi

    return 0
}

# Retry a condition until it succeeds or times out, optionally running a command each iteration
# Usage: retry_until <timeout_seconds> <interval_seconds> <condition_command> [loop_command]
# Returns: 0 on success, 1 on timeout
retry_until() {
    local timeout="$1"
    local interval="$2"
    local condition="$3"
    local loop_cmd="${4:-}"

    local start=${SECONDS}
    until eval "${condition}"; do
        if (( SECONDS - start >= timeout )); then
            return 1
        fi
        [[ -n "${loop_cmd}" ]] && eval "${loop_cmd}" || true
        sleep "${interval}"
    done
}

# Wait for a namespace to finish terminating if it exists in Terminating state
# Usage: wait_for_namespace_cleanup <namespace> [timeout_seconds]
wait_for_namespace_cleanup() {
    local namespace="$1"
    local timeout="${2:-300}"

    if oc get namespace "${namespace}" &>/dev/null && \
       [[ "$(oc get namespace "${namespace}" -o jsonpath='{.status.phase}')" == "Terminating" ]]; then
        echo "Waiting for namespace ${namespace} to finish terminating..."
        local wait_output wait_status
        if wait_output=$(oc wait --for=delete "namespace/${namespace}" --timeout="${timeout}s" 2>&1); then
            return 0
        else
            wait_status=$?
        fi

        # Finalizers can run resource cleanup, so do not remove them for API or auth errors.
        if [[ "${wait_output}" != *"timed out waiting for the condition"* ]]; then
            echo "ERROR: failed while waiting for namespace ${namespace}: ${wait_output}" >&2
            return "${wait_status}"
        fi

        echo "WARNING: namespace ${namespace} stuck in Terminating state, removing finalizers..."
        oc get namespace "${namespace}" -o json \
            | python3 -c "import json,sys; ns=json.load(sys.stdin); ns['spec']['finalizers']=[]; json.dump(ns,sys.stdout)" \
            | oc replace --raw "/api/v1/namespaces/${namespace}/finalize" -f - >/dev/null 2>&1 || true
        oc wait --for=delete "namespace/${namespace}" --timeout=60s 2>/dev/null || {
            echo "ERROR: namespace ${namespace} still exists after finalizer removal."
            exit 1
        }
    fi
}

# Wait for a namespace to exist and a resource within it to match a condition
# Usage: wait_for_resource <resource> <condition> [timeout_seconds] [namespace]
wait_for_resource() {
    local resource="$1"
    local condition="$2"
    local timeout="${3:-300}"
    local namespace="${4:-}"
    local ns_args=()

    if [[ -n "${namespace}" ]]; then
        ns_args=(-n "${namespace}")

        retry_until 300 5 '[[ -n "$(oc get namespace --ignore-not-found "${namespace}")" ]]' || {
            echo "Timed out waiting for namespace ${namespace} to exist"
            exit 1
        }
    fi

    retry_until 300 5 '[[ -n "$(oc get "${resource}" --ignore-not-found ${ns_args[@]+"${ns_args[@]}"})" ]]' || {
        echo "Timed out waiting for ${resource} to exist"
        exit 1
    }

    oc wait --for="${condition}" "${resource}" ${ns_args[@]+"${ns_args[@]}"} --timeout="${timeout}s"
}

# Retry a command until it succeeds or times out.
# All output (stdout/stderr) is preserved on every attempt.
# Usage: retry_command <timeout_seconds> <interval_seconds> <command> [args...]
retry_command() {
    local timeout="$1"
    local interval="$2"
    shift 2
    local start=${SECONDS}
    local attempt=1
    while true; do
        local elapsed=$(( SECONDS - start ))
        echo "  retry_command[attempt=${attempt} elapsed=${elapsed}s timeout=${timeout}s]: $*"
        local rc=0
        "$@" || rc=$?
        if (( rc == 0 )); then
            echo "  retry_command: succeeded on attempt ${attempt} after $(( SECONDS - start ))s"
            return 0
        fi
        if (( SECONDS - start >= timeout )); then
            echo "  retry_command: FAILED after ${attempt} attempts, $(( SECONDS - start ))s elapsed (exit code ${rc})"
            return "${rc}"
        fi
        echo "  retry_command: exit code ${rc}, retrying in ${interval}s..."
        sleep "${interval}"
        attempt=$(( attempt + 1 ))
    done
}

# HTTP request with retry. Outputs response body on success.
# Returns 1 and prints ERROR to stderr on persistent failure.
# Usage: http_retry <error_msg> <retries> <interval> [curl_args...]
http_retry() {
    local err_msg="$1" retries="$2" interval="$3"
    shift 3
    for attempt in $(seq 1 "$retries"); do
        curl -sS --fail-with-body "$@" && return 0
        if (( attempt < retries )); then
            echo "  http_retry: attempt ${attempt}/${retries} failed, retrying in ${interval}s..." >&2
            sleep "$interval"
        fi
    done
    echo "ERROR: ${err_msg}" >&2
    return 1
}

# HTTP request with retry + jq parsing. Outputs parsed value on success.
# Returns 1 and prints ERROR to stderr on persistent failure.
# Usage: http_json <error_msg> <retries> <interval> <jq_filter> [curl_args...]
http_json() {
    local err_msg="$1" retries="$2" interval="$3" filter="$4"
    shift 4
    local result
    for attempt in $(seq 1 "$retries"); do
        if result=$(curl -sS --fail-with-body "$@" | jq -r "$filter"); then
            printf '%s\n' "$result"
            return 0
        fi
        if (( attempt < retries )); then
            echo "  http_json: attempt ${attempt}/${retries} failed, retrying in ${interval}s..." >&2
            sleep "$interval"
        fi
    done
    echo "ERROR: ${err_msg}" >&2
    return 1
}

# Usage: get_fulfillment_access_token <issuer_url> <client_id> <client_secret> [curl_args...]
get_fulfillment_access_token() {
    if (($# < 3)); then
        echo "ERROR: Fulfillment client credentials are not configured." >&2
        return 1
    fi

    local issuer_url="$1" client_id="$2" client_secret="$3"
    shift 3
    local -a curl_args=("$@")

    if [[ -z "${issuer_url}" || -z "${client_id}" || -z "${client_secret}" ]]; then
        echo "ERROR: Fulfillment client credentials are not configured." >&2
        return 1
    fi
    if [[ "${issuer_url}" != https://* ]]; then
        echo "ERROR: Fulfillment token issuer URL must use HTTPS." >&2
        return 1
    fi

    local secret_file
    if ! secret_file=$(mktemp); then
        echo "ERROR: unable to prepare the Fulfillment token request." >&2
        return 1
    fi
    if ! chmod 600 "${secret_file}" || ! printf '%s' "${client_secret}" > "${secret_file}"; then
        rm -f "${secret_file}"
        echo "ERROR: unable to prepare the Fulfillment token request." >&2
        return 1
    fi

    local token_url="${issuer_url%/}/protocol/openid-connect/token"
    local response
    if ! response=$(curl --silent --show-error --fail \
        --connect-timeout 5 --max-time 30 \
        "${curl_args[@]}" \
        --request POST \
        --data-urlencode 'grant_type=client_credentials' \
        --data-urlencode "client_id=${client_id}" \
        --data-urlencode "client_secret@${secret_file}" \
        "${token_url}"); then
        rm -f "${secret_file}"
        echo "ERROR: Fulfillment token request failed." >&2
        return 1
    fi
    rm -f "${secret_file}"

    local access_token
    if ! access_token=$(printf '%s' "${response}" | python3 -c '
import json
import sys

try:
    token = json.load(sys.stdin).get("access_token")
except (json.JSONDecodeError, AttributeError):
    sys.exit(1)

if not isinstance(token, str) or not token:
    sys.exit(1)

sys.stdout.write(token)
'); then
        echo "ERROR: Fulfillment token response was invalid." >&2
        return 1
    fi

    printf '%s\n' "${access_token}"
}

# Usage: get_fulfillment_service_account_token <namespace> [curl-args...]
# Resolve the public issuer URL and service-account credentials from the
# installer's existing ConfigMap and Secret, then exchange them for a token.
get_fulfillment_service_account_token() {
    if (($# < 1)) || [[ -z "$1" ]]; then
        echo "ERROR: Fulfillment client credentials are not configured." >&2
        return 1
    fi

    local namespace="$1"
    shift
    local auth_config issuer_url client_id_secret_name client_id_secret_key
    local client_secret_secret_name client_secret_secret_key
    local encoded_client_id encoded_client_secret client_id client_secret
    local auth_config_map_output auth_config_map auth_config_map_name auth_config_map_count

    if ! auth_config_map_output=$(kubectl -n "${namespace}" get configmap \
        -l 'osac.openshift.io/fulfillment-auth-config=true' \
        -o name \
        2>/dev/null); then
        echo "ERROR: Fulfillment issuer configuration is unavailable." >&2
        return 1
    fi
    auth_config_map_name=""
    auth_config_map_count=0
    if [[ -n "${auth_config_map_output}" ]]; then
        while IFS= read -r auth_config_map; do
            [[ -n "${auth_config_map}" ]] || continue
            auth_config_map_name="${auth_config_map}"
            auth_config_map_count=$((auth_config_map_count + 1))
        done <<< "${auth_config_map_output}"
    fi
    if ((auth_config_map_count != 1)); then
        echo "ERROR: expected exactly one Fulfillment auth ConfigMap, found ${auth_config_map_count}." >&2
        return 1
    fi
    if ! auth_config=$(kubectl -n "${namespace}" get "${auth_config_map_name}" \
        -o 'jsonpath={.data.OSAC_FULFILLMENT_ISSUER_URL}{"\t"}{.data.OSAC_FULFILLMENT_CLIENT_ID_SECRET_NAME}{"\t"}{.data.OSAC_FULFILLMENT_CLIENT_ID_SECRET_KEY}{"\t"}{.data.OSAC_FULFILLMENT_CLIENT_SECRET_SECRET_NAME}{"\t"}{.data.OSAC_FULFILLMENT_CLIENT_SECRET_SECRET_KEY}' \
        2>/dev/null); then
        echo "ERROR: Fulfillment issuer configuration is unavailable." >&2
        return 1
    fi
    IFS=$'\t' read -r issuer_url client_id_secret_name client_id_secret_key \
        client_secret_secret_name client_secret_secret_key <<< "${auth_config}"
    if [[ -z "${issuer_url}" || -z "${client_id_secret_name}" || -z "${client_id_secret_key}" \
        || -z "${client_secret_secret_name}" || -z "${client_secret_secret_key}" ]]; then
        echo "ERROR: Fulfillment client credentials are unavailable." >&2
        return 1
    fi
    if ! encoded_client_id=$(kubectl -n "${namespace}" get secret "${client_id_secret_name}" \
        -o "jsonpath={.data['${client_id_secret_key}']}" 2>/dev/null); then
        echo "ERROR: Fulfillment client credentials are unavailable." >&2
        return 1
    fi
    if ! encoded_client_secret=$(kubectl -n "${namespace}" get secret "${client_secret_secret_name}" \
        -o "jsonpath={.data['${client_secret_secret_key}']}" 2>/dev/null); then
        echo "ERROR: Fulfillment client credentials are unavailable." >&2
        return 1
    fi

    if ! client_id=$(printf '%s' "${encoded_client_id}" | python3 -c '
import base64
import sys

try:
    value = base64.b64decode(sys.stdin.buffer.read(), validate=True)
    sys.stdout.buffer.write(value)
except (ValueError, base64.binascii.Error):
    sys.exit(1)
'); then
        echo "ERROR: Fulfillment client credentials are invalid." >&2
        return 1
    fi
    unset encoded_client_id
    if ! client_secret=$(printf '%s' "${encoded_client_secret}" | python3 -c '
import base64
import sys

try:
    value = base64.b64decode(sys.stdin.buffer.read(), validate=True)
    sys.stdout.buffer.write(value)
except (ValueError, base64.binascii.Error):
    sys.exit(1)
'); then
        echo "ERROR: Fulfillment client credentials are invalid." >&2
        return 1
    fi
    unset encoded_client_secret

    get_fulfillment_access_token "${issuer_url}" "${client_id}" "${client_secret}" "$@"
}

# Resolve the highest real (non-nightly) component release tag matching
# "<prefix>/vX.Y.Z" -- scoped by prefix since tags aren't path-scoped.
#
# Name-based (git tag -l), not ancestry-based (git describe --tags):
# release tags get created on a throwaway temp-branch commit that's never
# merged back into the default branch, so an ancestry walk from a fresh
# checkout can never see them -- confirmed live, it left nightly stuck
# resolving a week-old base version while real releases kept shipping.
# `git tag -l` lists every tag in the local object database regardless of
# branch, which is what "latest real release" actually means here.
#
# The glob is still not a real anchor, so candidates are re-validated with
# a regex before being trusted -- fails loudly rather than guessing a
# version and packaging a chart under a made-up tag.
# Usage: resolve_release_tag <repo_path> [tag_prefix]
# tag_prefix defaults to "osac" (umbrella chart tags: osac/vX.Y.Z).
resolve_release_tag() {
    local path="$1"
    local prefix="${2:-osac}"
    local tag
    local match_pattern
    local validate_regex

    if [[ ! "${prefix}" =~ ^[a-zA-Z0-9_-]+$ ]]; then
        echo "ERROR: invalid tag prefix '${prefix}' — must match [a-zA-Z0-9_-]+" >&2
        return 1
    fi

    match_pattern="${prefix}/v[0-9]*.[0-9]*.[0-9]*"
    validate_regex="^${prefix}/v[0-9]+\\.[0-9]+\\.[0-9]+$"

    tag=$(git -C "${path}" tag -l "${match_pattern}" | { grep -E "${validate_regex}" || true; } | sort -V | tail -1)
    if [[ -z "${tag}" ]]; then
        echo "ERROR: no real (non-nightly) ${prefix}/vX.Y.Z release tag found in ${path} — refusing to guess a version" >&2
        return 1
    fi
    echo "${tag}"
}

# Validate a plain semver.org version string (no leading 'v'). Full
# semver.org grammar adapted to POSIX ERE for bash's =~: rejects
# leading-zero numeric identifiers (e.g. "01.2.3", "1.2.3-01") and
# malformed dot-separated prerelease/build identifiers that a more
# permissive [a-zA-Z0-9.]+ charset would let through. Shared here so
# osac-build-and-publish.yaml's release_version/component_versions inputs
# (see OSAC-5337) don't grow a second hand-copied regex to drift out of
# sync with, the exact class of bug OSAC-5178 fixed.
# Usage: validate_semver <version>
validate_semver() {
    local version="$1"
    local semver_re='^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-(0|[1-9][0-9]*|[0-9]*[a-zA-Z-][0-9a-zA-Z-]*)(\.(0|[1-9][0-9]*|[0-9]*[a-zA-Z-][0-9a-zA-Z-]*))*)?(\+[0-9a-zA-Z-]+(\.[0-9a-zA-Z-]+)*)?$'
    # Reject pathologically long input before it ever reaches the regex engine --
    # cheap defense-in-depth against the prerelease clause's repeated alternation
    # groups, independent of how expensive a match against them actually is.
    # No real semver string needs anywhere near this length.
    [[ "${#version}" -le 128 ]] && [[ "${version}" =~ ${semver_re} ]]
}

# True if <prefix>/v<version> already exists as a tag reachable from this
# checkout. Used by release mode to decide whether an explicitly-requested
# component_versions entry needs a real build (doesn't exist yet) or can
# just be pinned like any other already-released version (OSAC-5337).
# Usage: real_tag_exists <prefix> <version>
real_tag_exists() {
    local prefix="$1" version="$2"
    git rev-parse -q --verify "refs/tags/${prefix}/v${version}^{commit}" >/dev/null 2>&1
}

# Resolve the nearest real (non-nightly) bare vX.Y.Z release tag reachable from an
# external repo path (e.g. osac-ui, which tags v0.0.5 rather than osac-ui/v0.0.5).
# Pre-release-only tags (e.g. v0.0.1-rc1) are ignored; repos with no stable tag fail loud.
# Usage: resolve_bare_release_tag <repo_path>
resolve_bare_release_tag() {
    local path="$1"
    local tag
    local validate_regex='^v[0-9]+\.[0-9]+\.[0-9]+$'

    while IFS= read -r tag; do
        [[ -z "${tag}" ]] && continue
        [[ "${tag}" == *-nightly* ]] && continue
        if [[ "${tag}" =~ ${validate_regex} ]]; then
            echo "${tag}"
            return 0
        fi
    done < <(git -C "${path}" tag -l 'v[0-9]*.[0-9]*.[0-9]*' --merged HEAD --sort=-v:refname 2>/dev/null)

    echo "ERROR: no real (non-nightly) vX.Y.Z release tag reachable from ${path} — refusing to guess a version" >&2
    return 1
}

readonly POSTGRES_INSTALL_DOC="../fulfillment-service/docs/INSTALL.md"

# PostgreSQL prerequisite helpers (production install via setup.sh).
# Snapshot CI refresh mirrors host/endpoint resolution in
# scripts/refresh-after-snapshot.py (_postgres_target and related helpers).
# Keep both in sync when changing URL parsing or endpoint checks.

_postgres_prereq_error() {
    echo "ERROR: $1" >&2
    echo "Deploy in-cluster PostgreSQL via an operator per ${POSTGRES_INSTALL_DOC}" >&2
    exit 1
}

_bundled_postgres_enabled() {
    local values_file="$1"
    [[ -r "${values_file}" ]] || return 2
    awk '
        /^bundledPostgres:/ { bp=1; next }
        bp && /^[^[:space:]#]/ { bp=0 }
        bp && /^[[:space:]]+enabled:[[:space:]]*true([[:space:]]*#.*)?$/ { found=1 }
        END { exit !found }
    ' "$values_file"
}

_parse_db_host_from_url() {
    local url="$1"
    case "${url}" in
        postgres://*) ;;
        postgresql://*) url="postgres://${url#postgresql://}" ;;
        *) return 1 ;;
    esac
    local hostport="${url#postgres://}"
    hostport="${hostport#*@}"
    hostport="${hostport%%/*}"
    hostport="${hostport%%\?*}"
    echo "${hostport%%:*}"
}

# Resolve a PostgreSQL host from osac-db-config URL to service and namespace.
# Prints "service target_namespace" on stdout; returns 1 if unrecognized.
_resolve_postgres_service() {
    local host="$1"
    local install_namespace="$2"
    local -a parts
    local i

    if [[ -z "${host}" ]]; then
        return 1
    fi

    if [[ "${host}" != *.* ]]; then
        printf '%s %s\n' "${host}" "${install_namespace}"
        return 0
    fi

    IFS='.' read -ra parts <<< "${host}"
    for i in "${!parts[@]}"; do
        if [[ "${parts[$i]}" == "svc" ]] && (( i >= 2 )); then
            printf '%s %s\n' "${parts[0]}" "${parts[1]}"
            return 0
        fi
    done

    if ((${#parts[@]} == 2)); then
        printf '%s %s\n' "${parts[0]}" "${parts[1]}"
        return 0
    fi

    return 1
}

_verify_postgres_endpoints() {
    local service="$1"
    local target_namespace="$2"

    oc get endpoints "${service}" -n "${target_namespace}" \
        -o jsonpath='{.subsets[0].addresses[0].ip}' 2>/dev/null | grep -q .
}

# Verify in-cluster PostgreSQL is deployed before Helm install.
# Usage: check_postgres_prerequisites <namespace> <values_file>
check_postgres_prerequisites() {
    local namespace="$1"
    local values_file="$2"
    local service target_namespace db_url db_host resolved bundled_status

    _bundled_postgres_enabled "${values_file}"
    bundled_status=$?
    if [[ ${bundled_status} -eq 2 ]]; then
        _postgres_prereq_error "Values file ${values_file} not found or unreadable."
    elif [[ ${bundled_status} -eq 0 ]]; then
        # bundledPostgres's Deployment/Service are templated inside charts/osac
        # itself and created by the very `helm upgrade --install osac --wait`
        # this check runs ahead of -- nothing to verify yet, and checking now
        # would always fail (the Service doesn't exist until that install
        # creates it). The chart's own --wait covers Postgres readiness.
        echo "bundledPostgres enabled -- readiness will be verified by the chart's own install --wait."
        return 0
    else
        echo "Checking in-cluster PostgreSQL prerequisites..."
        oc get secret osac-db-config -n "${namespace}" &>/dev/null || \
            _postgres_prereq_error "Secret osac-db-config not found in namespace ${namespace}."
        oc get secret osac-db-client-cert -n "${namespace}" &>/dev/null || \
            _postgres_prereq_error "Secret osac-db-client-cert not found in namespace ${namespace}."

        db_url=$(oc get secret osac-db-config -n "${namespace}" \
            -o jsonpath='{.data.url}' | base64 -d 2>/dev/null || true)
        [[ -n "${db_url}" ]] || \
            _postgres_prereq_error "Secret osac-db-config in ${namespace} has an empty url key."

        db_host=$(_parse_db_host_from_url "${db_url}") || \
            _postgres_prereq_error "Secret osac-db-config in ${namespace} has an invalid PostgreSQL url."
        resolved=$(_resolve_postgres_service "${db_host}" "${namespace}") || \
            _postgres_prereq_error "Unrecognized database hostname in osac-db-config url."
        read -r service target_namespace <<< "${resolved}"
    fi

    if ! _verify_postgres_endpoints "${service}" "${target_namespace}"; then
        _postgres_prereq_error "PostgreSQL Service referenced by osac-db-config has no ready endpoints."
    fi

    echo "PostgreSQL prerequisites satisfied."
}

_LIB_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${_LIB_DIR}/oc.sh"
