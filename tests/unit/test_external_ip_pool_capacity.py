from __future__ import annotations

from types import SimpleNamespace
from unittest.mock import Mock, call

import pytest

from tests.e2e.core import helpers, runner
from tests.e2e.core.k8s_client import K8sClient


@pytest.fixture
def clock(monkeypatch: pytest.MonkeyPatch) -> list[float]:
    """Advance polling time without sleeping or changing Python's shared time module."""
    elapsed = [0.0]

    def sleep(seconds: float) -> None:
        """Charge each polling delay to the runner-local fake clock."""
        elapsed[0] += seconds

    monkeypatch.setattr(runner, "time", SimpleNamespace(sleep=sleep, monotonic=lambda: elapsed[0]))
    return elapsed


@pytest.mark.parametrize("absent", [False, True], ids=["present", "canonical-not-found"])
def test_external_ip_deletion_requires_confirmed_absence(
    monkeypatch: pytest.MonkeyPatch, clock: list[float], absent: bool
) -> None:
    """Accept only exact canonical NotFound; a successful read must keep waiting."""
    output = (
        'Error from server (NotFound): externalips.osac.openshift.io "cr-first" not found'
        if absent
        else "resource still present"
    )
    read = Mock(return_value=(output, 1 if absent else 0))
    monkeypatch.setattr("tests.e2e.core.k8s_client.run_unchecked", read)
    k8s = K8sClient(namespace="test", as_system_admin=False)

    if absent:
        helpers.wait_for_external_ip_deletion(k8s=k8s, name="cr-first")
    else:
        with pytest.raises(TimeoutError, match="not reached after 595s"):
            helpers.wait_for_external_ip_deletion(k8s=k8s, name="cr-first")

    assert clock[0] == (0 if absent else 595)
    assert read.call_args_list == [
        call("kubectl", "get", "externalips.osac.openshift.io", "cr-first", "-n", "test")
    ] * (1 if absent else 120)


def test_external_ip_deletion_accepts_present_then_qualified_not_found(
    monkeypatch: pytest.MonkeyPatch, clock: list[float]
) -> None:
    """Recognize real kubectl's qualified NotFound only after the present IP disappears."""
    read = Mock(
        side_effect=[
            ("resource still present", 0),
            ('Error from server (NotFound): externalips.osac.openshift.io "cr-first" not found', 1),
        ]
    )
    monkeypatch.setattr("tests.e2e.core.k8s_client.run_unchecked", read)

    helpers.wait_for_external_ip_deletion(k8s=K8sClient(namespace="test", as_system_admin=False), name="cr-first")

    assert clock[0] == 5
    assert (
        read.call_args_list == [call("kubectl", "get", "externalips.osac.openshift.io", "cr-first", "-n", "test")] * 2
    )
