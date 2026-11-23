"""Helpers for exercising the conformance core without a live target."""

from __future__ import annotations

from pathlib import Path

from tests.conformance.config import Config


def make_config(root: Path, *, namespace: str = "osac") -> Config:
    """Return a loaded ``Config`` for tests that do not exercise loading.

    ``Config.target_namespace`` is always a non-empty string in production:
    ``load_config`` defaults it to ``"osac"`` and rejects an explicit empty
    value.
    """
    return Config(
        osac_release="0.0.1",
        path=root / "conformance.yaml",
        profiles=("generic",),
        target_namespace=namespace,
        target_kubeconfig=root / "target-hub.kubeconfig",
        target_api_url="https://fulfillment.example.test",
        target_token_file=root / "target-token",
        target_ca_file=root / "api-ca.pem",
        target_aap=None,
        timeouts={},
    )
