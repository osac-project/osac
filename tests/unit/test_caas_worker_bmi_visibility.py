from __future__ import annotations

import logging
from pathlib import Path
from unittest.mock import Mock

import pytest

from tests.e2e.caas.sanity import test_cluster_create as scenario
from tests.e2e.caas.sanity.test_cluster_create import _list_owned_worker_bmis
from tests.e2e.core import helpers, runner
from tests.e2e.core.helpers import cluster_order_agents


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

    assert cluster_order_agents(k8s=k8s, co_name="order-a", namespace="osac") == [expected]
    k8s.list_json.assert_called_once_with(resource="agents.agent-install.openshift.io", namespace="osac")


@pytest.mark.parametrize(
    ("status", "expected"),
    [
        ({"debugInfo": {"state": "installing-in-progress"}}, "installing-in-progress"),
        (
            {
                "debugInfo": {"state": "installing-in-progress"},
                "progress": {"currentStage": "Rebooting", "installationPercentage": 55},
            },
            "installing-in-progress, Rebooting, 55%",
        ),
        # Unknown states and stages are never echoed: they are free-form text
        # that can carry inventory or provider details.
        ({"debugInfo": {"state": "leaky"}, "progress": {"currentStage": "leaky-stage"}}, "unknown"),
    ],
)
def test_agent_install_progress_only_reports_known_states_and_stages(status: dict, expected: str) -> None:
    assert helpers._agent_install_progress({"status": status}) == expected


def test_primary_caas_scenario_reports_each_stage_without_logging_secret_values(
    monkeypatch: pytest.MonkeyPatch, caplog: pytest.LogCaptureFixture, tmp_path: Path
) -> None:
    key_file = tmp_path / "id.pub"
    key_file.write_text("ssh-ed25519 unit-test-key\n")
    cli, grpc, private, k8s, metering = (Mock() for _ in range(5))
    private.ensure_disk_image.return_value = "disk-id"
    private.ensure_cluster_version.return_value = {"id": "version-id", "name": "version-name"}
    cli.create_cluster.return_value = "cluster-id"
    cli.create_cluster_with_catalog_item.return_value = "cluster-id"
    grpc.list_cluster_ids.return_value = ["cluster-id"]
    catalog_item_id = Mock(name="catalog-item-id")
    private.create_cluster_catalog_item.return_value = catalog_item_id
    # The catalog offering, not caller input, materializes the cluster spec.
    grpc.get_cluster.return_value = {
        "object": {
            "metadata": {"tenant": "tenant1"},
            "spec": {
                "version": {"name": "version-name"},
                "nodeSets": {"workers": {"size": 1, "baremetalInstanceType": {"name": "ci-worker-bm"}}},
                "catalogItem": {"id": catalog_item_id},
                "template": {"id": "template-id"},
                "sshKey": {"id": "secret-id", "name": "test-ssh-key-secret"},
                "pullSecretSecret": {"name": "sensitive-token"},
            },
        }
    }
    grpc.list_baremetal_instance_ids.return_value = ["bmi-id"]
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
                "sshKey": {"id": "secret-id"},
            },
        }
    }
    k8s.namespace = "osac"
    k8s.get_cluster_order_spec.return_value = {
        "releaseImage": "release-image",
        "nodeRequests": [{"bareMetal": {"instanceType": "ci-worker-bm"}, "numberOfNodes": 1}],
        "sshKey": {"id": "secret-id"},
    }
    cluster_order = {
        "metadata": {"namespace": "osac", "annotations": {"osac.openshift.io/tenant": "tenant1"}},
        "spec": {"nodeRequests": [{"nodeSet": "compute", "numberOfNodes": 1}]},
        "status": {"desiredWorkers": 1, "currentWorkers": 1, "readyWorkers": 1},
    }
    k8s.get_json.side_effect = lambda *, resource, **_kwargs: (
        cluster_order if resource == "clusterorder" else {"spec": {"sshAuthorizedKey": "ssh-ed25519 unit-test-key"}}
    )
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

    class StopAfterWorkersJoined(Exception):
        pass

    calls = 0

    def poll_once(**kwargs: object) -> object:
        nonlocal calls
        calls += 1
        if calls == 5:  # Stop before the unrelated operator metrics check.
            raise StopAfterWorkersJoined
        fn = kwargs["fn"]
        assert callable(fn)
        return fn()

    # The bring-up waits live in helpers, so both modules must use the harness.
    monkeypatch.setattr(scenario, "poll_until", poll_once)
    monkeypatch.setattr(helpers, "poll_until", poll_once)
    monkeypatch.setattr(runner.time, "sleep", lambda _delay: None)
    with caplog.at_level(logging.INFO, logger=scenario.logger.name), pytest.raises(StopAfterWorkersJoined):
        scenario.test_cluster_create(
            cli=cli,
            grpc=grpc,
            private_grpc=private,
            k8s_hub_client=k8s,
            cluster_template="template",
            pull_secret_name="sensitive-token",
            ssh_key_secret="test-ssh-key-secret",
            ssh_public_key_path=str(key_file),
            metering=metering,
        )

    loggers = {scenario.logger.name, helpers.logger.name}
    messages = [record.getMessage() for record in caplog.records if record.name in loggers]
    stages = [message for message in messages if message.startswith(("CaaS:", "CaaS phase:"))]
    # Every bring-up step announces itself, so a long run reads as phases.
    assert stages == [
        "CaaS: ensuring DiskImage and ClusterVersion",
        "CaaS: ClusterVersion available",
        "CaaS: creating ClusterCatalogItem",
        "CaaS: ClusterCatalogItem created",
        "CaaS: creating cluster from catalog item",
        "CaaS: cluster created",
        "CaaS: waiting for ClusterOrder",
        "CaaS: waiting for ClusterOrder to progress",
        "CaaS phase: BareMetalInstances binding",
        "CaaS: waiting for tenant-visible child BareMetalInstance",
        "CaaS: child BareMetalInstance found",
        "CaaS phase: Agents binding",
        "CaaS: 1 of 1 worker Agent(s) registered",
        "CaaS: Agents bound: 1 worker Agent(s) registered",
        "CaaS phase: starting install",
        "CaaS: installing, 0 of 1 done (installing-in-progress)",
        "CaaS phase: install done",
        "CaaS: installation finished on 1 worker Agent(s)",
        "CaaS phase: waiting for workers to join",
        "CaaS: workers desired 1, current 1, ready 1 (unknown)",
        "CaaS phase: workers joined",
        "CaaS: 1 worker(s) joined and ready",
    ]
    assert "sensitive-token" not in caplog.text
    assert "sensitive-agent-data" not in caplog.text
