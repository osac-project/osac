"""Regression tests for CaaS transient failure tolerance.

Covers two defects surfaced by osac-test-infra GHA run 39391299393:

1. wait_for_cluster_ready must tolerate a transient "Failed" phase
   (the operator retries and the phase goes Failed -> Progressing ->
   Ready).  The test must NOT raise AssertionError on an intermediate
   Failed phase.

2. _assert_worker_aggregates must survive a transient kubectl
   CalledProcessError (e.g. "connection refused") by retrying through
   poll_until's retry_on_error path instead of terminating
   immediately.  Permanent errors (invalid namespace, authorization,
   unknown resource) must propagate immediately with the original
   diagnostic.

See osac-project/osac-test-infra#470.
"""

from __future__ import annotations

import subprocess
from unittest.mock import Mock

import pytest

from tests.e2e.caas.sanity import test_cluster_create
from tests.e2e.core import helpers, runner

# -- Fix 1: wait_for_cluster_ready tolerates transient Failed phase --


def test_wait_for_cluster_ready_tolerates_transient_failed(monkeypatch: pytest.MonkeyPatch) -> None:
    """A transient Failed phase followed by Ready must not raise."""
    k8s = Mock()
    k8s.get_cluster_order_phase.side_effect = ["Progressing", "Failed", "Progressing", "Ready"]
    monkeypatch.setattr(runner.time, "sleep", lambda _: None)

    helpers.wait_for_cluster_ready(k8s=k8s, name="order-abc")

    assert k8s.get_cluster_order_phase.call_count == 4


def test_wait_for_cluster_ready_times_out_when_never_ready(monkeypatch: pytest.MonkeyPatch) -> None:
    """If Ready is never reached, the poll times out normally."""
    k8s = Mock()
    k8s.get_cluster_order_phase.return_value = "Failed"
    monkeypatch.setattr(runner.time, "sleep", lambda _: None)

    with pytest.raises(TimeoutError):
        # Use a small retries count to keep the test fast.
        monkeypatch.setattr(helpers, "poll_until", lambda **kw: runner.poll_until(**{**kw, "retries": 3, "delay": 0}))
        helpers.wait_for_cluster_ready(k8s=k8s, name="order-stuck")


# -- Fix 2: _assert_worker_aggregates retries transient errors -------


def test_assert_worker_aggregates_retries_transient_kubectl_error(monkeypatch: pytest.MonkeyPatch) -> None:
    """A transient CalledProcessError must be retried, not fatal."""
    call_count = 0

    def fake_get_json(*, resource: str, name: str) -> dict:
        nonlocal call_count
        call_count += 1
        if call_count == 1:
            raise subprocess.CalledProcessError(
                1,
                ("kubectl", "get", "clusterorder", name),
                stderr=("The connection to the server api.cluster.example.com:6443 was refused"),
            )
        return {"status": {"desiredWorkers": "2", "currentWorkers": "2", "readyWorkers": "2"}}

    k8s = Mock()
    k8s.get_json = Mock(side_effect=fake_get_json)
    monkeypatch.setattr(runner.time, "sleep", lambda _: None)

    test_cluster_create._assert_worker_aggregates(k8s=k8s, co_name="order-xyz", expected_workers=2)

    assert call_count == 2, "_assert_worker_aggregates must retry after a transient error"


# -- Fix 2b: permanent errors propagate immediately ------------------


@pytest.mark.parametrize(
    ("stderr_text", "expected_category"),
    [
        pytest.param('Error from server (NotFound): namespaces "gone" not found', "not-found", id="invalid-namespace"),
        pytest.param(
            "Error from server (Forbidden): clusterorders.osac is forbidden: User cannot get resource",
            "forbidden",
            id="forbidden",
        ),
        pytest.param(
            'error: the server doesn\'t have a resource type "clusterorder"', "unknown-resource", id="unknown-resource"
        ),
        pytest.param("error: You must be logged in to the server (Unauthorized)", "unauthorized", id="unauthorized"),
    ],
)
def test_assert_worker_aggregates_propagates_permanent_error(
    monkeypatch: pytest.MonkeyPatch, stderr_text: str, expected_category: str
) -> None:
    """Permanent kubectl errors must propagate immediately.

    They must NOT be retried for ~20 minutes and then masked by a
    generic TimeoutError.  The raised RuntimeError must carry the
    return code and a safe category but never the raw command output
    (runner.py suppression policy).
    """

    def always_fail(*, resource: str, name: str) -> dict:
        raise subprocess.CalledProcessError(1, ("kubectl", "get", "clusterorder", name), stderr=stderr_text)

    k8s = Mock()
    k8s.get_json = Mock(side_effect=always_fail)
    monkeypatch.setattr(runner.time, "sleep", lambda _: None)

    with pytest.raises(RuntimeError) as exc_info:
        test_cluster_create._assert_worker_aggregates(k8s=k8s, co_name="order-bad", expected_workers=2)

    msg = str(exc_info.value)
    assert msg == f"permanent kubectl failure (rc=1, category={expected_category})"

    # Must fail on the very first attempt, not after exhausting retries.
    assert k8s.get_json.call_count == 1

    # The chained CalledProcessError must be suppressed so raw
    # command output never leaks through the exception chain.
    assert exc_info.value.__cause__ is None
