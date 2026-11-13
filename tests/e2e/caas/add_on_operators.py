from __future__ import annotations

import json
import logging
import subprocess
import tempfile
from collections.abc import Iterable
from dataclasses import dataclass
from typing import Any

from tests.e2e.core.grpc_client import PRIVATE_API, GRPCClient
from tests.e2e.core.helpers import wait_for_cluster_order_condition, wait_for_hosted_cluster_kubeconfig
from tests.e2e.core.k8s_client import K8sClient
from tests.e2e.core.osac_cli import OsacCLI
from tests.e2e.core.runner import poll_until
from tests.e2e.core.wait_kinds import WaitResourceKind

logger = logging.getLogger(__name__)


@dataclass(frozen=True, slots=True)
class AddOnOperatorSpec:
    name: str
    subscription_name: str
    subscription_namespace: str


OPERATOR_SPECS: dict[str, AddOnOperatorSpec] = {
    "cert-manager": AddOnOperatorSpec(
        name="cert-manager", subscription_name="cert-manager", subscription_namespace="openshift-operators"
    ),
    "serverless": AddOnOperatorSpec(
        name="serverless", subscription_name="serverless-operator", subscription_namespace="openshift-serverless"
    ),
    "node-feature-discovery": AddOnOperatorSpec(
        name="node-feature-discovery", subscription_name="nfd", subscription_namespace="openshift-nfd"
    ),
    "nvidia-gpu": AddOnOperatorSpec(
        name="nvidia-gpu", subscription_name="gpu-operator-certified", subscription_namespace="nvidia-gpu-operator"
    ),
}


def _operator_state_map(response: dict[str, Any]) -> dict[str, dict[str, Any]]:
    statuses = response.get("object", {}).get("status", {}).get("addOnOperators", [])
    return {status["name"]: status for status in statuses}


def _set_operator_published(grpc: GRPCClient, *, operator_id: str, published: bool) -> None:
    grpc.call(
        service=f"{PRIVATE_API}.AddOnOperators/Update",
        data={"object": {"id": operator_id, "published": published}, "update_mask": {"paths": ["published"]}},
    )


def _ensure_published_operators(
    private_cli: OsacCLI, grpc: GRPCClient, names: Iterable[str]
) -> tuple[tuple[AddOnOperatorSpec, ...], dict[str, bool], tuple[str, ...]]:
    specs: list[AddOnOperatorSpec] = []
    operator_ids: list[str] = []
    for name in names:
        try:
            specs.append(OPERATOR_SPECS[name])
        except KeyError as exc:
            raise ValueError(f"No E2E metadata for add-on operator {name!r}") from exc

    original_states: dict[str, bool] = {}
    try:
        for spec in specs:
            item = json.loads(private_cli.get_add_on_operator(name=spec.name))
            if item.get("metadata", {}).get("name") != spec.name or not item.get("id"):
                raise AssertionError(f"AddOnOperator {spec.name!r} was not found or has no ID")
            operator_id = item["id"]
            operator_ids.append(operator_id)
            original_states[operator_id] = item.get("published") is True
            if not original_states[operator_id]:
                _set_operator_published(grpc, operator_id=operator_id, published=True)
    except Exception:
        _restore_operator_publication(grpc, original_states)
        raise
    return tuple(specs), original_states, tuple(operator_ids)


def _restore_operator_publication(grpc: GRPCClient, original_states: dict[str, bool]) -> None:
    for operator_id, published in reversed(list(original_states.items())):
        if published:
            continue
        try:
            _set_operator_published(grpc, operator_id=operator_id, published=published)
        except Exception as exc:
            logger.error("Failed to restore AddOnOperator %s publication: %s", operator_id, type(exc).__name__)


def _assert_subscriptions_and_csvs(*, workload_k8s: K8sClient, expected: Iterable[AddOnOperatorSpec]) -> None:
    specs = tuple(expected)

    for spec in specs:
        try:
            subscription = workload_k8s.get_json(
                resource="subscriptions.operators.coreos.com",
                name=spec.subscription_name,
                namespace=spec.subscription_namespace,
            )
        except subprocess.CalledProcessError:
            raise AssertionError(
                f"Missing Subscription {spec.subscription_namespace}/{spec.subscription_name}"
            ) from None

        csv_name = subscription.get("status", {}).get("installedCSV", "")
        assert csv_name, f"Subscription {spec.subscription_namespace}/{spec.subscription_name} has no installedCSV"

        csv = workload_k8s.get_json(
            resource="clusterserviceversions.operators.coreos.com", name=csv_name, namespace=spec.subscription_namespace
        )
        phase = csv.get("status", {}).get("phase", "")
        assert phase == "Succeeded", (
            f"CSV {spec.subscription_namespace}/{csv_name} has phase {phase or 'unknown'}, expected Succeeded"
        )


def _assert_cluster_add_on_operators_installed(
    *,
    client: GRPCClient,
    hub_k8s: K8sClient,
    cluster_id: str,
    cluster_order_name: str,
    expected_operator_specs: Iterable[AddOnOperatorSpec],
    identity: str,
    hosted_cluster_reference: tuple[str, str],
) -> None:
    specs = tuple(expected_operator_specs)
    wait_for_cluster_order_condition(k8s=hub_k8s, name=cluster_order_name, condition_type="AddOnOperatorsReady")
    hosted_cluster_name, hosted_cluster_namespace = hosted_cluster_reference

    workload_kubeconfig = wait_for_hosted_cluster_kubeconfig(
        k8s=hub_k8s, hosted_cluster_namespace=hosted_cluster_namespace, hosted_cluster_name=hosted_cluster_name
    )
    with tempfile.NamedTemporaryFile(prefix="osac-workload-", suffix=".kubeconfig") as kubeconfig_file:
        kubeconfig_file.write(workload_kubeconfig)
        kubeconfig_file.flush()
        workload_k8s = K8sClient(
            namespace=hosted_cluster_namespace, kubeconfig=kubeconfig_file.name, as_system_admin=False
        )
        _assert_subscriptions_and_csvs(workload_k8s=workload_k8s, expected=specs)
    _wait_for_api_operators_installed(
        client=client, cluster_id=cluster_id, identity=identity, operator_names=(spec.name for spec in specs)
    )


def _wait_for_api_operators_installed(
    *, client: GRPCClient, cluster_id: str, identity: str, operator_names: Iterable[str]
) -> None:
    names = tuple(operator_names)

    def states() -> dict[str, dict[str, Any]]:
        return _operator_state_map(client.get_cluster(cluster_id=cluster_id))

    def all_installed(observed: dict[str, dict[str, Any]]) -> bool:
        return all(observed.get(name, {}).get("state") == "ADD_ON_OPERATOR_INSTALL_STATE_INSTALLED" for name in names)

    observed_operators = poll_until(
        fn=states,
        until=all_installed,
        retries=60,
        delay=10,
        description=f"{identity} add-on operator API states",
        retry_on_error=True,
        resource_kind=WaitResourceKind.CLUSTER,
        resource_id=cluster_id,
    )
    for name in names:
        observed = observed_operators.get(name)
        assert observed is not None, f"Cluster API did not report add-on operator {name!r}"
        assert observed.get("state") == "ADD_ON_OPERATOR_INSTALL_STATE_INSTALLED", (
            f"Unexpected state for {name!r}; expected ADD_ON_OPERATOR_INSTALL_STATE_INSTALLED"
        )
        assert observed.get("message", "") == "", f"Unexpected success message for {name!r}; expected it to be empty"
