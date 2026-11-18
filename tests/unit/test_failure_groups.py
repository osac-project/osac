"""Classify actual selector cleanup groups without changing their failure semantics."""

from __future__ import annotations

import json
import logging
from pathlib import Path
from unittest.mock import Mock

import pytest

from tests.e2e.core.failure_report import FailureReporter
from tests.e2e.core.failures import E2EFailure, Kind, classify, tagged
from tests.e2e.core.runner import CommandFailed, StuckState
from tests.e2e.networking.vmaas.test_vmaas_networking import _selector_row_cleanup


@pytest.mark.parametrize("body_fails", [False, True])
@pytest.mark.parametrize("as_bytes", [False, True])
def test_selector_cleanup_groups_keep_primary_kind_in_saved_reports(
    body_fails: bool, as_bytes: bool, tmp_path: Path, caplog: pytest.LogCaptureFixture
) -> None:
    """Attempt all cleanups and report the first known body/cleanup branch with its own IDs."""
    body = E2EFailure(Kind.TRAFFIC_BLOCKED, "fixture traffic blocked", ids={"vm_id": "fixture-vm"})
    fixture_password = "fixture-group-password-value"
    detail = f"Code: PermissionDenied\nMessage: fixture cleanup refused at fixture.invalid; password={fixture_password}"
    raw = detail.encode() if as_bytes else detail
    command = CommandFailed(7, ["grpcurl", "fixture.invalid:443", "osac.private.v1.ExternalIPs/Delete"], stderr=raw)
    cleanup = tagged(command, Kind.API_REFUSED, {"eip_id": "fixture-eip"})
    other = E2EFailure(Kind.STUCK_STATE, "fixture stuck", ids={"other_id": "fixture-other"})
    actions = [Mock(side_effect=cleanup), Mock(side_effect=other)]
    with pytest.raises(BaseExceptionGroup) as caught, _selector_row_cleanup(actions):
        if body_fails:
            raise body
    error = caught.value
    assert error.exceptions == ((body, cleanup, other) if body_fails else (cleanup, other))
    for action in actions:
        action.assert_called_once_with()
    result = classify(error)
    assert result.kind is (Kind.TRAFFIC_BLOCKED if body_fails else Kind.API_REFUSED)
    assert result.ids == ({"vm_id": "fixture-vm"} if body_fails else {"eip_id": "fixture-eip"})
    assert ("fixture traffic blocked" if body_fails else "PermissionDenied") in result.summary
    reporter = FailureReporter(Mock())
    reporter.snapshotter = Mock()
    reporter.snapshotter.take.return_value = None
    reporter._path = tmp_path / "results-main.jsonl"
    with caplog.at_level(logging.ERROR):
        reporter.capture_fixture_failure(error, "fixture-row", 0)
    saved = json.loads(reporter._path.read_text())
    assert saved["kind"] == result.kind and saved["ids"] == result.ids
    assert result.summary in saved["summary"] and result.kind in caplog.text
    for rendered in (result.summary, caplog.text, reporter._path.read_text()):
        assert fixture_password not in rendered and "fixture.invalid" not in rendered
    assert command.stderr == raw and command.returncode == 7


def test_nested_groups_select_first_known_member_without_sibling_ids() -> None:
    """Skip unknown branches but never borrow IDs or gRPC evidence from another known branch."""
    selected = StuckState("fixture resource ready", "pending", 2)
    sibling = tagged(
        CommandFailed(7, ["osac", "delete"], stderr="Code: Unavailable\nMessage: other"),
        Kind.API_REFUSED,
        {"wrong": "fixture-other"},
    )
    error = ExceptionGroup("outer", [ValueError("unknown"), ExceptionGroup("inner", [selected, sibling])])
    result = classify(error)
    assert result.kind is Kind.STUCK_STATE and result.ids == {}
    assert "fixture resource ready" in result.summary and "other" not in result.summary
    assert "Unavailable" not in result.summary


def test_group_tags_and_ordinary_cause_precede_member_classification() -> None:
    """Keep existing explicit outer tags and cause-before-context contracts for groups too."""
    member = E2EFailure(Kind.API_REFUSED, "member", ids={"wrong": "fixture-member"})
    error = ExceptionGroup("outer", [member])
    cause = E2EFailure(Kind.TRAFFIC_BLOCKED, "cause", ids={"vm": "fixture-vm"})
    error.__cause__ = cause
    error.__context__ = StuckState("context", "pending", 1)
    assert classify(error).kind is Kind.TRAFFIC_BLOCKED
    assert classify(error).ids == cause.ids
    tagged(error, Kind.TEST_SSH_FAILED, {"host": "fixture-host"})
    assert classify(error).kind is Kind.TEST_SSH_FAILED
    assert classify(error).ids == {"host": "fixture-host"}


def test_group_unknown_alias_cycles_and_depth_remain_bounded() -> None:
    """Unknown groups and aliased cyclic causes terminate without reaching beyond the chain limit."""
    unknown = ValueError("fixture unknown")
    unknown.__cause__ = unknown
    assert classify(ExceptionGroup("unknown", [unknown, unknown])).kind is Kind.UNCLASSIFIED
    error: Exception = E2EFailure(Kind.API_REFUSED, "too deep")
    for _depth in range(6):
        error = ExceptionGroup("layer", [error])
    assert classify(error).kind is Kind.UNCLASSIFIED
    known = E2EFailure(Kind.ASSERTION, "fixture assertion")
    assert classify(ExceptionGroup("mixed", [unknown, unknown, known])).kind is Kind.ASSERTION


def test_group_alias_at_shallower_depth_can_still_supply_known_kind() -> None:
    """A depth-limited earlier alias must not hide the later reachable primary known member."""
    shared = ExceptionGroup("shared", [E2EFailure(Kind.TRAFFIC_BLOCKED, "fixture traffic")])
    deep = shared
    for _depth in range(4):
        deep = ExceptionGroup("layer", [deep])
    result = classify(ExceptionGroup("outer", [deep, shared]))
    assert result.kind is Kind.TRAFFIC_BLOCKED and "fixture traffic" in result.summary


def test_group_summary_keeps_existing_sanitized_length_limit() -> None:
    """Selected group evidence obeys the same credential scrubbing and size cap as plain errors."""
    fixture_password = "fixture-summary-password-value"
    error = ExceptionGroup("fixture", [AssertionError(f"password={fixture_password} " + "x" * 2000)])
    result = classify(error)
    assert result.kind is Kind.ASSERTION
    assert fixture_password not in result.summary and len(result.summary) == 1000
