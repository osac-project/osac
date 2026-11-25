#!/usr/bin/env bash
# Shared by mirror-ubi-bases.yaml and the integration prepull step so the
# GHCR name of a UBI ref is computed in exactly one place.
set -euo pipefail

registry=${UBI_SOURCE_REGISTRY:-registry.access.redhat.com}
mirror=${UBI_MIRROR:-ghcr.io/osac-project}
script_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
repos_file=${script_dir}/../ubi-mirror-repos.txt

# Print every registry.access.redhat.com image the given files build on: FROM
# lines and ARG defaults (osac-aap's EE_BASE_IMAGE is only an ARG default).
list_refs() {
  awk -v prefix="${registry}/" '
    function comment(line) { return line ~ /^[[:space:]]*#/ }
    function blank(line) { return line ~ /^[[:space:]]*$/ }
    {
      if (comment($0)) { next }
      # Dockerfiles may continue an instruction with a trailing backslash.
      while ($0 ~ /\\[[:space:]]*$/) {
        sub(/\\[[:space:]]*$/, "")
        skip = 1
        while (skip) {
          if ((getline more) <= 0) { break }
          skip = comment(more) || blank(more)
        }
        if (skip) { break }
        $0 = $0 more
      }
      if (toupper($1) != "FROM" && toupper($1) != "ARG") { next }
      for (i = 2; i <= NF; i++) {
        ref = $i
        sub(/^[^=]*=/, "", ref)
        gsub(/"/, "", ref)
        if (index(ref, prefix) == 1) { print ref; break }
      }
    }' "$@" | sort -u
}

# Keep only refs whose source repository appears in the include list, one
# repository per line relative to the registry (e.g. ubi10/go-toolset).
filter_repos() {
  if [ ! -f "${repos_file}" ]; then
    echo "ubi-mirror: missing include list: ${repos_file}" >&2
    return 1
  fi
  awk -v registry="${registry}/" '
    NR == FNR {
      if ($0 !~ /^[[:space:]]*(#|$)/) { include[$0] = 1 }
      next
    }
    {
      if (index($0, registry) != 1) { next }
      repo = substr($0, length(registry) + 1)
      sub(/:[^:/]*$/, "", repo)
      if (repo in include) { print }
    }' "${repos_file}" -
}

# registry.access.redhat.com/<repo>:<tag> -> ghcr.io/osac-project/<repo->:tag
mirror_ref() {
  local ref=$1 path repo tag
  case ${ref} in
    *@*)
      echo "ubi-mirror: digest references are not supported: ${ref}" >&2
      return 1
      ;;
  esac
  if [ "${ref#"${registry}/"}" = "${ref}" ]; then
    echo "ubi-mirror: not a ${registry} reference: ${ref}" >&2
    return 1
  fi
  path=${ref#"${registry}/"}
  repo=${path%:*}
  tag=${path##*:}
  if [ "${repo}" = "${path}" ] || [ -z "${tag}" ]; then
    echo "ubi-mirror: reference has no tag: ${ref}" >&2
    return 1
  fi
  printf '%s/%s:%s\n' "$mirror" "${repo//\//-}" "${tag}"
}

case ${1:-} in
  list)
    shift
    list_refs "$@" | filter_repos
    ;;
  mirror)
    mirror_ref "${2:?usage: $0 mirror <ref>}"
    ;;
  *)
    echo "usage: $0 list <Containerfile>... | mirror <ref>" >&2
    exit 2
    ;;
esac
