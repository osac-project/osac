"""Read the suite image digest from the podman socket."""

from __future__ import annotations

import http.client
import json
import os
import socket
from collections.abc import Mapping
from pathlib import Path
from typing import Any


class _UnixConnection(http.client.HTTPConnection):
    def __init__(self, socket_path: Path, timeout: float = 10.0) -> None:
        super().__init__("localhost", timeout=timeout)
        self.socket_path = socket_path

    def connect(self) -> None:
        self.sock = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
        self.sock.settimeout(self.timeout)
        self.sock.connect(str(self.socket_path))


def _get(socket_path: Path, path: str) -> Mapping[str, Any] | None:
    connection = _UnixConnection(socket_path)
    try:
        connection.request("GET", path)
        response = connection.getresponse()
        if response.status != 200:
            return None
        body = response.read()
    finally:
        connection.close()
    try:
        data = json.loads(body)
    except json.JSONDecodeError:
        return None
    return data if isinstance(data, Mapping) else None


def container_inspect(socket_path: Path, container_id: str) -> Mapping[str, Any] | None:
    for path in (f"/v4.0.0/libpod/containers/{container_id}/json", f"/v1.40/containers/{container_id}/json"):
        data = _get(socket_path, path)
        if data is not None:
            return data
    return None


def suite_image_digest(socket_path: Path, *, container_id: str | None = None) -> str:
    env_digest = os.environ.get("OSAC_CONFORMANCE_SUITE_IMAGE_DIGEST", "")
    container = container_id or socket.gethostname()
    try:
        data = container_inspect(socket_path, container)
    except (OSError, http.client.HTTPException):
        data = None
    if isinstance(data, Mapping):
        name = data.get("ImageName")
        if isinstance(name, str) and "@" in name:
            return name.rsplit("@", 1)[1]
        image = data.get("Image")
        if isinstance(image, str) and image:
            return image
    return env_digest
