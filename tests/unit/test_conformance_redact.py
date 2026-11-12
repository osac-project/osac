"""Contract tests for the conformance redaction boundary."""

from __future__ import annotations

from tests.conformance.redact import Redactor

_FAKE_HOST = "api.internal.test"
_FAKE_SECRET = "not-a-real-secret"
_FAKE_BEARER = "not-a-real-bearer-value"
_FAKE_MARKER = "not-a-real-marker"
_FAKE_API_KEY = "not-a-real-api-key"
_FAKE_SESSION = "not-a-real-session"


def test_known_secret_and_host_are_redacted() -> None:
    redactor = Redactor(secrets=(_FAKE_SECRET,), hosts=(_FAKE_HOST,))

    text = redactor.text(f"token {_FAKE_SECRET} at {_FAKE_HOST}")

    assert _FAKE_SECRET not in text
    assert _FAKE_HOST not in text
    assert text.count("<redacted>") == 2


def test_existing_sanitizer_rules_are_composed() -> None:
    redactor = Redactor()

    text = redactor.text(f"Authorization: Bearer {_FAKE_BEARER}")

    assert _FAKE_BEARER not in text
    assert "<redacted>" in text


def test_unknown_credential_assignments_are_redacted() -> None:
    redactor = Redactor()

    text = redactor.text(f"api_key={_FAKE_API_KEY} session_id={_FAKE_SESSION}")

    assert _FAKE_API_KEY not in text
    assert _FAKE_SESSION not in text
    assert text.count("<redacted>") == 2


def test_objects_are_redacted_recursively() -> None:
    redactor = Redactor(secrets=(_FAKE_MARKER,))

    cleaned = redactor.obj({"outer": {"msg": _FAKE_MARKER}, "items": [_FAKE_MARKER, 1]})

    assert cleaned == {"outer": {"msg": "<redacted>"}, "items": ["<redacted>", 1]}


def test_empty_values_are_ignored() -> None:
    redactor = Redactor(secrets=("",))

    assert redactor.text("plain") == "plain"
