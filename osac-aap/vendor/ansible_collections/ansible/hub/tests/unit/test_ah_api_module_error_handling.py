import importlib.util
import pathlib
from unittest.mock import MagicMock

import pytest

# ansible-core stubbing (AnsibleModule stand-in, etc.) lives in conftest.py,
# shared with test_ah_pulp_object_update.py, and runs before this file is
# collected.

_module_path = pathlib.Path(__file__).resolve().parents[2] / "plugins" / "module_utils" / "ah_api_module.py"
spec = importlib.util.spec_from_file_location("ah_api_module", str(_module_path))
mod = importlib.util.module_from_spec(spec)
mod.__package__ = "plugins.module_utils"
spec.loader.exec_module(mod)

AHAPIModule = mod.AHAPIModule
AHAPIModuleError = mod.AHAPIModuleError


def _make_api(raw_response):
    """Build an AHAPIModule instance without running its heavy __init__.

    make_request() only needs make_request_raw_reponse() to return a response;
    it doesn't touch anything else __init__ would normally set up, so a bare
    instance with that one method stubbed is enough to exercise it in
    isolation.
    """
    api = object.__new__(AHAPIModule)
    api.make_request_raw_reponse = MagicMock(return_value=raw_response)
    api.no_log_values = set()
    return api


def test_make_request_surfaces_unrecognized_error_shape():
    """An API error body in a shape make_request() doesn't special-case should
    still surface its real content, not a generic "response has no read"
    message that hides what the server actually said.
    """
    api = _make_api({"status_code": 400, "json": {"content_guard": ["Invalid hyperlink - Incorrect URL match."]}})

    with pytest.raises(AHAPIModuleError) as exc_info:
        api.make_request("PATCH", "https://hub.example.com/pulp/api/v3/thing/1/", data={"description": "x"})

    assert "content_guard" in str(exc_info.value)
    assert "Invalid hyperlink" in str(exc_info.value)


def test_make_request_handles_text_only_error_body():
    """A non-JSON error body (no "json" key, only "text") must not crash the
    fallback logic that looks for "non_field_errors" or "errors" keys.
    """
    api = _make_api({"status_code": 400, "text": "plain text error"})

    with pytest.raises(AHAPIModuleError) as exc_info:
        api.make_request("PATCH", "https://hub.example.com/pulp/api/v3/thing/1/", data={"description": "x"})

    assert "plain text error" in str(exc_info.value)


def test_make_request_handles_null_json_error_body():
    """A JSON null error body must use the fallback instead of crashing while
    looking for dictionary-only error keys.
    """
    api = _make_api({"status_code": 400, "json": None})

    with pytest.raises(AHAPIModuleError) as exc_info:
        api.make_request("PATCH", "https://hub.example.com/pulp/api/v3/thing/1/", data={"description": "x"})

    assert "HTTP 400" in str(exc_info.value)
    assert "None" in str(exc_info.value)


def test_make_request_reports_the_real_status_code_for_non_field_errors():
    """The non_field_errors branch hardcoded "HTTP 400" regardless of the
    response's actual status code. A 409 (or any non-400 error the server
    returns through this same code path) should be reported as 409, not a
    fabricated 400.
    """
    api = _make_api({"status_code": 409, "json": {"non_field_errors": ["conflict"]}})

    with pytest.raises(AHAPIModuleError) as exc_info:
        api.make_request("PATCH", "https://hub.example.com/pulp/api/v3/thing/1/", data={"description": "x"})

    assert "409" in str(exc_info.value)
    assert "400" not in str(exc_info.value)


def test_make_request_reports_unknown_status_when_missing():
    """response.get("status_code") with no default renders as the literal
    string "None" when the key is absent, which reads like a bug in the
    error message itself. "unknown" is clearer.
    """
    api = _make_api({"json": {"weird_shape": True}})

    with pytest.raises(AHAPIModuleError) as exc_info:
        api.make_request("PATCH", "https://hub.example.com/pulp/api/v3/thing/1/", data={"description": "x"})

    assert "unknown" in str(exc_info.value)
    assert "None" not in str(exc_info.value)


def test_make_request_truncates_per_field_and_marks_it():
    """Per-field truncation keeps each value intact up to the limit so
    Ansible's no_log substring matching can still find and redact it.
    Truncating the whole stringified dict could split a credential value
    in half, defeating no_log.
    """
    huge_value = "x" * 5000
    api = _make_api({"status_code": 400, "json": {"some_field": huge_value}})

    with pytest.raises(AHAPIModuleError) as exc_info:
        api.make_request("PATCH", "https://hub.example.com/pulp/api/v3/thing/1/", data={"description": "x"})

    msg = str(exc_info.value)
    assert len(msg) < 1000
    assert "...(truncated)" in msg


def test_make_request_redacts_long_secret_before_truncating_error_body():
    """Redaction must happen before truncation so a long secret cannot leak a
    prefix that Ansible's complete-secret matching would miss.
    """
    secret = "s" * 300
    api = _make_api({"status_code": 400, "json": {"detail": "Token {0}".format(secret)}})
    api.no_log_values = {secret}

    with pytest.raises(AHAPIModuleError) as exc_info:
        api.make_request("PATCH", "https://hub.example.com/pulp/api/v3/thing/1/", data={"description": "x"})

    msg = str(exc_info.value)
    assert secret[:200] not in msg
    assert "********" in msg
