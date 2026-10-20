"""Regression tests for CaaS transient failure tolerance.

Covers two defects surfaced by osac-test-infra GHA run 39391299393:

1. wait_for_cluster_ready must tolerate a transient "Failed" phase
   (the operator retries and the phase goes Failed -> Progressing ->
   Ready).  The test must NOT raise AssertionError on an intermediate
   Failed phase.

2. _assert_worker_aggregates must survive a transient kubectl
   CalledProcessError (e.g. "connection refused") by retrying through
   poll_until's retry_on_error path instead of terminating
   immediately.

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
