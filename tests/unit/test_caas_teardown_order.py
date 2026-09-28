from __future__ import annotations

import logging
import subprocess
import traceback
from collections.abc import Iterator
from unittest.mock import Mock, call

import pytest

from tests.e2e.caas.sanity import test_cluster_create as scenario
from tests.e2e.core import helpers, runner
from tests.e2e.core import k8s_client as k8s_client_module
from tests.e2e.core.k8s_client import K8sClient

_PARENT = "clusterorder"
_INFRAENV = "infraenv.agent-install.openshift.io"


@pytest.fixture(autouse=True)
def forbid_cleanup(monkeypatch: pytest.MonkeyPatch) -> Iterator[None]:
    guards = []
    for module, name in (
        (scenario, "wait_for_cluster_deletion"),
        (helpers, "wait_for_cluster_deletion"),
        (helpers, "_force_cleanup_agentcluster_finalizers"),
        (helpers, "_force_cleanup_agent_labels"),
        (helpers, "_force_cleanup_machine_preterminate_hooks"),
        (helpers, "run_unchecked"),
    ):
        guard = Mock(side_effect=AssertionError("Natural teardown must not mutate lifecycle gates"))
        monkeypatch.setattr(module, name, guard)
        guards.append(guard)
    yield
    for guard in guards:
        guard.assert_not_called()


def _wait(k8s: K8sClient, observe: Mock, final_snapshot: Mock) -> None:
    scenario._wait_for_cluster_and_infraenv_deletion(
        k8s=k8s, co_name="order-a", infra_env_name="order-a-infraenv", on_poll=observe, on_timeout=final_snapshot
    )


def test_parent_removal_precedes_dependent_gc_with_separate_budgets(monkeypatch: pytest.MonkeyPatch) -> None:
    k8s = Mock(spec=K8sClient)
    k8s.is_absent.side_effect = [False, False, True, False, True]
    observe, final_snapshot, sleep = Mock(), Mock(), Mock()
    monkeypatch.setattr(runner.time, "sleep", sleep)
    parent_poll = Mock(wraps=runner.poll_until)
    infraenv_poll = Mock(wraps=runner.poll_until)
    monkeypatch.setattr(helpers, "poll_until", parent_poll)
    monkeypatch.setattr(scenario, "poll_until", infraenv_poll)

    _wait(k8s, observe, final_snapshot)

    assert (
        k8s.mock_calls
        == [call.is_absent(resource=_PARENT, name="order-a")] * 3
        + [call.is_absent(resource=_INFRAENV, name="order-a-infraenv")] * 2
    )
    assert observe.call_count == 5
    assert sleep.call_args_list == [call(10), call(10), call(5)]
    parent_kwargs = parent_poll.call_args.kwargs
    assert (parent_kwargs["retries"], parent_kwargs["delay"]) == (121, 10)
    assert parent_kwargs.get("retry_on_error", False) is False
    infraenv_kwargs = infraenv_poll.call_args.kwargs
    assert (infraenv_kwargs["retries"], infraenv_kwargs["delay"]) == (60, 5)
    assert infraenv_kwargs["retry_on_error"] is True
    final_snapshot.assert_not_called()
    k8s.patch.assert_not_called()
    k8s.apply.assert_not_called()
    k8s.delete.assert_not_called()


def test_existing_parent_without_phase_does_not_start_gc(monkeypatch: pytest.MonkeyPatch) -> None:
    get = Mock(
        side_effect=[
            ('{"metadata":{"name":"order-a"}}', 0),
            ('Error from server (NotFound): clusterorders.osac.openshift.io "order-a" not found', 1),
            ('Error from server (NotFound): infraenvs.agent-install.openshift.io "order-a-infraenv" not found', 1),
        ]
    )
    monkeypatch.setattr(k8s_client_module, "run_unchecked", get)
    sleep = Mock()
    monkeypatch.setattr(runner.time, "sleep", sleep)

    _wait(K8sClient(namespace="tenant-a", as_system_admin=False), Mock(), Mock())

    assert get.call_args_list == [call("kubectl", "get", _PARENT, "order-a", "-n", "tenant-a")] * 2 + [
        call("kubectl", "get", _INFRAENV, "order-a-infraenv", "-n", "tenant-a")
    ]
    sleep.assert_called_once_with(10)


@pytest.mark.parametrize(
    ("stage", "message", "polls", "sleeps", "delay"),
    [
        ("parent", "ClusterOrder natural teardown timed out", 121, 120, 10),
        ("infraenv", "InfraEnv garbage collection timed out after ClusterOrder removal", 61, 59, 5),
    ],
)
def test_timeout_identifies_stage_and_emits_final_snapshot(
    monkeypatch: pytest.MonkeyPatch, stage: str, message: str, polls: int, sleeps: int, delay: int
) -> None:
    k8s = Mock(spec=K8sClient)
    k8s.is_absent.side_effect = lambda *, resource, name: stage == "infraenv" and resource == _PARENT
    observe, final_snapshot, sleep = Mock(), Mock(), Mock()
    monkeypatch.setattr(runner.time, "sleep", sleep)

    with pytest.raises(TimeoutError, match=message) as exc_info:
        _wait(k8s, observe, final_snapshot)

    assert str(exc_info.value) == message
    assert exc_info.value.__suppress_context__ is True
    assert k8s.is_absent.call_count == polls
    assert observe.call_count == polls
    assert sleep.call_args_list == [call(delay)] * sleeps
    final_snapshot.assert_called_once_with()
    if stage == "parent":
        assert all(c.kwargs["resource"] == _PARENT for c in k8s.is_absent.call_args_list)


def test_already_absent_resources_complete_without_sleeps(monkeypatch: pytest.MonkeyPatch) -> None:
    k8s = Mock(spec=K8sClient)
    k8s.is_absent.return_value = True
    observe, final_snapshot, sleep = Mock(), Mock(), Mock()
    monkeypatch.setattr(runner.time, "sleep", sleep)

    _wait(k8s, observe, final_snapshot)

    assert k8s.is_absent.call_args_list == [
        call(resource=_PARENT, name="order-a"),
        call(resource=_INFRAENV, name="order-a-infraenv"),
    ]
    assert observe.call_count == 2
    sleep.assert_not_called()
    final_snapshot.assert_not_called()


def test_parent_lookup_error_propagates_without_starting_gc(monkeypatch: pytest.MonkeyPatch) -> None:
    error = subprocess.CalledProcessError(1, ["kubectl"], stderr="connection refused")
    k8s = Mock(spec=K8sClient)
    k8s.is_absent.side_effect = error
    sleep, final_snapshot = Mock(), Mock()
    monkeypatch.setattr(runner.time, "sleep", sleep)

    with pytest.raises(subprocess.CalledProcessError) as exc_info:
        _wait(k8s, Mock(), final_snapshot)

    assert exc_info.value is error
    k8s.is_absent.assert_called_once_with(resource=_PARENT, name="order-a")
    sleep.assert_not_called()
    final_snapshot.assert_not_called()


@pytest.mark.parametrize("recover", [False, True])
def test_infraenv_lookup_errors_are_not_absence_and_timeout_is_safe(
    monkeypatch: pytest.MonkeyPatch, caplog: pytest.LogCaptureFixture, recover: bool
) -> None:
    error = subprocess.CalledProcessError(1, ["kubectl", "--token=sensitive-command"], stderr="sensitive-output")
    k8s = Mock(spec=K8sClient)
    if recover:
        k8s.is_absent.side_effect = [True, error, False, True]
    else:
        k8s.is_absent.side_effect = [True] + [error] * 60
    sleep, observe, final_snapshot = Mock(), Mock(), Mock()
    monkeypatch.setattr(runner.time, "sleep", sleep)

    with caplog.at_level(logging.INFO):
        if recover:
            _wait(k8s, observe, final_snapshot)
            assert sleep.call_args_list == [call(5), call(5)]
            final_snapshot.assert_not_called()
        else:
            with pytest.raises(TimeoutError) as exc_info:
                _wait(k8s, observe, final_snapshot)
            assert str(exc_info.value) == "InfraEnv garbage collection timed out after ClusterOrder removal"
            rendered = "".join(traceback.format_exception(exc_info.value))
            assert "sensitive-command" not in rendered
            assert "sensitive-output" not in rendered
            assert sleep.call_args_list == [call(5)] * 59
            final_snapshot.assert_called_once_with()
    assert "sensitive-command" not in caplog.text
    assert "sensitive-output" not in caplog.text


@pytest.mark.parametrize("stage", ["parent", "infraenv"])
def test_stage_timeout_replaces_unsafe_message_without_chaining(monkeypatch: pytest.MonkeyPatch, stage: str) -> None:
    unsafe_timeout = Mock(side_effect=TimeoutError("sensitive-command-output"))
    if stage == "parent":
        monkeypatch.setattr(helpers, "poll_until", unsafe_timeout)
    else:
        monkeypatch.setattr(helpers, "poll_until", Mock())
        monkeypatch.setattr(scenario, "poll_until", unsafe_timeout)
    final_snapshot = Mock()

    with pytest.raises(TimeoutError) as exc_info:
        _wait(Mock(spec=K8sClient), Mock(), final_snapshot)

    assert "sensitive-command-output" not in "".join(traceback.format_exception(exc_info.value))
    final_snapshot.assert_called_once_with()


def test_focused_scenario_uses_natural_waits_and_throttled_best_effort_snapshots(
    monkeypatch: pytest.MonkeyPatch, caplog: pytest.LogCaptureFixture
) -> None:
    cli, grpc, private, metering = (Mock() for _ in range(4))
    k8s = Mock(spec=K8sClient)
    k8s.namespace = "osac"
    k8s._base.return_value = ["kubectl"]
    private.ensure_disk_image.return_value = "disk-id"
    private.ensure_cluster_version.return_value = {"name": "version-name"}
    cli.create_cluster.return_value = "cluster-id"
    grpc.list_cluster_ids.return_value = ["cluster-id"]
    grpc.get_cluster.return_value = {
        "object": {
            "metadata": {"tenant": "tenant1"},
            "spec": {
                "version": {"name": "version-name"},
                "nodeSets": {"workers": {"size": 1, "baremetalInstanceType": {"name": "ci-worker-bm"}}},
            },
        }
    }
    grpc.list_baremetal_instance_ids.side_effect = [["bmi-id"], ["bmi-id"], []]
    grpc.get_baremetal_instance.return_value = {
        "object": {
            "metadata": {
                "tenant": "tenant1",
                "labels": {"osac.openshift.io/cluster-order": "order-a"},
                "annotations": {"osac.openshift.io/owner-reference": "ClusterOrder/order-a"},
            },
            "spec": {
                "template": {"id": "osac.templates.bm_host_provisioning", "shared": True},
                "instanceType": {"name": "ci-worker-bm", "shared": True},
            },
        }
    }
    k8s.get_json.side_effect = [
        {"metadata": {"namespace": "osac", "annotations": {"osac.openshift.io/tenant": "tenant1"}}},
        {
            "metadata": {
                "labels": {"osac.openshift.io/baremetalinstance-uuid": "bmi-id"},
                "annotations": {
                    "osac.openshift.io/tenant": "tenant1",
                    "osac.openshift.io/owner-reference": "ClusterOrder/order-a",
                },
            }
        },
    ]
    k8s.list_json.return_value = {
        "items": [
            {
                "metadata": {
                    "name": "agent-a",
                    "namespace": "osac",
                    "labels": {"infraenvs.agent-install.openshift.io": "order-a-infraenv"},
                },
                "status": {"debugInfo": {"state": "installing-in-progress"}},
            }
        ]
    }
    k8s.get_operator_metrics.return_value = "\n".join(
        f'# TYPE {metric} gauge\n{metric}{{tenant="tenant1",worker_type="baremetal",instance_type="ci-worker-bm"}} 1'
        for metric in scenario._WORKER_LEVEL_METRICS
    )
    k8s.get_cluster_order_spec.return_value = {
        "releaseImage": "release-image",
        "nodeRequests": [{"bareMetal": {"instanceType": "ci-worker-bm"}, "numberOfNodes": 1}],
    }
    k8s.get_cluster_order_hosted_cluster_name.return_value = "hosted-cluster"
    k8s.get_cluster_order_namespace.return_value = "hosted-namespace"
    k8s.get_baremetal_instance_name.return_value = "bmi-cr"
    k8s.get_cluster_order_infra_env_name.return_value = "order-a-infraenv"
    metering.get_all_events.return_value = [
        {"data": {"billing_dimensions": {"component": component}}} for component in ("control_plane", "worker")
    ]
    metering.get_event.side_effect = [
        {"osacresourcetype": "cluster_order", "data": {"billing_dimensions": {"cluster_template": "template"}}},
        {"data": {"billing_dimensions": {"node_set": "workers", "node_count": 2}}},
    ]
    monkeypatch.setattr(scenario, "wait_for_cluster_order_cr", lambda **_kwargs: "order-a")
    for name in (
        "wait_for_cluster_progressing",
        "wait_for_cluster_order_condition",
        "wait_for_cluster_deleting",
        "wait_for_cluster_grpc_deleting_or_archived",
    ):
        monkeypatch.setattr(scenario, name, Mock())
    monkeypatch.setattr(scenario, "run", Mock(return_value="release-image"))
    monkeypatch.setattr(scenario, "wait_for_hosted_cluster_kubeconfig", Mock(return_value=b"test-kubeconfig"))
    node_pool = {
        "spec": {
            "platform": {
                "agent": {"agentLabelSelector": {"matchLabels": {"osac.openshift.io/instance_type": "ci-worker-bm"}}}
            }
        }
    }
    monkeypatch.setattr(scenario, "wait_for_cluster_guest_readiness", Mock(return_value=node_pool))
    grpc_removal = Mock()
    monkeypatch.setattr(scenario, "wait_for_cluster_grpc_removal", grpc_removal)
    k8s.is_present.return_value = False
    k8s.is_absent.side_effect = [False] * 7 + [True] + [False] * 11 + [True]
    now = 0

    def sleep(delay: int) -> None:
        nonlocal now
        now += delay

    monkeypatch.setattr(runner.time, "monotonic", lambda: now)
    monkeypatch.setattr(runner.time, "sleep", sleep)
    snapshot_times = []

    def snapshot(*_args: object) -> list[str]:
        snapshot_times.append(now)
        if len(snapshot_times) == 1:
            raise RuntimeError("sensitive-diagnostic-output")
        return ["clusterorder present=False"]

    monkeypatch.setattr(scenario, "snapshot_cluster_deletion", snapshot)
    with caplog.at_level(logging.INFO, logger=scenario.logger.name):
        scenario.test_cluster_create(
            cli=cli,
            grpc=grpc,
            private_grpc=private,
            k8s_hub_client=k8s,
            cluster_template="template",
            pull_secret_name="test-pull-secret",
            ssh_public_key_path="/tmp/test-key.pub",
            metering=metering,
        )

    assert snapshot_times == [0, 65, 125]  # BMI, parent and dependent-GC waits share a monotonic throttle.
    assert "CaaS deletion snapshot unavailable" in caplog.text
    assert "sensitive-diagnostic-output" not in caplog.text
    assert (
        k8s.is_absent.call_args_list
        == [call(resource=_PARENT, name="order-a")] * 8 + [call(resource=_INFRAENV, name="order-a-infraenv")] * 12
    )
    grpc_removal.assert_called_once_with(grpc=grpc, uuid="cluster-id")
    k8s.is_present.assert_called_once_with(resource=_PARENT, name="order-a")
    cli.delete_cluster.assert_has_calls([call(uuid="cluster-id"), call(uuid="cluster-id")])
    k8s.patch.assert_not_called()
    k8s.apply.assert_not_called()
    k8s.delete.assert_not_called()
