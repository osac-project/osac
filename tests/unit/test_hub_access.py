from __future__ import annotations

import json
import subprocess
from typing import Any
from unittest.mock import Mock

import pytest

from tests.e2e.core.hub_access import (
    CONSOLE_API_GROUP,
    HUB_ACCESS_CONSOLE_RULE,
    cluster_role_grants_console_access,
    ensure_hub_access_console_rbac,
    hub_access_cluster_role_name,
    remove_hub_access_console_rbac,
)
from tests.e2e.core.k8s_client import K8sClient


def test_hub_access_cluster_role_name_uses_installer_namespace_prefix() -> None:
    """Installer names the ClusterRole {namespace}-hub-access."""
    assert hub_access_cluster_role_name("osac-prod") == "osac-prod-hub-access"


@pytest.mark.parametrize(
    "rules",
    [
        [HUB_ACCESS_CONSOLE_RULE],
        [
            {"apiGroups": [CONSOLE_API_GROUP], "resources": ["computeinstances/console"], "verbs": ["get"]},
            {"apiGroups": [CONSOLE_API_GROUP], "resources": ["computeinstances/vnc"], "verbs": ["get"]},
        ],
        [{"apiGroups": ["*"], "resources": ["*"], "verbs": ["*"]}],
        [{"apiGroups": [CONSOLE_API_GROUP], "resources": ["*"], "verbs": ["get"]}],
    ],
)
def test_cluster_role_grants_console_access_when_serial_and_vnc_get_present(rules: list[Any]) -> None:
    """Chart-equivalent and wildcard rules satisfy serial and VNC console get."""
    assert cluster_role_grants_console_access(rules)


@pytest.mark.parametrize(
    "rules",
    [
        None,
        [],
        [{"apiGroups": ["osac.openshift.io"], "resources": ["computeinstances"], "verbs": ["get"]}],
        [{"apiGroups": [CONSOLE_API_GROUP], "resources": ["computeinstances/console"], "verbs": ["get"]}],
        [
            {
                "apiGroups": [CONSOLE_API_GROUP],
                "resources": ["computeinstances/console", "computeinstances/vnc"],
                "verbs": ["list"],
            }
        ],
    ],
)
def test_cluster_role_grants_console_access_rejects_incomplete_rules(rules: list[Any] | None) -> None:
    """Missing group, VNC subresource, or get does not satisfy hub-access console RBAC."""
    assert not cluster_role_grants_console_access(rules)


def test_ensure_hub_access_console_rbac_skips_patch_when_already_granted() -> None:
    """Leave a complete ClusterRole unchanged."""
    k8s = Mock(spec=K8sClient)
    k8s.get_cluster_json.return_value = {"rules": [HUB_ACCESS_CONSOLE_RULE]}

    assert ensure_hub_access_console_rbac(k8s=k8s, namespace="osac-prod") is False
    k8s.patch_cluster.assert_not_called()


def test_ensure_hub_access_console_rbac_appends_chart_rule_when_missing() -> None:
    """JSON-patch the chart console get rule onto a ClusterRole that omitted it."""
    k8s = Mock(spec=K8sClient)
    k8s.get_cluster_json.side_effect = [
        {"rules": [{"apiGroups": ["osac.openshift.io"], "resources": ["computeinstances"], "verbs": ["get"]}]},
        {"rules": [HUB_ACCESS_CONSOLE_RULE]},
    ]

    assert ensure_hub_access_console_rbac(k8s=k8s, namespace="osac-prod") is True
    k8s.patch_cluster.assert_called_once_with(
        resource="clusterrole",
        name="osac-prod-hub-access",
        patch=json.dumps([{"op": "add", "path": "/rules/-", "value": HUB_ACCESS_CONSOLE_RULE}]),
    )


def test_ensure_hub_access_console_rbac_replaces_empty_rules() -> None:
    """Create the rules array when the ClusterRole has none."""
    k8s = Mock(spec=K8sClient)
    k8s.get_cluster_json.side_effect = [{}, {"rules": [HUB_ACCESS_CONSOLE_RULE]}]

    assert ensure_hub_access_console_rbac(k8s=k8s, namespace="osac-prod") is True
    k8s.patch_cluster.assert_called_once_with(
        resource="clusterrole",
        name="osac-prod-hub-access",
        patch=json.dumps([{"op": "add", "path": "/rules", "value": [HUB_ACCESS_CONSOLE_RULE]}]),
    )


def test_ensure_hub_access_console_rbac_raises_when_cluster_role_missing() -> None:
    """Fail setup when hub-access ClusterRole cannot be read."""
    k8s = Mock(spec=K8sClient)
    k8s.get_cluster_json.side_effect = subprocess.CalledProcessError(1, "kubectl", stderr="NotFound")

    with pytest.raises(RuntimeError, match="osac-prod-hub-access"):
        ensure_hub_access_console_rbac(k8s=k8s, namespace="osac-prod")


def test_remove_hub_access_console_rbac_skips_when_absent() -> None:
    """Do not patch when the chart console rule is not on the ClusterRole."""
    k8s = Mock(spec=K8sClient)
    k8s.get_cluster_json.return_value = {
        "rules": [{"apiGroups": ["osac.openshift.io"], "resources": ["computeinstances"], "verbs": ["get"]}]
    }

    assert remove_hub_access_console_rbac(k8s=k8s, namespace="osac-prod") is False
    k8s.patch_cluster.assert_not_called()


def test_remove_hub_access_console_rbac_deletes_chart_rule() -> None:
    """JSON-remove the exact installer console get rule."""
    k8s = Mock(spec=K8sClient)
    k8s.get_cluster_json.return_value = {
        "rules": [
            {"apiGroups": ["osac.openshift.io"], "resources": ["computeinstances"], "verbs": ["get"]},
            HUB_ACCESS_CONSOLE_RULE,
        ]
    }

    assert remove_hub_access_console_rbac(k8s=k8s, namespace="osac-prod") is True
    k8s.patch_cluster.assert_called_once_with(
        resource="clusterrole", name="osac-prod-hub-access", patch=json.dumps([{"op": "remove", "path": "/rules/1"}])
    )
