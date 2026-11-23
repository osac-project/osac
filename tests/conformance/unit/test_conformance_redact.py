"""Contract tests for the conformance redaction boundary."""

from __future__ import annotations

import json

import pytest

from tests.conformance.redact import Redactor

_FAKE_HOST = "api.internal.test"
_REDACTION_MARKER = "redaction-marker"
_REDACTION_MARKER_E = "redaction-marker-e"
_REDACTION_MARKER_G = "redaction-marker-g"
_REDACTION_MARKER_C = "redaction-marker-c"
_REDACTION_MARKER_F = "redaction-marker-f"


def test_known_secret_and_host_are_redacted() -> None:
    redactor = Redactor(secrets=(_REDACTION_MARKER,), hosts=(_FAKE_HOST,))

    text = redactor.text(f"token {_REDACTION_MARKER} at {_FAKE_HOST}")

    assert _REDACTION_MARKER not in text
    assert _FAKE_HOST not in text
    assert text.count("<redacted>") == 2


def test_existing_sanitizer_rules_are_composed() -> None:
    redactor = Redactor()

    text = redactor.text(f"Authorization: Bearer {_REDACTION_MARKER_E}")

    assert _REDACTION_MARKER_E not in text
    assert "<redacted>" in text


def test_unknown_credential_assignments_are_redacted() -> None:
    redactor = Redactor()

    text = redactor.text(f"api_key={_REDACTION_MARKER_C} session_id={_REDACTION_MARKER_F}")

    assert _REDACTION_MARKER_C not in text
    assert _REDACTION_MARKER_F not in text
    assert text.count("<redacted>") == 2


def test_objects_are_redacted_recursively() -> None:
    redactor = Redactor(secrets=(_REDACTION_MARKER_G,))

    cleaned = redactor.obj({"outer": {"msg": _REDACTION_MARKER_G}, "items": [_REDACTION_MARKER_G, 1]})

    assert cleaned == {"outer": {"msg": "<redacted>"}, "items": ["<redacted>", 1]}


def test_empty_values_are_ignored() -> None:
    redactor = Redactor(secrets=("",))

    assert redactor.text("plain") == "plain"


@pytest.mark.parametrize("quote", ['"', "'"])
def test_quoted_credential_values_are_redacted_whole(quote: str) -> None:
    redactor = Redactor()
    value = "correct horse battery staple"

    text = redactor.text(f"password: {quote}{value}{quote}")

    assert value not in text
    assert text == "password: <redacted>"


def test_credential_keys_are_redacted_after_yaml_decoding() -> None:
    redactor = Redactor()

    cleaned = redactor.obj({"annotations": {"api_key": _REDACTION_MARKER_C, "owner": "team-a"}})

    assert cleaned == {"annotations": {"api_key": "<redacted>", "owner": "team-a"}}


def test_container_environment_credential_pairs_are_redacted() -> None:
    redactor = Redactor()

    cleaned = redactor.obj(
        {
            "env": [
                {"name": "API_KEY", "value": _REDACTION_MARKER_C},
                {"name": "NETRIS_CONTROLLER_URL", "value": "https://netris.example.test"},
            ]
        }
    )

    assert cleaned == {
        "env": [
            {"name": "API_KEY", "value": "<redacted>"},
            {"name": "NETRIS_CONTROLLER_URL", "value": "https://netris.example.test"},
        ]
    }


@pytest.mark.parametrize("name", ["DOCKER_AUTH_CONFIG", "REGISTRY_AUTH_CONFIG"])
def test_registry_auth_config_entries_are_redacted(name: str) -> None:
    redactor = Redactor()
    # base64 of "test:test", deliberately low-entropy so secret scanners do not
    # flag a fixture that only needs to look like a registry auth config.
    payload = '{"auths":{"registry.example.test":{"auth":"dGVzdDp0ZXN0"}}}'

    cleaned = redactor.obj({"env": [{"name": name, "value": payload}]})

    assert cleaned == {"env": [{"name": name, "value": "<redacted>"}]}


def test_quoted_json_api_keys_are_redacted() -> None:
    redactor = Redactor()

    text = redactor.text(f'{{"api_key":"{_REDACTION_MARKER_C}"}}')

    assert _REDACTION_MARKER_C not in text
    assert text == '{"api_key":"<redacted>"}'


def test_serialized_annotations_are_redacted_structurally() -> None:
    redactor = Redactor()
    annotation = f'{{"env":[{{"name":"PASSWORD","value":"{_REDACTION_MARKER_C}"}}]}}'

    cleaned = redactor.obj(
        {"metadata": {"annotations": {"kubectl.kubernetes.io/last-applied-configuration": annotation}}}
    )

    serialized = cleaned["metadata"]["annotations"]["kubectl.kubernetes.io/last-applied-configuration"]
    assert _REDACTION_MARKER_C not in serialized
    assert json.loads(serialized) == {"env": [{"name": "PASSWORD", "value": "<redacted>"}]}
