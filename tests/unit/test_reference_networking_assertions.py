from __future__ import annotations

import subprocess

import pytest

from tests.e2e.references.assertions import assert_disabled_create_error


@pytest.mark.parametrize("stream", ["stderr", "output"])
def test_disabled_create_error_accepts_exact_grpcurl_status(stream: str) -> None:
    error = subprocess.CalledProcessError(
        1,
        ["grpcurl", "sensitive-command"],
        **{stream: "ERROR:\n  Code: FailedPrecondition\n  Message: provider networking is disabled\n"},
    )
    assert_disabled_create_error(error)


@pytest.mark.parametrize(
    "output",
    [
        "",
        "connection refused",
        "Code: Unavailable\nMessage: provider networking is disabled",
        "Code: FailedPreconditionExtra\nMessage: provider networking is disabled",
        "Code: FailedPrecondition\nMessage: pool is not ready",
        "Code: FailedPrecondition\nMessage: provider networking is disabled unexpectedly",
        "Code: FailedPrecondition\nMessage: provider networking is",
        "Message: provider networking is disabled",
        "Code: FailedPrecondition",
        "Code: FailedPrecondition\nMessage: provider networking is disabled\nMessage: other",
    ],
)
def test_disabled_create_error_rejects_other_failures_without_leaking(output: str) -> None:
    error = subprocess.CalledProcessError(1, ["grpcurl", "sensitive-command"], stderr=output)
    with pytest.raises(AssertionError) as failure:
        assert_disabled_create_error(error)
    assert "sensitive-command" not in str(failure.value)
    if output:
        assert output not in str(failure.value)
