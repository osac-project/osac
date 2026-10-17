"""Regression tests for _safe_delete_* fixture teardown helpers.

Each helper's contract states that errors are logged rather than
raised so that remaining teardown fixtures can still run.  Before
the fix, TimeoutError from the wait_for_*_deletion calls propagated
unhandled, violating this contract and causing spurious
ExceptionGroup test errors (e.g. the test_subnet_lifecycle teardown
failure in run 39040205073).
"""

from __future__ import annotations

import contextlib
import logging
import subprocess
from unittest.mock import Mock, patch

import pytest

from tests.e2e.core import runner
from tests.e2e.core.grpc_client import GRPCClient
from tests.e2e.core.k8s_client import K8sClient
from tests.e2e.references import conftest as ref_conftest
from tests.e2e.references.conftest import _safe_delete_sg, _safe_delete_subnet, _safe_delete_vn


@pytest.fixture()
def _no_sleep(monkeypatch: pytest.MonkeyPatch) -> None:
    """Eliminate real sleeps from poll_until during unit tests."""
    monkeypatch.setattr(runner.time, "sleep", lambda _: None)


# -- _safe_delete_vn ------------------------------------------------


@pytest.mark.usefixtures("_no_sleep")
def test_safe_delete_vn_suppresses_deletion_wait_timeout() -> None:
    """TimeoutError from wait_for_virtual_network_deletion
    must be caught and logged, not propagated."""
    grpc = Mock(spec=GRPCClient)
    k8s = Mock(spec=K8sClient)
    k8s.is_present.return_value = True

    with (
        patch(
            "tests.e2e.references.conftest.wait_for_virtual_network_deletion",
            side_effect=TimeoutError("timed out after 595s"),
        ),
        patch("tests.e2e.references.conftest._cleanup_child_nat_gateways"),
    ):
        # Must not raise
        _safe_delete_vn(grpc, k8s, vn_id="vn-1", vn_cr_name="vn-cr-1")

    grpc.delete_virtual_network.assert_called_once_with(vn_id="vn-1")


@pytest.mark.usefixtures("_no_sleep")
def test_safe_delete_vn_still_attempts_delete_before_wait() -> None:
    """The gRPC delete must still be called even when
    the wait will time out."""
    grpc = Mock(spec=GRPCClient)
    k8s = Mock(spec=K8sClient)
    k8s.is_present.return_value = True

    with (
        patch("tests.e2e.references.conftest.wait_for_virtual_network_deletion", side_effect=TimeoutError("stuck")),
        patch("tests.e2e.references.conftest._cleanup_child_nat_gateways"),
    ):
        _safe_delete_vn(grpc, k8s, vn_id="vn-2", vn_cr_name="vn-cr-2")

    grpc.delete_virtual_network.assert_called_once_with(vn_id="vn-2")


@pytest.mark.usefixtures("_no_sleep")
def test_safe_delete_vn_suppresses_generic_exception() -> None:
    """Any exception from the deletion wait must be suppressed."""
    grpc = Mock(spec=GRPCClient)
    k8s = Mock(spec=K8sClient)
    k8s.is_present.return_value = True

    with (
        patch(
            "tests.e2e.references.conftest.wait_for_virtual_network_deletion",
            side_effect=RuntimeError("unexpected k8s error"),
        ),
        patch("tests.e2e.references.conftest._cleanup_child_nat_gateways"),
    ):
        _safe_delete_vn(grpc, k8s, vn_id="vn-3", vn_cr_name="vn-cr-3")


# -- _safe_delete_subnet --------------------------------------------


@pytest.mark.usefixtures("_no_sleep")
def test_safe_delete_subnet_suppresses_deletion_wait_timeout() -> None:
    """TimeoutError from wait_for_subnet_deletion
    must be caught and logged, not propagated."""
    grpc = Mock(spec=GRPCClient)
    k8s = Mock(spec=K8sClient)
    k8s.is_present.return_value = True

    with patch(
        "tests.e2e.references.conftest.wait_for_subnet_deletion", side_effect=TimeoutError("timed out after 595s")
    ):
        _safe_delete_subnet(grpc, k8s, subnet_id="sub-1", subnet_cr_name="sub-cr-1")

    grpc.delete_subnet.assert_called_once_with(subnet_id="sub-1")


@pytest.mark.usefixtures("_no_sleep")
def test_safe_delete_subnet_still_attempts_delete() -> None:
    """The gRPC delete must be called before the wait."""
    grpc = Mock(spec=GRPCClient)
    k8s = Mock(spec=K8sClient)
    k8s.is_present.return_value = True

    with patch("tests.e2e.references.conftest.wait_for_subnet_deletion", side_effect=TimeoutError("stuck")):
        _safe_delete_subnet(grpc, k8s, subnet_id="sub-2", subnet_cr_name="sub-cr-2")

    grpc.delete_subnet.assert_called_once_with(subnet_id="sub-2")


# -- _safe_delete_sg -------------------------------------------------


@pytest.mark.usefixtures("_no_sleep")
def test_safe_delete_sg_suppresses_deletion_wait_timeout() -> None:
    """TimeoutError from wait_for_security_group_deletion
    must be caught and logged, not propagated."""
    grpc = Mock(spec=GRPCClient)
    k8s = Mock(spec=K8sClient)
    k8s.is_present.return_value = True

    with patch(
        "tests.e2e.references.conftest.wait_for_security_group_deletion",
        side_effect=TimeoutError("timed out after 595s"),
    ):
        _safe_delete_sg(grpc, k8s, sg_id="sg-1", sg_cr_name="sg-cr-1")

    grpc.delete_security_group.assert_called_once_with(sg_id="sg-1")


@pytest.mark.usefixtures("_no_sleep")
def test_safe_delete_sg_still_attempts_delete() -> None:
    """The gRPC delete must be called before the wait."""
    grpc = Mock(spec=GRPCClient)
    k8s = Mock(spec=K8sClient)
    k8s.is_present.return_value = True

    with patch("tests.e2e.references.conftest.wait_for_security_group_deletion", side_effect=TimeoutError("stuck")):
        _safe_delete_sg(grpc, k8s, sg_id="sg-2", sg_cr_name="sg-cr-2")

    grpc.delete_security_group.assert_called_once_with(sg_id="sg-2")


# -- NotFound gRPC errors still handled correctly -------------------


@pytest.mark.usefixtures("_no_sleep")
def test_safe_delete_subnet_notfound_still_waits_for_cr() -> None:
    """When gRPC returns NotFound the CR may still exist in
    K8s, so the deletion wait must still run."""
    grpc = Mock(spec=GRPCClient)
    grpc.delete_subnet.side_effect = subprocess.CalledProcessError(returncode=1, cmd="grpcurl", stderr="Code: NotFound")
    k8s = Mock(spec=K8sClient)
    k8s.is_present.return_value = True

    with patch("tests.e2e.references.conftest.wait_for_subnet_deletion") as mock_wait:
        _safe_delete_subnet(grpc, k8s, subnet_id="sub-nf", subnet_cr_name="sub-cr-nf")

    mock_wait.assert_called_once_with(k8s=k8s, name="sub-cr-nf")


# -- Warning logs must not include runtime values -------------------


@pytest.mark.usefixtures("_no_sleep")
@pytest.mark.parametrize(
    ("helper", "wait_target", "extra_patches", "kwargs"),
    [
        (
            _safe_delete_vn,
            "wait_for_virtual_network_deletion",
            ["_cleanup_child_nat_gateways"],
            {"vn_id": "vn-s", "vn_cr_name": "vn-cr-s"},
        ),
        (_safe_delete_subnet, "wait_for_subnet_deletion", [], {"subnet_id": "sub-s", "subnet_cr_name": "sub-cr-s"}),
        (_safe_delete_sg, "wait_for_security_group_deletion", [], {"sg_id": "sg-s", "sg_cr_name": "sg-cr-s"}),
    ],
    ids=["vn", "subnet", "sg"],
)
def test_safe_delete_warnings_log_no_runtime_values(
    caplog: pytest.LogCaptureFixture, helper: object, wait_target: str, extra_patches: list[str], kwargs: dict[str, str]
) -> None:
    """Teardown warnings must be fully constant strings with
    no exception-derived or API-derived content so that
    sensitive data stays out of CI logs."""
    secret_msg = "secret-api-response-payload"
    grpc = Mock(spec=GRPCClient)
    k8s = Mock(spec=K8sClient)
    k8s.is_present.return_value = True

    prefix = "tests.e2e.references.conftest"

    with caplog.at_level(logging.WARNING, logger=ref_conftest.logger.name), contextlib.ExitStack() as stack:
        stack.enter_context(patch(f"{prefix}.{wait_target}", side_effect=TimeoutError(secret_msg)))
        for name in extra_patches:
            stack.enter_context(patch(f"{prefix}.{name}"))
        helper(grpc, k8s, **kwargs)  # type: ignore[operator]

    assert "timed out" in caplog.text
    assert secret_msg not in caplog.text
    assert "TimeoutError" not in caplog.text
    # CR names come from K8s API responses; must not appear.
    for v in kwargs.values():
        assert v not in caplog.text
