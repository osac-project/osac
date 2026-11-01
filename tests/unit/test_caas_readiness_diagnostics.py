import json

import pytest

from tests.e2e.caas import readiness_diagnostics
from tests.e2e.core.k8s_client import K8sClient


def test_snapshot_reports_readiness_facts_without_resource_payloads(monkeypatch: pytest.MonkeyPatch) -> None:
    objects = {
        ("clusterorders.osac.openshift.io", "osac-e2e-ci", "order-x"): {
            "status": {
                "phase": "Progressing",
                "desiredWorkers": 2,
                "currentWorkers": 2,
                "readyWorkers": 2,
                "clusterReference": {"hostedClusterName": "order-x", "namespace": "osac-e2e-ci-order-x"},
                "conditions": [{"type": "ClusterAvailable", "status": "True", "reason": "WorkersReady"}],
            }
        },
        ("hostedclusters.hypershift.openshift.io", "osac-e2e-ci-order-x", None): {
            "items": [
                {
                    "metadata": {"name": "order-x"},
                    "status": {"conditions": [{"type": "Available", "status": "False", "reason": "Waiting"}]},
                }
            ]
        },
        ("hostedcontrolplanes.hypershift.openshift.io", "osac-e2e-ci-order-x-order-x", None): {
            "items": [
                {
                    "metadata": {"name": "order-x"},
                    "status": {"conditions": [{"type": "Available", "status": "False", "reason": "WaitingForPods"}]},
                }
            ]
        },
        ("nodepools.hypershift.openshift.io", "osac-e2e-ci-order-x", None): {
            "items": [
                {
                    "metadata": {"labels": {"osac.openshift.io/clusterorder": "order-x"}},
                    "spec": {"replicas": 2},
                    "status": {"nodesInfo": {"nodeVersions": [{"readyNodeCount": 1}]}},
                }
            ]
        },
        ("agents.agent-install.openshift.io", "osac-e2e-ci", None): {
            "items": [
                {
                    "metadata": {"labels": {"osac.openshift.io/clusterorder": "order-x"}},
                    "status": {"debugInfo": {"state": "installing"}, "conditions": []},
                },
                {
                    "metadata": {"labels": {"infraenvs.agent-install.openshift.io": "order-x-infraenv"}},
                    "status": {
                        "debugInfo": {"state": "installed", "secret": "must-not-log"},
                        "conditions": [{"type": "Installed", "status": "True"}],
                    },
                },
            ]
        },
    }

    def fake_run(*args: str, timeout: int = 300) -> tuple[str, int]:
        resource = args[args.index("get") + 1]
        namespace = args[args.index("-n") + 1]
        name = None
        if args[args.index("get") + 2] != "-n":
            name = args[args.index("get") + 2]
        value = objects.get((resource, namespace, name), {"items": []})
        return json.dumps(value), 0

    monkeypatch.setattr(readiness_diagnostics, "run_unchecked", fake_run)
    output = "\n".join(readiness_diagnostics.snapshot_cluster_readiness(K8sClient(namespace="osac-e2e-ci"), "order-x"))

    assert "clusterorder phase=Progressing desired_workers=2 current_workers=2 ready_workers=2" in output
    assert "hostedcontrolplane condition=Available status=False reason=WaitingForPods" in output
    assert "nodepools count=1 desired=2 ready=1" in output
    assert "agents count=2 installed=1" in output
    assert "agent state=installing count=1" in output
    assert "must-not-log" not in output


def test_snapshot_omits_untrusted_condition_and_agent_state_values(monkeypatch: pytest.MonkeyPatch) -> None:
    def fake_run(*args: str, timeout: int = 300) -> tuple[str, int]:
        resource = args[args.index("get") + 1]
        if resource == "clusterorders.osac.openshift.io":
            return json.dumps(
                {
                    "status": {
                        "phase": ["secret-value"],
                        "conditions": [{"type": "bad reason", "status": "False", "reason": "secret value"}],
                    }
                }
            ), 0
        if resource == "agents.agent-install.openshift.io":
            return json.dumps(
                {
                    "items": [
                        {
                            "metadata": {"labels": {"osac.openshift.io/clusterorder": "order-x"}},
                            "status": {"debugInfo": {"state": "secret-value"}},
                        }
                    ]
                }
            ), 0
        return json.dumps({"items": []}), 0

    monkeypatch.setattr(readiness_diagnostics, "run_unchecked", fake_run)
    output = "\n".join(readiness_diagnostics.snapshot_cluster_readiness(K8sClient(namespace="osac-e2e-ci"), "order-x"))

    assert "secret-value" not in output
    assert "phase=omitted" in output
    assert "agent state=omitted count=1" in output
