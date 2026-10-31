from __future__ import annotations

import base64
import json
import logging
import time
from concurrent.futures import ThreadPoolExecutor
from pathlib import Path
from types import SimpleNamespace

import pytest

from tests.e2e.networking.vmaas import guest_ssh, ssh_helper

KEY_TEXT = (
    "-----BEGIN "
    + "OPENSSH PRIVATE"
    + " KEY-----\nSUPERSECRETKEYMATERIAL\n-----END "
    + "OPENSSH PRIVATE"
    + " KEY-----\n"
)


@pytest.fixture(autouse=True)
def _env(monkeypatch: pytest.MonkeyPatch, tmp_path: Path) -> Path:
    ssh_helper.reset_state()
    monkeypatch.setenv("OSAC_VM_KUBECONFIG", "/tmp/vm.kubeconfig")
    monkeypatch.delenv("OSAC_VM_SSH_USER", raising=False)
    monkeypatch.delenv(ssh_helper.IMAGE_ENV, raising=False)
    key = tmp_path / "id_ed25519"
    key.write_text(KEY_TEXT)
    return key


class _Kubectl:
    """Records kubectl calls and answers them from a table keyed by the verb."""

    def __init__(self, answers: dict[str, tuple[int, str, str]] | None = None) -> None:
        self.calls: list[dict[str, object]] = []
        self.answers = answers or {}

    def __call__(self, args: list[str], **kwargs: object) -> SimpleNamespace:
        self.calls.append({"args": args, **kwargs})
        verb = next(a for a in args if a in {"apply", "wait", "get", "delete", "exec"})
        rc, out, err = self.answers.get(verb, (0, "", ""))
        return SimpleNamespace(returncode=rc, stdout=out, stderr=err)

    def verbs(self) -> list[str]:
        return [next(a for a in c["args"] if a in {"apply", "wait", "get", "delete", "exec"}) for c in self.calls]  # type: ignore[union-attr]


def _install(monkeypatch: pytest.MonkeyPatch, kubectl: _Kubectl) -> None:
    monkeypatch.setattr(ssh_helper.subprocess, "run", kubectl)


def test_manifests_pod_is_non_root_drops_capabilities_and_policy_allows_only_tcp_22() -> None:
    secret, policy, pod = ssh_helper.manifests(namespace="ns", key_b64="a2V5", helper_image="img")

    spec = pod["spec"]
    container = spec["containers"][0]  # type: ignore[index]
    assert spec["securityContext"]["runAsNonRoot"] is True  # type: ignore[index]
    assert container["securityContext"]["capabilities"] == {"drop": ["ALL"]}
    assert container["securityContext"]["allowPrivilegeEscalation"] is False
    assert container["image"] == "img"
    assert policy["spec"]["policyTypes"] == ["Egress"]  # type: ignore[index]
    assert policy["spec"]["egress"] == [{"ports": [{"protocol": "TCP", "port": 22}]}]  # type: ignore[index]
    assert policy["spec"]["podSelector"] == {"matchLabels": {"osac-e2e/ssh-helper": "true"}}  # type: ignore[index]
    assert secret["data"] == {"id": "a2V5"}
    assert spec["terminationGracePeriodSeconds"] == 1
    assert container["readinessProbe"]["exec"]["command"] == ["test", "-s", "/tmp/id"]


def test_ssh_args_use_a_pod_local_key_path_and_the_configured_user(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setenv("OSAC_VM_SSH_USER", "cloud-user")

    args = ssh_helper.ssh_args(address="10.220.1.5", command="hostname", timeout=30)

    assert args[-2:] == ["cloud-user@10.220.1.5", "hostname"]
    assert args[args.index("-i") + 1] == "/tmp/id"
    assert "BatchMode=yes" in args and "StrictHostKeyChecking=no" in args and "ConnectTimeout=10" in args
    assert "-n" in args and "UserKnownHostsFile=/dev/null" in args


def test_image_defaults_to_a_pinned_digest_and_can_be_overridden(monkeypatch: pytest.MonkeyPatch) -> None:
    assert ssh_helper.image().startswith("ghcr.io/osac-project/osac-aap@sha256:")
    monkeypatch.setenv(ssh_helper.IMAGE_ENV, "registry.example/ssh:1")
    assert ssh_helper.image() == "registry.example/ssh:1"


def test_ensure_helper_applies_once_waits_for_ready_and_reuses(monkeypatch: pytest.MonkeyPatch, _env: Path) -> None:
    kubectl = _Kubectl()
    _install(monkeypatch, kubectl)

    ssh_helper.ensure_helper("ns", str(_env))
    ssh_helper.ensure_helper("ns", str(_env))

    assert kubectl.verbs() == ["apply", "wait"]
    # The default image is ~990 MB: the Pod may need minutes to pull it, and the process outlives the wait.
    wait_call = kubectl.calls[1]
    assert "--timeout=600s" in wait_call["args"]  # type: ignore[operator]
    assert wait_call["timeout"] == 660
    applied = json.loads(str(kubectl.calls[0]["input"]))
    assert [item["kind"] for item in applied["items"]] == ["Secret", "NetworkPolicy", "Pod"]
    assert applied["items"][0]["data"]["id"] == base64.b64encode(KEY_TEXT.encode()).decode()
    # The key travels on stdin only, never in an argument.
    assert all(KEY_TEXT.strip() not in " ".join(c["args"]) for c in kubectl.calls)  # type: ignore[arg-type]


def test_ensure_helper_from_eight_threads_on_one_namespace_applies_once(
    monkeypatch: pytest.MonkeyPatch, _env: Path
) -> None:
    kubectl = _Kubectl()

    def slow_kubectl(args: list[str], **kwargs: object) -> SimpleNamespace:
        time.sleep(0.05)  # widens the window between the _READY check and the apply
        return kubectl(args, **kwargs)

    monkeypatch.setattr(ssh_helper.subprocess, "run", slow_kubectl)

    with ThreadPoolExecutor(max_workers=8) as executor:
        for future in [executor.submit(ssh_helper.ensure_helper, "ns", str(_env)) for _ in range(8)]:
            future.result()

    assert kubectl.verbs() == ["apply", "wait"]


def test_ensure_helper_does_not_serialize_different_namespaces(monkeypatch: pytest.MonkeyPatch, _env: Path) -> None:
    kubectl = _Kubectl()
    _install(monkeypatch, kubectl)

    ssh_helper.ensure_helper("one", str(_env))
    ssh_helper.ensure_helper("two", str(_env))

    assert kubectl.verbs() == ["apply", "wait", "apply", "wait"]


def test_ensure_helper_fails_clearly_when_the_pod_is_not_ready(monkeypatch: pytest.MonkeyPatch, _env: Path) -> None:
    kubectl = _Kubectl({"wait": (1, "", "timed out waiting for the condition"), "get": (0, "Pending", "")})
    _install(monkeypatch, kubectl)

    with pytest.raises(ssh_helper.HelperPodError, match="not Running and Ready") as exc_info:
        ssh_helper.ensure_helper("ns", str(_env))

    assert ssh_helper.IMAGE_ENV in str(exc_info.value)
    assert "events:" in str(exc_info.value)
    assert "ns" not in ssh_helper._READY


def test_apply_failure_never_leaks_the_key(monkeypatch: pytest.MonkeyPatch, _env: Path) -> None:
    b64 = base64.b64encode(KEY_TEXT.encode()).decode()
    kubectl = _Kubectl({"apply": (1, "", f"error validating data: {b64} {KEY_TEXT}")})
    _install(monkeypatch, kubectl)

    with pytest.raises(ssh_helper.HelperPodError) as exc_info:
        ssh_helper.ensure_helper("ns", str(_env))

    message = str(exc_info.value)
    assert "SUPERSECRETKEYMATERIAL" not in message and b64 not in message


def _exec_setup(monkeypatch: pytest.MonkeyPatch, _env: Path, exec_answer: tuple[int, str, str]) -> _Kubectl:
    kubectl = _Kubectl({"get": (0, "10.220.1.5", ""), "exec": exec_answer})
    _install(monkeypatch, kubectl)
    return kubectl


def _run(_env: Path, command: str = "true") -> tuple[str, int]:
    return guest_ssh.ssh_guest_unchecked(vm_namespace="ns", vm_name="vm", identity_file=str(_env), command=command)


@pytest.mark.parametrize("rc", [0, 1, 255])
def test_exec_maps_the_ssh_status_not_the_exec_status(monkeypatch: pytest.MonkeyPatch, _env: Path, rc: int) -> None:
    kubectl = _exec_setup(monkeypatch, _env, (7, f"hello\n\n__OSAC_SSH_RC__={rc}\n", "warn"))

    output, status = _run(_env, "hostname")

    assert status == rc
    assert output == "hello\nwarn"
    exec_call = next(c for c in kubectl.calls if "exec" in c["args"])  # type: ignore[operator]
    args = exec_call["args"]
    assert args[args.index("--") + 1 : args.index("--") + 3] == ["sh", "-c"]  # type: ignore[index]
    assert args[-2:] == ["fedora@10.220.1.5", "hostname"]  # type: ignore[index]


def test_exec_failure_is_a_helper_error_not_a_guest_status(monkeypatch: pytest.MonkeyPatch, _env: Path) -> None:
    _exec_setup(monkeypatch, _env, (1, "", 'Error from server (NotFound): pods "osac-e2e-ssh-helper" not found'))

    with pytest.raises(ssh_helper.HelperExecError, match="kubectl exec into helper Pod"):
        _run(_env)
    # The next call re-checks the Pod instead of trusting the cache.
    assert "ns" not in ssh_helper._READY


def test_a_vm_without_an_address_is_a_transport_failure(monkeypatch: pytest.MonkeyPatch, _env: Path) -> None:
    kubectl = _Kubectl({"get": (0, "", "")})
    _install(monkeypatch, kubectl)

    with pytest.raises(RuntimeError, match="has no VMI address yet"):
        _run(_env)


def test_exec_timeout_is_a_transport_failure(monkeypatch: pytest.MonkeyPatch, _env: Path) -> None:
    kubectl = _Kubectl({"get": (0, "10.220.1.5", "")})

    def run(args: list[str], **kwargs: object) -> SimpleNamespace:
        if "exec" in args:
            raise ssh_helper.subprocess.TimeoutExpired(cmd="kubectl", timeout=30)
        return kubectl(args, **kwargs)

    monkeypatch.setattr(ssh_helper.subprocess, "run", run)

    with pytest.raises(RuntimeError, match="timed out after 30s"):
        _run(_env)


def test_requires_the_vm_kubeconfig(monkeypatch: pytest.MonkeyPatch, _env: Path) -> None:
    monkeypatch.delenv("OSAC_VM_KUBECONFIG")

    with pytest.raises(RuntimeError, match="OSAC_VM_KUBECONFIG"):
        _run(_env)


def test_key_never_reaches_logs_or_errors(
    monkeypatch: pytest.MonkeyPatch, _env: Path, caplog: pytest.LogCaptureFixture
) -> None:
    caplog.set_level(logging.DEBUG)
    kubectl = _exec_setup(monkeypatch, _env, (0, "__OSAC_SSH_RC__=255\n", "Permission denied (publickey)."))

    _run(_env)

    assert "SUPERSECRETKEYMATERIAL" not in caplog.text
    assert all("SUPERSECRETKEYMATERIAL" not in " ".join(c["args"]) for c in kubectl.calls)  # type: ignore[arg-type]


def test_remove_helper_deletes_all_three_objects_and_ignores_missing(monkeypatch: pytest.MonkeyPatch) -> None:
    kubectl = _Kubectl()
    _install(monkeypatch, kubectl)
    ssh_helper._READY.add("ns")

    ssh_helper.remove_helper("ns")
    ssh_helper.remove_helper("ns")

    deletes = [c["args"] for c in kubectl.calls]
    assert len(deletes) == 6
    assert all("--ignore-not-found" in args for args in deletes)  # type: ignore[operator]
    assert [args[args.index("delete") + 1] for args in deletes[:3]] == ["pod", "networkpolicy", "secret"]  # type: ignore[attr-defined]
    assert "ns" not in ssh_helper._READY


def test_remove_helpers_for_a_network_removes_each_recorded_namespace(monkeypatch: pytest.MonkeyPatch) -> None:
    kubectl = _Kubectl()
    _install(monkeypatch, kubectl)
    ssh_helper.note_namespace("vn-1", "ns-a")
    ssh_helper.note_namespace("vn-1", "ns-b")
    ssh_helper.note_namespace("vn-2", "ns-c")

    ssh_helper.remove_helpers_for("vn-1")

    namespaces = {args[args.index("-n") + 1] for args in (c["args"] for c in kubectl.calls)}  # type: ignore[attr-defined]
    assert namespaces == {"ns-a", "ns-b"}


@pytest.mark.parametrize(
    ("output", "expected"),
    [
        ("fedora@10.0.0.1: Permission denied (publickey,gssapi).", "authentication refused"),
        ("ssh: connect to host 10.0.0.1 port 22: Connection timed out", "TCP 22 is unreachable"),
        ("ssh: connect to host 10.0.0.1 port 22: Connection refused", "TCP 22 is refused"),
        ("weird", "SSH failed with status 255"),
    ],
)
def test_login_failures_are_classified(output: str, expected: str) -> None:
    assert expected in guest_ssh.classify_login_failure(output, 255)


def test_assert_guest_login_names_a_pod_that_is_not_ready(monkeypatch: pytest.MonkeyPatch) -> None:
    def fail(**_kwargs: object) -> tuple[str, int]:
        raise ssh_helper.HelperPodError("Pod is not Running and Ready")

    monkeypatch.setattr(guest_ssh, "ssh_guest_unchecked", fail)

    with pytest.raises(pytest.fail.Exception, match="helper Pod not ready"):
        guest_ssh.assert_guest_login("ns", "vm", "key")


def test_assert_guest_login_distinguishes_blocked_port_from_rejected_key(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setattr(guest_ssh, "ssh_guest_unchecked", lambda **_k: ("Connection timed out", 255))
    with pytest.raises(pytest.fail.Exception, match="TCP 22 is unreachable"):
        guest_ssh.assert_guest_login("ns", "vm", "key")

    monkeypatch.setattr(guest_ssh, "ssh_guest_unchecked", lambda **_k: ("Permission denied (publickey)", 255))
    with pytest.raises(pytest.fail.Exception, match="authentication refused"):
        guest_ssh.assert_guest_login("ns", "vm", "key")

    monkeypatch.setattr(guest_ssh, "ssh_guest_unchecked", lambda **_k: ("", 0))
    guest_ssh.assert_guest_login("ns", "vm", "key")


def test_wait_for_guest_ssh_fails_at_once_when_the_pod_is_not_ready(monkeypatch: pytest.MonkeyPatch) -> None:
    calls: list[int] = []

    def fail(**_kwargs: object) -> tuple[str, int]:
        calls.append(1)
        raise ssh_helper.HelperPodError("not ready")

    monkeypatch.setattr(guest_ssh, "ssh_guest_unchecked", fail)

    with pytest.raises(ssh_helper.HelperPodError):
        guest_ssh.wait_for_guest_ssh("ns", "vm", "key")
    assert calls == [1]


def test_tcp_state_reports_transport_errors_distinctly(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setattr(guest_ssh, "ssh_guest_unchecked", lambda **_k: ("Connection timed out", 255))
    assert guest_ssh.tcp_state("ns", "vm", "key", "10.0.0.2", 8080) == guest_ssh.TRANSPORT_ERROR

    def timeout(**_kwargs: object) -> tuple[str, int]:
        raise RuntimeError("timed out")

    monkeypatch.setattr(guest_ssh, "ssh_guest_unchecked", timeout)
    assert guest_ssh.tcp_state("ns", "vm", "key", "10.0.0.2", 8080) == guest_ssh.TRANSPORT_ERROR


def test_dhcp_nameservers_returns_empty_on_an_ssh_failure(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setattr(guest_ssh, "ssh_guest_unchecked", lambda **_k: ("", 255))

    assert guest_ssh.dhcp_nameservers("ns", "vm", "key") == []


def test_a_lost_exec_is_transient_for_waits_and_probes(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setattr("tests.e2e.core.runner.time.sleep", lambda _delay: None)
    results: list[object] = [ssh_helper.HelperExecError("blip"), ("", 0)]

    def run(**_kwargs: object) -> tuple[str, int]:
        result = results.pop(0)
        if isinstance(result, Exception):
            raise result
        return result  # type: ignore[return-value]

    monkeypatch.setattr(guest_ssh, "ssh_guest_unchecked", run)
    guest_ssh.wait_for_guest_ssh("ns", "vm", "key")
    assert results == []

    def lost(**_kwargs: object) -> tuple[str, int]:
        raise ssh_helper.HelperExecError("blip")

    monkeypatch.setattr(guest_ssh, "ssh_guest_unchecked", lost)
    assert guest_ssh.tcp_state("ns", "vm", "key", "10.0.0.2", 8080) == guest_ssh.TRANSPORT_ERROR


def test_apply_rejection_message_mentions_the_scc_requirement(monkeypatch: pytest.MonkeyPatch, _env: Path) -> None:
    _install(monkeypatch, _Kubectl({"apply": (1, "", "unable to validate against any security context constraint")}))

    with pytest.raises(ssh_helper.HelperPodError, match="allows that uid"):
        ssh_helper.ensure_helper("ns", str(_env))
