#!/usr/bin/env python3
"""Test the Keycloak admin-password upgrade contract and rotation script."""

from __future__ import annotations

import base64
import json
import os
import pathlib
import shutil
import subprocess
import tempfile
import threading
from http.server import BaseHTTPRequestHandler, HTTPServer
from typing import Any
from urllib.parse import urlparse

import yaml

INSTALLER = pathlib.Path(__file__).resolve().parents[1]
CHART = INSTALLER / "charts/osac-infra"
ROTATION_SCRIPT = CHART / "files/hooks/rotate-keycloak-admin.sh"
OLD_PASSWORD = "legacy-admin-password"
NEW_PASSWORD = "next-admin-password"


class FakeKubernetesHandler(BaseHTTPRequestHandler):
    """Serve discovery and the existing Secret needed by Helm lookup."""

    current_password = OLD_PASSWORD
    pending_rotation_password: str | None = None

    def log_message(self, _format: str, *_args: object) -> None:
        return

    def send_json(self, status: int, value: dict[str, Any]) -> None:
        body = json.dumps(value).encode()
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def do_GET(self) -> None:
        path = urlparse(self.path).path
        if path == "/version":
            self.send_json(200, {"major": "1", "minor": "31", "gitVersion": "v1.31.0"})
        elif path == "/api":
            self.send_json(200, {"kind": "APIVersions", "versions": ["v1"], "serverAddressByClientCIDRs": []})
        elif path == "/apis":
            self.send_json(200, {"kind": "APIGroupList", "groups": []})
        elif path == "/api/v1":
            self.send_json(
                200,
                {
                    "kind": "APIResourceList",
                    "groupVersion": "v1",
                    "resources": [
                        {
                            "name": "secrets",
                            "singularName": "secret",
                            "namespaced": True,
                            "kind": "Secret",
                            "verbs": ["get"],
                        }
                    ],
                },
            )
        elif path == "/api/v1/namespaces/keycloak/secrets/keycloak-admin-credentials":
            self.send_json(
                200,
                {
                    "apiVersion": "v1",
                    "kind": "Secret",
                    "metadata": {"name": "keycloak-admin-credentials", "namespace": "keycloak"},
                    "data": {
                        "admin-username": base64.b64encode(b"admin").decode(),
                        "admin-password": base64.b64encode(self.current_password.encode()).decode(),
                        "default-user-password": base64.b64encode(b"legacy-user").decode(),
                    },
                },
            )
        elif path == "/api/v1/namespaces/keycloak/secrets/keycloak-admin-password-rotation":
            if self.pending_rotation_password is None:
                self.send_json(404, {"kind": "Status", "status": "Failure", "reason": "NotFound", "code": 404})
            else:
                self.send_json(
                    200,
                    {
                        "apiVersion": "v1",
                        "kind": "Secret",
                        "metadata": {"name": "keycloak-admin-password-rotation", "namespace": "keycloak"},
                        "data": {"admin-password": base64.b64encode(self.pending_rotation_password.encode()).decode()},
                    },
                )
        else:
            self.send_json(404, {"kind": "Status", "status": "Failure", "reason": "NotFound", "code": 404})


def run_upgrade_render(
    helm: str,
    *,
    admin_password: str | None,
    admin_username: str = "admin",
    current_password: str = OLD_PASSWORD,
    pending_rotation_password: str | None = None,
    dev_fixtures: bool = False,
) -> subprocess.CompletedProcess[str]:
    FakeKubernetesHandler.current_password = current_password
    FakeKubernetesHandler.pending_rotation_password = pending_rotation_password
    server = HTTPServer(("127.0.0.1", 0), FakeKubernetesHandler)
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()
    try:
        command = [
            helm,
            "template",
            "rotation-test",
            str(CHART),
            "--namespace",
            "osac-infra",
            "--is-upgrade",
            "--dry-run=server",
            "--kube-apiserver",
            f"http://127.0.0.1:{server.server_port}",
            "--kube-insecure-skip-tls-verify",
            "--set-string",
            f"keycloak.adminUsername={admin_username}",
        ]
        if admin_password is not None:
            command.extend(("--set-string", f"keycloak.adminPassword={admin_password}"))
        if dev_fixtures:
            command.extend(("--set", "keycloak.devFixtures.enabled=true"))
        return subprocess.run(command, capture_output=True, text=True, check=False, timeout=60)
    finally:
        server.shutdown()
        thread.join(timeout=5)
        server.server_close()


def named_document(documents: list[dict[str, Any]], kind: str, name: str) -> dict[str, Any]:
    matches = [doc for doc in documents if doc.get("kind") == kind and doc.get("metadata", {}).get("name") == name]
    assert len(matches) == 1, f"expected one {kind}/{name}, found {len(matches)}"
    return matches[0]


def test_upgrade_render(helm: str) -> None:
    result = run_upgrade_render(helm, admin_password=NEW_PASSWORD)
    if result.returncode:
        raise RuntimeError(f"upgrade render failed: {result.stderr}")
    documents = [doc for doc in yaml.safe_load_all(result.stdout) if isinstance(doc, dict)]

    credentials = named_document(documents, "Secret", "keycloak-admin-credentials")
    assert credentials["stringData"]["admin-password"] == OLD_PASSWORD, (
        "the canonical Secret must keep the current password until rotation succeeds"
    )

    staged = named_document(documents, "Secret", "keycloak-admin-password-rotation")
    assert staged["stringData"]["admin-password"] == NEW_PASSWORD
    assert staged["metadata"]["annotations"]["helm.sh/hook"] == "post-upgrade"

    job = named_document(documents, "Job", "keycloak-admin-password-rotation")
    assert job["metadata"]["annotations"]["helm.sh/hook"] == "post-upgrade"
    assert int(job["metadata"]["annotations"]["helm.sh/hook-weight"]) > int(
        staged["metadata"]["annotations"]["helm.sh/hook-weight"]
    )
    pod = job["spec"]["template"]["spec"]
    assert pod["serviceAccountName"] == "keycloak-admin-rotation"
    container = pod["containers"][0]
    env = {entry["name"]: entry for entry in container["env"]}
    assert env["CURRENT_ADMIN_PASSWORD"]["valueFrom"]["secretKeyRef"] == {
        "name": "keycloak-admin-credentials",
        "key": "admin-password",
    }
    assert env["NEW_ADMIN_PASSWORD"]["valueFrom"]["secretKeyRef"] == {
        "name": "keycloak-admin-password-rotation",
        "key": "admin-password",
    }
    assert NEW_PASSWORD not in json.dumps(job), "the new password must not be in the Job spec"

    role = named_document(documents, "Role", "keycloak-admin-rotation")
    assert role["rules"] == [
        {
            "apiGroups": [""],
            "resources": ["secrets"],
            "resourceNames": ["keycloak-admin-credentials"],
            "verbs": ["patch"],
        },
        {
            "apiGroups": [""],
            "resources": ["secrets"],
            "resourceNames": ["keycloak-admin-password-rotation"],
            "verbs": ["delete"],
        },
    ]

    unchanged = run_upgrade_render(helm, admin_password=OLD_PASSWORD)
    if unchanged.returncode:
        raise RuntimeError(f"unchanged-password render failed: {unchanged.stderr}")
    unchanged_docs = [doc for doc in yaml.safe_load_all(unchanged.stdout) if isinstance(doc, dict)]
    assert not any(
        doc.get("metadata", {}).get("name") == "keycloak-admin-password-rotation" for doc in unchanged_docs
    ), "an unchanged admin password must not render a rotation hook"

    renamed = run_upgrade_render(helm, admin_password=NEW_PASSWORD, admin_username="renamed-admin")
    assert renamed.returncode != 0 and "cannot be changed on upgrade" in renamed.stderr, (
        "changing the admin username must fail with migration guidance"
    )

    legacy_password = run_upgrade_render(helm, admin_password="admin")
    assert legacy_password.returncode != 0 and "must not use the legacy default 'admin'" in legacy_password.stderr

    legacy_default = run_upgrade_render(helm, admin_password=NEW_PASSWORD, current_password="admin")
    if legacy_default.returncode:
        raise RuntimeError(f"legacy-default upgrade render failed: {legacy_default.stderr}")
    legacy_docs = [doc for doc in yaml.safe_load_all(legacy_default.stdout) if isinstance(doc, dict)]
    legacy_secret = named_document(legacy_docs, "Secret", "keycloak-admin-credentials")
    legacy_rotation = named_document(legacy_docs, "Secret", "keycloak-admin-password-rotation")
    assert legacy_secret["stringData"]["admin-password"] == "admin"
    assert legacy_rotation["stringData"]["admin-password"] == NEW_PASSWORD

    legacy_dev = run_upgrade_render(helm, admin_password=None, current_password="admin", dev_fixtures=True)
    if legacy_dev.returncode:
        raise RuntimeError(f"dev legacy-default upgrade render failed: {legacy_dev.stderr}")
    legacy_dev_docs = [doc for doc in yaml.safe_load_all(legacy_dev.stdout) if isinstance(doc, dict)]
    legacy_dev_rotation = named_document(legacy_dev_docs, "Secret", "keycloak-admin-password-rotation")
    generated_password = legacy_dev_rotation["stringData"]["admin-password"]
    assert len(generated_password) >= 32 and generated_password != "admin"

    retried_dev = run_upgrade_render(
        helm,
        admin_password=None,
        current_password="admin",
        pending_rotation_password=generated_password,
        dev_fixtures=True,
    )
    if retried_dev.returncode:
        raise RuntimeError(f"dev legacy-default retry render failed: {retried_dev.stderr}")
    retried_dev_docs = [doc for doc in yaml.safe_load_all(retried_dev.stdout) if isinstance(doc, dict)]
    retried_rotation = named_document(retried_dev_docs, "Secret", "keycloak-admin-password-rotation")
    assert retried_rotation["stringData"]["admin-password"] == generated_password, (
        "a retry must reuse the staged random password"
    )


def write_executable(path: pathlib.Path, content: str) -> None:
    path.write_text(content, encoding="utf-8")
    path.chmod(0o755)


def run_rotation_script(*, mode: str, new_password: str) -> tuple[bool, str]:
    with tempfile.TemporaryDirectory(prefix="osac-keycloak-rotation-") as temp_dir:
        temp = pathlib.Path(temp_dir)
        bin_dir = temp / "bin"
        bin_dir.mkdir()
        reset_capture = temp / "reset.json"
        patch_capture = temp / "patch.json"
        delete_capture = temp / "delete.txt"

        write_executable(
            bin_dir / "curl",
            """#!/usr/bin/env python3
import json
import os
import pathlib
import sys

args = sys.argv[1:]
if "--cacert" not in args or args[args.index("--cacert") + 1] != "/etc/keycloak/ca/ca.crt":
    raise SystemExit(91)
if "-k" in args or "--insecure" in args:
    raise SystemExit(92)
url = next((arg for arg in args if arg.startswith("https://")), "")
if "/protocol/openid-connect/token" in url:
    passwords = [arg.removeprefix("password=") for arg in args if arg.startswith("password=")]
    password = passwords[-1] if passwords else ""
    mode = os.environ["ROTATION_MODE"]
    valid = password == "current-password" if mode == "normal" else password == os.environ["NEW_ADMIN_PASSWORD"]
    if not valid:
        raise SystemExit(22)
    token = "current-token" if password == "current-password" else "new-token"
    print(json.dumps({"access_token": token}))
elif "/admin/realms/master/users" in url and "reset-password" not in url:
    print(json.dumps([{"id": "admin-id", "username": "admin"}]))
elif "reset-password" in url:
    payload = args[args.index("-d") + 1]
    pathlib.Path(os.environ["RESET_CAPTURE"]).write_text(payload, encoding="utf-8")
elif "/realms/master" in url:
    pass
else:
    raise SystemExit(93)
""",
        )
        write_executable(
            bin_dir / "oc",
            """#!/usr/bin/env python3
import os
import pathlib
import sys

args = sys.argv[1:]
if args[:2] == ["patch", "secret"]:
    pathlib.Path(os.environ["PATCH_CAPTURE"]).write_text(args[args.index("-p") + 1], encoding="utf-8")
elif args[:2] == ["delete", "secret"]:
    pathlib.Path(os.environ["DELETE_CAPTURE"]).write_text(" ".join(args), encoding="utf-8")
else:
    raise SystemExit(94)
""",
        )
        write_executable(bin_dir / "sleep", "#!/usr/bin/env bash\nexit 0\n")

        env = os.environ.copy()
        env.update(
            {
                "PATH": f"{bin_dir}{os.pathsep}{env['PATH']}",
                "CURRENT_ADMIN_USERNAME": "admin",
                "CURRENT_ADMIN_PASSWORD": "current-password",
                "NEW_ADMIN_PASSWORD": new_password,
                "KEYCLOAK_URL": "https://keycloak:443",
                "ROTATION_MODE": mode,
                "RESET_CAPTURE": str(reset_capture),
                "PATCH_CAPTURE": str(patch_capture),
                "DELETE_CAPTURE": str(delete_capture),
            }
        )
        result = subprocess.run(
            ["bash", str(ROTATION_SCRIPT)], capture_output=True, text=True, check=False, env=env, timeout=20
        )
        if result.returncode:
            raise AssertionError(f"rotation script failed: {result.stderr}")
        patch = json.loads(patch_capture.read_text(encoding="utf-8"))
        patched_password = base64.b64decode(patch["data"]["admin-password"]).decode()
        assert patched_password == new_password
        assert "keycloak-admin-password-rotation" in delete_capture.read_text(encoding="utf-8")
        return reset_capture.exists(), result.stdout


def test_rotation_script() -> None:
    new_password = 'next "quoted" \\ password'
    reset, output = run_rotation_script(mode="normal", new_password=new_password)
    assert reset, "the script must reset an account authenticated with the current password"
    assert "rotation completed" in output

    reset, _ = run_rotation_script(mode="already-rotated", new_password=new_password)
    assert not reset, "retries must not reset the password twice"


def main() -> None:
    helm = os.environ.get("HELM_BIN", "helm")
    if shutil.which(helm) is None:
        raise RuntimeError("helm executable is required for the Keycloak admin rotation contract")
    test_upgrade_render(helm)
    test_rotation_script()
    print("Keycloak admin password rotation contract passed.")


if __name__ == "__main__":
    main()
