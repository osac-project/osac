"""Contract tests for reading the suite image digest from the container runtime."""

from __future__ import annotations

from pathlib import Path

import pytest

from tests.conformance import image

_PODMAN_SOCKET = Path("/run/podman/podman.sock")
_DOCKER_SOCKET = Path("/var/run/docker.sock")


def test_environment_variable_wins_over_the_socket(monkeypatch: pytest.MonkeyPatch) -> None:
    def unexpected(*_args: object, **_kwargs: object) -> None:
        raise AssertionError("the socket must not be read when the digest is explicit")

    monkeypatch.setattr(image, "container_inspect", unexpected)
    monkeypatch.setenv("OSAC_CONFORMANCE_SUITE_IMAGE_DIGEST", "sha256:env")

    assert image.suite_image_digest(_PODMAN_SOCKET, container_id="id") == "sha256:env"


def test_image_name_with_digest_wins(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.delenv("OSAC_CONFORMANCE_SUITE_IMAGE_DIGEST", raising=False)
    monkeypatch.setattr(
        image,
        "container_inspect",
        lambda *_args, **_kwargs: {"ImageName": "quay.io/etabak/osac-conformance@sha256:abc"},
    )

    assert image.suite_image_digest(_PODMAN_SOCKET, container_id="id") == "sha256:abc"


def test_image_digest_wins_over_the_image_id(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.delenv("OSAC_CONFORMANCE_SUITE_IMAGE_DIGEST", raising=False)
    monkeypatch.setattr(
        image,
        "container_inspect",
        lambda *_args, **_kwargs: {
            "ImageName": "quay.io/etabak/osac-conformance:dev",
            "ImageDigest": "sha256:digest",
            "Image": "sha256:id",
        },
    )

    assert image.suite_image_digest(_PODMAN_SOCKET, container_id="id") == "sha256:digest"


def test_docker_compatible_reference_with_a_digest_is_used(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.delenv("OSAC_CONFORMANCE_SUITE_IMAGE_DIGEST", raising=False)
    monkeypatch.setattr(
        image,
        "container_inspect",
        lambda *_args, **_kwargs: {
            "Config": {"Image": "quay.io/etabak/osac-conformance@sha256:ref"},
            "Image": "sha256:id",
        },
    )

    assert image.suite_image_digest(_DOCKER_SOCKET, container_id="id") == "sha256:ref"


def test_docker_compatible_reference_resolves_its_repo_digest(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.delenv("OSAC_CONFORMANCE_SUITE_IMAGE_DIGEST", raising=False)
    monkeypatch.setattr(
        image,
        "container_inspect",
        lambda *_args, **_kwargs: {"Config": {"Image": "quay.io/etabak/osac-conformance:dev"}, "Image": "sha256:id"},
    )

    def fake_get(_socket_path: Path, path: str) -> dict[str, object] | None:
        if "/images/" in path:
            return {"RepoDigests": ["quay.io/etabak/osac-conformance@sha256:repo"]}
        return {"Config": {"Image": "quay.io/etabak/osac-conformance:dev"}}

    monkeypatch.setattr(image, "_get", fake_get)

    assert image.suite_image_digest(_DOCKER_SOCKET, container_id="id") == "sha256:repo"


def test_image_id_is_the_fallback(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.delenv("OSAC_CONFORMANCE_SUITE_IMAGE_DIGEST", raising=False)
    monkeypatch.setattr(image, "container_inspect", lambda *_args, **_kwargs: {"Image": "sha256:def"})

    assert image.suite_image_digest(_PODMAN_SOCKET, container_id="id") == "sha256:def"


def test_environment_variable_is_the_last_fallback(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setattr(image, "container_inspect", lambda *_args, **_kwargs: None)
    monkeypatch.setenv("OSAC_CONFORMANCE_SUITE_IMAGE_DIGEST", "sha256:env")

    assert image.suite_image_digest(_PODMAN_SOCKET, container_id="id") == "sha256:env"


def test_unreachable_socket_falls_back_to_the_environment(monkeypatch: pytest.MonkeyPatch) -> None:
    def fail(*_args: object, **_kwargs: object) -> None:
        raise OSError("no socket")

    monkeypatch.setattr(image, "container_inspect", fail)
    monkeypatch.setenv("OSAC_CONFORMANCE_SUITE_IMAGE_DIGEST", "sha256:env")

    assert image.suite_image_digest(_PODMAN_SOCKET, container_id="id") == "sha256:env"


def test_discovery_tries_every_candidate_socket(monkeypatch: pytest.MonkeyPatch) -> None:
    for name in ("OSAC_CONFORMANCE_SUITE_IMAGE_DIGEST", "CONTAINER_HOST", "DOCKER_HOST", "XDG_RUNTIME_DIR"):
        monkeypatch.delenv(name, raising=False)
    seen: list[Path] = []

    def fake_inspect(socket_path: Path, _container_id: str) -> dict[str, str] | None:
        seen.append(socket_path)
        if socket_path == _DOCKER_SOCKET:
            return {"ImageName": "quay.io/etabak/osac-conformance@sha256:docker"}
        return None

    monkeypatch.setattr(image, "container_inspect", fake_inspect)

    assert image.suite_image_digest(container_id="id") == "sha256:docker"
    assert seen == [_PODMAN_SOCKET, _DOCKER_SOCKET]


def test_explicit_socket_replaces_the_defaults() -> None:
    assert image.runtime_sockets(Path("/custom.sock"), env={}) == (Path("/custom.sock"),)


def test_runtime_environment_sockets_come_first() -> None:
    env = {
        "CONTAINER_HOST": "unix:///run/user/1000/podman/podman.sock",
        "DOCKER_HOST": "unix:///run/user/1000/docker.sock",
        "XDG_RUNTIME_DIR": "/run/user/1000",
    }

    assert image.runtime_sockets(env=env) == (
        Path("/run/user/1000/podman/podman.sock"),
        Path("/run/user/1000/docker.sock"),
        _PODMAN_SOCKET,
        _DOCKER_SOCKET,
    )


def test_remote_runtime_hosts_are_ignored() -> None:
    env = {"DOCKER_HOST": "tcp://127.0.0.1:2375"}

    assert image.runtime_sockets(env=env) == (_PODMAN_SOCKET, _DOCKER_SOCKET)


def test_inspect_tries_libpod_then_compat_api(monkeypatch: pytest.MonkeyPatch) -> None:
    calls: list[str] = []

    def fake_get(_socket_path: Path, path: str) -> dict[str, str] | None:
        calls.append(path)
        if "v4.0.0" in path:
            return None
        return {"Image": "sha256:compat"}

    monkeypatch.setattr(image, "_get", fake_get)

    assert image.container_inspect(_PODMAN_SOCKET, "id") == {"Image": "sha256:compat"}
    assert calls == ["/v4.0.0/libpod/containers/id/json", "/v1.40/containers/id/json"]


def test_runtime_command_prefers_podman(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setattr(image.shutil, "which", lambda name: f"/usr/bin/{name}")

    assert image.runtime_command() == "podman"


def test_runtime_command_falls_back_to_docker(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setattr(image.shutil, "which", lambda name: "/usr/bin/docker" if name == "docker" else None)

    assert image.runtime_command() == "docker"


def test_runtime_command_is_empty_without_a_runtime(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setattr(image.shutil, "which", lambda _name: None)

    assert image.runtime_command() is None
