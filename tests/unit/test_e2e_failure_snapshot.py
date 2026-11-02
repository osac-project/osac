"""Snapshot sources, budget, cap and sanitizing, plus the kinds the VMaaS helpers attach to failures."""

from __future__ import annotations

import json
import shlex
import subprocess
import sys
import time
from collections.abc import Callable
from datetime import UTC, datetime
from pathlib import Path
from unittest.mock import Mock

import pytest

from tests.e2e.core import failure_snapshot as snap
from tests.e2e.core.failures import Kind, classify
from tests.e2e.core.grpc_client import PRIVATE_API
from tests.e2e.core.runner import CommandFailed
from tests.e2e.core.wait_kinds import WaitResourceKind
from tests.e2e.networking.vmaas import guest_ssh, stack
from tests.e2e.networking.vmaas.ssh_helper import HelperPodError

JWT = ".".join(["eyJhbGciOiJSUzI1NiJ9", "eyJzdWIiOiJ0ZXN0In0", "c2lnbmF0dXJl"])
START = datetime(2026, 1, 2, 3, 4, 5, tzinfo=UTC)


def take(tmp_path: Path, sources: snap.SnapshotSources, kind: Kind = Kind.ASSERTION, **options: float) -> Path:
    snapshotter = snap.Snapshotter(tmp_path, "main", sources, **options)
    return tmp_path / snapshotter.take("test_x", kind, START, {"nodeid": "n"})


def test_failure_json_is_written_and_sanitized(tmp_path: Path) -> None:
    directory = take(tmp_path, snap.SnapshotSources())
    record = json.loads((directory / "failure.json").read_text())
    assert record["test"] == "test_x"
    assert record["snapshot"] == "failures/main/01-test_x"
    assert (directory.stat().st_mode & 0o777) == 0o700


def test_kinds_limit_a_source(tmp_path: Path) -> None:
    sources = snap.SnapshotSources()
    sources.register("any", lambda d, w: snap.write_text(d, "any.txt", "a"))
    sources.register("guest", lambda d, w: snap.write_text(d, "guest.txt", "g"), kinds=snap.GUEST_KINDS)
    api = take(tmp_path / "a", sources, Kind.API_REFUSED)
    traffic = take(tmp_path / "b", sources, Kind.TRAFFIC_BLOCKED)
    assert (api / "any.txt").exists() and not (api / "guest.txt").exists()
    assert (traffic / "any.txt").exists() and (traffic / "guest.txt").exists()


def test_everything_written_is_sanitized(tmp_path: Path) -> None:
    sources = snap.SnapshotSources()
    sources.register("raw", lambda d, w: (d / "raw.txt").write_text(f"Authorization: Bearer {JWT}"))
    sources.register("json", lambda d, w: snap.write_json(d, "o.json", {"spec": {"userData": "x"}, "n": JWT}))
    directory = take(tmp_path, sources)
    for name in ("raw.txt", "o.json"):
        assert JWT not in (directory / name).read_text()
    assert "userData" not in (directory / "o.json").read_text()


def test_a_raising_source_is_recorded_and_others_still_run(tmp_path: Path) -> None:
    def boom(directory: Path, window: snap.Window) -> None:
        raise RuntimeError(f"failed with Bearer {JWT}")

    sources = snap.SnapshotSources()
    sources.register("boom", boom)
    sources.register("ok", lambda d, w: snap.write_text(d, "ok.txt", "ok"))
    directory = take(tmp_path, sources)
    assert (directory / "ok.txt").exists()
    errors = (directory / snap.ERRORS_FILE).read_text()
    assert "boom: RuntimeError" in errors and JWT not in errors


def test_budget_expiry_returns_without_waiting_and_names_the_source(tmp_path: Path) -> None:
    sources = snap.SnapshotSources()
    sources.register("slow", lambda d, w: time.sleep(2))
    began = time.monotonic()
    directory = take(tmp_path, sources, budget_s=0.2)
    assert time.monotonic() - began < 1.5
    assert "unfinished sources: slow" in (directory / snap.ERRORS_FILE).read_text()


def test_at_most_twelve_snapshots_per_worker(tmp_path: Path) -> None:
    snapshotter = snap.Snapshotter(tmp_path, "gw0", snap.SnapshotSources())
    results = [snapshotter.take(f"t{n}", Kind.ASSERTION, START) for n in range(14)]
    assert results[11] == "failures/gw0/12-t11"
    assert results[12:] == [snap.CAP_REACHED, snap.CAP_REACHED]
    assert len(list((tmp_path / "failures" / "gw0").iterdir())) == 12


def test_api_source_lists_every_resource_and_one_failure_does_not_stop_the_rest(tmp_path: Path) -> None:
    grpc = Mock()

    def call(*, service: str, timeout: int) -> dict:
        if service == f"{PRIVATE_API}.Subnets/List":
            raise CommandFailed(1, ["grpcurl", "-H", f"Authorization: Bearer {JWT}"], stderr="Code: Unavailable")
        return {"items": [{"id": service}]}

    grpc.call.side_effect = call
    snap.api_source(grpc)(tmp_path, snap.Window(START))
    names = sorted(path.name for path in tmp_path.glob("api-*.json"))
    assert len(names) == 8 and "api-subnets.json" not in names
    assert "api:Subnets" in (tmp_path / snap.ERRORS_FILE).read_text()
    assert JWT not in (tmp_path / snap.ERRORS_FILE).read_text()


def test_logs_source_asks_for_the_three_deployments_since_the_test_began(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    calls: list[tuple[str, ...]] = []

    def fake_run(*args: str, timeout: int) -> str:
        calls.append(args)
        assert timeout <= 30
        return "line"

    monkeypatch.setattr(snap, "run", fake_run)
    k8s = Mock()
    k8s.base_args.return_value = ["kubectl"]
    snap.logs_source(k8s, "osac-ns")(tmp_path, snap.Window(START))
    assert [c[2] for c in calls] == [
        "deployment/fulfillment-grpc-server",
        "deployment/fulfillment-controller",
        "deployment/osac-operator",
    ]
    assert "--since-time=2026-01-02T03:04:05Z" in calls[0]
    assert {"--all-containers", "--timestamps", "--tail=20000", "-n", "osac-ns"} <= set(calls[0])
    assert (tmp_path / "log-osac-operator.txt").read_text() == "line"


def test_k8s_source_never_asks_for_secrets(tmp_path: Path, monkeypatch: pytest.MonkeyPatch) -> None:
    calls: list[tuple[str, ...]] = []
    monkeypatch.setattr(snap, "run", lambda *args, timeout: calls.append(args) or "{}")
    k8s = Mock()
    k8s.base_args.return_value = ["kubectl"]
    snap.k8s_source(k8s, "ns")(tmp_path, snap.Window(START))
    assert not any("secret" in part.lower() for call in calls for part in call)
    assert (tmp_path / "k8s-events.json").exists() and (tmp_path / "k8s-computeinstances.json").exists()


def test_fabric_source_is_absent_unless_the_command_is_set(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.delenv(snap.FABRIC_COMMAND_ENV, raising=False)
    assert snap.fabric_source() is None


def test_fabric_source_writes_stdout_and_stderr(tmp_path: Path, monkeypatch: pytest.MonkeyPatch) -> None:
    script = "import sys; print('{\"vpc\": 1}'); print('oops', file=sys.stderr)"
    monkeypatch.setenv(snap.FABRIC_COMMAND_ENV, f"{sys.executable} -c {shlex.quote(script)}")
    source = snap.fabric_source()
    assert source is not None
    source(tmp_path, snap.Window(START))
    assert json.loads((tmp_path / "fabric.json").read_text()) == {"vpc": 1}
    assert "fabric: oops" in (tmp_path / snap.ERRORS_FILE).read_text()


def test_guest_source_runs_each_guest_and_stops_when_the_budget_is_used(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    seen: list[tuple[str, int]] = []

    def runner(name: str) -> Callable[[str, int], tuple[str, int]]:
        def run(command: str, timeout: int) -> tuple[str, int]:
            seen.append((name, timeout))
            assert command == snap.GUEST_COMMAND
            if name == "bad":
                raise RuntimeError("exec failed")
            return f"{name} addr", 0

        return run

    snap.guest_source(lambda: [("a", runner("a")), ("bad", runner("bad")), ("c", runner("c"))])(
        tmp_path, snap.Window(START)
    )
    assert (tmp_path / "guest-a.txt").read_text() == "exit status 0\na addr\n"
    assert (tmp_path / "guest-c.txt").exists()
    assert "guest:bad: RuntimeError" in (tmp_path / snap.ERRORS_FILE).read_text()
    assert {timeout for _, timeout in seen} == {10}

    clock = iter([0, 0, 50])
    monkeypatch.setattr(snap.time, "monotonic", lambda: next(clock))
    other = tmp_path / "late"
    other.mkdir()
    snap.guest_source(lambda: [("a", runner("a")), ("b", runner("b"))])(other, snap.Window(START))
    assert (other / "guest-a.txt").exists() and not (other / "guest-b.txt").exists()
    assert "guest budget used up" in (other / snap.ERRORS_FILE).read_text()


def test_vmaas_guest_source_uses_the_helper_pod_path(tmp_path: Path, monkeypatch: pytest.MonkeyPatch) -> None:
    calls: list[dict] = []
    monkeypatch.setattr(guest_ssh, "ssh_guest_unchecked", lambda **kw: calls.append(kw) or ("ip out", 0))
    instances = [{"name": "vm-a", "cr": "cr-a", "vm_namespace": "ns-a"}, {"name": "vm-pending", "cr": "cr-p"}]
    stack.register_guest_snapshot("guests-test", instances, "/key")
    try:
        sources = dict(snap.SOURCES.for_kind(Kind.TRAFFIC_BLOCKED))
        assert "guests-test" in sources and "guests-test" not in dict(snap.SOURCES.for_kind(Kind.API_REFUSED))
        sources["guests-test"](tmp_path, snap.Window(START))
    finally:
        stack.unregister_guest_snapshot("guests-test")
    assert [(c["vm_namespace"], c["vm_name"], c["identity_file"]) for c in calls] == [("ns-a", "cr-a", "/key")]
    assert (tmp_path / "guest-vm-a.txt").exists()


# Kinds attached to failures by the VMaaS helpers


def test_poll_or_fail_with_a_kind_raises_a_classified_failure() -> None:
    with pytest.raises(AssertionError) as raised:
        stack.poll_or_fail(
            hint="check the lab",
            fn=lambda: 500,
            until=lambda v: v == 200,
            description="NAT egress",
            resource_kind=WaitResourceKind.COMPUTE_INSTANCE,
            resource_id="ci-1",
            retries=1,
            delay=0,
            kind=Kind.TRAFFIC_BLOCKED,
            ids={"ci_id": "ci-1"},
        )
    result = classify(raised.value)
    assert (result.kind, result.ids) == (Kind.TRAFFIC_BLOCKED, {"ci_id": "ci-1"})
    assert "check the lab" in result.summary


def test_poll_or_fail_without_a_kind_is_still_a_stuck_state() -> None:
    with pytest.raises(pytest.fail.Exception) as raised:
        stack.poll_or_fail(
            hint="h",
            fn=lambda: 1,
            until=lambda v: False,
            description="d",
            resource_kind=WaitResourceKind.COMPUTE_INSTANCE,
            resource_id="ci-1",
            retries=1,
            delay=0,
        )
    assert classify(raised.value).kind is Kind.STUCK_STATE


def test_a_transport_error_from_the_helper_pod_is_test_ssh_failed(monkeypatch: pytest.MonkeyPatch) -> None:
    def broken(**_kwargs: object) -> tuple[str, int]:
        raise HelperPodError("pod not ready")

    monkeypatch.setattr(guest_ssh.ssh_helper, "exec_ssh", broken)
    with pytest.raises(HelperPodError) as raised:
        guest_ssh.ssh_guest_unchecked(vm_namespace="ns", vm_name="vm", identity_file="k", command="true")
    assert classify(raised.value).kind is Kind.TEST_SSH_FAILED


def test_assert_guest_login_keeps_pytest_fail_and_is_test_ssh_failed(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setattr(guest_ssh, "login_problem", lambda *_a: "TCP 22 is unreachable")
    with pytest.raises(pytest.fail.Exception) as raised:
        guest_ssh.assert_guest_login("ns", "vm", "k")
    result = classify(raised.value)
    assert (result.kind, result.ids["vm"]) == (Kind.TEST_SSH_FAILED, "vm")


def test_curl_status_checked_transport_failure_is_test_ssh_failed(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setattr(guest_ssh, "_run_curl", lambda *_a: ("denied", 255))
    with pytest.raises(RuntimeError, match="rc=255") as raised:
        guest_ssh.curl_status_checked("ns", "vm", "k")
    assert classify(raised.value).kind is Kind.TEST_SSH_FAILED


@pytest.mark.parametrize(
    ("stderr", "kind"),
    [
        ("ssh: connect to host 1.2.3.4 port 22: Connection timed out", Kind.TRAFFIC_BLOCKED),
        ("ssh: connect to host 1.2.3.4 port 22: Connection refused", Kind.TRAFFIC_BLOCKED),
        ("user@1.2.3.4: Permission denied (publickey).", Kind.TEST_SSH_FAILED),
        ("Host key verification failed.", Kind.TEST_SSH_FAILED),
    ],
)
def test_external_ssh_errors_are_split_into_blocked_traffic_and_our_own_failure(stderr: str, kind: Kind) -> None:
    text, found = guest_ssh.external_ssh_error(subprocess.CalledProcessError(255, ["ssh"], stderr=stderr))
    assert found is kind
    assert stderr.split(": ")[-1][:10] in text


def test_external_ssh_timeout_is_traffic_blocked() -> None:
    assert guest_ssh.external_ssh_error(subprocess.TimeoutExpired(["ssh"], 10))[1] is Kind.TRAFFIC_BLOCKED


# Fix round 1: what a timed-out wait means

VM = {"id": "ci-1", "name": "vm", "ip": "10.0.0.5", "vm_namespace": "ns", "cr": "cr"}


def _wait(monkeypatch: pytest.MonkeyPatch, *, want_open: bool, tcp: str, login: str | None) -> Exception:
    from tests.e2e.networking.vmaas import test_vmaas_networking as suite

    monkeypatch.setattr(suite.guest_ssh, "tcp_state", lambda *_a: tcp)
    monkeypatch.setattr(suite.guest_ssh, "login_problem", lambda *_a: login)
    monkeypatch.setattr("tests.e2e.core.runner.time.sleep", lambda _s: None)
    with pytest.raises(AssertionError) as raised:
        suite._wait_tcp({"identity_file": "k"}, VM, "10.0.0.9", 8080, want_open=want_open, retries=2, delay=0)
    return raised.value


def test_a_deny_wait_timeout_means_the_traffic_was_still_allowed(monkeypatch: pytest.MonkeyPatch) -> None:
    result = classify(_wait(monkeypatch, want_open=False, tcp="open", login=None))
    assert result.kind is Kind.ASSERTION
    assert "still allowed" in result.summary


def test_a_deny_wait_that_timed_out_on_transport_errors_is_test_ssh_failed(monkeypatch: pytest.MonkeyPatch) -> None:
    result = classify(_wait(monkeypatch, want_open=False, tcp="transport-error", login=None))
    assert result.kind is Kind.TEST_SSH_FAILED
    assert "still allowed" not in result.summary


def test_an_allow_wait_is_traffic_blocked_when_the_guest_login_works(monkeypatch: pytest.MonkeyPatch) -> None:
    assert classify(_wait(monkeypatch, want_open=True, tcp="blocked", login=None)).kind is Kind.TRAFFIC_BLOCKED


def test_an_allow_wait_is_test_ssh_failed_when_the_guest_login_fails(monkeypatch: pytest.MonkeyPatch) -> None:
    result = classify(_wait(monkeypatch, want_open=True, tcp="blocked", login="TCP 22 is unreachable"))
    assert result.kind is Kind.TEST_SSH_FAILED
    assert "TCP 22 is unreachable" in result.summary


def test_nat_egress_timeout_asks_the_guest_login_check(monkeypatch: pytest.MonkeyPatch) -> None:
    from tests.e2e.networking.vmaas import test_vmaas_networking as suite

    monkeypatch.setattr(suite.guest_ssh, "curl_status", lambda *_a: 0)
    monkeypatch.setattr("tests.e2e.core.runner.time.sleep", lambda _s: None)
    monkeypatch.setattr(suite, "_identity", lambda _s: "k")
    kinds = []
    for login in (None, "authentication refused"):
        monkeypatch.setattr(suite.guest_ssh, "login_problem", lambda *_a, login=login: login)
        monkeypatch.setattr(stack, "poll_until", _fast_poll)
        with pytest.raises(AssertionError) as raised:
            suite._assert_nat_egress({}, VM, "tenant1")
        kinds.append(classify(raised.value).kind)
    assert kinds == [Kind.TRAFFIC_BLOCKED, Kind.TEST_SSH_FAILED]


def _fast_poll(**kwargs: object) -> object:
    from tests.e2e.core import runner

    return runner.poll_until(**{**kwargs, "retries": 1, "delay": 0})
