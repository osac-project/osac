from __future__ import annotations

import logging
from unittest.mock import Mock

import pytest

from tests.e2e.caas.sanity import test_cluster_create as scenario
from tests.e2e.caas.sanity.test_cluster_create import (
    _agent_is_installing,
    _cluster_order_agents,
    _list_owned_worker_bmis,
)


@pytest.mark.parametrize("bad_field", ["tenant", "label", "owner"])
def test_public_worker_lookup_rejects_bmis_not_owned_by_this_cluster(bad_field: str) -> None:
    grpc = Mock()
    grpc.list_baremetal_instance_ids.return_value = ["worker-id"]
    metadata = {
        "tenant": "tenant1",
        "labels": {"osac.openshift.io/cluster-order": "order-a"},
        "annotations": {"osac.openshift.io/owner-reference": "ClusterOrder/order-a"},
    }
    if bad_field == "tenant":
        metadata["tenant"] = "tenant2"
    elif bad_field == "label":
        metadata["labels"]["osac.openshift.io/cluster-order"] = "order-b"
    else:
        metadata["annotations"]["osac.openshift.io/owner-reference"] = "ClusterOrder/order-b"
    grpc.get_baremetal_instance.return_value = {"object": {"id": "worker-id", "metadata": metadata}}

    with pytest.raises(AssertionError):
        _list_owned_worker_bmis(grpc=grpc, co_name="order-a", tenant="tenant1")


def test_public_worker_lookup_filters_by_cluster_and_returns_owned_bmis() -> None:
    grpc = Mock()
    grpc.list_baremetal_instance_ids.side_effect = [[], ["worker-id"]]
    worker = {
        "id": "worker-id",
        "metadata": {
            "tenant": "tenant1",
            "labels": {"osac.openshift.io/cluster-order": "order-a"},
            "annotations": {"osac.openshift.io/owner-reference": "ClusterOrder/order-a"},
        },
    }
    grpc.get_baremetal_instance.return_value = {"object": worker}

    assert _list_owned_worker_bmis(grpc=grpc, co_name="order-a", tenant="tenant1") == {}
    assert _list_owned_worker_bmis(grpc=grpc, co_name="order-a", tenant="tenant1") == {"worker-id": worker}
    grpc.list_baremetal_instance_ids.assert_called_with(
        filter_expr='this.metadata.labels["osac.openshift.io/cluster-order"] == "order-a"'
    )
    grpc.get_baremetal_instance.assert_called_once_with(bmi_id="worker-id")


def test_cluster_order_agents_only_returns_agents_from_its_infraenv_and_namespace() -> None:
    k8s = Mock()
    expected = {
        "metadata": {
            "name": "agent-a",
            "namespace": "osac",
            "labels": {"infraenvs.agent-install.openshift.io": "order-a-infraenv"},
        },
        "status": {"debugInfo": {"state": "known"}},
    }
    wrong_infraenv = {
        "metadata": {"namespace": "osac", "labels": {"infraenvs.agent-install.openshift.io": "order-b-infraenv"}}
    }
    wrong_namespace = {
        "metadata": {"namespace": "other", "labels": {"infraenvs.agent-install.openshift.io": "order-a-infraenv"}}
    }
    k8s.list_json.return_value = {"items": [wrong_infraenv, wrong_namespace, expected]}

    assert _cluster_order_agents(k8s=k8s, co_name="order-a", namespace="osac") == [expected]
    k8s.list_json.assert_called_once_with(resource="agents.agent-install.openshift.io", namespace="osac")


@pytest.mark.parametrize(
    ("state", "expected"),
    [
        ("known", False),
        ("preparing-for-installation", False),
        ("Installing", False),
        ("installing", True),
        ("installing-in-progress", True),
        ("installed", False),
    ],
)
def test_agent_is_installing_uses_assisted_service_debug_state(state: str, expected: bool) -> None:
    assert _agent_is_installing({"status": {"debugInfo": {"state": state}}}) is expected


def test_primary_caas_scenario_reports_each_stage_without_logging_secret_values(
    monkeypatch: pytest.MonkeyPatch, caplog: pytest.LogCaptureFixture
) -> None:
    cli, grpc, private, k8s, metering = (Mock() for _ in range(5))
    private.ensure_disk_image.return_value = "disk-id"
    private.ensure_cluster_version.return_value = {"name": "version-name"}
    cli.create_cluster.return_value = "cluster-id"
    grpc.list_cluster_ids.return_value = ["cluster-id"]
    grpc.get_cluster.return_value = {"object": {"metadata": {"tenant": "tenant1"}}}
    grpc.list_baremetal_instance_ids.return_value = ["bmi-id"]
    grpc.get_baremetal_instance.return_value = {
        "object": {
            "metadata": {
                "tenant": "tenant1",
                "labels": {"osac.openshift.io/cluster-order": "order-a"},
                "annotations": {"osac.openshift.io/owner-reference": "ClusterOrder/order-a"},
            }
        }
    }
    k8s.namespace = "osac"
    k8s.get_json.return_value = {
        "metadata": {"namespace": "osac", "annotations": {"osac.openshift.io/tenant": "tenant1"}}
    }
    k8s.list_json.return_value = {
        "items": [
            {
                "metadata": {
                    "name": "agent-a",
                    "namespace": "osac",
                    "labels": {"infraenvs.agent-install.openshift.io": "order-a-infraenv"},
                },
                "status": {"debugInfo": {"state": "installing-in-progress", "stateInfo": "sensitive-agent-data"}},
            }
        ]
    }
    monkeypatch.setattr(scenario, "wait_for_cluster_order_cr", lambda **_kwargs: "order-a")
    monkeypatch.setattr(scenario, "wait_for_cluster_progressing", lambda **_kwargs: None)

    class StopAfterAgent(Exception):
        pass

    calls = 0

    def poll_once(**kwargs: object) -> object:
        nonlocal calls
        calls += 1
        if calls == 4:  # Stop before the unrelated operator metrics check.
            raise StopAfterAgent
        fn = kwargs["fn"]
        assert callable(fn)
        return fn()

    monkeypatch.setattr(scenario, "poll_until", poll_once)
    with caplog.at_level(logging.INFO, logger=scenario.logger.name), pytest.raises(StopAfterAgent):
        scenario.test_cluster_create(
            cli=cli,
            grpc=grpc,
            private_grpc=private,
            k8s_hub_client=k8s,
            cluster_template="template",
            pull_secret_name="sensitive-token",
            ssh_public_key_path="/tmp/key.pub",
            metering=metering,
        )

    messages = [record.getMessage() for record in caplog.records if record.name == scenario.logger.name]
    stages = [message for message in messages if message.startswith("CaaS:")]
    assert stages == [
        "CaaS: ensuring DiskImage and ClusterVersion",
        "CaaS: ClusterVersion available",
        "CaaS: creating cluster",
        "CaaS: cluster created",
        "CaaS: waiting for ClusterOrder",
        "CaaS: waiting for ClusterOrder to progress",
        "CaaS: waiting for tenant-visible child BareMetalInstance",
        "CaaS: child BareMetalInstance found",
        "CaaS: waiting for Agent in ClusterOrder namespace",
        "CaaS: Agent found; waiting for installing state",
        "CaaS: Agent entered installing state",
    ]
    assert "CaaS Agent state: installing-in-progress" in messages
    assert "sensitive-token" not in caplog.text
    assert "sensitive-agent-data" not in caplog.text
