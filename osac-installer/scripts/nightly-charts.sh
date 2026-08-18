#!/usr/bin/env bash

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

sort_chart_manifest() {
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

build_slack_charts_table() {
  local manifest_file=$1
  local -a names=() versions=()
  local chart_name version display_name

  while read -r chart_name version; do
    [[ -z "${chart_name}" ]] && continue
    display_name=$(_slack_display_name "${chart_name}")
    names+=("${display_name}")
    versions+=("${version}")
  done < <(sort_chart_manifest "${manifest_file}")

  local name_w=5 version_w=7
  local name version
  for name in "${names[@]}"; do
    (( ${#name} > name_w )) && name_w=${#name}
  done
  for version in "${versions[@]}"; do
    (( ${#version} > version_w )) && version_w=${#version}
  done

  hline() { printf '─%.0s' $(seq 1 "$1"); }
  name_cell() { printf '%-*s' "${name_w}" "$1"; }
  version_cell() {
    local ver=$1
    local pad=$(( version_w - ${#ver} ))
    (( pad < 0 )) && pad=0
    printf '%s%*s' "${ver}" "${pad}" ""
  }

  local table
  table="┌$(hline "${name_w}")┬$(hline "${version_w}")┐"
  table="${table}"$'\n'"│ $(name_cell "Chart") │ $(version_cell "Version") │"
  table="${table}"$'\n'"├$(hline "${name_w}")┼$(hline "${version_w}")┤"
  local i
  for i in "${!names[@]}"; do
    table="${table}"$'\n'"│ $(name_cell "${names[$i]}") │ $(version_cell "${versions[$i]}") │"
  done
  table="${table}"$'\n'"└$(hline "${name_w}")┴$(hline "${version_w}")┘"
  printf '%s' "${table}"
}

rewrite_umbrella_osac_ui_dependency() {
  local chart_yaml=$1 ui_version=$2 oci_repo=$3
  yq -i "(.dependencies[] | select(.name == \"osac-ui\")).version = \"${ui_version}\"" "${chart_yaml}"
  yq -i "(.dependencies[] | select(.name == \"osac-ui\")).repository = \"${oci_repo}\"" "${chart_yaml}"
}

stamp_osac_ui_chart() {
  local chart_dir=$1 sub_version=$2 image_ref=$3
  yq -i ".version = \"${sub_version}\"" "${chart_dir}/Chart.yaml"
  yq -i ".appVersion = \"${sub_version}\"" "${chart_dir}/Chart.yaml"
  yq -i ".images.ui = \"${image_ref}\"" "${chart_dir}/values.yaml"
}

chart_version_url() {
  local chart_name=$1 version=$2 repo_owner=$3 gh_token=$4
  local encoded_version url
  encoded_version=$(jq -rn --arg v "${version}" '$v|@uri')
  url=$(curl -sf --max-time 15 \
    -H "Authorization: Bearer ${gh_token}" \
    -H "Accept: application/vnd.github+json" \
    "https://api.github.com/orgs/${repo_owner}/packages/container/charts%2F${chart_name}/versions?per_page=100" \
  | jq -r --arg v "${version}" '.[] | select(.metadata.container.tags | index($v)) | .html_url' \
  | head -1) || true
  if [[ -n "${url}" ]]; then
    echo "${url}?tag=${encoded_version}"
  fi
}

format_slack_linked_version() {
  local version=$1 url=$2
  if [[ -n "${url}" ]]; then
    printf '<%s|%s>' "${url}" "${version}"
  else
    printf '%s' "${version}"
  fi
}

build_slack_charts_published_summary() {
  local manifest_file=$1 repo_owner=$2 gh_token=$3
  local -a names=() versions=() urls=()
  local chart_name version display_name url

  while read -r chart_name version; do
    [[ -z "${chart_name}" ]] && continue
    display_name=$(_slack_display_name "${chart_name}")
    url=$(chart_version_url "${chart_name}" "${version}" "${repo_owner}" "${gh_token}" || true)
    names+=("${display_name}")
    versions+=("${version}")
    urls+=("${url}")
  done < <(sort_chart_manifest "${manifest_file}")

  local name_w=5 version_w=7
  local name ver
  for name in "${names[@]}"; do
    (( ${#name} > name_w )) && name_w=${#name}
  done
  for ver in "${versions[@]}"; do
    (( ${#ver} > version_w )) && version_w=${#ver}
  done

  hline() { printf '─%.0s' $(seq 1 "$1"); }
  name_cell() { printf '%-*s' "${name_w}" "$1"; }
  version_cell() {
    local ver=$1 url=$2 linked
    linked=$(format_slack_linked_version "${ver}" "${url}")
    local pad=$(( version_w - ${#ver} ))
    (( pad < 0 )) && pad=0
    printf '%s%*s' "${linked}" "${pad}" ""
  }

  local table
  table="┌$(hline "${name_w}")┬$(hline "${version_w}")┐"
  table="${table}"$'
'"│ $(name_cell "Chart") │ $(version_cell "Version" "") │"
  table="${table}"$'
'"├$(hline "${name_w}")┼$(hline "${version_w}")┤"
  local i
  for i in "${!names[@]}"; do
    table="${table}"$'
'"│ $(name_cell "${names[$i]}") │ $(version_cell "${versions[$i]}" "${urls[$i]}") │"
  done
  table="${table}"$'
'"└$(hline "${name_w}")┴$(hline "${version_w}")┘"
  printf '*Charts published:*
```
%s
```' "${table}"
}
