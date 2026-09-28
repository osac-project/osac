"""Unit checks for the temporary, read-only CaaS teardown snapshot."""

import json
import subprocess

import pytest

from tests.e2e.caas import deletion_diagnostics
from tests.e2e.core.k8s_client import K8sClient


def test_snapshot_reports_blockers_without_dumping_resource_data(monkeypatch: pytest.MonkeyPatch) -> None:
    calls: list[tuple[str, ...]] = []

    def fake_run(*args: str, timeout: int = 300) -> tuple[str, int]:
        calls.append(args)
        resource = args[args.index("get") + 1]
        if resource == "namespaces":
            return json.dumps(
                {
                    "metadata": {"deletionTimestamp": "2026-01-01T00:00:00Z", "finalizers": ["kubernetes"]},
                    "status": {"phase": "Terminating"},
                }
            ), 0
        items = {
            "clusterorders": [
                {
                    "metadata": {
                        "name": "order-x",
                        "deletionTimestamp": "2026-01-01T00:00:00Z",
                        "finalizers": ["osac.openshift.io/finalizer"],
                    }
                }
            ],
            "infraenvs.agent-install.openshift.io": [{"metadata": {"name": "order-x-infraenv"}}],
            "hostedclusters.hypershift.openshift.io": [
                {
                    "metadata": {
                        "name": "order-x",
                        "deletionTimestamp": "2026-01-01T00:00:00Z",
                        "finalizers": ["hypershift.openshift.io/finalizer"],
                    }
                }
            ],
            "nodepools.hypershift.openshift.io": [
                {"metadata": {"name": "nodepool-order-x-worker", "deletionTimestamp": "2026-01-01T00:00:00Z"}}
            ],
            "hostedcontrolplanes.hypershift.openshift.io": [
                {
                    "metadata": {"name": "order-x", "deletionTimestamp": "2026-01-01T00:00:00Z", "generation": 5},
                    "status": {
                        "conditions": [
                            {
                                "type": "Available",
                                "status": "False",
                                "reason": "WaitingForPods",
                                "message": "secret-value",
                                "observedGeneration": 4,
                            },
                            {
                                "type": "Degraded",
                                "status": "True",
                                "reason": "secret-value with spaces",
                                "message": "secret-value",
                            },
                        ]
                    },
                }
            ],
            "clusters.cluster.x-k8s.io": [{"metadata": {"name": "capi-x"}}],
            "machinesets.cluster.x-k8s.io": [{"metadata": {"name": "machineset-x"}}],
            "machines.cluster.x-k8s.io": [
                {
                    "metadata": {
                        "name": "machine-x",
                        "deletionTimestamp": "2026-01-01T00:00:00Z",
                        "annotations": {
                            "pre-terminate.delete.hook.machine.cluster.x-k8s.io/agentmachine": "secret-value"
                        },
                    },
                    "status": {
                        "conditions": [
                            {
                                "type": "Deleting",
                                "status": "True",
                                "reason": "WaitingForPreTerminateHook",
                                "message": "secret-value",
                            },
                            {
                                "type": "PreTerminateDeleteHookSucceeded",
                                "status": "False",
                                "reason": "WaitingForPreTerminateHook",
                                "message": "secret-value",
                            },
                        ]
                    },
                }
            ],
            "agents.agent-install.openshift.io": [
                {
                    "metadata": {"labels": {"infraenvs.agent-install.openshift.io": "order-x-infraenv"}},
                    "status": {"debugInfo": {"state": "installed", "message": "secret-value"}},
                },
                {
                    "metadata": {"labels": {"infraenvs.agent-install.openshift.io": "order-x-infraenv"}},
                    "status": {"debugInfo": {"state": "added-to-existing-cluster", "message": "secret-value"}},
                },
                {
                    "metadata": {"labels": {"infraenvs.agent-install.openshift.io": "order-x-infraenv"}},
                    "status": {"debugInfo": {"state": "unbinding", "message": "secret-value"}},
                },
                {
                    "metadata": {"labels": {"infraenvs.agent-install.openshift.io": "order-x-infraenv"}},
                    "status": {"debugInfo": {"state": "secret-value"}},
                },
            ],
            "agentclusters.capi-provider.agent-install.openshift.io": [
                {
                    "metadata": {
                        "name": "agentcluster-x",
                        "finalizers": ["agentclustercapi-provider.agent-install.openshift.io/deprovision"],
                    }
                }
            ],
        }
        return json.dumps({"items": items[resource], "password": "secret-value"}), 0

    monkeypatch.setattr(deletion_diagnostics, "run_unchecked", fake_run)
    lines = deletion_diagnostics.snapshot_cluster_deletion(K8sClient(namespace="osac-e2e-ci"), "order-x")
    output = "\n".join(lines)
    assert "hostedcluster present=True terminating=True finalizers=1 hypershift_finalizer=True" in output
    assert "hostedcontrolplane count=1 terminating=1" in output
    assert "hcp generation=5 condition=Available status=False reason=WaitingForPods observed=4" in output
    assert "hcp generation=5 condition=Degraded status=True reason=omitted" in output
    assert "capi-cluster count=1 terminating=0" in output
    assert "machineset count=1 terminating=0" in output
    assert "machine count=1 terminating=1 preterminate_hook=1" in output
    assert "machine deletion condition=Deleting status=True reason=WaitingForPreTerminateHook" in output
    assert (
        "machine deletion condition=PreTerminateDeleteHookSucceeded status=False reason=WaitingForPreTerminateHook"
        in output
    )
    assert "agent state=added-to-existing-cluster count=1" in output
    assert "agent state=unbinding count=1" in output
    assert "agent state=installed count=1" in output
    assert "agent state=omitted count=1" in output
    assert "agentcluster count=1 deprovision_finalizer=1" in output
    assert "infraenv present=True" in output
    assert "namespace control-plane present=True terminating=True" in output
    assert "secret-value" not in output
    assert all("patch" not in args and "delete" not in args for args in calls)
    assert any("osac-e2e-ci-order-x-order-x" in args for args in calls)


def test_snapshot_omits_untrusted_agent_state_types(monkeypatch: pytest.MonkeyPatch) -> None:
    def fake_run(*args: str, timeout: int = 300) -> tuple[str, int]:
        resource = args[args.index("get") + 1]
        if resource == "agents.agent-install.openshift.io":
            return json.dumps(
                {
                    "items": [
                        {
                            "metadata": {"labels": {"infraenvs.agent-install.openshift.io": "order-x-infraenv"}},
                            "status": {"debugInfo": {"state": ["secret-value"]}},
                        }
                    ]
                }
            ), 0
        return json.dumps({"items": []}), 0

    monkeypatch.setattr(deletion_diagnostics, "run_unchecked", fake_run)
    lines = deletion_diagnostics.snapshot_cluster_deletion(K8sClient(namespace="osac-e2e-ci"), "order-x")
    assert "agent state=omitted count=1" in lines
    assert "secret-value" not in "\n".join(lines)


def test_snapshot_suppresses_kubectl_errors_and_untrusted_output(monkeypatch: pytest.MonkeyPatch) -> None:
    def fake_run(*args: str, timeout: int = 300) -> tuple[str, int]:
        resource = args[args.index("get") + 1]
        if resource == "machines.cluster.x-k8s.io":
            raise subprocess.TimeoutExpired(args, 3, output="SECRET")
        return "SECRET from server", 1

    monkeypatch.setattr(deletion_diagnostics, "run_unchecked", fake_run)
    lines = deletion_diagnostics.snapshot_cluster_deletion(K8sClient(namespace="osac-e2e-ci"), "order-x")
    assert "machine unavailable" in lines
    assert "SECRET" not in "\n".join(lines)
