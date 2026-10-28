#!/usr/bin/env bash
# Render deployment-internal hostnames into a copy of the Carbonite policy.
#
# tools/carbonite/policy.yaml keeps reserved .example placeholders for the
# deployment-internal endpoints so no real hostname is committed. This host
# helper reads the real hostnames from the environment and writes a
# substituted copy for 'openshell sandbox create --policy'. The source file is
# never modified, and the copy must live outside the checkout because the
# sandbox mounts the checkout.
#
# Usage: render-policy.sh SOURCE DEST
#   CARBONITE_GITLAB_HOST    replaces gitlab.internal.example
#   CARBONITE_JIRA_HOST      replaces jira.internal.example
#   CARBONITE_PATTERNS_HOST  replaces patterns.internal.example
# An unset or empty variable leaves its placeholder (and the rule inert).
set -euo pipefail

if [ -n "${OPENSHELL_SANDBOX:-}" ]; then
    echo 'Run this helper on the host, not inside an OpenShell sandbox.' >&2
    exit 1
fi

if [ "$#" -ne 2 ]; then
    echo "usage: $0 SOURCE DEST" >&2
    exit 2
fi
source_policy=$1
dest=$2

script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)
checkout=$(cd -- "$script_dir/../.." && pwd -P)

if [ ! -f "$source_policy" ]; then
    echo "Carbonite policy not found: $source_policy" >&2
    exit 1
fi

dest_dir=$(realpath -m -- "$(dirname -- "$dest")")
case "$dest_dir" in
    "$checkout"|"$checkout"/*)
        echo "Render the policy outside the checkout: $dest" >&2
        exit 1 ;;
esac

hostname_re='^[A-Za-z0-9]([A-Za-z0-9-]*[A-Za-z0-9])?(\.[A-Za-z0-9]([A-Za-z0-9-]*[A-Za-z0-9])?)*$'

# The trailing sentinel keeps the final newline through command substitution.
content=$(cat -- "$source_policy"; printf x)
content=${content%x}

substitute() {
    local variable=$1 placeholder=$2 value=${!1:-}
    if [ -z "$value" ]; then
        return 0
    fi
    if [[ ! "$value" =~ $hostname_re ]]; then
        echo "$variable must be a plain hostname, got: $value" >&2
        exit 1
    fi
    if [[ "$content" != *"$placeholder"* ]]; then
        echo "Warning: $variable is set but $placeholder is not in $source_policy" >&2
        return 0
    fi
    content=${content//"$placeholder"/$value}
}

substitute CARBONITE_GITLAB_HOST gitlab.internal.example
substitute CARBONITE_JIRA_HOST jira.internal.example
substitute CARBONITE_PATTERNS_HOST patterns.internal.example

umask 077
mkdir -p -- "$dest_dir"
tmp=$(mktemp "$dest_dir/.render-policy.XXXXXX")
trap 'rm -f -- "$tmp"' EXIT
printf '%s' "$content" > "$tmp"
mv -f -- "$tmp" "$dest"
trap - EXIT
