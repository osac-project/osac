#!/usr/bin/env python3
"""Renew the host-managed, scoped KiND kubeconfig mounted into Carbonite."""

import base64
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import time


def token_expiry(token: str) -> int:
    parts = token.split(".")
    if len(parts) != 3:
        raise ValueError("Kubernetes did not return a JWT")
    payload = parts[1] + "=" * (-len(parts[1]) % 4)
    claims = json.loads(base64.urlsafe_b64decode(payload))
    expiry = claims.get("exp")
    if not isinstance(expiry, int) or expiry <= time.time():
        raise ValueError("Kubernetes returned an expired token")
    return expiry


def refresh() -> float:
    if os.environ.get("OPENSHELL_SANDBOX"):
        raise ValueError("Run this command on the host, outside OpenShell")

    home = Path.home()
    state_home = Path(os.environ.get("XDG_STATE_HOME", home / ".local/state"))
    state_dir = state_home / "osac-carbonite-kind"
    kube_dir = state_dir / "kube"
    config_path = kube_dir / "config"
    admin_config = home / ".kube/osac-dev-kind.kubeconfig"

    if any(path.is_symlink() for path in (state_dir, kube_dir, config_path)):
        raise ValueError("Refusing a symlinked kubeconfig path")
    if not admin_config.is_file() or not config_path.is_file():
        raise ValueError("Create the initial scoped kubeconfig using the KiND guide first")
    if kube_dir.stat().st_mode & 0o077 or config_path.stat().st_mode & 0o077:
        raise ValueError("The scoped kubeconfig directory and file must be private")

    config = json.loads(config_path.read_text())
    if (
        len(config.get("clusters", [])) != 1
        or len(config.get("users", [])) != 1
        or len(config.get("contexts", [])) != 1
        or config["clusters"][0]["cluster"].get("server")
        != "https://host.openshell.internal:18443"
        or not config["clusters"][0]["cluster"].get("certificate-authority-data")
        or not config["clusters"][0]["cluster"].get("tls-server-name")
    ):
        raise ValueError("Existing kubeconfig is not the expected scoped KiND config")

    kubectl = shutil.which("kubectl")
    if kubectl is None:
        raise ValueError("kubectl is required on the host")
    result = subprocess.run(
        [
            kubectl,
            "--kubeconfig",
            str(admin_config),
            "-n",
            "osac",
            "create",
            "token",
            "carbonite-operator-test",
            "--duration=12h",
        ],
        check=True,
        capture_output=True,
        text=True,
    )
    token = result.stdout.strip()
    expiry = token_expiry(token)
    config["users"][0]["user"] = {"token": token}

    fd, next_path = tempfile.mkstemp(prefix="config.", dir=state_dir)
    try:
        with os.fdopen(fd, "w") as next_file:
            json.dump(config, next_file)
            next_file.flush()
            os.fsync(next_file.fileno())
        getenforce = shutil.which("getenforce")
        if getenforce and subprocess.run(
            [getenforce], check=True, capture_output=True, text=True
        ).stdout.strip() != "Disabled":
            chcon = shutil.which("chcon")
            if chcon is None:
                raise ValueError("chcon is required on an SELinux host")
            subprocess.run(
                [chcon, f"--reference={kube_dir}", next_path],
                check=True,
                capture_output=True,
            )
        os.replace(next_path, config_path)
    finally:
        if os.path.exists(next_path):
            os.unlink(next_path)

    return (expiry - time.time()) / 3600


if __name__ == "__main__":
    try:
        hours = refresh()
    except (OSError, ValueError, KeyError, TypeError, subprocess.CalledProcessError):
        print("Could not renew the scoped kubeconfig; check host KiND access and setup.", file=sys.stderr)
        sys.exit(1)
    print(f"Scoped kubeconfig renewed; token lifetime granted: {hours:.1f} hours.")
