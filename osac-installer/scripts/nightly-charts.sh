#!/usr/bin/env bash
set -euo pipefail

# Shared helpers for nightly chart publishing and Slack notifications.

if ! declare -F http_retry >/dev/null 2>&1; then
    # shellcheck source=lib.sh
    source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib.sh"
fi

readonly NIGHTLY_CHART_SLACK_ORDER=(
    osac-operator-crds
    bare-metal-fulfillment-operator-crds
    osac-operator
    fulfillment-service
    osac-aap
    bare-metal-fulfillment-operator
    osac-metering
    csi-driver
    csi-backends
    osac-ui
    osac
)

# CI overlay values files with their own separate floating image tag
# overrides for mono-repo components (operator/aap/bmf/metering/csiDriver),
# on top of the umbrella chart's own values.yaml. Not every file overrides
# every component -- see stamp_ci_overlay_if_present.
readonly NIGHTLY_CI_OVERLAY_VALUES=(
    osac-installer/values/dev/instance.yaml
    osac-installer/values/vmaas-ci/instance.yaml
    osac-installer/values/bmaas-ci/instance.yaml
    osac-installer/values/full-ci/instance.yaml
)

# Usage: append_chart_source <manifest_file> <chart_name> <version> [short_sha] [full_sha]
# Appends one line to chart-sources.txt (chart name, version, optional source SHA).
append_chart_source() {
    local manifest_file="$1" chart_name="$2" version="$3"
    local short_sha="${4:-}" full_sha="${5:-}"
    if [[ -n "${short_sha}" && -n "${full_sha}" ]]; then
        printf '%s %s %s %s\n' "${chart_name}" "${version}" "${short_sha}" "${full_sha}" >> "${manifest_file}"
    else
        printf '%s %s\n' "${chart_name}" "${version}" >> "${manifest_file}"
    fi
}

# Usage: check_osac_ui_image [repo_name]
# Resolve osac-ui@main HEAD and verify ghcr.io/osac-project/<repo>:sha-<7> exists.
# Prints the full commit SHA on stdout; fails if the image is not published yet.
check_osac_ui_image() {
    local repo="${1:-osac-ui}"
    local sha tag token safe_repo safe_sha attempt

    if [[ ! "${repo}" =~ ^[a-zA-Z0-9._-]+$ ]]; then
        safe_repo=$(_gha_sanitize_for_message "${repo}")
        echo "::error::Invalid osac-ui repo name '${safe_repo}' — must match [a-zA-Z0-9._-]+" >&2
        return 1
    fi

    for attempt in 1 2 3; do
        if sha=$(git -c http.lowSpeedLimit=1000 -c http.lowSpeedTime=10 \
            ls-remote "https://github.com/osac-project/${repo}.git" refs/heads/main | cut -f1); then
            [[ -n "${sha}" ]] && break
        fi
        if (( attempt < 3 )); then
            echo "  check_osac_ui_image: git ls-remote attempt ${attempt}/3 failed, retrying in 5s..." >&2
            sleep 5
        fi
    done

    if [[ -z "${sha}" || ! "${sha}" =~ ^[0-9a-f]{40}$ ]]; then
        safe_sha=$(_gha_sanitize_for_message "${sha}")
        safe_repo=$(_gha_sanitize_for_message "${repo}")
        echo "::error::Could not resolve ${safe_repo} main HEAD SHA (got: '${safe_sha}')" >&2
        return 1
    fi

    tag="sha-${sha:0:7}"
    safe_repo=$(_gha_sanitize_for_message "${repo}")
    if ! token=$(http_json "Could not obtain GHCR token to verify ${safe_repo}:${tag}" 3 5 '.token' \
        "https://ghcr.io/token?scope=repository:osac-project/${repo}:pull"); then
        echo "::error::Could not obtain GHCR token to verify ${safe_repo}:${tag}" >&2
        return 1
    fi
    if [[ -z "${token}" || "${token}" == "null" ]]; then
        echo "::error::GHCR token is empty or null for ${safe_repo}:${tag}" >&2
        return 1
    fi

    if ! http_retry "${safe_repo} main HEAD ${sha:0:7} has no published image (tag ${tag})" 3 5 \
        -s -o /dev/null \
        -H "Authorization: Bearer ${token}" \
        -H "Accept: application/vnd.oci.image.index.v1+json, application/vnd.oci.image.manifest.v1+json, application/vnd.docker.distribution.manifest.list.v2+json, application/vnd.docker.distribution.manifest.v2+json" \
        "https://ghcr.io/v2/osac-project/${repo}/manifests/${tag}"; then
        echo "::error::${safe_repo} main HEAD ${sha:0:7} has no published image (tag ${tag})" >&2
        return 1
    fi

    echo "${sha}"
}

# Usage: retag_component_image <image_repo> <source_short_sha> <target_version>
# Alias-tags the already-built <image_repo>:sha-<source_short_sha> image to
# <image_repo>:<target_version> via a server-side skopeo copy (no rebuild).
# <image_repo>:sha-<source_short_sha> is expected to already exist -- every
# mono-repo component's image-build workflow triggers unconditionally on
# push to main with no path filter, so a sha-<short> tag for main's current
# HEAD is reliably present in GHCR by the time nightly runs. If it isn't
# (e.g. a very recent merge whose build is still in flight), skopeo copy
# itself fails with a clear "manifest unknown" error under set -euo
# pipefail -- that failure IS the existence check; no separate pre-flight
# HEAD request is needed. Fails loudly, no silent fallback, no retry.
retag_component_image() {
    local image_repo="$1" source_short_sha="$2" target_version="$3"
    local source_tag="sha-${source_short_sha}"
    local safe_repo safe_source safe_target

    if [[ ! "${image_repo}" =~ ^[a-zA-Z0-9._/-]+$ ]]; then
        safe_repo=$(_gha_sanitize_for_message "${image_repo}")
        echo "::error::Invalid image repo '${safe_repo}'" >&2
        return 1
    fi
    if [[ ! "${source_short_sha}" =~ ^[0-9a-f]{7,40}$ ]]; then
        safe_source=$(_gha_sanitize_for_message "${source_short_sha}")
        echo "::error::Invalid source SHA '${safe_source}' for ${image_repo}" >&2
        return 1
    fi
    if [[ ! "${target_version}" =~ ^[a-zA-Z0-9._-]+$ ]]; then
        safe_target=$(_gha_sanitize_for_message "${target_version}")
        echo "::error::Invalid target version '${safe_target}' for ${image_repo}" >&2
        return 1
    fi

    echo "Retagging ${image_repo}:${source_tag} -> ${image_repo}:${target_version}..."
    if ! skopeo copy --all \
        "docker://${image_repo}:${source_tag}" \
        "docker://${image_repo}:${target_version}"; then
        safe_repo=$(_gha_sanitize_for_message "${image_repo}")
        echo "::error::Could not retag ${safe_repo}:${source_tag} -> ${target_version} — source image not found in GHCR yet (its build may still be in flight); failing nightly run rather than silently skipping or falling back to an older commit" >&2
        return 1
    fi
    skopeo inspect "docker://${image_repo}:${target_version}" > /dev/null
}

# Usage: stamp_component_image_refs <component> <umbrella_values> <tag_value>
# Stamps every values.yaml field (the component's own sub-chart plus the
# umbrella's corresponding field) that references <component>'s image to
# <tag_value>. Called twice per nightly run with two different tag_values:
# once in `prepare` with the provisional sha-<short> tag (before the image
# is even built, so E2E has something consistent to install), and once more
# in `publish` with the final resolved nightly version (after every test box
# passes and the image has been promoted via retag_component_image) --
# publish's call re-stamps the same fields in place before packaging, so the
# shipped chart's image tag literally equals the chart version. Components
# with no image of their own (operator-crds, bmf-crds, csi-backends) are not
# handled here -- there's nothing to stamp.
stamp_component_image_refs() {
    local component="$1" umbrella_values="$2" tag_value="$3"

    case "${component}" in
        osac-operator)
            TAG_VALUE="${tag_value}" yq -i '.image.tag = strenv(TAG_VALUE)' "osac-operator/charts/operator/values.yaml"
            stamp_umbrella_nested_field "${umbrella_values}" operator image tag "${tag_value}"
            ;;
        bare-metal-fulfillment-operator)
            TAG_VALUE="${tag_value}" yq -i '.image.tag = strenv(TAG_VALUE)' "bare-metal-fulfillment-operator/charts/operator/values.yaml"
            stamp_umbrella_nested_field "${umbrella_values}" bmf image tag "${tag_value}"
            ;;
        fulfillment-service)
            IMAGE_REF="ghcr.io/osac-project/fulfillment-service:${tag_value}" \
                yq -i '.images.service = strenv(IMAGE_REF)' "fulfillment-service/charts/service/values.yaml"
            stamp_umbrella_nested_field "${umbrella_values}" service images service "ghcr.io/osac-project/fulfillment-service:${tag_value}"
            ;;
        osac-aap)
            IMAGE_REF="ghcr.io/osac-project/osac-aap:${tag_value}" \
                yq -i '.bootstrap.image = strenv(IMAGE_REF)' "osac-aap/charts/aap/values.yaml"
            stamp_umbrella_nested_field "${umbrella_values}" aap bootstrap image "ghcr.io/osac-project/osac-aap:${tag_value}"
            ;;
        osac-metering)
            TAG_VALUE="${tag_value}" yq -i '.image.tag = strenv(TAG_VALUE)' "osac-metering/charts/osac-metering/values.yaml"
            stamp_umbrella_nested_field "${umbrella_values}" metering image tag "${tag_value}"
            # m360Adapter has no umbrella-level override field -- stamp only
            # the subchart's own values.yaml. Stamped unconditionally even
            # though disabled by default: cheap, and avoids a stale
            # provisional/placeholder tag surfacing the moment someone flips
            # m360Adapter.enabled=true on a nightly install. (echoAdapter's
            # image block is commented out by default in this chart -- only
            # the vmaas-ci CI overlay enables and overrides it, stamped
            # separately.)
            TAG_VALUE="${tag_value}" yq -i '.m360Adapter.image.tag = strenv(TAG_VALUE)' "osac-metering/charts/osac-metering/values.yaml"
            ;;
        osac-csi-driver)
            TAG_VALUE="${tag_value}" yq -i '.image.tag = strenv(TAG_VALUE)' "osac-csi-driver/charts/csi-driver/values.yaml"
            stamp_umbrella_nested_field "${umbrella_values}" csiDriver image tag "${tag_value}"
            ;;
        *)
            echo "::error::stamp_component_image_refs: unknown component '${component}'" >&2
            return 1
            ;;
    esac
}

# Strip characters that break or inject GitHub Actions workflow commands.
_gha_sanitize_for_message() {
    local value="$1"
    value="${value//$'\n'/}"
    value="${value//$'\r'/}"
    value="${value//$'\x1b'/}"
    value="${value//::/ }"
    value="${value//%0A/}"
    value="${value//%0D/}"
    value="${value//%0a/}"
    value="${value//%0d/}"
    printf '%s' "${value}"
}

# Usage: read_validated_chart_name <chart_dir>
# Print chart name from Chart.yaml; return 1 with sanitized ::error if invalid.
read_validated_chart_name() {
    local chart_dir="$1"
    local chart_name safe_name safe_dir

    if [[ ! -f "${chart_dir}/Chart.yaml" ]]; then
        safe_dir=$(_gha_sanitize_for_message "${chart_dir}")
        echo "::error::Chart manifest '${safe_dir}/Chart.yaml' not found!" >&2
        return 1
    fi

    chart_name=$(yq -r '.name' "${chart_dir}/Chart.yaml")
    if [[ -z "${chart_name}" || "${chart_name}" == "null" ]]; then
        safe_dir=$(_gha_sanitize_for_message "${chart_dir}")
        echo "::error::Chart name missing in ${safe_dir}/Chart.yaml" >&2
        return 1
    fi
    if [[ ! "${chart_name}" =~ ^[a-zA-Z0-9._-]+$ ]]; then
        safe_name=$(_gha_sanitize_for_message "${chart_name}")
        safe_dir=$(_gha_sanitize_for_message "${chart_dir}")
        echo "::error title=${safe_name}::Invalid chart name in ${safe_dir}/Chart.yaml (name must match [a-zA-Z0-9._-]+)" >&2
        return 1
    fi
    echo "${chart_name}"
}

# Usage: compute_nightly_chart_version <base_tag> <nightly_suffix>
compute_nightly_chart_version() {
    local base_tag="$1" nightly_suffix="$2"
    local base_version="${base_tag#v}"
    printf '%s-%s' "${base_version}" "${nightly_suffix}"
}

# Usage: stamp_umbrella_nested_field <values_yaml> <top_key> <nested_key> <leaf_key> <new_value> [optional]
# Awk-based rewrite of a single "<top_key>:\n  <nested_key>:\n    <leaf_key>: <value>"
# scalar field, preserving every other line byte-for-byte.
#
# Uses awk instead of yq -i because yq reformats the entire YAML file on
# write — removing blank lines and normalizing inline comment spacing from
# 2 spaces to 1 space before '#'. This causes ct lint's yamllint (which
# requires 2-space comment padding via ~/.ct/lintconf.yaml) to fail during
# the nightly publish job. awk preserves all formatting outside the target
# line.
#
# If [optional] is passed as a truthy 6th arg, a field not found in this
# file is a silent no-op (return 0) instead of a hard error — needed for CI
# overlay files, where not every environment overrides every component's
# image. Without it (the umbrella chart's own values.yaml, where every
# field is a mandatory always-present placeholder), a miss is a hard
# ::error::/return 1, since that would indicate real corruption.
stamp_umbrella_nested_field() {
    local values_yaml="$1" top_key="$2" nested_key="$3" leaf_key="$4" new_value="$5"
    local optional="${6:-false}"
    local safe_values_yaml tmp

    if [[ ! -f "${values_yaml}" ]]; then
        safe_values_yaml=$(_gha_sanitize_for_message "${values_yaml}")
        echo "::warning title=Missing values file::Values file not found: ${safe_values_yaml} — skipping ${top_key}.${nested_key}.${leaf_key} stamp" >&2
        return 0
    fi

    tmp="$(mktemp)"
    if ! awk -v top="${top_key}:" -v nested_pat="^[[:space:]]+${nested_key}:" \
            -v leaf="${leaf_key}" -v val="${new_value}" '
        BEGIN { in_top=0; in_nested=0; stamped=0; nested_indent="" }
        $0 == top { in_top=1; in_nested=0; nested_indent=""; print; next }
        /^[^ #\t]/ && $0 != top { in_top=0; in_nested=0; nested_indent="" }
        in_top && $0 ~ nested_pat {
            match($0, /^[[:space:]]+/)
            nested_indent = substr($0, RSTART, RLENGTH) "  "
            in_nested=1
            print
            next
        }
        in_nested && nested_indent != "" && match($0, "^" nested_indent leaf ":[[:space:]]") {
            print nested_indent leaf ": " val
            in_nested=0
            stamped=1
            next
        }
        { print }
        END { exit(stamped ? 0 : 1) }
    ' "${values_yaml}" > "${tmp}"; then
        rm -f "${tmp}"
        if [[ "${optional}" == true ]]; then
            return 0
        fi
        safe_values_yaml=$(_gha_sanitize_for_message "${values_yaml}")
        echo "::error::${top_key}.${nested_key}.${leaf_key} not found in ${safe_values_yaml}" >&2
        return 1
    fi
    chmod --reference="${values_yaml}" "${tmp}"
    mv "${tmp}" "${values_yaml}"
    if ! grep -qF "${new_value}" "${values_yaml}"; then
        safe_values_yaml=$(_gha_sanitize_for_message "${values_yaml}")
        echo "::error::Failed to stamp ${top_key}.${nested_key}.${leaf_key} in ${safe_values_yaml}" >&2
        return 1
    fi
}

# Usage: stamp_ci_overlay_if_present <values_yaml> <yq_path> <new_value>
# Stamps an arbitrary-depth field (e.g. .metering.echoAdapter.image.tag) in a
# CI overlay file (osac-installer/values/*/instance.yaml), only if that path
# already resolves to a non-null value in the file -- `yq -e <path> <file>`
# exits non-zero when the path is absent/null, which we use purely as an
# existence check (its own stdout/stderr is discarded). This avoids yq -i's
# default auto-vivification behavior, which would otherwise silently create
# a whole new key structure (e.g. add a `csiDriver:` block) in overlay files
# that don't already configure that component. Unlike
# stamp_umbrella_nested_field, this uses yq -i directly (not awk): these CI
# overlay files are not yamllint/ct-lint-checked anywhere in this pipeline,
# and changes here only ever live on the ephemeral nightly/* temp branch
# (never merged, deleted by the cleanup job), so yq's whole-file comment
# reformatting has no consequence.
stamp_ci_overlay_if_present() {
    local values_yaml="$1" yq_path="$2" new_value="$3"
    if yq -e "${yq_path}" "${values_yaml}" > /dev/null 2>&1; then
        VALUE="${new_value}" yq -i "${yq_path} = strenv(VALUE)" "${values_yaml}"
    fi
}

# Usage: _chart_slack_rank <chart_name>
_chart_slack_rank() {
    local chart_name="$1"
    local i
    for i in "${!NIGHTLY_CHART_SLACK_ORDER[@]}"; do
        if [[ "${NIGHTLY_CHART_SLACK_ORDER[$i]}" == "${chart_name}" ]]; then
            echo "${i}"
            return 0
        fi
    done
    echo 999
}

# Usage: _sort_chart_manifest <manifest_file>
_sort_chart_manifest() {
    local manifest_file="$1"
    local -a rows=()
    local chart_name version rank _short_sha _full_sha safe_path

    if [[ ! -f "${manifest_file}" ]]; then
        safe_path=$(_gha_sanitize_for_message "${manifest_file}")
        echo "::warning title=_sort_chart_manifest::Manifest file not found: ${safe_path}" >&2
        return 0
    fi

    while read -r chart_name version _short_sha _full_sha; do
        [[ -z "${chart_name}" ]] && continue
        rank=$(_chart_slack_rank "${chart_name}")
        rows+=("${rank} ${chart_name} ${version}")
    done < "${manifest_file}"

    if ((${#rows[@]} == 0)); then
        return 0
    fi

    printf '%s\n' "${rows[@]}" | sort -n -k1,1 | while read -r _rank chart version; do
        printf '%s %s\n' "${chart}" "${version}"
    done
}

# Usage: _slack_display_name <chart_name>
_slack_display_name() {
    local chart_name="$1"
    if [[ "${chart_name}" == "osac" ]]; then
        echo "osac (umbrella)"
    else
        echo "${chart_name}"
    fi
}

# Usage: _slack_table_hline <width>
_slack_table_hline() {
    local width="$1"
    printf '─%.0s' $(seq 1 "${width}")
}

# Usage: _slack_name_cell <width> <name>
_slack_name_cell() {
    local width="$1" name="$2"
    printf '%-*s' "${width}" "${name}"
}

# Usage: _slack_version_cell_plain <version_w> <ver>
_slack_version_cell_plain() {
    local version_w="$1" ver="$2"
    local pad=$(( version_w - ${#ver} ))
    (( pad < 0 )) && pad=0
    printf '%s%*s' "${ver}" "${pad}" ""
}

# Usage: _format_slack_linked_version <version> <url>
_format_slack_linked_version() {
    local version="$1" url="$2"
    if [[ -n "${url}" ]]; then
        printf '<%s|%s>' "${url}" "${version}"
    else
        printf '%s' "${version}"
    fi
}

# Usage: _slack_version_cell_linked <version_w> <ver> <url>
_slack_version_cell_linked() {
    local version_w="$1" ver="$2" url="$3"
    local linked pad
    linked=$(_format_slack_linked_version "${ver}" "${url}")
    pad=$(( version_w - ${#ver} ))
    (( pad < 0 )) && pad=0
    printf '%s%*s' "${linked}" "${pad}" ""
}

# Usage: _build_slack_charts_table <manifest_file> [linked] [repo_owner]
# Reads GH_TOKEN from environment when linked=true.
_build_slack_charts_table() {
    local manifest_file="$1"
    local linked="${2:-false}"
    local repo_owner="${3:-}"
    local -a names=() versions=() urls=()
    local chart_name version display_name url
    local name_w=5 version_w=7
    local name ver i table

    if [[ "${linked}" == true ]]; then
        while read -r chart_name version _short_sha _full_sha; do
            [[ -z "${chart_name}" ]] && continue
            display_name=$(_slack_display_name "${chart_name}")
            url=$(chart_version_url "${chart_name}" "${version}" "${repo_owner}")
            names+=("${display_name}")
            versions+=("${version}")
            urls+=("${url}")
        done < <(_sort_chart_manifest "${manifest_file}")
    else
        while read -r chart_name version _short_sha _full_sha; do
            [[ -z "${chart_name}" ]] && continue
            display_name=$(_slack_display_name "${chart_name}")
            names+=("${display_name}")
            versions+=("${version}")
        done < <(_sort_chart_manifest "${manifest_file}")
    fi

    for name in "${names[@]}"; do
        (( ${#name} > name_w )) && name_w=${#name}
    done
    for ver in "${versions[@]}"; do
        (( ${#ver} > version_w )) && version_w=${#ver}
    done

    local name_border=$(( name_w + 2 )) version_border=$(( version_w + 2 ))

    table="┌$(_slack_table_hline "${name_border}")┬$(_slack_table_hline "${version_border}")┐"
    if [[ "${linked}" == true ]]; then
        table="${table}"$'\n'"│ $(_slack_name_cell "${name_w}" "Chart") │ $(_slack_version_cell_linked "${version_w}" "Version" "") │"
        table="${table}"$'\n'"├$(_slack_table_hline "${name_border}")┼$(_slack_table_hline "${version_border}")┤"
        for i in "${!names[@]}"; do
            table="${table}"$'\n'"│ $(_slack_name_cell "${name_w}" "${names[$i]}") │ $(_slack_version_cell_linked "${version_w}" "${versions[$i]}" "${urls[$i]}") │"
        done
    else
        table="${table}"$'\n'"│ $(_slack_name_cell "${name_w}" "Chart") │ $(_slack_version_cell_plain "${version_w}" "Version") │"
        table="${table}"$'\n'"├$(_slack_table_hline "${name_border}")┼$(_slack_table_hline "${version_border}")┤"
        for i in "${!names[@]}"; do
            table="${table}"$'\n'"│ $(_slack_name_cell "${name_w}" "${names[$i]}") │ $(_slack_version_cell_plain "${version_w}" "${versions[$i]}") │"
        done
    fi
    table="${table}"$'\n'"└$(_slack_table_hline "${name_border}")┴$(_slack_table_hline "${version_border}")┘"
    printf '%s' "${table}"
}

# Usage: rewrite_umbrella_osac_ui_dependency <chart_yaml> <ui_version> <oci_repo>
# yamllint is not performed on Chart.yaml. Hence, use of yq is safe here.
rewrite_umbrella_osac_ui_dependency() {
    local chart_yaml="$1" ui_version="$2" oci_repo="$3"
    local safe_chart_yaml
    if [[ ! -f "${chart_yaml}" ]]; then
        safe_chart_yaml=$(_gha_sanitize_for_message "${chart_yaml}")
        echo "::error::Chart manifest not found: ${safe_chart_yaml}" >&2
        return 1
    fi
    UI_VERSION="${ui_version}" yq -i \
        '(.dependencies[] | select(.name == "osac-ui")).version = strenv(UI_VERSION)' \
        "${chart_yaml}"
    UI_REPO="${oci_repo}" yq -i \
        '(.dependencies[] | select(.name == "osac-ui")).repository = strenv(UI_REPO)' \
        "${chart_yaml}"
}

# Usage: rewrite_umbrella_osac_ui_dependency_and_rebuild <chart_yaml> <ui_version> <oci_repo>
rewrite_umbrella_osac_ui_dependency_and_rebuild() {
    local chart_yaml="$1" ui_version="$2" oci_repo="$3"
    local chart_dir
    chart_dir=$(dirname "${chart_yaml}")

    rewrite_umbrella_osac_ui_dependency "${chart_yaml}" "${ui_version}" "${oci_repo}"
    rm -f "${chart_dir}/Chart.lock"
    helm dependency build "${chart_dir}/"
}

# Usage: stamp_osac_ui_chart <chart_dir> <sub_version> <image_ref>
# The subchart is not subject to yamllinting. Hence, safe to use yq here.
stamp_osac_ui_chart() {
    local chart_dir="$1" sub_version="$2" image_ref="$3"
    # osac-ui/charts/ui/templates/deployment.yaml reads .Values.images.ui
    SUB_VERSION="${sub_version}" yq -i '.version = strenv(SUB_VERSION)' "${chart_dir}/Chart.yaml"
    SUB_VERSION="${sub_version}" yq -i '.appVersion = strenv(SUB_VERSION)' "${chart_dir}/Chart.yaml"
    IMAGE_REF="${image_ref}" yq -i '.images.ui = strenv(IMAGE_REF)' "${chart_dir}/values.yaml"
}

# Usage: chart_version_url <chart_name> <version> <repo_owner>
# Reads GH_TOKEN from environment.
chart_version_url() {
    local chart_name="$1" version="$2" repo_owner="$3"
    local gh_token="${GH_TOKEN:-}"
    local encoded_version response url

    if [[ ! "${chart_name}" =~ ^[a-zA-Z0-9._-]+$ ]]; then
        echo "::warning::Skipping GHCR package lookup for chart with invalid name; Slack link omitted" >&2
        return 0
    fi

    if [[ ! "${repo_owner}" =~ ^[a-zA-Z0-9._-]+$ ]]; then
        echo "::warning::Skipping GHCR package lookup for invalid repo owner; Slack link omitted" >&2
        return 0
    fi

    if [[ ! "${version}" =~ ^[a-zA-Z0-9._+-]+$ ]]; then
        echo "::warning::Skipping GHCR package lookup for invalid version; Slack link omitted" >&2
        return 0
    fi

    encoded_version=$(jq -rn --arg v "${version}" '$v|@uri')

    if ! response=$(curl -sf --max-time 15 \
        -H "Authorization: Bearer ${gh_token}" \
        -H "Accept: application/vnd.github+json" \
        "https://api.github.com/orgs/${repo_owner}/packages/container/charts%2F${chart_name}/versions?per_page=100"); then
        echo "::warning::Could not look up GHCR package page for chart ${chart_name}@${version} (Packages API request failed); Slack link omitted" >&2
        return 0
    fi

    url=$(jq -r --arg v "${version}" '.[] | select(.metadata.container.tags | index($v)) | .html_url' <<< "${response}" | head -1) || {
        echo "::warning::Could not parse Packages API response for chart ${chart_name}@${version}; Slack link omitted" >&2
        return 0
    }

    if [[ -z "${url}" || "${url}" == "null" ]]; then
        echo "::warning::No GHCR package page found for chart ${chart_name}@${version} (not in first 100 package versions); Slack link omitted" >&2
        return 0
    fi

    echo "${url}?tag=${encoded_version}"
}

# Usage: _osac_ui_source_from_manifest <manifest_file>
_osac_ui_source_from_manifest() {
    local manifest_file="$1"
    local chart_name version short_sha full_sha

    while read -r chart_name version short_sha full_sha; do
        if [[ "${chart_name}" == "osac-ui" && -n "${short_sha}" ]]; then
            if [[ -n "${full_sha}" ]]; then
                printf '%s (%s)' "${short_sha}" "${full_sha}"
            else
                printf '%s' "${short_sha}"
            fi
            return 0
        fi
    done < "${manifest_file}"
}

# Usage: build_slack_charts_published_summary <manifest_file> <repo_owner>
# Reads GH_TOKEN from environment.
build_slack_charts_published_summary() {
    local manifest_file="$1" repo_owner="$2"
    local table ui_source

    table=$(_build_slack_charts_table "${manifest_file}" true "${repo_owner}")

    # Wrapped in a triple-backtick code fence to keep the box-drawing table
    # monospace-aligned -- unfenced, Slack renders it in a proportional font
    # and the columns don't line up (confirmed live: a real posted message
    # without the fence showed a jagged, misaligned table). The fence does
    # NOT break the <url|text> link markup used for the per-chart GHCR links
    # -- also confirmed live: an earlier fenced message still rendered the
    # linked version cell as a real, clickable, underlined link with a
    # working preview tooltip. Slack supports both at once; the previous
    # comment here assumed otherwise, which was the actual bug.
    printf '*Charts published:*\n```\n%s\n```' "${table}"

    ui_source=$(_osac_ui_source_from_manifest "${manifest_file}") || true
    if [[ -n "${ui_source}" ]]; then
        printf '\n\n*osac-ui source:* `%s` (image tag `sha-%s`)' "${ui_source}" "${ui_source%% *}"
    fi
}
