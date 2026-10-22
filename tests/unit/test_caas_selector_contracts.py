from __future__ import annotations

import subprocess
from collections.abc import Callable
from typing import Any
from unittest.mock import Mock

import pytest

from tests.e2e.caas.sanity import test_cluster_create as scenario
from tests.e2e.core.k8s_client import K8sClient
from tests.e2e.references import test_cluster_baremetal_references as references


def _node_pool(node_set: str, instance_type: str, replicas: int = 1) -> dict[str, Any]:
    selector = {"osac.openshift.io/clusterorder": "order-a", "osac.openshift.io/node-set": node_set}
    return {
        "metadata": {"labels": {**selector, "osac.openshift.io/instance_type": instance_type}},
        "spec": {"replicas": replicas, "platform": {"agent": {"agentLabelSelector": {"matchLabels": selector}}}},
    }


def test_single_worker_accepts_nodeset_selector(monkeypatch: pytest.MonkeyPatch) -> None:
    k8s = Mock(spec=K8sClient)
    # Another pool uses the same BMIT; the requested NodeSet must win.
    pools = [_node_pool("batch", "ci-worker-bm"), _node_pool("workers", "ci-worker-bm")]
    k8s.list_json.return_value = {"items": pools}
    monkeypatch.setattr(scenario, "wait_for_hosted_cluster_kubeconfig", Mock(return_value=b"test-kubeconfig"))

    def guest_ready(*, get_node_pool: Callable[[], dict[str, Any]], **kwargs: object) -> dict[str, Any]:
        pool = get_node_pool()
        assert pool == pools[1]
        return pool

    monkeypatch.setattr(scenario, "wait_for_cluster_guest_readiness", guest_ready)
    scenario._assert_guest_workers_ready(
        k8s=k8s,
        co_name="order-a",
        hosted_cluster_name="cluster-a",
        hosted_cluster_ns="osac",
        worker_node_set="workers",
        worker_instance_type="ci-worker-bm",
        expected_workers=1,
    )


def test_node_requests_preserve_nodesets_sharing_a_type() -> None:
    instance_types = {"compute": "bm.large", "gpu": "bm.large"}
    replicas = scenario._assert_two_node_set_requests(
        cluster_order={
            "spec": {
                "nodeRequests": [
                    {"nodeSet": node_set, "bareMetal": {"instanceType": instance_type}, "numberOfNodes": 1}
                    for node_set, instance_type in instance_types.items()
                ]
            }
        },
        cluster_node_sets={
            node_set: {"baremetalInstanceType": {"name": instance_type}, "size": 1}
            for node_set, instance_type in instance_types.items()
        },
        instance_types=instance_types,
    )
    assert replicas == {"compute": 1, "gpu": 1}


@pytest.mark.parametrize("wrong_label", ["osac.openshift.io/node-set", "osac.openshift.io/clusterorder"])
def test_node_pool_rejects_wrong_selector(monkeypatch: pytest.MonkeyPatch, wrong_label: str) -> None:
    k8s = Mock(spec=K8sClient)
    pool = _node_pool("compute", "bm.large")
    pool["spec"]["platform"]["agent"]["agentLabelSelector"]["matchLabels"][wrong_label] = "other"
    k8s.list_json.return_value = {"items": [pool]}
    monkeypatch.setattr(scenario, "poll_until", lambda **kwargs: kwargs["fn"]())
    with pytest.raises(AssertionError):
        scenario._assert_isolated_node_pools(
            k8s=k8s,
            co_name="order-a",
            hosted_cluster_ns="osac",
            expected_replicas={"compute": 1},
            instance_types={"compute": "bm.large"},
        )


def test_node_pools_preserve_shared_type_and_independent_replicas(monkeypatch: pytest.MonkeyPatch) -> None:
    k8s = Mock(spec=K8sClient)
    k8s.list_json.return_value = {"items": [_node_pool("compute", "bm.large", 2), _node_pool("gpu", "bm.large", 3)]}

    def poll_once(
        *, fn: Callable[[], dict[str, Any]], until: Callable[[dict[str, Any]], bool], **kwargs: object
    ) -> dict[str, Any]:
        value = fn()
        assert until(value)
        return value

    monkeypatch.setattr(scenario, "poll_until", poll_once)
    scenario._assert_isolated_node_pools(
        k8s=k8s,
        co_name="order-a",
        hosted_cluster_ns="osac",
        expected_replicas={"compute": 2, "gpu": 3},
        instance_types={"compute": "bm.large", "gpu": "bm.large"},
    )


def test_reference_scenario_accepts_non_shared_type_creation_rejection() -> None:
    private = Mock()
    private.create_bare_metal_instance_type.side_effect = subprocess.CalledProcessError(
        67,
        ["grpcurl"],
        stderr=(
            "Code: InvalidArgument\n"
            "Message: field 'metadata.tenant' must be 'shared' or empty for bare metal instance types"
        ),
    )
    references.TestClusterBareMetalReferences().test_non_shared_hardware_type_creation_is_rejected(private_grpc=private)
    private.create_bare_metal_instance_type.assert_called_once()
    assert private.create_bare_metal_instance_type.call_args.kwargs["tenant"] == "tenant2"
    private.call.assert_not_called()


def test_installed_agents_keep_nodesets_sharing_a_type_separate(monkeypatch: pytest.MonkeyPatch) -> None:
    k8s = Mock(spec=K8sClient)
    k8s.namespace = "osac"
    agents = [
        {
            "metadata": {"name": node_set, "labels": _node_pool(node_set, "bm.large")["metadata"]["labels"]},
            "status": {"conditions": [{"type": "Installed", "status": "True"}]},
        }
        for node_set in ("compute", "gpu")
    ]
    k8s.list_json.return_value = {"items": agents}
    monkeypatch.setattr(scenario, "poll_until", lambda **kwargs: kwargs["fn"]())
    result = scenario._assert_installed_agents_by_node_set(
        k8s=k8s,
        co_name="order-a",
        expected_replicas={"compute": 1, "gpu": 1},
        instance_types={"compute": "bm.large", "gpu": "bm.large"},
    )
    assert {node_set: [agent["metadata"]["name"] for agent in items] for node_set, items in result.items()} == {
        "compute": ["compute"],
        "gpu": ["gpu"],
    }


def test_reference_scenario_checks_missing_shared_type_without_creating_one() -> None:
    public = Mock()
    public.call.side_effect = subprocess.CalledProcessError(
        67,
        ["grpcurl"],
        stderr=(
            'Code: InvalidArgument\nMessage: object.spec.node_sets["workers"].baremetal_instance_type: object not found'
        ),
    )
    references.TestClusterBareMetalReferences().test_unknown_shared_hardware_type_is_not_selectable_for_caas(
        jwt_grpc_tenant1=public, cluster_template="template", cluster_version="version"
    )
    public.call.assert_called_once()
    assert public.call.call_args.kwargs["service"].endswith(".Clusters/Create")
    node_sets = public.call.call_args.kwargs["data"]["object"]["spec"]["node_sets"]
    assert node_sets["workers"]["baremetal_instance_type"]["name"].startswith("ref-missing-worker-")
