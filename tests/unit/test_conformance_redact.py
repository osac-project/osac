"""Contract tests for the conformance redaction boundary."""

from __future__ import annotations

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
