from __future__ import annotations

from tests.e2e.networking.vmaas.cleanup import _run_cleanup


def test_cleanup_records_safe_error_and_continues_with_remaining_resources() -> None:
    cleanup_errors: list[str] = []
    cleaned: list[str] = []

    def fail_cleanup() -> None:
        raise RuntimeError("sensitive provider response")

    _run_cleanup(cleanup_errors, "ComputeInstance test-id", fail_cleanup)
    _run_cleanup(cleanup_errors, "Subnet test-id", lambda: cleaned.append("subnet"))

    assert cleanup_errors == ["ComputeInstance test-id: RuntimeError"]
    assert cleaned == ["subnet"]
