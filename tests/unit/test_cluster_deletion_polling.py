from __future__ import annotations

import subprocess
from typing import Any
from unittest.mock import Mock, call

import pytest

from tests.e2e.core import helpers, runner
from tests.e2e.core import k8s_client as k8s_client_module
from tests.e2e.core.k8s_client import K8sClient


def _mock_get(k8s: K8sClient, *, output: str, returncode: int) -> None:
    def get(*args: str, checked: bool = True) -> tuple[str, int]:
        return output, returncode

    k8s._get = get  # type: ignore[method-assign]


def test_cluster_order_not_found_is_distinct_from_an_empty_phase() -> None:
    k8s = K8sClient(namespace="tenant-a")

    _mock_get(
        k8s,
        output='Error from server (NotFound): clusterorders.osac.openshift.io "cluster-a" not found',
        returncode=1,
    )
    assert k8s.get_cluster_order_phase(name="cluster-a", checked=False) is None

    _mock_get(k8s, output="", returncode=0)
    assert k8s.get_cluster_order_phase(name="cluster-a", checked=False) == ""


@pytest.mark.parametrize(
    "kubectl_output",
    [
        "Error from server (Forbidden): clusterorders.osac.openshift.io is forbidden",
        'Error from server (NotFound): namespaces "tenant-a" not found',
        'Error from server (NotFound): namespaces "clusterorders" not found',
        'Error from server (NotFound): clusterorders.osac.openshift.io "other-cluster" not found',
        'Error from server (NotFound): clusterorders.osac.openshift.io "cluster-a" not found: extra text',
        'prefix: Error from server (NotFound): clusterorders.osac.openshift.io "cluster-a" not found',
        "Unable to connect to the server: dial tcp: lookup api.example.test: no such host",
    ],
)
def test_cluster_order_phase_propagates_non_resource_not_found_errors(kubectl_output: str) -> None:
    k8s = K8sClient(namespace="tenant-a")
    _mock_get(k8s, output=kubectl_output, returncode=1)

    with pytest.raises(subprocess.CalledProcessError) as exc_info:
        k8s.get_cluster_order_phase(name="cluster-a", checked=False)

    assert exc_info.value.returncode == 1
    assert exc_info.value.stderr == kubectl_output


def test_wait_for_cluster_deleting_accepts_only_not_found_as_already_gone(monkeypatch: pytest.MonkeyPatch) -> None:
    k8s = Mock(spec=K8sClient)
    captured: dict[str, Any] = {}

    def capture_poll(**kwargs: Any) -> None:
        captured.update(kwargs)

    monkeypatch.setattr(helpers, "poll_until", capture_poll)
    helpers.wait_for_cluster_deleting(k8s=k8s, name="cluster-a")

    until = captured["until"]
    assert until("Deleting") is True
    assert until(None) is True
    assert until("") is False


@pytest.mark.parametrize(
    "kubectl_output",
    [
        "Error from server (Forbidden): clusterorders.osac.openshift.io is forbidden",
        'Error from server (NotFound): namespaces "tenant-a" not found',
        "Unable to connect to the server: connection refused",
    ],
)
def test_wait_for_cluster_deleting_propagates_kubectl_errors(
    monkeypatch: pytest.MonkeyPatch, kubectl_output: str
) -> None:
    k8s = Mock(spec=K8sClient)
    k8s.get_cluster_order_phase.side_effect = subprocess.CalledProcessError(
        1, ["kubectl"], stderr=kubectl_output
    )

    def invoke_once(**kwargs: Any) -> None:
        kwargs["fn"]()

    monkeypatch.setattr(helpers, "poll_until", invoke_once)

    with pytest.raises(subprocess.CalledProcessError) as exc_info:
        helpers.wait_for_cluster_deleting(k8s=k8s, name="cluster-a")

    assert exc_info.value.stderr == kubectl_output


@pytest.mark.parametrize("phase, expected", [(None, True), ("", False), ("Deleting", False)])
def test_wait_for_cluster_deletion_uses_not_found_as_the_deletion_signal(
    monkeypatch: pytest.MonkeyPatch, phase: str | None, expected: bool
) -> None:
    k8s = Mock(spec=K8sClient)
    k8s.get_cluster_order_phase.return_value = phase
    captured: dict[str, Any] = {}

    for cleanup in (
        "_force_cleanup_agentcluster_finalizers",
        "_force_cleanup_agent_labels",
        "_force_cleanup_machine_preterminate_hooks",
    ):
        monkeypatch.setattr(helpers, cleanup, lambda **kwargs: None)

    def capture_poll(**kwargs: Any) -> None:
        captured.update(kwargs)

    monkeypatch.setattr(helpers, "poll_until", capture_poll)
    helpers.wait_for_cluster_deletion(k8s=k8s, name="cluster-a")

    assert captured["fn"]() is expected


def test_wait_for_cluster_deletion_propagates_kubectl_errors(monkeypatch: pytest.MonkeyPatch) -> None:
    k8s = Mock(spec=K8sClient)
    error = subprocess.CalledProcessError(1, ["kubectl"], stderr="Unable to connect to the server")
    k8s.get_cluster_order_phase.side_effect = error

    for cleanup in (
        "_force_cleanup_agentcluster_finalizers",
        "_force_cleanup_agent_labels",
        "_force_cleanup_machine_preterminate_hooks",
    ):
        monkeypatch.setattr(helpers, cleanup, lambda **kwargs: None)

    def invoke_once(**kwargs: Any) -> None:
        kwargs["fn"]()

    monkeypatch.setattr(helpers, "poll_until", invoke_once)

    with pytest.raises(subprocess.CalledProcessError) as exc_info:
        helpers.wait_for_cluster_deletion(k8s=k8s, name="cluster-a")

    assert exc_info.value.stderr == "Unable to connect to the server"


@pytest.fixture
def forbid_cleanup(monkeypatch: pytest.MonkeyPatch) -> list[Mock]:
    cleanups = []
    for cleanup in (
        "_force_cleanup_agentcluster_finalizers",
        "_force_cleanup_agent_labels",
        "_force_cleanup_machine_preterminate_hooks",
    ):
        mock = Mock(side_effect=AssertionError("Natural deletion must not force cleanup"))
        monkeypatch.setattr(helpers, cleanup, mock)
        cleanups.append(mock)
    return cleanups


def test_natural_deletion_is_read_only_and_observes_each_poll(
    monkeypatch: pytest.MonkeyPatch, forbid_cleanup: list[Mock]
) -> None:
    k8s = Mock(spec=K8sClient)
    k8s.is_absent.side_effect = [False, False, True]
    observe = Mock()
    sleep = Mock()
    monkeypatch.setattr(runner.time, "sleep", sleep)

    helpers.wait_for_cluster_deletion_without_cleanup(k8s=k8s, name="cluster-a", on_poll=observe)

    assert k8s.mock_calls == [call.is_absent(resource="clusterorder", name="cluster-a")] * 3
    assert observe.call_count == 3
    assert sleep.call_args_list == [call(10), call(10)]
    for cleanup in forbid_cleanup:
        cleanup.assert_not_called()
    k8s.patch.assert_not_called()
    k8s.delete.assert_not_called()
    k8s.apply.assert_not_called()


def test_natural_deletion_uses_separate_parent_polling_budget(monkeypatch: pytest.MonkeyPatch) -> None:
    k8s = Mock(spec=K8sClient)
    k8s.is_absent.return_value = True
    poll = Mock()
    monkeypatch.setattr(helpers, "poll_until", poll)
    helpers.wait_for_cluster_deletion_without_cleanup(k8s=k8s, name="cluster-a")
    captured = poll.call_args.kwargs

    assert captured["fn"]() is True
    assert captured["until"](True) is True
    assert captured["until"](False) is False
    assert captured["until"](None) is False
    assert captured["until"](1) is False
    assert captured["retries"] == 121
    assert captured["delay"] == 10
    assert captured["description"] == "ClusterOrder natural deletion"
    assert captured.get("retry_on_error", False) is False


@pytest.mark.parametrize(
    "present_output",
    [
        '{"metadata":{"name":"cluster-a"}}',
        '{"metadata":{"name":"cluster-a","deletionTimestamp":"2026-10-01T14:25:00Z"}}',
    ],
)
def test_natural_deletion_requires_not_found_even_without_status(
    monkeypatch: pytest.MonkeyPatch, present_output: str
) -> None:
    get = Mock(
        side_effect=[
            (present_output, 0),
            ('Error from server (NotFound): clusterorders.osac.openshift.io "cluster-a" not found', 1),
        ]
    )
    monkeypatch.setattr(k8s_client_module, "run_unchecked", get)
    sleep = Mock()
    monkeypatch.setattr(runner.time, "sleep", sleep)
    k8s = K8sClient(namespace="tenant-a", as_system_admin=False)

    helpers.wait_for_cluster_deletion_without_cleanup(k8s=k8s, name="cluster-a")

    assert get.call_args_list == [call("kubectl", "get", "clusterorder", "cluster-a", "-n", "tenant-a")] * 2
    sleep.assert_called_once_with(10)


@pytest.mark.parametrize(
    ("resource", "server_resource"),
    [
        ("clusterorder", "clusterorders"),
        ("clusterorder", "clusterorders.osac.openshift.io"),
        ("infraenv.agent-install.openshift.io", "infraenvs.agent-install.openshift.io"),
    ],
)
def test_exact_resource_not_found_is_absent(
    monkeypatch: pytest.MonkeyPatch, resource: str, server_resource: str
) -> None:
    output = f'Error from server (NotFound): {server_resource} "cluster-a" not found'
    monkeypatch.setattr(k8s_client_module, "run_unchecked", Mock(return_value=(output, 1)))

    assert K8sClient(namespace="tenant-a").is_absent(resource=resource, name="cluster-a") is True


_INVALID_ABSENCE_ERRORS = [
    "Error from server (Forbidden): clusterorders.osac.openshift.io is forbidden",
    'Error from server (NotFound): namespaces "tenant-a" not found',
    'Error from server (NotFound): namespaces "cluster-a" not found',
    'Error from server (NotFound): secrets "cluster-a" not found',
    'Error from server (NotFound): clusterorders.other.example.io "cluster-a" not found',
    'Error from server (NotFound): clusterorders.osac.openshift.io "other-cluster" not found',
    'Error from server (NotFound): clusterorders.osac.openshift.io "cluster-a" not found: extra text',
    'prefix: Error from server (NotFound): clusterorders.osac.openshift.io "cluster-a" not found',
    "Unable to connect to the server: connection refused",
]


@pytest.mark.parametrize("kubectl_output", _INVALID_ABSENCE_ERRORS)
def test_natural_deletion_propagates_lookup_errors_without_retrying(
    monkeypatch: pytest.MonkeyPatch, kubectl_output: str, forbid_cleanup: list[Mock]
) -> None:
    get = Mock(return_value=(kubectl_output, 1))
    sleep = Mock()
    monkeypatch.setattr(k8s_client_module, "run_unchecked", get)
    monkeypatch.setattr(runner.time, "sleep", sleep)

    with pytest.raises(subprocess.CalledProcessError) as exc_info:
        helpers.wait_for_cluster_deletion_without_cleanup(k8s=K8sClient(namespace="tenant-a"), name="cluster-a")

    assert exc_info.value.stderr == kubectl_output
    assert get.call_count == 1
    sleep.assert_not_called()
    for cleanup in forbid_cleanup:
        cleanup.assert_not_called()


@pytest.mark.parametrize("resource", ["clusterorder", "infraenv.agent-install.openshift.io"])
@pytest.mark.parametrize("kubectl_output", _INVALID_ABSENCE_ERRORS)
def test_is_absent_rejects_other_resources_and_non_exact_errors(
    monkeypatch: pytest.MonkeyPatch, kubectl_output: str, resource: str
) -> None:
    monkeypatch.setattr(k8s_client_module, "run_unchecked", Mock(return_value=(kubectl_output, 1)))

    with pytest.raises(subprocess.CalledProcessError):
        K8sClient(namespace="tenant-a").is_absent(resource=resource, name="cluster-a")
