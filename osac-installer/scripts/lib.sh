#!/usr/bin/env bash

# Sourced by other scripts, all of which already set this themselves before
# sourcing -- set here too so a function like resolve_release_tag() still
# fails loudly instead of silently continuing on an internal error if this
# is ever sourced by a script that forgot to.
set -euo pipefail

ensure_podman_pid_limits() {
    local cluster="$1" kind_command="$2" podman_command="$3" nodes node limit
    local limit_pattern='^(-1|0|[1-9][0-9]*)$'
    if ! nodes=$("${kind_command}" get nodes --name "${cluster}") || [[ -z "${nodes}" ]]; then
        echo "ERROR: Could not list Podman Kind nodes for '${cluster}'; check the selected connection." >&2
        return 1
    fi
    while IFS= read -r node; do
        [[ -n "${node}" ]] || continue
        if ! limit=$("${podman_command}" inspect --format '{{.HostConfig.PidsLimit}}' "${node}") ||
           [[ ! "${limit}" =~ ${limit_pattern} ]]; then
            echo "ERROR: Could not inspect configured Podman PID limit for '${node}'; check the selected connection." >&2
            return 1
        fi
        if [[ "${limit}" != -1 && "${limit}" != 0 ]] && (( limit < 65536 )); then
            if ! "${podman_command}" update --pids-limit 65536 "${node}"; then
                echo "ERROR: Could not raise Podman PID limit for '${node}' from ${limit} to 65536; check runtime permissions." >&2
                return 1
            fi
            if ! limit=$("${podman_command}" inspect --format '{{.HostConfig.PidsLimit}}' "${node}") ||
               [[ ! "${limit}" =~ ${limit_pattern} ]] ||
               { [[ "${limit}" != -1 && "${limit}" != 0 ]] && (( limit < 65536 )); }; then
                echo "ERROR: Podman PID limit verification failed for '${node}' (reported '${limit}'); expected at least 65536 or unlimited." >&2
                return 1
            fi
        fi
    done <<< "${nodes}"
}

# Validate the Kubernetes Secret metadata.name and data key used for the
# remote-cluster kubeconfig before either value reaches a command or manifest.
validate_remote_kubeconfig_secret() {
    local secret_name="$1"
    local secret_key="$2"
    local secret_name_pattern='^[a-z0-9]([-a-z0-9]*[a-z0-9])?(\.[a-z0-9]([-a-z0-9]*[a-z0-9])?)*$'
    local LC_ALL=C

    if (( ${#secret_name} > 253 )) || \
        [[ ! "${secret_name}" =~ ${secret_name_pattern} ]]; then
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

# Validate the target namespace before NS is interpolated into any later
# recipe command text. The value reaches this validator through the
# exported environment (never through Make expansion into command text),
# so it cannot break out of this quoting either; once it passes, every
# later $(NS) interpolation is known to carry namespace-safe characters.
# The value itself is deliberately not echoed back: a rejected name could
# carry control characters that corrupt the log.
validate_namespace() {
    local ns="$1"
    local namespace_pattern='^[a-z0-9]([-a-z0-9]*[a-z0-9])?$'
    local LC_ALL=C

    if (( ${#ns} == 0 || ${#ns} > 63 )) || \
        [[ ! "${ns}" =~ ${namespace_pattern} ]]; then
        echo "ERROR: NS must be a valid namespace name: a lowercase DNS label of at most 63 characters (a-z, 0-9, and '-', starting and ending with a-z or 0-9)." >&2
        return 1
    fi

    return 0
}

# Render the osac chart offline for the input-validation gate, bridging the
# NetBox lookup branch. helm template cannot see cluster Secrets (lookup
# returns nil offline), so a profile relying on the install-time
# netbox-token-seed hook to create the token Secret fails the render with a
# specific "tokenValue is required" error. Only that failure is retried with
# a render-only placeholder token (never passed to an installation). Two
# bridges exist, and each is legitimate only under a verified condition:
#
# Seeded (netbox_seed_check, same shell, exported NETBOX_TOKEN_SEEDED=1):
# the seed hook creates the token Secret during install-infra, before
# install-osac renders. The bridge holds only when the seed hook's Secret
# identity -- name, namespace, and key, recorded by netbox_seed_check from
# the rendered hook -- matches the Secret the osac chart reads: a caller
# override that diverges (for example bmf.netbox.tokenSecretKey) would
# pass a bridged render yet fail the real install only after
# install-infra changed the cluster, so a mismatch fails the gate instead.
#
# Pre-existing (not seeded): the token Secret can already exist in the
# cluster -- created by a prior install or an operator -- and helm upgrade
# reads it through lookup at install time even though helm template
# cannot. netbox_existing_secret_matches verifies the Secret the osac
# chart reads, with its key, through a read-only cluster lookup; a missing
# Secret or key stays a real input error that fails the gate.
#
# Every other failure -- including a caller-supplied token that violates
# the schema, however it was supplied (--set, --set-string, or a values
# file) -- aborts with safe diagnostics, so the gate always validates the
# caller's actual merged value, never a placeholder masking it.
#
# Usage: render_osac_chart_offline PLACEHOLDER_TOKEN RELEASE CHART [HELM_ARGS...]
render_osac_chart_offline() {
    local placeholder_token="$1"
    shift
    local err bridged bridged_err_file
    if err=$(helm template "$@" 2>&1 >/dev/null); then
        return 0
    fi
    case "${err}" in
        *"netbox.tokenValue is required when Secret"*)
            bridged_err_file=$(mktemp)
            if ! bridged=$(helm template "$@" \
                --set "bmf.netbox.tokenValue=${placeholder_token}" \
                2>"${bridged_err_file}"); then
                offline_render_error "$(cat "${bridged_err_file}")"
                rm -f "${bridged_err_file}"
                return 1
            fi
            rm -f "${bridged_err_file}"
            if [ "${NETBOX_TOKEN_SEEDED:-0}" = "1" ]; then
                netbox_seed_identity_matches "${bridged}" "${placeholder_token}"
                return
            fi
            if ! netbox_existing_secret_matches "${bridged}" \
                "${placeholder_token}"; then
                echo "ERROR: netbox.tokenValue is required when the NetBox token Secret is neither seeded by the infra hook nor present in the cluster; no cluster changes were made. Supply bmf.netbox.tokenValue, pre-create the Secret the osac chart reads (bmf.secrets.netboxToken, bmf.netbox.tokenSecretKey), or install the netbox profile's infra release first. The Secret name and key are deliberately not printed: they are customer-supplied identifiers that can carry internal hostnames." >&2
                return 1
            fi
            return 0
            ;;
    esac
    offline_render_error "${err}"
    return 1
}

# Dotted paths of the installer values-schema maps whose entry names come
# from the caller: JSON-schema objects that validate their unknown keys, so
# Helm echoes such a key in a schema error path (a serviceMonitor label that
# is a hostname, for example). A failure under one of these maps reports only
# the map root. The validation diagnostics tests derive the same set from
# every charts/*/values.schema.json and fail when this list and the schemas
# drift, so a new open map cannot start printing caller-supplied keys
# unnoticed. A map that accepts any additional property
# (`"additionalProperties": true`) is deliberately absent: it validates no
# unknown key, so no key can reach a path through it.
_LIB_OPEN_SCHEMA_MAPS=(
    aap.instanceGroups.networkFulfillment.config
    aap.instanceGroups.networkFulfillment.secret
    metering.prometheusRule.labels
    metering.serviceMonitor.labels
    operator.fabricDomainInventory
    operator.networkManagers.fabricManagers
    operator.networkManagers.k8sManagers
    operator.stall.workersJoiningByInstanceType
    operator.vendorControllers
    preflight.skip
)

# Render a chart offline and report only value-free diagnostics on failure.
# The arguments pass through to helm template unchanged, so every gate keeps
# its own release name, values files, and --set inputs. Helm echoes the
# offending value in some schema errors (Helm 3.22 and later print it for
# pattern failures) and in template fail/required messages, so the raw
# diagnostics never reach the terminal or the collected CI logs.
# Usage: render_chart_offline [HELM_ARGS...]
render_chart_offline() {
    local err
    if err=$(helm template "$@" 2>&1 >/dev/null); then
        return 0
    fi
    offline_render_error "${err}"
    return 1
}

offline_render_error() {
    echo "ERROR: offline chart rendering failed; check chart dependencies, values schema and template requirements. Helm diagnostics are suppressed because they may contain sensitive input values." >&2
    case "$1" in
        *bmf.netbox.tokenValue*"Invalid type"*|*/bmf/netbox/tokenValue*"got number, want string"*)
            echo "bmf.netbox.tokenValue: Invalid type; expected string" >&2 ;;
        *"duplicate metering reader"*)
            echo "duplicate metering reader: each reader name must be unique" >&2 ;;
        *"NETRIS_CONTROLLER_URL must be an https:// URL"*)
            echo "instanceGroups.networkFulfillment.config.NETRIS_CONTROLLER_URL must be an https:// URL" >&2 ;;
        *)
            # Any other failure keeps the generic line above. Only the
            # diagnostics Helm itself writes are echoed back: the
            # chart-relative template location, or the first values
            # schema field path. Both are read from the first "Error: "
            # line, because a fail/required message or a rejected value
            # can hold a newline followed by text shaped like a second
            # error or schema item. Helm prints chart warnings before the
            # error, so the scan starts at the first error line rather
            # than the first line of output. Nothing is read past the
            # first schema item: later items cannot be told apart from
            # such echoed text. A failure without a recognizable marker
            # keeps the generic line alone. An open schema map in
            # _LIB_OPEN_SCHEMA_MAPS carries caller-supplied entry keys (a
            # serviceMonitor label that is a hostname, for example), and
            # such a key can appear in the path, so the path is printed
            # as the map root without the entry key.
            local maps="${_LIB_OPEN_SCHEMA_MAPS[*]}"
            printf '%s\n' "$1" | LC_ALL=C awk -v q="'" -v maps="${maps}" '
                BEGIN {
                    n_maps = split(maps, map_path, " ")
                }
                # Dotted name of the open values-schema map the path
                # enters, or "" when it enters none. The segment after the
                # map name is a caller-supplied key; Helm 4 separates
                # path segments with a slash and Helm 3 with a dot.
                function open_map(path,    slash, i) {
                    slash = path
                    gsub(/\//, ".", slash)
                    for (i = 1; i <= n_maps; i++)
                        if (index(slash, map_path[i] ".") == 1)
                            return map_path[i]
                    return ""
                }
                !seen && /^Error: / {
                    seen = 1
                    if ($0 ~ /^Error: execution error at \(/) {
                        loc = $0
                        sub(/^Error: execution error at \(/, "", loc)
                        sub(/\):.*/, "", loc)
                        if (loc ~ /^[A-Za-z0-9_.\/-]+:[0-9]+:[0-9]+$/)
                            print "template check failed in " loc \
                                " (message suppressed)"
                    } else if ($0 ~ /^Error: failed parsing --set-file data:/) {
                        # Helm names the file it could not read; the path
                        # can carry customer data, so only the argument is
                        # reported.
                        print "--set-file input could not be read; check" \
                            " OIDC_IDP_CA_FILE and other --set-file" \
                            " arguments"
                    } else if ($0 ~ /^Error: values don/) {
                        schema = 1
                    }
                    next
                }
                schema && !done {
                    done = 1
                    if (match($0, "^- at " q "[^" q "]*" q ":")) {
                        path = substr($0, RSTART + 6, RLENGTH - 8)
                        # Helm 4 writes the path with a leading slash; drop
                        # it so both schema error formats name the field the
                        # same way.
                        sub(/^\//, "", path)
                        map = open_map(path)
                        if (map != "")
                            print map ": violates the values schema"
                        else if (path ~ /^[A-Za-z0-9_.~\/-]+$/)
                            print path ": violates the values schema"
                    } else if (match($0, "^- [A-Za-z0-9_.()-]+: ")) {
                        path = substr($0, RSTART + 2, RLENGTH - 4)
                        map = open_map(path)
                        if (map != "")
                            print map ": violates the values schema"
                        else
                            print path ": violates the values schema"
                    } else if ($0 !~ /^- /) {
                        # Only the chart-name line precedes the first item;
                        # any other non-item line means the schema block
                        # ended. A `- ` line that no branch recognizes is
                        # still an item (a Helm 3 path can hold characters
                        # outside the pattern), so scanning stays stopped:
                        # a line after it could be echoed rejected input.
                        done = 0
                    }
                }' >&2 ;;
    esac
}

# Extract the NetBox token Secret identity the osac chart renders for the
# bridged placeholder token: the Secret whose stringData carries the
# placeholder is the one the bmf subchart creates from
# bmf.secrets.netboxToken / bmf.netbox.tokenSecretKey. Prints the name,
# namespace, and key, one per line.
# Usage: netbox_render_secret_identity RENDER PLACEHOLDER_TOKEN
netbox_render_secret_identity() {
    local render="$1" placeholder="$2"
    printf '%s\n' "${render}" | awk -v ph="${placeholder}" '
        /^---$/ { kind = ""; name = ""; ns = ""; next }
        /^kind: / { kind = $2 }
        /^  name: / { if (name == "") name = $2 }
        /^  namespace: / { if (ns == "") ns = $2 }
        index($0, ph) != 0 && kind == "Secret" && !done {
            key = $1
            sub(/:$/, "", key)
            gsub(/"/, "", key)
            gsub(/"/, "", name)
            gsub(/"/, "", ns)
            print name
            print ns
            print key
            done = 1
        }'
}

# Verify read-only, against the cluster, that the NetBox token Secret the
# osac chart reads already exists with the right key: with no seed hook,
# the token can only come from a Secret created outside this install
# (which `helm upgrade` reads through lookup even though `helm template`
# cannot). The lookup prints no Secret content -- only whether the key
# exists -- so the gate accepts exactly the installs that would succeed
# and still rejects a missing Secret or key before any cluster change.
# The identity fields are restricted to a safe charset because they are
# interpolated into the kubectl go-template; anything else cannot be
# verified safely, so it fails closed with the underlying token error.
# Tries kubectl, then oc; with neither available, or no reachable
# cluster, the lookup fails closed the same way.
# Usage: netbox_existing_secret_matches RENDER PLACEHOLDER_TOKEN
netbox_existing_secret_matches() {
    local render="$1" placeholder="$2"
    local identity name ns key cli out
    identity=$(netbox_render_secret_identity "${render}" "${placeholder}")
    name=$(printf '%s\n' "${identity}" | sed -n 1p)
    ns=$(printf '%s\n' "${identity}" | sed -n 2p)
    key=$(printf '%s\n' "${identity}" | sed -n 3p)
    [ -n "${name}" ] && [ -n "${ns}" ] && [ -n "${key}" ] || return 1
    case "${name}${ns}${key}" in
        *[!A-Za-z0-9._-]*) return 1 ;;
    esac
    for cli in kubectl oc; do
        command -v "${cli}" >/dev/null 2>&1 || continue
        if out=$("${cli}" get secret "${name}" -n "${ns}" \
            -o go-template="{{if index .data \"${key}\"}}present{{end}}" \
            2>/dev/null); then
            [ "${out}" = "present" ]
            return
        fi
    done
    return 1
}

# Compare the NetBox token Secret identity the osac chart renders with the
# seed hook's identity exported by netbox_seed_check. Any mismatch fails
# the gate before a cluster change; the message names which identity
# fields differ, never their values -- Secret names and keys are
# customer-supplied and can carry internal identifiers, and the gate's
# errors reach the terminal and CI logs.
# Usage: netbox_seed_identity_matches RENDER PLACEHOLDER_TOKEN
netbox_seed_identity_matches() {
    local render="$1" placeholder="$2"
    local identity bmf_name bmf_ns bmf_key differing=""
    identity=$(netbox_render_secret_identity "${render}" "${placeholder}")
    bmf_name=$(printf '%s\n' "${identity}" | sed -n 1p)
    bmf_ns=$(printf '%s\n' "${identity}" | sed -n 2p)
    bmf_key=$(printf '%s\n' "${identity}" | sed -n 3p)
    if [ -z "${bmf_name}" ] || [ -z "${bmf_key}" ]; then
        echo "ERROR: could not determine which NetBox token Secret the osac chart reads; no cluster changes were made." >&2
        return 1
    fi
    [ "${bmf_name}" != "${NETBOX_SEED_SECRET_NAME}" ] && differing="name"
    [ "${bmf_ns}" != "${NETBOX_SEED_SECRET_NS}" ] \
        && differing="${differing:+${differing}, }namespace"
    [ "${bmf_key}" != "${NETBOX_SEED_SECRET_KEY}" ] \
        && differing="${differing:+${differing}, }key"
    if [ -n "${differing}" ]; then
        echo "ERROR: NetBox token Secret identity mismatch (differing: ${differing}): the osac chart's NetBox token Secret settings (bmf.secrets.netboxToken, bmf.netbox.tokenSecretKey, and the release namespace) do not match the infra seed hook's (netbox.apiTokenSecret, osacNamespace); no cluster changes were made. Align the osac chart settings with the seed hook and retry. The differing values are deliberately not printed: they are customer-supplied Secret names and keys that can carry internal identifiers." >&2
        return 1
    fi
    return 0
}

# Export NETBOX_TOKEN_SEEDED=1 plus the seed hook's Secret identity --
# NETBOX_SEED_SECRET_NAME, NETBOX_SEED_SECRET_NS, and NETBOX_SEED_SECRET_KEY
# -- when the given infra-chart render deploys the netbox-token-seed hook:
# the post-install Job that creates the NetBox token Secret during
# install-infra, before install-osac renders. render_osac_chart_offline may
# bridge a missing NetBox token only when this is set and the identity
# matches the Secret the osac chart reads. Never fails: the gate renders
# the infra chart itself with full error reporting right before this runs,
# so here a render failure simply counts as not seeded. Must run in the
# same shell as render_osac_chart_offline -- make recipe lines are separate
# shells, so the gate calls both in one recipe line.
#
# The release-prefixed names ("-netbox-token-seed", from
# {{ .Release.Name }}-netbox-token-seed) are the discriminator: the hook's
# script ConfigMap key "netbox-token-seed.sh" renders unconditionally, while
# the Job, ServiceAccount, and RBAC carrying the prefixed name are gated on
# netbox.enabled.
#
# The identity is read from the rendered hook itself -- the Job's OPERATOR_NS
# and SECRET_NAME env values and the Secret key in the seed script's
# `oc create secret --from-literal=<key>=` call -- so it follows the values
# this install actually renders instead of assuming defaults. The key is
# extracted only from the netbox-token-seed.sh block of the hook-scripts
# ConfigMap: other hook scripts create Secrets of their own. If any part of
# the identity cannot be extracted the hook counts as not seeded, leaving
# the bridge off so the gate reports the underlying token error rather than
# bridging an unverifiable identity.
#
# Usage: netbox_seed_check RELEASE CHART [HELM_ARGS...]
netbox_seed_check() {
    local render
    NETBOX_TOKEN_SEEDED=0
    NETBOX_SEED_SECRET_NAME=""
    NETBOX_SEED_SECRET_NS=""
    NETBOX_SEED_SECRET_KEY=""
    if render=$(helm template "$@" 2>/dev/null); then
        case "${render}" in
            *-netbox-token-seed*)
                # Scoped to the seed Job's own document: other rendered
                # resources (e.g. the NetBox Deployment's extraEnvs) can
                # carry env entries named SECRET_NAME or OPERATOR_NS, and
                # search state must not leak across document boundaries.
                NETBOX_SEED_SECRET_NS=$(printf '%s\n' "${render}" \
                    | awk '/^---$/ { in_seed = 0; want = 0; kind = ""; next }
                           /^kind: / { kind = $2 }
                           /^  name: / && kind == "Job" \
                               && /-netbox-token-seed$/ { in_seed = 1; next }
                           in_seed && /^ *- name: OPERATOR_NS$/ { want = 1; next }
                           in_seed && want && /^ *value: / && !done {
                               gsub(/"/, "", $2); print $2; done = 1 }')
                NETBOX_SEED_SECRET_NAME=$(printf '%s\n' "${render}" \
                    | awk '/^---$/ { in_seed = 0; want = 0; kind = ""; next }
                           /^kind: / { kind = $2 }
                           /^  name: / && kind == "Job" \
                               && /-netbox-token-seed$/ { in_seed = 1; next }
                           in_seed && /^ *- name: SECRET_NAME$/ { want = 1; next }
                           in_seed && want && /^ *value: / && !done {
                               gsub(/"/, "", $2); print $2; done = 1 }')
                NETBOX_SEED_SECRET_KEY=$(printf '%s\n' "${render}" \
                    | awk '/^  netbox-token-seed\.sh:/ { in_script = 1; next }
                           in_script && /^  [^ ]/ { in_script = 0 }
                           in_script && /--from-literal=/ && !done {
                               line = $0
                               sub(/^.*--from-literal=/, "", line)
                               sub(/=.*/, "", line)
                               print line
                               done = 1
                           }')
                if [ -n "${NETBOX_SEED_SECRET_NAME}" ] \
                    && [ -n "${NETBOX_SEED_SECRET_NS}" ] \
                    && [ -n "${NETBOX_SEED_SECRET_KEY}" ]; then
                    NETBOX_TOKEN_SEEDED=1
                fi
                ;;
        esac
    fi
    export NETBOX_TOKEN_SEEDED NETBOX_SEED_SECRET_NAME \
        NETBOX_SEED_SECRET_NS NETBOX_SEED_SECRET_KEY
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

# Wait for what the osac-infra release leaves starting when its
# `helm upgrade --install --wait-for-jobs` returns. That flag waits for hook
# Jobs, not for Deployments, so the bundled PostgreSQL pod (it needs its server
# certificate first) and the default CA issuer can still be coming up when the
# next step starts. Each resource is waited for only if the profile created it.
# Usage: wait_for_infra_ready <cli> [timeout_seconds]
wait_for_infra_ready() {
    local cli="$1"
    local timeout="${2:-300}"
    local entry resource namespace condition found rc message lookup_err
    local -a ns_args
    local -a targets=(
        "clusterissuer/default-ca||condition=Ready"
        "deployment/postgres|osac-infra|condition=Available"
        "kafka/osac-kafka|osac-kafka|condition=Ready"
    )

    for entry in "${targets[@]}"; do
        IFS='|' read -r resource namespace condition <<<"${entry}"
        ns_args=()
        [[ -n "${namespace}" ]] && ns_args=(-n "${namespace}")

        # A resource the profile did not create is skipped: the object is absent
        # (empty output) or its CRD is not installed. Any other failure (API
        # unreachable, forbidden, timed out) is an error and not "absent".
        lookup_err="$(mktemp)"
        rc=0
        found="$("${cli}" get "${resource}" "${ns_args[@]+"${ns_args[@]}"}" --ignore-not-found -o name --request-timeout=15s 2>"${lookup_err}")" || rc=$?
        message="$(<"${lookup_err}")"
        rm -f "${lookup_err}"
        if (( rc != 0 )); then
            if [[ "${message}" == *"the server doesn't have a resource type"* ]]; then
                continue
            fi
            echo "ERROR: could not look up ${resource}: ${message}" >&2
            return 1
        fi
        [[ -n "${found}" ]] || continue
        echo "Waiting for ${resource} (${condition}, up to ${timeout}s)..."
        "${cli}" wait --for="${condition}" "${resource}" "${ns_args[@]+"${ns_args[@]}"}" --timeout="${timeout}s" || {
            echo "ERROR: ${resource} did not reach ${condition} within ${timeout}s" >&2
            return 1
        }
    done
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
