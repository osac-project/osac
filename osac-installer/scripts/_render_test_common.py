"""Facts shared by the installer render tests.

Import this as ``_render_test_common``: the tests run as standalone scripts, so
their own directory is on sys.path.
"""

from __future__ import annotations

from typing import Any

# Chart hook Jobs that were removed on purpose. A render test must never assert
# one of these again; it should assert the retired Job stays absent instead, and
# cover the replacement (the render-time preflight engine, or another chart).
RETIRED_HOOK_JOBS = frozenset({"osac-pre-install-validate"})  # allow-retired-reference


def retired_hook_jobs(docs: list[dict[str, Any]]) -> list[str]:
    """Names of retired hook Jobs that a render still produced."""
    return [
        doc["metadata"]["name"]
        for doc in docs
        if doc.get("kind") == "Job" and doc.get("metadata", {}).get("name") in RETIRED_HOOK_JOBS
    ]


def assert_no_retired_hook_jobs(docs: list[dict[str, Any]]) -> None:
    rendered = retired_hook_jobs(docs)
    assert not rendered, (
        f"retired hook Job(s) rendered again: {', '.join(sorted(rendered))}. "
        f"See RETIRED_HOOK_JOBS in {__file__} before reviving one."
    )
