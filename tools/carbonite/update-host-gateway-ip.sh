#!/usr/bin/env bash
# Repin the OpenShell gateway host address after a network change.
#
# The Podman driver resolves host.openshell.internal inside every sandbox to
# the address pinned as host_gateway_ip in the OpenShell gateway config.
# That pin follows the host's default route when it is set, so moving a
# laptop between networks (office and home, dock and wifi) leaves every
# existing sandbox dialing an address the host no longer owns: SSH to
# host.openshell.internal times out during banner exchange and the host sshd
# never sees the connection. See
# docs/guides/developer/carbonite-kind-integration.md for the background.
#
# This host-only helper repins host_gateway_ip to the current default-route
# source address (or to ADDRESS when given), validates the result as TOML,
# keeps a first-run backup beside the config, and restarts the gateway. The
# pin is injected into NEW supervisors only: restore the host-build
# authorization a failed entry removed with 'make enable-carb', then rebuild
# the sandbox with 'make rebuild-carb'.
set -euo pipefail

if [ -n "${OPENSHELL_SANDBOX:-}" ]; then
    echo 'Run this helper on the host, not inside an OpenShell sandbox.' >&2
    exit 1
fi

config="${OPENSHELL_GATEWAY_CONFIG:-$HOME/.config/openshell/gateway.toml}"

if [ "$#" -gt 1 ]; then
    echo "usage: $0 [ADDRESS]" >&2
    exit 2
fi

for command in ip python3 systemctl; do
    if ! command -v "$command" >/dev/null 2>&1; then
        echo "Required host command not found: $command" >&2
        exit 1
    fi
done

if [ "$#" -eq 1 ]; then
    address="$1"
else
    route=""
    if ! route="$(ip -4 route get 1.1.1.1 2>/dev/null)"; then
        route=""
    fi
    address="$(printf '%s\n' "$route" | awk '{ for (i = 1; i < NF; i++) if ($i == "src") { print $(i+1); exit } }')"
fi

if [ -z "$address" ]; then
    echo "Could not derive the host's current IPv4 address; is there a default route?" >&2
    exit 1
fi

status="$(python3 - "$config" "$address" <<'PY'
import ipaddress
import os
import re
import shutil
import stat
import sys
import tempfile
import tomllib
from pathlib import Path

path = Path(sys.argv[1])
address = ipaddress.IPv4Address(sys.argv[2])
assert address.is_private and not address.is_loopback and not address.is_link_local, (
    f"refusing to pin {address}: want a private non-loopback IPv4 address")
original = path.read_text()
lines = original.splitlines(keepends=True)
section = '[openshell.drivers.podman]'
starts = [i for i, line in enumerate(lines) if line.strip() == section]
assert len(starts) == 1, f"want exactly one {section} section in {path}"
start = starts[0]
end = next((i for i in range(start + 1, len(lines)) if lines[i].lstrip().startswith('[')), len(lines))
keys = [i for i in range(start + 1, end) if re.match(r'\s*host_gateway_ip\s*=', lines[i])]
assert len(keys) <= 1, f"want at most one host_gateway_ip key in {path}"
setting = f'host_gateway_ip = "{address}"\n'
if keys:
    if lines[keys[0]] == setting:
        print("unchanged")
        sys.exit(0)
    lines[keys[0]] = setting
else:
    lines.insert(start + 1, setting)
updated = ''.join(lines)
tomllib.loads(updated)
backup = path.with_name(path.name + '.before-host-gateway-ip')
if not backup.exists():
    shutil.copy2(path, backup)
fd, next_path = tempfile.mkstemp(prefix=path.name + '.', dir=path.parent)
try:
    os.fchmod(fd, stat.S_IMODE(path.stat().st_mode))
    with os.fdopen(fd, 'w') as out:
        out.write(updated)
        out.flush()
        os.fsync(out.fileno())
    os.replace(next_path, path)
finally:
    if os.path.exists(next_path):
        os.unlink(next_path)
print("updated")
PY
)"

case "$status" in
    updated) ;;
    unchanged)
        echo "host_gateway_ip is already pinned to $address; nothing to do."
        exit 0
        ;;
    *)
        echo "Unexpected result updating $config: $status" >&2
        exit 1
        ;;
esac

systemctl --user restart openshell-gateway
systemctl --user is-active openshell-gateway

echo "Pinned host_gateway_ip to $address in $config and restarted openshell-gateway."
echo 'The gateway pin is updated, but entry is not fixed yet. Required next steps:'
echo '  1. make enable-carb   # restores the host-build authorization a failed entry removed'
echo '  2. make rebuild-carb  # recreates the sandbox so its supervisor uses the new address'
