#!/usr/bin/env python3

from __future__ import annotations

import importlib.util
from pathlib import Path

import pytest


SCRIPT = Path(__file__).resolve().parents[1] / "charts/osac/files/validate-caas-dns-settings.py"
SPEC = importlib.util.spec_from_file_location("caas_dns_guard", SCRIPT)
assert SPEC and SPEC.loader
guard = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(guard)


def settings(**overrides: str) -> dict[str, str]:
    result = {
        "EXTERNAL_ACCESS_BASE_DOMAIN": "clusters.example.test",
        "HOSTED_CLUSTER_BASE_DOMAIN": "clusters.example.test",
        "DNS_ZONE": "example.test",
        "DNS_CLASS": "dns.route53.dns",
    }
    result.update(overrides)
    return result


def test_dns_zone_defaults_to_external_base_domain() -> None:
    effective = guard.effective_settings(settings(DNS_ZONE=""))
    assert effective["DNS_ZONE"] == "clusters.example.test"


@pytest.mark.parametrize(
    "field,changed",
    [
        ("EXTERNAL_ACCESS_BASE_DOMAIN", "new.example.test"),
        ("DNS_ZONE", "new.example.test"),
        ("DNS_CLASS", "dns.noop.dns"),
    ],
)
def test_dns_setting_changes_are_blocked_while_clusters_exist(field: str, changed: str) -> None:
    previous = settings()
    proposed = settings(**{field: changed})
    with pytest.raises(RuntimeError, match=field):
        guard.validate_settings(previous, proposed, live_clusters=True)


def test_dns_setting_changes_are_allowed_without_live_clusters() -> None:
    guard.validate_settings(settings(), settings(DNS_ZONE="new.example.test"), live_clusters=False)


def test_unchanged_settings_are_allowed_with_live_clusters() -> None:
    guard.validate_settings(settings(), settings(), live_clusters=True)


def test_missing_previous_settings_with_live_clusters_fails_closed() -> None:
    with pytest.raises(RuntimeError, match="previous CaaS DNS settings"):
        guard.validate_settings(None, settings(), live_clusters=True)


def test_read_previous_settings_accepts_not_found_and_uses_effective_defaults(monkeypatch) -> None:
    class Result:
        returncode = 1
        stderr = "Error from server (NotFound): configmaps \"cluster-fulfillment-ig\" not found"
        stdout = ""

    monkeypatch.setattr(guard.subprocess, "run", lambda *args, **kwargs: Result())
    assert guard.read_previous_settings("osac") is None


def test_live_cluster_probe_uses_private_api_and_bearer_token(monkeypatch) -> None:
    class Response:
        def __enter__(self):
            return self

        def __exit__(self, *_args):
            return False

        def read(self):
            return b'{"items":[{"id":"cluster-1"}]}'

    seen = {}

    def fake_open(request, **kwargs):
        seen["url"] = request.full_url
        seen["auth"] = request.get_header("Authorization")
        return Response()

    monkeypatch.setenv("FULFILLMENT_URL", "https://fulfillment-internal-api:8001")
    monkeypatch.setenv("AUTH_TOKEN", "test-token")
    monkeypatch.setattr(guard.urllib.request, "urlopen", fake_open)

    assert guard.has_live_clusters() is True
    assert seen["url"].startswith("https://fulfillment-internal-api:8001/api/private/v1/clusters?")
    assert seen["auth"] == "Bearer test-token"
