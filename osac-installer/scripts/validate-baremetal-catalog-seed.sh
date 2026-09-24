#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
CHART_DIR="${SCRIPT_DIR}/../charts/osac"
INSTANCE_VALUES="${SCRIPT_DIR}/../values/caas-ci/instance.yaml"
BMAAS_VALUES="${SCRIPT_DIR}/../values/bmaas-ci/instance.yaml"
TMP_DIR=$(mktemp -d)
SERVER_PID=""
FAKE_PORT=""
trap 'if [[ -n "${SERVER_PID}" ]]; then kill "${SERVER_PID}" 2>/dev/null || true; fi; rm -rf "${TMP_DIR}"' EXIT

python3 -c "import yaml" 2>/dev/null || {
    printf '%s\n' "ERROR: PyYAML is required to inspect rendered Helm manifests." >&2
    exit 1
}

assert_contains() {
    local haystack="$1" needle="$2" description="$3"
    if ! grep -qF -- "${needle}" <<<"${haystack}"; then
        printf 'FAIL: %s\n' "${description}" >&2
        exit 1
    fi
}

assert_not_contains() {
    local haystack="$1" needle="$2" description="$3"
    if grep -qF -- "${needle}" <<<"${haystack}"; then
        printf 'FAIL: %s\n' "${description}" >&2
        exit 1
    fi
}

render_profile() {
    local values_file="$1" output_file="$2"
    helm template osac "${CHART_DIR}" \
        --values "${values_file}" \
        --set service.externalHostname=fulfillment-api.example.com \
        --set service.internalHostname=fulfillment-internal-api.example.com \
        >"${output_file}"
}

extract_hook_script() {
    local render_file="$1" script_file="$2"
    python3 - "${render_file}" "${script_file}" <<'PY'
import sys
from pathlib import Path

import yaml

render_path, script_path = sys.argv[1:]
documents = yaml.safe_load_all(Path(render_path).read_text())
hook = next(
    document
    for document in documents
    if document
    and document.get("kind") == "Job"
    and document.get("metadata", {}).get("name") == "seed-baremetal-catalog-item"
)
script = hook["spec"]["template"]["spec"]["containers"][0]["command"][-1]
script = script.replace(
    'AUTH_TOKEN=$(< /var/run/secrets/kubernetes.io/serviceaccount/token)',
    'AUTH_TOKEN=test-token',
)
script_path = Path(script_path)
script_path.write_text(script)
PY
}

start_fake_api() {
    local mode="$1"
    local port
    port=$(python3 -c 'import socket; s=socket.socket(); s.bind(("127.0.0.1", 0)); print(s.getsockname()[1]); s.close()')
    python3 - "${port}" "${mode}" >"${TMP_DIR}/server-${mode}.log" 2>&1 <<'PY' &
import json
import sys
from http.server import BaseHTTPRequestHandler, HTTPServer

port = int(sys.argv[1])
mode = sys.argv[2]
calls = []


class Handler(BaseHTTPRequestHandler):
    def do_GET(self):
        if self.path == "/calls":
            self.respond(200, calls)
            return
        if self.path == "/api/private/v1/bare_metal_instance_types":
            calls.append({"method": "GET", "path": self.path})
            items = [] if mode == "empty" else [{"id": "bmit-example"}]
            self.respond(200, {"items": items})
            return
        self.respond(404, {})

    def do_POST(self):
        length = int(self.headers.get("Content-Length", "0"))
        body = self.rfile.read(length)
        calls.append({"method": "POST", "path": self.path, "body": json.loads(body)})
        self.respond(409, {"message": "already exists"})

    def respond(self, status, body):
        encoded = json.dumps(body).encode()
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(encoded)))
        self.end_headers()
        self.wfile.write(encoded)

    def log_message(self, *_):
        pass


server = HTTPServer(("127.0.0.1", port), Handler)
server.serve_forever()
PY
    SERVER_PID=$!
    for _ in {1..50}; do
        if curl -fsS "http://127.0.0.1:${port}/calls" >/dev/null 2>&1; then
            FAKE_PORT="${port}"
            return
        fi
        sleep 0.1
    done
    printf '%s\n' "fake API did not start" >&2
    exit 1
}

stop_fake_api() {
    kill "${SERVER_PID}" 2>/dev/null || true
    wait "${SERVER_PID}" 2>/dev/null || true
    SERVER_PID=""
}

run_hook_against_fake_api() {
    local script_file="$1" port="$2"
    sed -i \
        "s#API=\"https://fulfillment-internal-api:8001/api/private/v1\"#API=\"http://127.0.0.1:${port}/api/private/v1\"#" \
        "${script_file}"
    bash "${script_file}"
}

render_profile "${INSTANCE_VALUES}" "${TMP_DIR}/caas-render.yaml"
render_profile "${BMAAS_VALUES}" "${TMP_DIR}/bmaas-render.yaml"

CAAS_RENDER=$(cat "${TMP_DIR}/caas-render.yaml")
BMAAS_RENDER=$(cat "${TMP_DIR}/bmaas-render.yaml")
assert_contains "${CAAS_RENDER}" "name: seed-baremetal-catalog-item" "CaaS must render the catalog seed hook"
assert_not_contains "${BMAAS_RENDER}" "name: seed-baremetal-catalog-item" "standalone BMaaS must not render the CaaS catalog seed hook"
assert_contains "${BMAAS_RENDER}" "host-inventory" "standalone BMaaS inventory namespace must remain rendered"

extract_hook_script "${TMP_DIR}/caas-render.yaml" "${TMP_DIR}/hook.sh"
HOOK_SCRIPT=$(cat "${TMP_DIR}/hook.sh")
assert_contains "${HOOK_SCRIPT}" "/bare_metal_instance_types" "hook must list BareMetalInstanceTypes"
assert_not_contains "${HOOK_SCRIPT}" "/baremetal_instance_templates" "hook must not list arbitrary templates"
assert_contains "${HOOK_SCRIPT}" "osac.templates.bm_host_provisioning" "hook must use the canonical BMI template"
assert_contains "${HOOK_SCRIPT}" '"tenant": "system"' "CatalogItem must use the system tenant"
assert_contains "${HOOK_SCRIPT}" '"field_definitions": []' "CatalogItem fields must remain unlocked"
assert_contains "${HOOK_SCRIPT}" "osac.openshift.io/owner-reference" "CatalogItem owner metadata must be rendered"
assert_contains "${HOOK_SCRIPT}" "409)" "AlreadyExists must be handled as success"
bash -n "${TMP_DIR}/hook.sh"

start_fake_api empty
EMPTY_PORT="${FAKE_PORT}"
run_hook_against_fake_api "${TMP_DIR}/hook.sh" "${EMPTY_PORT}"
EMPTY_CALLS=$(curl -fsS "http://127.0.0.1:${EMPTY_PORT}/calls")
python3 - "${EMPTY_CALLS}" <<'PY'
import json
import sys

calls = json.loads(sys.argv[1])
assert [call["method"] for call in calls] == ["GET"]
assert calls[0]["path"] == "/api/private/v1/bare_metal_instance_types"
PY
stop_fake_api

extract_hook_script "${TMP_DIR}/caas-render.yaml" "${TMP_DIR}/hook.sh"
start_fake_api usable
USABLE_PORT="${FAKE_PORT}"
run_hook_against_fake_api "${TMP_DIR}/hook.sh" "${USABLE_PORT}"
USABLE_CALLS=$(curl -fsS "http://127.0.0.1:${USABLE_PORT}/calls")
python3 - "${USABLE_CALLS}" <<'PY'
import json
import sys

calls = json.loads(sys.argv[1])
assert [call["method"] for call in calls] == ["GET", "POST"]
assert calls[1]["path"] == "/api/private/v1/baremetal_instance_catalog_items"
body = calls[1]["body"]
assert body["metadata"]["tenant"] == "system"
assert body["metadata"]["annotations"]["osac.openshift.io/owner-reference"] == (
    "BareMetalInstanceTemplate/osac.templates.bm_host_provisioning"
)
assert body["template"] == {"id": "osac.templates.bm_host_provisioning", "shared": True}
assert body["field_definitions"] == []
assert body["published"] is True
PY
stop_fake_api

printf '%s\n' "Bare-metal catalog seed regression checks passed."
