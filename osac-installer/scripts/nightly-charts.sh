#!/usr/bin/env bash
set -euo pipefail

# Shared helpers for nightly chart publishing and Slack notifications.

_LIB_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
if ! declare -F http_retry >/dev/null 2>&1; then
    # shellcheck source=lib.sh
    source "${_LIB_DIR}/lib.sh"
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

# Umbrella and CI values files that override osac-ui subchart .Values.images.ui.
readonly NIGHTLY_UMBRELLA_UI_VALUES=(
    osac-installer/charts/osac/values.yaml
    osac-installer/values/development/values.yaml
    osac-installer/values/vmaas-ci/values.yaml
    osac-installer/values/bmaas-ci/values.yaml
    osac-installer/values/full-ci/values.yaml
)

# Usage: append_chart_version <manifest_file> <chart_name> <version> [short_sha] [full_sha]
append_chart_version() {
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
    local sha tag token

    sha=$(git -c http.lowSpeedLimit=1000 -c http.lowSpeedTime=10 \
        ls-remote "https://github.com/osac-project/${repo}.git" refs/heads/main | cut -f1)

    if [[ -z "${sha}" || ! "${sha}" =~ ^[0-9a-f]{40}$ ]]; then
        echo "::error::Could not resolve ${repo} main HEAD SHA (got: '${sha}')" >&2
        return 1
    fi

    tag="sha-${sha:0:7}"
    if ! token=$(http_json "Could not obtain GHCR token to verify ${repo}:${tag}" 3 5 '.token' \
        "https://ghcr.io/token?scope=repository:osac-project/${repo}:pull"); then
        echo "::error::Could not obtain GHCR token to verify ${repo}:${tag}" >&2
        return 1
    fi

    if ! http_retry "${repo} main HEAD ${sha:0:7} has no published image (tag ${tag})" 3 5 \
        -s -o /dev/null \
        -H "Authorization: Bearer ${token}" \
        -H "Accept: application/vnd.oci.image.index.v1+json, application/vnd.oci.image.manifest.v1+json, application/vnd.docker.distribution.manifest.list.v2+json, application/vnd.docker.distribution.manifest.v2+json" \
        "https://ghcr.io/v2/osac-project/${repo}/manifests/${tag}"; then
        echo "::error::${repo} main HEAD ${sha:0:7} has no published image (tag ${tag})" >&2
        return 1
    fi

    echo "${sha}"
}

# Usage: compute_nightly_chart_version <base_tag> <nightly_suffix>
compute_nightly_chart_version() {
    local base_tag="$1" nightly_suffix="$2"
    local base_version="${base_tag#v}"
    printf '%s-%s' "${base_version}" "${nightly_suffix}"
}

# Usage: osac_ui_nightly_image_ref <image_repo> <short_sha>
osac_ui_nightly_image_ref() {
    local image_repo="$1" short_sha="$2"
    printf '%s:sha-%s' "${image_repo}" "${short_sha}"
}

# Usage: stamp_umbrella_ui_image_ref <values_yaml> <image_ref>
stamp_umbrella_ui_image_ref() {
    local values_yaml="$1" image_ref="$2"
    if [[ ! -f "${values_yaml}" ]]; then
        echo "::warning title=Missing values file::Values file not found: ${values_yaml} — skipping ui.images.ui stamp" >&2
        return 0
    fi
    IMAGE_REF="${image_ref}" yq -i '.ui.images.ui = strenv(IMAGE_REF)' "${values_yaml}"
}

# Usage: stamp_umbrella_ui_values <image_ref>
stamp_umbrella_ui_values() {
    local image_ref="$1" values_yaml
    for values_yaml in "${NIGHTLY_UMBRELLA_UI_VALUES[@]}"; do
        stamp_umbrella_ui_image_ref "${values_yaml}" "${image_ref}"
    done
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
    local chart_name version rank _short_sha _full_sha
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

# Usage: _build_slack_charts_table <manifest_file> [linked] [repo_owner] [gh_token]
_build_slack_charts_table() {
    local manifest_file="$1"
    local linked="${2:-false}"
    local repo_owner="${3:-}"
    local gh_token="${4:-}"
    local -a names=() versions=() urls=()
    local chart_name version display_name url
    local name_w=5 version_w=7
    local name ver i table

    if [[ "${linked}" == true ]]; then
        while read -r chart_name version _short_sha _full_sha; do
            [[ -z "${chart_name}" ]] && continue
            display_name=$(_slack_display_name "${chart_name}")
            url=$(chart_version_url "${chart_name}" "${version}" "${repo_owner}" "${gh_token}")
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
rewrite_umbrella_osac_ui_dependency() {
    local chart_yaml="$1" ui_version="$2" oci_repo="$3"
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
stamp_osac_ui_chart() {
    local chart_dir="$1" sub_version="$2" image_ref="$3"
    # osac-ui/charts/ui/templates/deployment.yaml reads .Values.images.ui
    SUB_VERSION="${sub_version}" yq -i '.version = strenv(SUB_VERSION)' "${chart_dir}/Chart.yaml"
    SUB_VERSION="${sub_version}" yq -i '.appVersion = strenv(SUB_VERSION)' "${chart_dir}/Chart.yaml"
    IMAGE_REF="${image_ref}" yq -i '.images.ui = strenv(IMAGE_REF)' "${chart_dir}/values.yaml"
}

# Usage: chart_version_url <chart_name> <version> <repo_owner> <gh_token>
chart_version_url() {
    local chart_name="$1" version="$2" repo_owner="$3" gh_token="$4"
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

# Usage: build_slack_charts_published_summary <manifest_file> <repo_owner> <gh_token>
build_slack_charts_published_summary() {
    local manifest_file="$1" repo_owner="$2" gh_token="$3"
    local table ui_source

    table=$(_build_slack_charts_table "${manifest_file}" true "${repo_owner}" "${gh_token}")

    # Plain mrkdwn (no code fence): linked version cells use <url|text> markup.
    printf '*Charts published:*\n%s' "${table}"

    ui_source=$(_osac_ui_source_from_manifest "${manifest_file}") || true
    if [[ -n "${ui_source}" ]]; then
        printf '\n\n*osac-ui source:* `%s` (image tag `sha-%s`)' "${ui_source}" "${ui_source%% *}"
    fi
}
