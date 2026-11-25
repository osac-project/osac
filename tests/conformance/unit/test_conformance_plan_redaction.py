"""Plan redaction tests for fields other than test IDs and names."""

from __future__ import annotations

from pathlib import Path

from tests.conformance.model import Classification, Plan, Selector, TestIdentity
from tests.conformance.output import write_plan
from tests.conformance.redact import Redactor

_REDACTION_MARKER = "redaction-marker"


def test_write_plan_redacts_profile_selector_and_marker_fields(tmp_path: Path) -> None:
    test = TestIdentity(
        id="TC-1",
        name="TC-1: test",
        nodeid="tests/e2e/test_tc.py::test_tc",
        labels=frozenset({f"conformance_{_REDACTION_MARKER}"}),
        profiles=(f"profile-{_REDACTION_MARKER}",),
        classification=Classification.BLOCKING,
    )
    plan = Plan(
        (test,),
        (f"generic-{_REDACTION_MARKER}",),
        (Selector(f"selector-{_REDACTION_MARKER}", "marker", (f"conformance_{_REDACTION_MARKER}",)),),
        True,
    )

    path = write_plan(plan, tmp_path, Redactor(secrets=(_REDACTION_MARKER,)))

    text = path.read_text()
    assert _REDACTION_MARKER not in text
    assert "<redacted>" in text
    assert "key:" in text
