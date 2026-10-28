#!/usr/bin/env bash
# Dry-run every USE_CARBONITE / OSAC_USE_CARBONITE permutation for make image.
set -euo pipefail

SCRIPT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
REPO_ROOT=$(cd "${SCRIPT_DIR}/../.." && pwd)
GREEN=$'\033[32m'
RED=$'\033[31m'
RESET=$'\033[0m'

fail() {
    printf '%sFAIL:%s %s\n' "$RED" "$RESET" "$*" >&2
    exit 1
}

run_case() {
    local cli_value=$1
    local environment_value=$2
    local expected=$3
    local output
    local status=0
    local selected
    local -a command=(env -u USE_CARBONITE -u OSAC_USE_CARBONITE)

    if [[ "$environment_value" != "unset" ]]; then
        command+=("OSAC_USE_CARBONITE=${environment_value}")
    fi
    command+=(make --no-print-directory --dry-run image)
    if [[ "$cli_value" != "unset" ]]; then
        command+=("USE_CARBONITE=${cli_value}")
    fi

    output=$("${command[@]}" 2>&1) || status=$?

    if [[ "$output" == *" -t osac-dev-carbonite -f"* ]]; then
        selected=carbonite
    elif [[ "$output" == *" -t osac-dev -f"* ]]; then
        selected=distrobox
    else
        selected=none
    fi

    if [[ "$expected" == "fail" ]]; then
        [[ "$status" -ne 0 ]] || fail "expected failure: cli=${cli_value}, env=${environment_value}"
        [[ "$output" == *"USE_CARBONITE must be true or false"* ]] || {
            printf '%s\n' "$output" >&2
            fail "missing invalid-value error: cli=${cli_value}, env=${environment_value}"
        }
    else
        [[ "$status" -eq 0 ]] || {
            printf '%s\n' "$output" >&2
            fail "make failed: cli=${cli_value}, env=${environment_value}"
        }
        [[ "$selected" == "$expected" ]] || {
            printf '%s\n' "$output" >&2
            fail "expected ${expected}, got ${selected}: cli=${cli_value}, env=${environment_value}"
        }
    fi

    printf '%sPASS:%s cli=%-7s env=%-7s => %s\n' "$GREEN" "$RESET" "$cli_value" "$environment_value" "$expected"
}

cd "$REPO_ROOT"

for cli_value in unset true false invalid; do
    for environment_value in unset true false invalid; do
        if [[ "$cli_value" == "invalid" ]]; then
            expected=fail
        elif [[ "$cli_value" == "true" ]]; then
            expected=carbonite
        elif [[ "$cli_value" == "false" ]]; then
            expected=distrobox
        elif [[ "$environment_value" == "invalid" ]]; then
            expected=fail
        elif [[ "$environment_value" == "true" ]]; then
            expected=carbonite
        else
            expected=distrobox
        fi

        run_case "$cli_value" "$environment_value" "$expected"
    done
done

printf '%sPASS:%s 16 selector permutations\n' "$GREEN" "$RESET"
