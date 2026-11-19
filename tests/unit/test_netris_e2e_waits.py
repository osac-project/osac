"""Waits added to the Netris VMaaS and BMaaS suites after flaky periodic runs.

Each case drives the real helper with faked SSH, gRPC, kubectl and time, and checks the one rule the wait exists
for: a state that has not converged yet is retried, and a broken transport never passes as blocked traffic.
"""

from __future__ import annotations

import subprocess
from collections.abc import Iterator
from types import SimpleNamespace
from typing import Any
from unittest.mock import Mock

import pytest

from tests.e2e.core import helpers, runner
from tests.e2e.core.failures import E2EFailure, Kind
from tests.e2e.core.k8s_client import K8sClient
from tests.e2e.core.runner import CommandTimedOut
from tests.e2e.core.ssh_probe import ProbeTransportError, false_on_transport_error
from tests.e2e.core.wait_kinds import WaitResourceKind
from tests.e2e.networking.bmaas import bmi_ssh
from tests.e2e.networking.vmaas import test_vmaas_networking as suite


@pytest.fixture
def clock(monkeypatch: pytest.MonkeyPatch) -> list[float]:
    """Advance polling time without sleeping."""
    elapsed = [0.0]

    def sleep(seconds: float) -> None:
        elapsed[0] += seconds

    monkeypatch.setattr(runner, "time", SimpleNamespace(sleep=sleep, monotonic=lambda: elapsed[0]))
    return elapsed


def _replies(*replies: object) -> Iterator[Any]:
    yield from replies


# --- runner ---


def test_retry_on_error_retries_a_command_timeout(clock: list[float]) -> None:
    """A grpcurl or kubectl call that ran past its timeout is a blip like a non-zero exit, not a failure."""
    replies = _replies(CommandTimedOut(["grpcurl", "x"], 30), "ready")

    def fn() -> str:
        reply = next(replies)
        if isinstance(reply, Exception):
            raise reply
        return reply

    result = runner.poll_until(
        fn=fn,
        until=lambda value: value == "ready",
        retries=3,
        delay=5,
        description="read after a timeout",
        retry_on_error=True,
        resource_kind=WaitResourceKind.EXTERNAL_IP,
        resource_id="eip",
    )
    assert result == "ready"
    assert clock[0] == 5


def test_timeout_names_the_last_command_timeout(clock: list[float]) -> None:
    def fn() -> str:
        raise CommandTimedOut(["grpcurl", "x"], 30)

    with pytest.raises(TimeoutError, match="timed out after 30s"):
        runner.poll_until(
            fn=fn,
            until=bool,
            retries=2,
            delay=5,
            description="always times out",
            retry_on_error=True,
            resource_kind=WaitResourceKind.EXTERNAL_IP,
            resource_id="eip",
        )


# --- probes ---


def test_false_on_transport_error_retries_only_transport_failures() -> None:
    logged: list[str] = []
    assert false_on_transport_error(Mock(side_effect=ProbeTransportError("lost")), logged.append)() is False
    assert logged and "lost" in logged[0]
    assert false_on_transport_error(Mock(return_value=True), logged.append)() is True
    with pytest.raises(ValueError):
        false_on_transport_error(Mock(side_effect=ValueError("bug")), logged.append)()


def test_bmi_probe_without_exit_marker_is_a_transport_error(monkeypatch: pytest.MonkeyPatch) -> None:
    """A refused SSH never ran the probe, so it must not read as an unreachable target."""
    monkeypatch.setattr(bmi_ssh, "ssh_bmi_unchecked", lambda *_a, **_k: ("Connection refused", 255))
    with pytest.raises(ProbeTransportError):
        bmi_ssh.arping("192.0.2.10", "192.0.2.2")


def test_vmaas_lost_helper_exec_is_a_transport_error(monkeypatch: pytest.MonkeyPatch) -> None:
    from tests.e2e.networking.vmaas import guest_ssh
    from tests.e2e.networking.vmaas.ssh_helper import HelperExecError

    monkeypatch.setattr(guest_ssh, "ssh_guest_unchecked", Mock(side_effect=HelperExecError("exec lost")))
    with pytest.raises(ProbeTransportError):
        guest_ssh.ping("ns", "vm", "/key", "192.0.2.2")


def test_vmaas_timed_out_helper_exec_is_a_transport_error(monkeypatch: pytest.MonkeyPatch) -> None:
    """A timed-out exec never ran the probe either, so a positive wait retries it instead of stopping."""
    from tests.e2e.networking.vmaas import guest_ssh, ssh_helper

    monkeypatch.setattr(ssh_helper, "ensure_helper", Mock())
    monkeypatch.setattr(ssh_helper, "vm_ip", Mock(return_value="172.16.1.6"))
    monkeypatch.setattr(ssh_helper, "_base", Mock(return_value=["kubectl"]))
    monkeypatch.setattr(ssh_helper.subprocess, "run", Mock(side_effect=subprocess.TimeoutExpired("kubectl", 30)))
    with pytest.raises(ssh_helper.HelperExecError, match="timed out after 30s"):
        ssh_helper.exec_ssh(namespace="ns", vm_name="vm", identity_file="/key", command="true", timeout=30)
    with pytest.raises(ProbeTransportError):
        guest_ssh.ping("ns", "vm", "/key", "192.0.2.2")


# --- BMaaS SSH readiness ---

_BMIS = [{"name": "bmi1", "id": "bmi-1", "ssh_host": "192.0.2.5"}, {"name": "bmi2", "id": "bmi-2", "ssh_host": "h2"}]


def test_ssh_ready_waits_through_a_booting_host(monkeypatch: pytest.MonkeyPatch, clock: list[float]) -> None:
    """sshd refuses for a while after the handoff power-on; the wait outlasts it and probes ready hosts once."""
    replies = {
        "192.0.2.5": _replies(("ssh: connect to host 192.0.2.5 port 22: Connection refused", 255), ("", 0)),
        "h2": _replies(("", 0)),
    }
    calls: list[str] = []

    def ssh(host: str, command: str, **_kwargs: object) -> tuple[str, int]:
        calls.append(host)
        assert command == "true"
        return next(replies[host])

    monkeypatch.setattr(bmi_ssh, "ssh_bmi_unchecked", ssh)
    bmi_ssh.wait_for_ssh_ready(_BMIS, retries=5, delay=10)
    assert calls == ["192.0.2.5", "h2", "192.0.2.5"]
    assert clock[0] == 10


def test_ssh_ready_fails_fast_on_a_rejected_key(monkeypatch: pytest.MonkeyPatch, clock: list[float]) -> None:
    monkeypatch.setattr(
        bmi_ssh, "ssh_bmi_unchecked", lambda *_a, **_k: ("fedora@192.0.2.5: Permission denied (publickey).", 255)
    )
    with pytest.raises(E2EFailure) as caught:
        bmi_ssh.wait_for_ssh_ready(_BMIS[:1], retries=60, delay=10)
    assert caught.value.kind is Kind.TEST_SSH_FAILED
    assert "Permission denied" in str(caught.value)
    assert clock[0] == 20  # three attempts, not the whole budget


def test_ssh_ready_timeout_names_each_host_and_its_last_error(
    monkeypatch: pytest.MonkeyPatch, clock: list[float]
) -> None:
    monkeypatch.setattr(
        bmi_ssh, "ssh_bmi_unchecked", lambda host, *_a, **_k: ("", 0) if host == "h2" else ("Connection refused", 255)
    )
    with pytest.raises(E2EFailure) as caught:
        bmi_ssh.wait_for_ssh_ready(_BMIS, retries=3, delay=10)
    assert caught.value.kind is Kind.STUCK_STATE
    assert "192.0.2.5: Connection refused" in str(caught.value)
    assert "h2" not in caught.value.ids["ssh_hosts"]


# --- VMaaS NAT precondition ---


def _vm() -> dict[str, str]:
    return {"id": "ci-a1", "ip": "172.16.1.6", "vm_namespace": "ns", "cr": "vm-a1"}


def test_no_egress_precondition_waits_for_a_deleted_nat_to_stop(
    monkeypatch: pytest.MonkeyPatch, clock: list[float]
) -> None:
    """The previous row's NAT still translates for a while: 200, then two blocked attempts in a row."""
    statuses = _replies(200, 0, 0)
    monkeypatch.setattr(suite.guest_ssh, "curl_status_checked", lambda *_a, **_k: next(statuses))
    suite._assert_no_egress_without_nat({"identity_file": "/key"}, _vm(), "tenant1")
    assert clock[0] == 20


def test_no_egress_precondition_never_counts_a_broken_probe_as_blocked(
    monkeypatch: pytest.MonkeyPatch, clock: list[float]
) -> None:
    replies = _replies(0, RuntimeError("SSH failed (rc=255)"), 0, 0)

    def curl(*_a: object, **_k: object) -> int:
        reply = next(replies)
        if isinstance(reply, Exception):
            raise reply
        return reply

    monkeypatch.setattr(suite.guest_ssh, "curl_status_checked", curl)
    suite._assert_no_egress_without_nat({"identity_file": "/key"}, _vm(), "tenant1")
    # blocked, broken (resets), blocked, blocked: the pair after the reset ends the wait.
    assert clock[0] == 30


def test_no_egress_precondition_fails_with_the_last_status(monkeypatch: pytest.MonkeyPatch, clock: list[float]) -> None:
    monkeypatch.setattr(suite.guest_ssh, "curl_status_checked", lambda *_a, **_k: 200)
    with pytest.raises(E2EFailure) as caught:
        suite._assert_no_egress_without_nat({"identity_file": "/key"}, _vm(), "tenant1")
    assert caught.value.kind is Kind.ASSERTION
    assert "last result: HTTP 200" in str(caught.value)


def test_no_egress_precondition_reports_a_probe_that_never_ran_as_ssh_failure(
    monkeypatch: pytest.MonkeyPatch, clock: list[float]
) -> None:
    """An HTTP 200 followed by broken probes must not be reported as egress that still exists."""
    replies = _replies(200, *[RuntimeError("SSH failed (rc=255)")] * 11)

    def curl(*_a: object, **_k: object) -> int:
        reply = next(replies)
        if isinstance(reply, Exception):
            raise reply
        return reply

    monkeypatch.setattr(suite.guest_ssh, "curl_status_checked", curl)
    with pytest.raises(E2EFailure) as caught:
        suite._assert_no_egress_without_nat({"identity_file": "/key"}, _vm(), "tenant1")
    assert caught.value.kind is Kind.TEST_SSH_FAILED
    assert "did not run" in str(caught.value)
    assert "Last HTTP result: HTTP 200" in str(caught.value)


# --- VMaaS ExternalIP ingress stop ---


def test_ingress_stop_needs_three_failures_in_a_row(monkeypatch: pytest.MonkeyPatch, clock: list[float]) -> None:
    """One dropped SSH on a path that still works must not count as the attachment being gone."""
    timeout = subprocess.TimeoutExpired("ssh", 20)
    replies = _replies(timeout, "vm-a1", timeout, timeout, timeout)

    def ssh(*_a: object, **_k: object) -> str:
        reply = next(replies)
        if isinstance(reply, BaseException):
            raise reply
        return reply

    monkeypatch.setattr(suite.guest_ssh, "ssh_via_external_ip", ssh)
    suite._wait_external_ssh_stopped(
        state={"identity_file": "/key"},
        vm=_vm(),
        address="198.51.100.26",
        host_key="ssh-ed25519 AAAA",
        description="SSH stops",
        ids={},
    )
    assert clock[0] == 40


@pytest.mark.parametrize(
    "failure",
    [
        subprocess.CalledProcessError(255, "ssh", stderr="fedora@198.51.100.26: Permission denied (publickey)."),
        subprocess.CalledProcessError(255, "ssh", stderr="Host key verification failed."),
        FileNotFoundError("ssh"),
    ],
)
def test_ingress_stop_does_not_count_an_access_failure_as_closed(
    monkeypatch: pytest.MonkeyPatch, clock: list[float], failure: BaseException
) -> None:
    """A rejected key, a host-key mismatch or ssh failing to start cannot show that the path closed."""
    monkeypatch.setattr(suite.guest_ssh, "ssh_via_external_ip", Mock(side_effect=failure))
    with pytest.raises(E2EFailure) as caught:
        suite._wait_external_ssh_stopped(
            state={"identity_file": "/key"},
            vm=_vm(),
            address="198.51.100.26",
            host_key="ssh-ed25519 AAAA",
            description="SSH stops",
            ids={},
        )
    assert caught.value.kind is Kind.TEST_SSH_FAILED
    assert clock[0] == 0


# --- deletion waits ---


def test_deletion_wait_does_not_read_a_kubectl_error_as_gone(
    monkeypatch: pytest.MonkeyPatch, clock: list[float]
) -> None:
    """A failed read used to end the wait as if the object were gone; now only an exact NotFound does."""
    read = Mock(
        side_effect=[
            ("Unable to connect to the server: net/http: TLS handshake timeout", 1),
            ("natgateway-x present", 0),
            ('Error from server (NotFound): natgateways.osac.openshift.io "natgateway-x" not found', 1),
        ]
    )
    monkeypatch.setattr("tests.e2e.core.k8s_client.run_unchecked", read)
    helpers.wait_for_nat_gateway_deletion(k8s=K8sClient(namespace="test", as_system_admin=False), name="natgateway-x")
    assert read.call_count == 3
    assert clock[0] == 10


def test_teardown_does_not_read_a_kubectl_error_as_cr_gone(monkeypatch: pytest.MonkeyPatch, clock: list[float]) -> None:
    """The API record is gone and a CR list fails: the teardown waits for a list that succeeds and finds nothing."""
    from tests.e2e.networking import teardown

    lists = Mock(
        side_effect=[
            runner.CommandFailed(1, ["kubectl"], output="", stderr="Unable to connect to the server"),
            "externalip-x",
            "",
        ]
    )
    monkeypatch.setattr("tests.e2e.core.k8s_client.run", lists)
    grpc = Mock()
    grpc.list_external_ip_ids.return_value = []
    teardown.teardown_external_ip(grpc, K8sClient(namespace="test", as_system_admin=False), "eip-1", "billing")
    assert lists.call_count == 3
    assert "osac.openshift.io/externalip-uuid=eip-1" in lists.call_args.args


def test_sanitizer_drops_base64_cloud_init_user_data() -> None:
    from tests.e2e.core.sanitize import sanitize_obj

    cleaned = sanitize_obj({"cloudInitNoCloud": {"userDataBase64": "c2VjcmV0", "networkData": "keep"}})
    assert cleaned == {"cloudInitNoCloud": {"networkData": "keep"}}


def test_billing_external_ip_is_torn_down_when_its_allocation_wait_fails(monkeypatch: pytest.MonkeyPatch) -> None:
    grpc = Mock()
    grpc.create_external_ip.return_value = "eip-1"
    monkeypatch.setattr(suite, "wait_for_external_ip_cr", Mock(return_value="externalip-x"))
    monkeypatch.setattr(suite, "wait_for_external_ip_allocated", Mock(side_effect=TimeoutError("never allocated")))
    teardown = Mock()
    monkeypatch.setattr(suite, "teardown_external_ip", teardown)
    fixture = suite.billing_external_ip.__wrapped__(grpc, Mock(), {"pool": {"id": "pool-1"}}, "project")
    with pytest.raises(TimeoutError):
        next(fixture)
    teardown.assert_called_once()
    assert teardown.call_args.args[2] == "eip-1"
