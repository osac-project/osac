#!/usr/bin/env bash
# Explicit, one-time host setup. Deliberately separate from tools/bootstrap.sh.
set -euo pipefail

if [[ -n "${OPENSHELL_SANDBOX:-}" ]]; then
    echo 'Run this enabler on the host, not inside OpenShell.' >&2
    exit 1
fi

script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)
checkout=$(cd -- "$script_dir/../.." && pwd -P)
manager="$HOME/.local/libexec/osac-host-build-manager"
action=enable
gateway=openshell
workspace=default
sandbox=osac-carbonite
gateway_set=false
workspace_set=false
sandbox_set=false

while (($#)); do
    case "$1" in
        --gateway|--workspace|--sandbox|--checkout)
            option=$1
            if (($# < 2)); then echo "Missing value for $option" >&2; exit 2; fi
            case "$option" in
                --gateway) gateway=$2; gateway_set=true ;;
                --workspace) workspace=$2; workspace_set=true ;;
                --sandbox) sandbox=$2; sandbox_set=true ;;
                --checkout) checkout=$2 ;;
            esac
            shift 2 ;;
        --revoke|--upgrade)
            if [[ "$action" != enable ]]; then
                echo 'Choose only one of --revoke and --upgrade' >&2
                exit 2
            fi
            action=${1#--}
            shift ;;
        -h|--help)
            echo 'Usage: enable-host-build.sh [--gateway NAME] [--workspace NAME] [--sandbox NAME] [--checkout PATH] [--revoke|--upgrade]'
            exit 0 ;;
        *) echo "Unknown argument: $1" >&2; exit 2 ;;
    esac
done

if [[ -L "$manager" ]]; then
    echo "Refusing symlinked installed manager: $manager" >&2
    exit 1
fi
if [[ -e "$manager" ]]; then
    if [[ ! -f "$manager" || ! -x "$manager" ]]; then
        echo "Installed manager is not a regular executable file: $manager" >&2
        exit 1
    fi
    source_manager=$manager
else
    if [[ "$action" == revoke || "$action" == upgrade ]]; then
        echo 'Host build has not been enabled.' >&2
        exit 1
    fi
    source_manager="$script_dir/host_build_manager.py"
fi

if [[ "$action" == upgrade ]]; then
    # Deliberate exception to the installed-code rule: this explicit action
    # installs the newly reviewed checkout sources after revocation.
    source_manager="$script_dir/host_build_manager.py"
    arguments=(upgrade)
elif [[ "$action" == revoke ]]; then
    arguments=(revoke)
    if $gateway_set; then arguments+=(--gateway "$gateway"); fi
    if $workspace_set; then arguments+=(--workspace "$workspace"); fi
    if $sandbox_set; then arguments+=(--sandbox "$sandbox"); fi
else
    arguments=(enable --gateway "$gateway" --workspace "$workspace" --sandbox "$sandbox" --checkout "$checkout")
fi
exec /usr/bin/python3 -I -S "$source_manager" "${arguments[@]}"
