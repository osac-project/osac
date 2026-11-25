#!/usr/bin/env bash
# Shared by mirror-ubi-bases.yaml and the integration prepull step so the
# GHCR name of a UBI ref is computed in exactly one place.
set -euo pipefail

registry=${UBI_SOURCE_REGISTRY:-registry.access.redhat.com}
mirror=${UBI_MIRROR:-ghcr.io/osac-project}

# Print every registry.access.redhat.com image the given files build on: FROM
# lines and ARG defaults (osac-aap's EE_BASE_IMAGE is only an ARG default).
list_refs() {
  awk -v prefix="${registry}/" '
    toupper($1) == "FROM" || toupper($1) == "ARG" {
      for (i = 2; i <= NF; i++) {
        ref = $i
        sub(/^[^=]*=/, "", ref)
        gsub(/"/, "", ref)
        if (index(ref, prefix) == 1) { print ref; break }
      }
    }' "$@" | sort -u
}

# registry.access.redhat.com/<repo>:<tag> -> ghcr.io/osac-project/<repo->:tag
mirror_ref() {
  local path=${1#"${registry}/"} repo tag
  tag=${path##*:}
  repo=${path%:*}
  printf '%s/%s:%s\n' "$mirror" "${repo//\//-}" "${tag}"
}

case ${1:-} in
  list)
    shift
    list_refs "$@"
    ;;
  mirror)
    mirror_ref "${2:?usage: $0 mirror <ref>}"
    ;;
  *)
    echo "usage: $0 list <Containerfile>... | mirror <ref>" >&2
    exit 2
    ;;
esac
