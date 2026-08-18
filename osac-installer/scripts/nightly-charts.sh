#!/usr/bin/env bash
set -euo pipefail

# Shared helpers for nightly chart publishing and Slack notifications.

NIGHTLY_CHART_SLACK_ORDER=(
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

append_chart_version() {
    local manifest_file=$1 chart_name=$2 version=$3
    printf '%s %s\n' "${chart_name}" "${version}" >> "${manifest_file}"
}

compute_nightly_chart_version() {
    local base_tag=$1 nightly_suffix=$2
    local base_version="${base_tag#v}"
    printf '%s-%s' "${base_version}" "${nightly_suffix}"
}

osac_ui_nightly_image_ref() {
    local image_repo=$1 short_sha=$2
    printf '%s:sha-%s' "${image_repo}" "${short_sha}"
}

_chart_slack_rank() {
    local chart_name=$1
    local i
    for i in "${!NIGHTLY_CHART_SLACK_ORDER[@]}"; do
        if [[ "${NIGHTLY_CHART_SLACK_ORDER[$i]}" == "${chart_name}" ]]; then
            echo "${i}"
            return 0
        fi
    done
    echo 999
}

_sort_chart_manifest() {
    local manifest_file=$1
    local -a rows=()
    local chart_name version rank
    while read -r chart_name version; do
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

_slack_display_name() {
    local chart_name=$1
    if [[ "${chart_name}" == "osac" ]]; then
        echo "osac (umbrella)"
    else
        echo "${chart_name}"
    fi
}

_slack_table_hline() {
    printf '─%.0s' $(seq 1 "$1")
}

_slack_name_cell() {
    printf '%-*s' "$1" "$2"
}

_slack_version_cell_plain() {
    local version_w=$1 ver=$2
    local pad=$(( version_w - ${#ver} ))
    (( pad < 0 )) && pad=0
    printf '%s%*s' "${ver}" "${pad}" ""
}

_format_slack_linked_version() {
    local version=$1 url=$2
    if [[ -n "${url}" ]]; then
        printf '<%s|%s>' "${url}" "${version}"
    else
        printf '%s' "${version}"
    fi
}

_slack_version_cell_linked() {
    local version_w=$1 ver=$2 url=$3
    local linked pad
    linked=$(_format_slack_linked_version "${ver}" "${url}")
    pad=$(( version_w - ${#ver} ))
    (( pad < 0 )) && pad=0
    printf '%s%*s' "${linked}" "${pad}" ""
}

_build_slack_charts_table() {
    local manifest_file=$1
    local linked=${2:-false}
    local repo_owner=${3:-}
    local gh_token=${4:-}
    local -a names=() versions=() urls=()
    local chart_name version display_name url
    local name_w=5 version_w=7
    local name ver i table

    if [[ "${linked}" == true ]]; then
        while read -r chart_name version; do
            [[ -z "${chart_name}" ]] && continue
            display_name=$(_slack_display_name "${chart_name}")
            url=$(chart_version_url "${chart_name}" "${version}" "${repo_owner}" "${gh_token}")
            names+=("${display_name}")
            versions+=("${version}")
            urls+=("${url}")
        done < <(_sort_chart_manifest "${manifest_file}")
    else
        while read -r chart_name version; do
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

build_slack_charts_table() {
    _build_slack_charts_table "$1" false
}

rewrite_umbrella_osac_ui_dependency() {
    local chart_yaml=$1 ui_version=$2 oci_repo=$3
    UI_VERSION="${ui_version}" yq -i \
        '(.dependencies[] | select(.name == "osac-ui")).version = strenv(UI_VERSION)' \
        "${chart_yaml}"
    UI_REPO="${oci_repo}" yq -i \
        '(.dependencies[] | select(.name == "osac-ui")).repository = strenv(UI_REPO)' \
        "${chart_yaml}"
}

stamp_osac_ui_chart() {
    local chart_dir=$1 sub_version=$2 image_ref=$3
    # osac-ui/charts/ui/templates/deployment.yaml reads .Values.images.ui
    SUB_VERSION="${sub_version}" yq -i '.version = strenv(SUB_VERSION)' "${chart_dir}/Chart.yaml"
    SUB_VERSION="${sub_version}" yq -i '.appVersion = strenv(SUB_VERSION)' "${chart_dir}/Chart.yaml"
    IMAGE_REF="${image_ref}" yq -i '.images.ui = strenv(IMAGE_REF)' "${chart_dir}/values.yaml"
}

chart_version_url() {
    local chart_name=$1 version=$2 repo_owner=$3 gh_token=$4
    local encoded_version response url

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

build_slack_charts_published_summary() {
    local manifest_file=$1 repo_owner=$2 gh_token=$3
    local table

    table=$(_build_slack_charts_table "${manifest_file}" true "${repo_owner}" "${gh_token}")

    printf '*Charts published:*
```
%s
```' "${table}"
}
