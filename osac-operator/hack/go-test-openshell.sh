#!/usr/bin/env bash
set -euo pipefail

# controller-runtime sends SIGTERM/SIGKILL to a process group when envtest
# stops. OpenShell rejects negative-PID signals but permits signaling an
# owned child by its positive PID. Patch only a temporary dependency copy
# for this test run; never modify the module cache or the repository's go.mod.
# The patched version is derived from go.mod so it cannot drift from the
# module graph; the patched-line check below fails loudly when a controller-
# runtime update changes the signal implementation.

version=$(GOWORK=off go list -m -f '{{.Version}}' sigs.k8s.io/controller-runtime)
GOWORK=off go mod download "sigs.k8s.io/controller-runtime@${version}"
source_dir=$(GOWORK=off go list -m -f '{{.Dir}}' sigs.k8s.io/controller-runtime)
patch_source="$source_dir/pkg/internal/testing/process/signal_unix.go"
if [[ ! -f "$patch_source" ]]; then
	printf 'controller-runtime signal source not found: %s\n' "$patch_source" >&2
	exit 1
fi

temp_dir=$(mktemp -d "${TMPDIR:-/tmp}/osac-envtest-XXXXXX")
mod_file=$(mktemp --tmpdir="$temp_dir" --suffix=.mod .envtest-openshell-XXXXXXXX)
trap 'rm -rf "$temp_dir"; rm -f "$mod_file" "${mod_file%.mod}.sum"' EXIT

cp -a "$source_dir" "$temp_dir/controller-runtime"
# The module cache is read-only; make the temporary copy removable on exit.
chmod -R u+w "$temp_dir/controller-runtime"
patched_source="$temp_dir/controller-runtime/pkg/internal/testing/process/signal_unix.go"
python3 - "$patched_source" <<'PY'
from pathlib import Path
import sys

source = Path(sys.argv[1])
original = source.read_text()
needle = "return syscall.Kill(-process.Pid, sig)"
if original.count(needle) != 1:
    raise SystemExit("controller-runtime signal implementation changed; review the OpenShell patch")
replacement = """err := syscall.Kill(-process.Pid, sig)
\tif err == syscall.EPERM {
\t\treturn syscall.Kill(process.Pid, sig)
\t}
\treturn err"""
source.write_text(original.replace(needle, replacement))
PY

cp go.mod "$mod_file"
cp go.sum "${mod_file%.mod}.sum"
GOWORK=off go mod edit -modfile="$mod_file" \
	"-replace=sigs.k8s.io/controller-runtime=$temp_dir/controller-runtime"
GOWORK=off go test -modfile="$mod_file" "$@"
