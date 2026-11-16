from __future__ import annotations

from unittest.mock import Mock

import pytest

from tests.e2e.caas.sanity import test_cluster_create as scenario
from tests.e2e.core.k8s_client import K8sClient


def test_disabled_assertions_skip_endpoint_conditions_for_legacy_templates() -> None:
    """The CI template has no automatic endpoint capability, so no condition exists."""
    k8s = Mock(spec=K8sClient)
    k8s.get_status_jobs.return_value = []
    k8s.get_condition.return_value = {}

    scenario._assert_disabled_networking_cluster(k8s=k8s, co_name="order", automatic_endpoints=False)

    requested = [call.kwargs["condition_type"] for call in k8s.get_condition.call_args_list]
    assert "EndpointsReady" not in requested


def test_disabled_assertions_require_endpoints_ready_for_automatic_templates() -> None:
    k8s = Mock(spec=K8sClient)
    k8s.get_status_jobs.return_value = []
    k8s.get_condition.return_value = {"type": "EndpointsReady", "status": "True", "reason": "ProvisioningDisabled"}
    k8s.get_cluster_order_status.return_value = {"ingressEndpoint": ""}

    scenario._assert_disabled_networking_cluster(k8s=k8s, co_name="order", automatic_endpoints=True)

    k8s.get_condition.assert_any_call(resource="clusterorder", name="order", condition_type="EndpointsReady")


def test_disabled_assertions_reject_a_missing_endpoints_condition() -> None:
    k8s = Mock(spec=K8sClient)
    k8s.get_status_jobs.return_value = []
    k8s.get_condition.return_value = {}

    with pytest.raises(AssertionError, match="EndpointsReady must be True"):
        scenario._assert_disabled_networking_cluster(k8s=k8s, co_name="order", automatic_endpoints=True)
