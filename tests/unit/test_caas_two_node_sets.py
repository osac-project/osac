from __future__ import annotations

from collections.abc import Callable
from typing import Any
from unittest.mock import Mock, call

import pytest

from tests.e2e.caas.regression import test_cluster_node_sets as regression
from tests.e2e.caas.sanity import test_cluster_create as scenario
from tests.e2e.core import helpers, runner
from tests.e2e.core.k8s_client import K8sClient


@pytest.fixture
def two_node_set_clients(monkeypatch: pytest.MonkeyPatch) -> dict[str, Mock]:
    clients = {name: Mock() for name in ("cli", "grpc", "private_grpc", "metering")}
    k8s = clients["k8s_hub_client"] = Mock(spec=K8sClient)
    k8s.namespace = "osac"
    cli, grpc, private = (clients[name] for name in ("cli", "grpc", "private_grpc"))
    cli.create_cluster.return_value = "cluster-id"
    private.ensure_disk_image.return_value = "disk-id"
    private.ensure_cluster_version.return_value = {"name": "version-name"}
    grpc.list_cluster_ids.return_value = ["cluster-id"]
    grpc.get_cluster.return_value = {
        "object": {
            "metadata": {"tenant": "tenant1"},
            "spec": {
                "nodeSets": {
                    "compute": {"size": 1, "baremetalInstanceType": {"name": "ci-worker-bm"}},
                    "gpu": {"size": 1, "baremetalInstanceType": {"name": "ci-worker-bm-gpu"}},
                }
            },
        }
    }
    workers = {}
    agents = []
    node_pools = []
    for bmi_id, node_set, instance_type in (
        ("cpu-id", "compute", "ci-worker-bm"),
        ("gpu-id", "gpu", "ci-worker-bm-gpu"),
    ):
        workers[bmi_id] = {
            "object": {
                "metadata": {
                    "tenant": "tenant1",
                    "labels": {"osac.openshift.io/cluster-order": "order-a"},
                    "annotations": {"osac.openshift.io/owner-reference": "ClusterOrder/order-a"},
                },
                "spec": {
                    "template": {"id": "osac.templates.bm_host_provisioning", "shared": True},
                    "instanceType": {"name": instance_type, "shared": True},
                },
            }
        }
        selector = {"osac.openshift.io/clusterorder": "order-a", "osac.openshift.io/node-set": node_set}
        labels = {**selector, "osac.openshift.io/instance_type": instance_type}
        agents.append(
            {
                "metadata": {"name": f"{bmi_id}-agent", "namespace": "osac", "labels": labels},
                "status": {"conditions": [{"type": "Installed", "status": "True"}]},
            }
        )
        node_pools.append(
            {
                "metadata": {"labels": labels},
                "spec": {"replicas": 1, "platform": {"agent": {"agentLabelSelector": {"matchLabels": selector}}}},
            }
        )

    # The GPU worker outlives the CPU worker: teardown must wait for both.
    grpc.list_baremetal_instance_ids.side_effect = [["cpu-id", "gpu-id"], ["gpu-id"], []]
    grpc.get_baremetal_instance.side_effect = lambda *, bmi_id: workers[bmi_id]
    k8s.list_json.side_effect = lambda *, resource, namespace: {
        "items": agents if resource == "agents.agent-install.openshift.io" else node_pools
    }
    cluster_order = {
        "metadata": {"namespace": "osac", "annotations": {"osac.openshift.io/tenant": "tenant1"}},
        "spec": {
            "nodeRequests": [
                {"nodeSet": node_set, "bareMetal": {"instanceType": instance_type}, "numberOfNodes": 1}
                for node_set, instance_type in (("compute", "ci-worker-bm"), ("gpu", "ci-worker-bm-gpu"))
            ]
        },
        "status": {"desiredWorkers": 2, "currentWorkers": 2, "readyWorkers": 2},
    }

    def get_resource(*, resource: str, name: str) -> dict[str, Any]:
        if resource == "clusterorder":
            return cluster_order
        assert resource == "baremetalinstance"
        return {
            "metadata": {
                "labels": {"osac.openshift.io/baremetalinstance-uuid": name.removesuffix("-cr")},
                "annotations": {"osac.openshift.io/tenant": "tenant1"},
            }
        }

    k8s.get_json.side_effect = get_resource
    k8s.get_cluster_order_namespace.return_value = "osac"
    k8s.get_baremetal_instance_name.side_effect = lambda *, uuid, checked: f"{uuid}-cr"
    k8s.get_cluster_order_infra_env_name.return_value = "order-a-infraenv"
    k8s.is_absent.side_effect = [False, True, False, True]
    k8s.is_present.return_value = False
    monkeypatch.setattr(scenario, "wait_for_cluster_order_cr", Mock(return_value="order-a"))
    for name in (
        "wait_for_cluster_progressing",
        "wait_for_cluster_deleting",
        "wait_for_cluster_grpc_deleting_or_archived",
        "wait_for_cluster_grpc_removal",
    ):
        monkeypatch.setattr(scenario, name, Mock())
    monkeypatch.setattr(scenario, "snapshot_cluster_deletion", Mock(return_value=[]))
    monkeypatch.setattr(runner.time, "sleep", Mock())
    monkeypatch.setattr(
        helpers,
        "wait_for_cluster_deletion",
        Mock(side_effect=AssertionError("Two-node-set teardown must not use forced cleanup")),
    )
    return clients


def _run_scenario(clients: dict[str, Mock]) -> None:
    regression.test_cluster_create_with_two_node_sets(
        **clients, cluster_template="template", pull_secret_name="pull-secret", ssh_public_key_path="/tmp/key.pub"
    )


def test_two_node_set_scenario_uses_same_natural_teardown_budgets(
    monkeypatch: pytest.MonkeyPatch, two_node_set_clients: dict[str, Mock]
) -> None:
    scenario_poll = Mock(wraps=runner.poll_until)
    parent_poll = Mock(wraps=runner.poll_until)
    monkeypatch.setattr(scenario, "poll_until", scenario_poll)
    monkeypatch.setattr(helpers, "poll_until", parent_poll)

    _run_scenario(two_node_set_clients)

    polls = {item.kwargs["description"]: item.kwargs for item in scenario_poll.call_args_list}
    bmi_wait = polls["order-a CaaS worker BMI removal"]
    assert (bmi_wait["retries"], bmi_wait["delay"]) == (480, 5)
    assert (parent_poll.call_args.kwargs["retries"], parent_poll.call_args.kwargs["delay"]) == (121, 10)
    infraenv_wait = polls["InfraEnv removal after ClusterOrder deletion"]
    assert (infraenv_wait["retries"], infraenv_wait["delay"]) == (60, 5)
    assert two_node_set_clients["k8s_hub_client"].is_absent.call_args_list == [
        call(resource="clusterorder", name="order-a"),
        call(resource="clusterorder", name="order-a"),
        call(resource="infraenv.agent-install.openshift.io", name="order-a-infraenv"),
        call(resource="infraenv.agent-install.openshift.io", name="order-a-infraenv"),
    ]
    # The remaining GPU BMI must cause a retry, not early parent deletion checks.
    assert runner.time.sleep.call_args_list == [call(5), call(10), call(5)]
    helpers.wait_for_cluster_deletion.assert_not_called()
    scenario.wait_for_cluster_grpc_removal.assert_called_once_with(grpc=two_node_set_clients["grpc"], uuid="cluster-id")
    two_node_set_clients["cli"].delete_cluster.assert_has_calls([call(uuid="cluster-id"), call(uuid="cluster-id")])
    for name in ("patch", "apply", "delete"):
        getattr(two_node_set_clients["k8s_hub_client"], name).assert_not_called()


def test_two_node_set_scenario_rejects_foreign_worker_before_teardown(two_node_set_clients: dict[str, Mock]) -> None:
    grpc = two_node_set_clients["grpc"]
    lookup: Callable[..., dict[str, Any]] = grpc.get_baremetal_instance.side_effect
    foreign_worker = lookup(bmi_id="gpu-id")
    foreign_worker["object"]["metadata"]["annotations"]["osac.openshift.io/owner-reference"] = "ClusterOrder/other"

    with pytest.raises(AssertionError):
        _run_scenario(two_node_set_clients)

    # Only best-effort finally cleanup is allowed after an ownership failure.
    two_node_set_clients["cli"].delete_cluster.assert_called_once_with(uuid="cluster-id")
    scenario.wait_for_cluster_deleting.assert_not_called()
    helpers.wait_for_cluster_deletion.assert_not_called()
    two_node_set_clients["k8s_hub_client"].is_absent.assert_not_called()
