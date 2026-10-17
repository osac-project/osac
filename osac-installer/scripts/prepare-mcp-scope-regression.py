#!/usr/bin/env python3
"""Remove the MCP API scope from a local Kind realm before installer integration tests."""

from __future__ import annotations

import base64
import json
import pathlib
import socket
import ssl
import subprocess
import sys
import time
import urllib.error
import urllib.request
from urllib.parse import quote, urlsplit

INSTALLER = pathlib.Path(__file__).resolve().parents[1]
RECONCILER_PATH = INSTALLER / "charts/osac-infra/files/hooks/reconcile_mcp_exchange_client.py"
sys.path.insert(0, str(RECONCILER_PATH.parent))
import reconcile_mcp_exchange_client as reconciler  # noqa: E402


def run(command: list[str]) -> str:
    result = subprocess.run(command, check=False, capture_output=True, text=True)
    if result.returncode:
        raise RuntimeError(f"command failed ({' '.join(command)}): {result.stderr.strip()}")
    return result.stdout.strip()


def credentials() -> tuple[str, str]:
    secret = json.loads(
        run(["kubectl", "-n", "keycloak", "get", "secret", "keycloak-admin-credentials", "-o", "json"])
    )
    data = secret.get("data", {})
    try:
        username = base64.b64decode(data["admin-username"], validate=True).decode()
        password = base64.b64decode(data["admin-password"], validate=True).decode()
    except (KeyError, ValueError, UnicodeDecodeError):
        raise RuntimeError("Keycloak admin credentials are missing from the local Kind test cluster") from None
    if not username or not password:
        raise RuntimeError("Keycloak admin credentials are empty in the local Kind test cluster")
    return username, password


def verify_local_kind_context(cluster_name: str) -> None:
    expected_context = f"kind-{cluster_name}"
    context = run(["kubectl", "config", "current-context"])
    if context != expected_context:
        raise RuntimeError(
            f"refusing to change Keycloak in context {context!r}; "
            f"expected local Kind context {expected_context!r}"
        )
    endpoint = run(["kubectl", "config", "view", "--minify", "-o", "jsonpath={.clusters[0].cluster.server}"])
    host = urlsplit(endpoint).hostname
    if host not in {"127.0.0.1", "localhost", "::1"}:
        raise RuntimeError(f"refusing to change Keycloak through non-local Kubernetes endpoint {endpoint!r}")


def port_forward() -> tuple[subprocess.Popen[bytes], str]:
    with socket.socket() as listener:
        listener.bind(("127.0.0.1", 0))
        local_port = listener.getsockname()[1]
    process = subprocess.Popen(
        ["kubectl", "-n", "keycloak", "port-forward", "svc/keycloak", f"{local_port}:443"],
        stdout=subprocess.DEVNULL,
        stderr=subprocess.DEVNULL,
    )
    base_url = f"https://127.0.0.1:{local_port}"
    context = ssl._create_unverified_context()
    for _ in range(30):
        if process.poll() is not None:
            process.wait()
            raise RuntimeError("could not port-forward the Keycloak service from the local Kind cluster")
        try:
            with urllib.request.urlopen(f"{base_url}/realms/master", context=context, timeout=1):
                return process, base_url
        except (OSError, urllib.error.URLError):
            time.sleep(1)
    process.terminate()
    process.wait(timeout=5)
    raise RuntimeError("timed out connecting to Keycloak through the local Kind port-forward")


def remove_scope(keycloak_url: str, username: str, password: str) -> None:
    token = reconciler.get_admin_token(keycloak_url, username, password)
    scopes = reconciler.admin_request(keycloak_url, token, "GET", "/admin/realms/osac/client-scopes")
    if not isinstance(scopes, list):
        raise RuntimeError("Keycloak did not return the osac realm's client scopes")
    scope = next((item for item in scopes if item.get("name") == "osac-api"), None)
    if scope is None or not scope.get("id"):
        raise RuntimeError("the local Kind realm already lacks osac-api; install-infra must be run before this test")

    mappers_path = f"/admin/realms/osac/client-scopes/{quote(scope['id'], safe='')}/protocol-mappers/models"
    mappers = reconciler.admin_request(keycloak_url, token, "GET", mappers_path)
    audience_mapper = next(
        (item for item in mappers if item.get("name") == "osac-api-audience"),
        None,
    ) if isinstance(mappers, list) else None
    if not audience_mapper or audience_mapper.get("config", {}).get("included.custom.audience") != "osac-api":
        raise RuntimeError("the local Kind realm does not have the expected osac-api audience mapper before the test")

    reconciler.admin_request(
        keycloak_url,
        token,
        "DELETE",
        f"/admin/realms/osac/client-scopes/{quote(scope['id'], safe='')}",
    )
    remaining = reconciler.admin_request(keycloak_url, token, "GET", "/admin/realms/osac/client-scopes")
    if any(item.get("name") == "osac-api" for item in remaining):
        raise RuntimeError("Keycloak did not remove osac-api; refusing to run a false-positive regression test")


def main() -> int:
    if len(sys.argv) != 2:
        print("usage: prepare-mcp-scope-regression.py <kind-cluster-name>", file=sys.stderr)
        return 2
    process: subprocess.Popen[bytes] | None = None
    try:
        verify_local_kind_context(sys.argv[1])
        username, password = credentials()
        process, keycloak_url = port_forward()
        remove_scope(keycloak_url, username, password)
    except (RuntimeError, KeyError, OSError, json.JSONDecodeError) as error:
        print(f"ERROR: MCP scope regression setup failed: {error}", file=sys.stderr)
        return 1
    finally:
        if process is not None:
            process.terminate()
            process.wait(timeout=5)
    print("Removed osac-api from the local Kind realm; the next install-infra hook must recreate it")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
