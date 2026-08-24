#!/usr/bin/env bash

# Sourced by other scripts, all of which already set this themselves before
# sourcing -- set here too so a function like resolve_release_tag() still
# fails loudly instead of silently continuing on an internal error if this
# is ever sourced by a script that forgot to.
set -euo pipefail

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
        oc wait --for=delete "namespace/${namespace}" --timeout="${timeout}s" || {
            echo "ERROR: namespace ${namespace} stuck in Terminating state. You may need to manually remove finalizers."
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
        curl -ksS --fail-with-body "$@" && return 0
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
        if result=$(curl -ksS --fail-with-body "$@" | jq -r "$filter"); then
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

# Resolve the nearest real (non-nightly) osac/vX.Y.Z umbrella-chart release
# tag reachable from a repo path's HEAD. --match narrows git describe's glob
# search to tags shaped like "osac/vX.Y.Z" -- scoped by the "osac/" prefix,
# not just a bare "vX.Y.Z", because git tags aren't path-scoped and a bare
# vX.Y.Z pattern can match an unrelated component's old tag (e.g.
# fulfillment-service tagged bare vX.Y.Z releases before OSAC-3529 moved it
# to a component-scoped prefix; that history is still reachable from HEAD).
# --match is still a glob, not a real anchor (its trailing '*' is needed to
# allow multi-digit version segments, but that same '*' would also accept a
# stray "-rc1"/".4" suffix), so the result is re-validated with a real regex
# before being trusted. Fails loudly rather than silently guessing a
# version: publishing a chart under a made-up placeholder tag would be worse
# than failing the build outright, since it could get pushed to the
# registry unnoticed.
# Usage: resolve_release_tag <repo_path>
resolve_release_tag() {
    local path="$1"
    local tag
    if ! tag=$(git -C "${path}" describe --tags --abbrev=0 --match 'osac/v[0-9]*.[0-9]*.[0-9]*' --exclude '*-nightly*' 2>/dev/null); then
        echo "ERROR: no real (non-nightly) osac/vX.Y.Z release tag reachable from ${path} — refusing to guess a version" >&2
        return 1
    fi
    if [[ ! "${tag}" =~ ^osac/v[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
        echo "ERROR: nearest release tag '${tag}' reachable from ${path} is not a plain osac/vX.Y.Z tag — refusing to guess a version" >&2
        return 1
    fi
    echo "${tag}"
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
