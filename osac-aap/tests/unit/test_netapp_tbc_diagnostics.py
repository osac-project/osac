"""Contracts for the NetApp integration target's failed PATCH diagnostics."""

import json
import warnings
from pathlib import Path
from types import SimpleNamespace

import pytest
from ansible.executor.task_result import TaskResult

STRATEGY_PLUGINS = Path(__file__).resolve().parents[1] / "integration/strategy_plugins"
UPDATE_TASK = (
    "osac.templates.netapp_storage : Update the TridentBackendConfig "
    "for dispatched backend"
)


@pytest.fixture
def diagnostic(monkeypatch):
    monkeypatch.syspath_prepend(str(STRATEGY_PLUGINS))
    from netapp_tbc_diagnostics import diagnostic_for_result

    return diagnostic_for_result


def task_result(return_data, *, name=UPDATE_TASK, action="kubernetes.core.k8s"):
    task = SimpleNamespace(get_name=lambda: name, action=action)
    return TaskResult(None, task, return_data, task_fields={})


def status_body(**overrides):
    return json.dumps(
        {
            "apiVersion": "v1",
            "kind": "Status",
            "status": "Failure",
            "code": 422,
            "reason": "Invalid",
            "message": "synthetic message that must stay redacted",
            "details": {"name": "synthetic-object"},
        }
        | overrides
    )


@pytest.mark.parametrize("bytes_repr", [False, True])
def test_failed_patch_prints_only_allowlisted_fields(diagnostic, bytes_repr):
    body = status_body()
    wrapped = repr(body.encode()) if bytes_repr else body
    result = task_result(
        {
            "failed": True,
            "failed_when_result": True,
            "_ansible_no_log": True,
            "reason": "Unprocessable Entity",
            "msg": f"Failed to patch object: {wrapped}",
            "invocation": {"module_args": {"definition": "synthetic-payload"}},
        }
    )

    assert diagnostic(result) == (
        "NetApp TBC update failed (HTTP status=422, Kubernetes reason=Invalid)"
    )


@pytest.mark.parametrize(
    "data, expected",
    [
        (
            {"msg": "Failed to patch object: " + status_body(reason="Invalid\nextra")},
            "HTTP status=422, Kubernetes reason=unavailable",
        ),
        (
            {"msg": "Failed to patch object: " + status_body(code="422")},
            "HTTP status=unavailable, Kubernetes reason=Invalid",
        ),
        (
            {"msg": "Failed to patch object: " + status_body(code=200)},
            "HTTP status=unavailable, Kubernetes reason=Invalid",
        ),
        (
            {"msg": "Failed to patch object: " + status_body(code=True)},
            "HTTP status=unavailable, Kubernetes reason=Invalid",
        ),
        (
            {"msg": "Failed to patch object: " + status_body(reason=["Invalid"])},
            "HTTP status=422, Kubernetes reason=unavailable",
        ),
        (
            {"msg": "Failed to patch object: " + status_body(kind="Other")},
            "HTTP status=unavailable, Kubernetes reason=unavailable",
        ),
        (
            {"msg": "Failed to patch object: " + status_body(status="Success")},
            "HTTP status=unavailable, Kubernetes reason=unavailable",
        ),
        (
            {"msg": "Failed to patch object: b'unterminated"},
            "HTTP status=unavailable, Kubernetes reason=unavailable",
        ),
        (
            {"msg": "Failed to patch object: " + "x" * (16 * 1024 + 1)},
            "HTTP status=unavailable, Kubernetes reason=unavailable",
        ),
        (
            {"msg": "Failed to retrieve requested object: " + status_body()},
            "HTTP status=unavailable, Kubernetes reason=unavailable",
        ),
        (
            {"msg": {"exception": "synthetic"}, "reason": "Invalid", "code": 422},
            "HTTP status=unavailable, Kubernetes reason=unavailable",
        ),
    ],
)
def test_error_fields_have_fixed_fallbacks(diagnostic, data, expected):
    assert diagnostic(task_result(data | {"failed": True})) == (
        f"NetApp TBC update failed ({expected})"
    )


def test_malformed_bytes_literal_cannot_emit_parser_warnings(diagnostic):
    result = task_result({"failed": True, "msg": r"Failed to patch object: b'\q'"})
    with warnings.catch_warnings(record=True) as caught:
        warnings.simplefilter("always")
        assert diagnostic(result) == (
            "NetApp TBC update failed (HTTP status=unavailable, Kubernetes reason=unavailable)"
        )

    assert caught == []


@pytest.mark.parametrize(
    ("data", "scope"),
    [
        ({"changed": True, "failed": False}, {}),
        ({"skipped": True}, {}),
        ({"failed": True, "failed_when_result": False, "reason": "Conflict"}, {}),
        ({"failed": True}, {"name": UPDATE_TASK.replace("Update", "Create")}),
        ({"failed": True}, {"name": UPDATE_TASK.replace("netapp_storage", "other")}),
        ({"failed": True}, {"action": "ansible.builtin.fail"}),
    ],
)
def test_diagnostics_are_scoped_to_failed_netapp_updates(diagnostic, data, scope):
    assert diagnostic(task_result(data, **scope)) is None
