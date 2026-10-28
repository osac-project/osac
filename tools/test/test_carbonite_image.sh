#!/usr/bin/env bash
# Verify the actual image's tools and caches as its unprivileged runtime user.
set -euo pipefail

if [[ -n ${OPENSHELL_SANDBOX:-} ]]; then
    echo 'Run this image test on the host, not inside an OpenShell sandbox: it starts a container.' >&2
    exit 1
fi

container_cmd="${CONTAINER_CMD:-podman}"
image="${1:-osac-dev-carbonite:latest}"

"$container_cmd" run --rm --network none --security-opt no-new-privileges \
    --cap-drop ALL --workdir /sandbox --entrypoint /bin/bash "$image" -lc '
    set -eu
    test "$(id -u)" -ne 0
    export GOTOOLCHAIN=local
    module_cache="$(go env GOMODCACHE)"
    build_cache="$(go env GOCACHE)"
    mkdir -p "$module_cache/cache/download/golang.org/toolchain" "$build_cache"
    module_probe="$(mktemp -d "$module_cache/cache/download/golang.org/toolchain/cache-test.XXXXXX")"
    build_probe="$(mktemp -d "$build_cache/cache-test.XXXXXX")"
    touch "$module_probe/download" "$build_probe/compile"
    npm_cache="$(npm config get cache)"
    mkdir -p "$npm_cache/_cacache/tmp"
    npm_probe="$(mktemp -d "$npm_cache/_cacache/tmp/cache-test.XXXXXX")"
    touch "$npm_probe/download"
    uv_cache="$(uv cache dir)"
    mkdir -p "$uv_cache"
    uv_probe="$(mktemp -d "$uv_cache/cache-test.XXXXXX")"
    touch "$uv_probe/download"
    go version
    ! command -v kind >/dev/null
    test "$(pnpm --version)" = 9.15.9
    test "$(helm version --template "{{.Version}}")" = v3.22.0
    command -v claude >/dev/null
    claude --version | grep -q "Claude Code"
    command -v codex >/dev/null
    codex --version | grep -q "^codex-cli "
    command -v initdb >/dev/null
    command -v postgres >/dev/null
    python -c "import sys; assert sys.version_info[:2] == (3, 13), sys.version"
    python3 -c "import sys; assert sys.version_info[:2] == (3, 13), sys.version"
    python -c "import importlib.metadata as m; assert m.version(\"ansible\") == \"11.5.0\"; assert m.version(\"ansible-core\") == \"2.18.5\""
    ansible-galaxy --version >/dev/null
    test "$(pytest --version)" = "pytest 9.1.1"
    uv python find 3.13 --no-python-downloads >/dev/null
    printf "Go and npm caches are writable by %s (UID %s); pnpm 9.15.9, Helm v3.22.0, Python 3.13, Ansible 11.5.0, Claude Code, Codex, and PostgreSQL server tools are installed.\n" "$(id -un)" "$(id -u)"
    '
