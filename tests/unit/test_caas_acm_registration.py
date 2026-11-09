"""Regressions for CaaS ACM checks; Kubernetes, controllers and time are faked."""

from __future__ import annotations

import json
import subprocess
from typing import Any
from unittest.mock import Mock

import pytest

from tests.e2e.caas import acm_registration as acm
from tests.e2e.core import k8s_client as k8s_module
from tests.e2e.core import runner
from tests.e2e.core.failures import E2EFailure, Kind
from tests.e2e.core.k8s_client import K8sClient


@pytest.fixture
def k8s(monkeypatch: pytest.MonkeyPatch) -> Mock:
    """Fake hub reads: a disabled policy, an unlabelled HostedCluster and absence."""
    client = Mock(spec=K8sClient)
    client.is_absent.return_value = True
    client.namespace = "osac"
    client.base_args.return_value = ["kubectl", "--kubeconfig", "/tmp/hub"]
    policy = {"spec": {"customizedVariables": [{"name": "autoImportDisabled", "value": "true"}]}}
    hosted_cluster = {"metadata": {"labels": {}}}

    def get_json(*, resource: str, **_kwargs: object) -> dict[str, Any]:
        """Answer the policy and HostedCluster reads; reject anything unexpected."""
        if resource == "addondeploymentconfigs.addon.open-cluster-management.io":
            return policy
        if resource == "hostedclusters.hypershift.openshift.io":
            return hosted_cluster
        raise AssertionError(f"Unexpected resource {resource}")

    client.get_json.side_effect = get_json
    monkeypatch.setattr(acm, "run", Mock(return_value=json.dumps({"items": []})))
    monkeypatch.setattr(acm, "_ABSENCE_OBSERVATIONS", 3)
    monkeypatch.setattr(runner.time, "sleep", Mock())
    return client


def assert_registration(k8s: Mock, *, template: str = "ocp-ci-small") -> None:
    acm.assert_acm_registration(
        k8s=k8s, hosted_cluster_name="order-a", hosted_cluster_ns="osac", cluster_template=template
    )


def agent() -> dict[str, Any]:
    return {
        "metadata": {"name": "hypershift-addon-agent", "namespace": "agent-ns", "generation": 2},
        "spec": {
            "replicas": 1,
            "template": {
                "spec": {
                    "containers": [
                        {"name": "hypershift-addon-agent", "env": [{"name": "DISABLE_AUTO_IMPORT", "value": "true"}]}
                    ]
                }
            },
        },
        "status": {"observedGeneration": 2, "replicas": 1, "updatedReplicas": 1, "availableReplicas": 1},
    }


def test_managed_cluster_appearing_after_first_observation_fails(k8s: Mock) -> None:
    """One absent observation is not proof: a late ManagedCluster fails the check."""
    k8s.is_absent.side_effect = [True, False]
    with pytest.raises(E2EFailure, match="ManagedCluster") as failure:
        assert_registration(k8s)
    assert failure.value.kind is Kind.ASSERTION
    assert k8s.is_absent.call_count == 2


def test_regular_cluster_observes_full_absence_window(k8s: Mock) -> None:
    """A regular cluster is observed over the whole scheduled absence window."""
    assert_registration(k8s)
    assert k8s.is_absent.call_count == 3
    assert runner.time.sleep.call_count == 2
    assert all(
        call.kwargs == {"resource": "managedclusters.cluster.open-cluster-management.io", "name": "order-a"}
        for call in k8s.is_absent.call_args_list
    )


def test_policy_must_disable_real_import_not_merely_omit_label(k8s: Mock) -> None:
    """The shared policy, not the missing label, is what must disable import."""
    k8s.get_json.side_effect = None
    k8s.get_json.return_value = {"spec": {"customizedVariables": []}}
    with pytest.raises(AssertionError, match="autoImportDisabled"):
        assert_registration(k8s)
    k8s.is_absent.assert_not_called()


@pytest.mark.parametrize("field", ["enabled", "old-replicas", "stale-generation", "not-ready"])
def test_effective_agent_must_be_disabled_and_rolled_out(
    k8s: Mock, monkeypatch: pytest.MonkeyPatch, field: str
) -> None:
    """A rollout that never converges fails after exhausting the retry budget."""
    deployed = agent()
    if field == "enabled":
        deployed["spec"]["template"]["spec"]["containers"][0]["env"] = []
    elif field == "old-replicas":
        deployed["status"]["replicas"] = 2
    elif field == "not-ready":
        deployed["status"]["availableReplicas"] = 0
    else:
        deployed["status"]["observedGeneration"] = 1
    monkeypatch.setattr(acm, "_AGENT_ROLLOUT_RETRIES", 3)
    monkeypatch.setattr(acm, "run", Mock(side_effect=[json.dumps({"items": [deployed]})] + [json.dumps(deployed)] * 3))
    with pytest.raises(runner.StuckState, match=r"agent-ns/hypershift-addon-agent.*DISABLE_AUTO_IMPORT"):
        acm.assert_auto_import_disabled(k8s=k8s)
    assert acm.run.call_count == 4
    assert runner.time.sleep.call_count == 2


def test_agent_rollout_rechecks_until_disabled_and_ready(k8s: Mock, monkeypatch: pytest.MonkeyPatch) -> None:
    """Wait for the policy to reach the pod template and for old replicas to leave."""
    enabled = agent()
    enabled["spec"]["template"]["spec"]["containers"][0]["env"] = []
    replacing = agent()
    replacing["status"]["replicas"] = 2
    monkeypatch.setattr(
        acm, "run", Mock(side_effect=[json.dumps({"items": [enabled]}), json.dumps(replacing), json.dumps(agent())])
    )

    acm.assert_auto_import_disabled(k8s=k8s)

    assert acm.run.call_count == 3
    acm.run.assert_called_with(
        *k8s.base_args(), "get", "deployments.apps", "hypershift-addon-agent", "-n", "agent-ns", "-o", "json", timeout=5
    )
    runner.time.sleep.assert_called_once_with(5)


def test_ready_disabled_agent_is_accepted(k8s: Mock, monkeypatch: pytest.MonkeyPatch) -> None:
    """A disabled, fully rolled-out agent passes and is read cluster-wide."""
    monkeypatch.setattr(acm, "run", Mock(return_value=json.dumps({"items": [agent()]})))
    acm.assert_auto_import_disabled(k8s=k8s)
    assert "--all-namespaces" in acm.run.call_args.args


def test_no_agent_reports_the_coverage_boundary(k8s: Mock, caplog: pytest.LogCaptureFixture) -> None:
    """No running agent logs the boundary instead of claiming proof of non-import."""
    acm.assert_auto_import_disabled(k8s=k8s)
    assert "no active import controller" in caplog.text


def test_forbidden_is_not_managed_cluster_absence(k8s: Mock) -> None:
    """An RBAC failure surfaces instead of reading as ManagedCluster absence."""
    k8s.is_absent.side_effect = subprocess.CalledProcessError(1, ["kubectl"], output="Forbidden")
    with pytest.raises(subprocess.CalledProcessError):
        assert_registration(k8s)


@pytest.mark.parametrize("operation", ["policy", "deployments", "agent-rollout"])
def test_policy_read_permission_failure_propagates(k8s: Mock, monkeypatch: pytest.MonkeyPatch, operation: str) -> None:
    """Policy and deployment read failures both propagate and stop the check."""
    error = subprocess.CalledProcessError(1, ["kubectl"], output="Forbidden")
    if operation == "policy":
        k8s.get_json.side_effect = error
    elif operation == "deployments":
        monkeypatch.setattr(acm, "run", Mock(side_effect=error))
    else:
        pending = agent()
        pending["status"]["observedGeneration"] = 1
        monkeypatch.setattr(acm, "run", Mock(side_effect=[json.dumps({"items": [pending]}), error]))
    with pytest.raises(subprocess.CalledProcessError):
        assert_registration(k8s)
    runner.time.sleep.assert_not_called()
    k8s.is_absent.assert_not_called()


@pytest.mark.parametrize(
    ("output", "expected"),
    [
        ('Error from server (NotFound): managedclusters.cluster.open-cluster-management.io "order-a" not found', True),
        ('Error from server (NotFound): managedclusters.cluster.open-cluster-management.io "other" not found', False),
        ('Error from server (NotFound): clusterorders.osac.openshift.io "order-a" not found', False),
        ("Error from server (Forbidden): managedclusters is forbidden", False),
        ('error: the server does not have a resource type "managedclusters"', False),
        ("Unable to connect to the server: context deadline exceeded", False),
    ],
)
def test_managed_cluster_absence_requires_exact_object_not_found(
    monkeypatch: pytest.MonkeyPatch, output: str, expected: bool
) -> None:
    """Only an exact object NotFound counts as absence; other errors do not."""
    monkeypatch.setattr(k8s_module, "run_unchecked", Mock(return_value=(output, 1)))
    client = K8sClient(namespace="osac")
    if expected:
        assert client.is_absent(resource="managedclusters.cluster.open-cluster-management.io", name="order-a")
    else:
        with pytest.raises(subprocess.CalledProcessError):
            client.is_absent(resource="managedclusters.cluster.open-cluster-management.io", name="order-a")


def test_unexpected_hosted_cluster_label_fails(k8s: Mock) -> None:
    """A clusterset label on a regular CaaS template fails the assertion."""
    original = k8s.get_json.side_effect

    def get_json(**kwargs: str) -> dict[str, Any]:
        """Return an import-inviting HostedCluster and defer other reads."""
        if kwargs["resource"] == "hostedclusters.hypershift.openshift.io":
            return {"metadata": {"labels": {acm.CLUSTERSET_LABEL: "default"}}}
        return original(**kwargs)

    k8s.get_json.side_effect = get_json
    with pytest.raises(AssertionError, match="clusterset"):
        assert_registration(k8s)


def test_ai_maas_waits_for_explicit_registration_join_and_availability(k8s: Mock) -> None:
    """AI MaaS keeps polling through Joined until the cluster is Available."""
    original = k8s.get_json.side_effect
    conditions: list[dict[str, str]] = []
    reads = 0

    def get_json(**kwargs: str) -> dict[str, Any]:
        """Report Joined on the first ManagedCluster read and Available on the second."""
        nonlocal reads
        if kwargs["resource"] == "hostedclusters.hypershift.openshift.io":
            return {"metadata": {"labels": {acm.CLUSTERSET_LABEL: "default"}}}
        if kwargs["resource"] == "managedclusters.cluster.open-cluster-management.io":
            reads += 1
            conditions.append(
                {"type": "ManagedClusterJoined" if reads == 1 else "ManagedClusterConditionAvailable", "status": "True"}
            )
            return {
                "metadata": {"annotations": {"osac.openshift.io/owner-reference": "ClusterOrder/order-a"}},
                "status": {"conditions": list(conditions)},
            }
        return original(**kwargs)

    k8s.get_json.side_effect = get_json
    k8s.is_absent.return_value = False
    assert_registration(k8s, template="ocp-4-20-ai-maas")
    assert reads == 2


def test_opt_in_registration_timeout_is_classified(k8s: Mock, monkeypatch: pytest.MonkeyPatch) -> None:
    """An unreachable registration is classified as a stuck state, not an assertion."""
    original = k8s.get_json.side_effect

    def get_json(**kwargs: str) -> dict[str, Any]:
        """Keep the import label and leave the ManagedCluster absent."""
        if kwargs["resource"] == "hostedclusters.hypershift.openshift.io":
            return {"metadata": {"labels": {acm.CLUSTERSET_LABEL: "default"}}}
        return original(**kwargs)

    k8s.get_json.side_effect = get_json
    monkeypatch.setattr(acm, "_REGISTRATION_RETRIES", 2)
    with pytest.raises(E2EFailure) as failure:
        assert_registration(k8s, template="ocp-4-20-ai-maas")
    assert failure.value.kind is Kind.STUCK_STATE
    assert failure.value.ids["cluster_order"] == "order-a"
