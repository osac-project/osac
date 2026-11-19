"""Read the suite image digest from the local container runtime.

The suite is not tied to one container runtime. Podman and Docker both expose
an HTTP API over a unix socket, so the lookup inspects the container the suite
runs in through whichever socket it can reach. Podman's libpod endpoint is
tried first, then the Docker-compatible endpoint that both runtimes serve.

An explicit ``OSAC_CONFORMANCE_SUITE_IMAGE_DIGEST`` always wins. That lets an
operator supply the digest without mounting a runtime socket at all.
"""

from __future__ import annotations

import http.client
import json
import os
import shutil
import socket
from collections.abc import Mapping
from pathlib import Path
from typing import Any
from urllib.parse import quote

_RUNTIME_COMMANDS = ("podman", "docker")
_DEFAULT_SOCKETS = (Path("/run/podman/podman.sock"), Path("/var/run/docker.sock"))
_PODMAN_API = "/v4.0.0/libpod/containers/{container}/json"
_COMPAT_API = "/v1.40/containers/{container}/json"
_IMAGE_API = "/v1.40/images/{image}/json"


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


def runtime_command() -> str | None:
    """The container CLI available locally, preferring podman."""
    for name in _RUNTIME_COMMANDS:
        if shutil.which(name) is not None:
            return name
    return None


def _environment_sockets(env: Mapping[str, str]) -> tuple[Path, ...]:
    """Unix sockets named by the runtime environment, in precedence order."""
    paths: list[Path] = []
    for name in ("CONTAINER_HOST", "DOCKER_HOST"):
        value = env.get(name, "")
        if value.startswith("unix://"):
            paths.append(Path(value.removeprefix("unix://")))
    xdg_runtime_dir = env.get("XDG_RUNTIME_DIR")
    if xdg_runtime_dir:
        paths.append(Path(xdg_runtime_dir) / "podman" / "podman.sock")
    return tuple(paths)


def runtime_sockets(explicit: Path | None = None, *, env: Mapping[str, str] | None = None) -> tuple[Path, ...]:
    """Candidate runtime sockets: the explicit one, else the runtime defaults."""
    if explicit is not None:
        return (explicit,)
    ordered: list[Path] = []
    for path in (*_environment_sockets(os.environ if env is None else env), *_DEFAULT_SOCKETS):
        if path not in ordered:
            ordered.append(path)
    return tuple(ordered)


def _repository_digest(socket_path: Path, reference: str) -> str:
    """The repo digest for an image reference, through the compatible API."""
    data = _get(socket_path, _IMAGE_API.format(image=quote(reference, safe="")))
    if data is None:
        return ""
    digests = data.get("RepoDigests")
    if not isinstance(digests, list):
        return ""
    for entry in digests:
        if isinstance(entry, str) and "@" in entry:
            return entry.rsplit("@", 1)[1]
    return ""


def _digest(data: Mapping[str, Any], socket_path: Path) -> str:
    name = data.get("ImageName")
    if isinstance(name, str) and "@" in name:
        return name.rsplit("@", 1)[1]
    # Podman's libpod inspect reports the resolved digest directly.
    digest = data.get("ImageDigest")
    if isinstance(digest, str) and digest:
        return digest
    # Docker-compatible inspect reports the image ID and the image reference.
    config = data.get("Config")
    reference = config.get("Image") if isinstance(config, Mapping) else None
    if isinstance(reference, str) and reference:
        if "@" in reference:
            return reference.rsplit("@", 1)[1]
        digest = _repository_digest(socket_path, reference)
        if digest:
            return digest
    # ``Image`` is the image ID, not a digest, so it is only a last resort.
    image_id = data.get("Image")
    if isinstance(image_id, str) and image_id:
        return image_id
    return ""


def container_inspect(socket_path: Path, container_id: str) -> Mapping[str, Any] | None:
    for path in (_PODMAN_API.format(container=container_id), _COMPAT_API.format(container=container_id)):
        data = _get(socket_path, path)
        if data is not None:
            return data
    return None


def suite_image_digest(socket_path: Path | None = None, *, container_id: str | None = None) -> str:
    explicit = os.environ.get("OSAC_CONFORMANCE_SUITE_IMAGE_DIGEST", "")
    if explicit:
        return explicit
    container = container_id or socket.gethostname()
    for candidate in runtime_sockets(socket_path):
        try:
            data = container_inspect(candidate, container)
        except (OSError, http.client.HTTPException):
            continue
        if isinstance(data, Mapping):
            digest = _digest(data, candidate)
            if digest:
                return digest
    return ""
