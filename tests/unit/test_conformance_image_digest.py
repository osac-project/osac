"""Contract tests for reading the suite image digest from podman."""

from __future__ import annotations

from pathlib import Path

import pytest

from tests.conformance import image


def test_image_name_with_digest_wins(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setattr(
        image,
        "container_inspect",
        lambda *_args, **_kwargs: {"ImageName": "quay.io/etabak/osac-conformance@sha256:abc"},
    )

    assert image.suite_image_digest(Path("/run/podman/podman.sock"), container_id="id") == "sha256:abc"


def test_image_id_is_the_fallback(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setattr(image, "container_inspect", lambda *_args, **_kwargs: {"Image": "sha256:def"})

    assert image.suite_image_digest(Path("/run/podman/podman.sock"), container_id="id") == "sha256:def"


def test_environment_variable_is_the_last_fallback(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setattr(image, "container_inspect", lambda *_args, **_kwargs: None)
    monkeypatch.setenv("OSAC_CONFORMANCE_SUITE_IMAGE_DIGEST", "sha256:env")

    assert image.suite_image_digest(Path("/run/podman/podman.sock"), container_id="id") == "sha256:env"


def test_podman_failure_falls_back_to_the_environment(monkeypatch: pytest.MonkeyPatch) -> None:
    def fail(*_args: object, **_kwargs: object) -> None:
        raise OSError("no socket")

    monkeypatch.setattr(image, "container_inspect", fail)
    monkeypatch.setenv("OSAC_CONFORMANCE_SUITE_IMAGE_DIGEST", "sha256:env")

    assert image.suite_image_digest(Path("/run/podman/podman.sock"), container_id="id") == "sha256:env"


def test_inspect_tries_libpod_then_compat_api(monkeypatch: pytest.MonkeyPatch) -> None:
    calls: list[str] = []

    def fake_get(_socket_path: Path, path: str) -> dict[str, str] | None:
        calls.append(path)
        if "v4.0.0" in path:
            return None
        return {"Image": "sha256:compat"}

    monkeypatch.setattr(image, "_get", fake_get)

    assert image.container_inspect(Path("/run/podman/podman.sock"), "id") == {"Image": "sha256:compat"}
    assert calls == ["/v4.0.0/libpod/containers/id/json", "/v1.40/containers/id/json"]
