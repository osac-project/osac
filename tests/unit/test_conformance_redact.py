"""Contract tests for the conformance redaction boundary."""

from __future__ import annotations

from tests.conformance.redact import Redactor


def test_known_secret_and_host_are_redacted() -> None:
    redactor = Redactor(secrets=("super-secret",), hosts=("api.internal.test",))

    text = redactor.text("token super-secret at api.internal.test")

    assert "super-secret" not in text
    assert "api.internal.test" not in text
    assert text.count("<redacted>") == 2


def test_existing_sanitizer_rules_are_composed() -> None:
    redactor = Redactor()

    text = redactor.text("Authorization: Bearer abcdefghijklmnop")

    assert "abcdefghijklmnop" not in text
    assert "<redacted>" in text


def test_objects_are_redacted_recursively() -> None:
    redactor = Redactor(secrets=("s3",))

    cleaned = redactor.obj({"outer": {"msg": "s3"}, "items": ["s3", 1]})

    assert cleaned == {"outer": {"msg": "<redacted>"}, "items": ["<redacted>", 1]}


def test_empty_values_are_ignored() -> None:
    redactor = Redactor(secrets=("",))

    assert redactor.text("plain") == "plain"
