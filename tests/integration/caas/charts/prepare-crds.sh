#!/usr/bin/env bash
# Prepare or verify the pinned native API bundles.
set -euo pipefail
cd "$(dirname "$0")"
mode=${1:-}
case "$mode" in
  ""|--check) ;;
  *) echo "Usage: $0 [--check]" >&2; exit 2 ;;
esac

checksum=(sha256sum)
if ! command -v sha256sum >/dev/null; then
  checksum=(shasum -a 256)
fi
tmp=
trap 'if [[ -n "$tmp" ]]; then rm -f "$tmp"; fi' EXIT

# Ensure the manifest contains pins with unique destinations.
validate_manifest() {
  local duplicates
  if ! grep -qEv '^[[:space:]]*(#|$)' crds.txt; then
    echo 'Empty CRD manifest: crds.txt' >&2
    exit 1
  fi
  duplicates=$(awk '!/^#/ && NF { print $1 }' crds.txt | LC_ALL=C sort | uniq -d)
  if [[ -n "$duplicates" ]]; then
    printf 'Duplicate CRD destinations:\n%s\n' "$duplicates" >&2
    exit 1
  fi
}

# Verify installed or downloaded bytes, reporting their intended destination.
verify_crd() {
  local file=$1 digest=$2 destination=${3:-$1}
  if [[ ! -f "$file" ]]; then
    printf 'Missing CRD: %s\n' "$destination" >&2
    exit 1
  fi
  if ! printf '%s  %s\n' "$digest" "$file" | "${checksum[@]}" --check --status; then
    printf 'CRD checksum mismatch: %s\n' "$destination" >&2
    exit 1
  fi
}

# Reuse verified files and install downloads only after verifying their checksum.
prepare_crd() {
  local path=$1 digest=$2 url=$3
  if [[ -f "$path" ]] && printf '%s  %s\n' "$digest" "$path" | "${checksum[@]}" --check --status; then
    return
  fi
  printf 'Downloading %s\n' "$path"
  mkdir -p "$(dirname "$path")"
  tmp=$(mktemp "${path}.XXXXXX")
  if ! curl --fail --silent --show-error --location \
    --connect-timeout 10 --max-time 60 --retry 3 --retry-max-time 180 \
    "$url" --output "$tmp"; then
    printf 'CRD download failed: %s\n' "$path" >&2
    exit 1
  fi
  verify_crd "$tmp" "$digest" "$path"
  mv "$tmp" "$path"
  tmp=
}

# Reject chart CRDs without a corresponding manifest entry.
check_unlisted_crds() {
  local extra
  extra=$(comm -23 \
    <(find bootstrap/crds providers/crds -type f \( -iname '*.yaml' -o -iname '*.yml' -o -iname '*.json' \) | LC_ALL=C sort) \
    <(awk '!/^#/ && NF { print $1 }' crds.txt | LC_ALL=C sort))
  if [[ -n "$extra" ]]; then
    printf 'Unlisted CRDs:\n%s\n' "$extra" >&2
    exit 1
  fi
}

# Process each pin and report success after all chart destinations are checked.
main() {
  local path digest url extra count=0 action=Prepared
  if [[ "$mode" == --check ]]; then
    action=Verified
    echo 'Checking pinned CRDs...'
  else
    echo 'Preparing pinned CRDs...'
  fi
  validate_manifest
  while read -r path digest url extra || [[ -n "$path" ]]; do
    [[ -n "$path" && "$path" != \#* ]] || continue
    if [[ ! "$path" =~ ^(bootstrap|providers)/crds/[[:alnum:]_-]+\.yaml$ ||
          ! "$digest" =~ ^[0-9a-f]{64}$ || "$url" != https://* || -n "$extra" ]]; then
      printf 'Invalid CRD pin: %s\n' "$path" >&2
      exit 1
    fi
    if [[ "$mode" != --check ]]; then
      prepare_crd "$path" "$digest" "$url"
    fi
    verify_crd "$path" "$digest"
    printf '%s %s\n' "$action" "$path"
    count=$((count + 1))
  done < crds.txt
  check_unlisted_crds
  printf '%s %d pinned CRDs.\n' "$action" "$count"
}

main
