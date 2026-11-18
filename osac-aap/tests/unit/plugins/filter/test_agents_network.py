"""MAC-driven Agent network mapping tests (OSAC-6828)."""

from __future__ import annotations

import importlib.util
from pathlib import Path


_FILTER_PATH = (
    Path(__file__).resolve().parents[4]
    / "collections"
    / "ansible_collections"
    / "osac"
    / "service"
    / "plugins"
    / "filter"
    / "agents.py"
)


def _load_agents():
    spec = importlib.util.spec_from_file_location("osac_agents", _FILTER_PATH)
    assert spec and spec.loader
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


agents = _load_agents()


def test_agent_network_interfaces_matches_mac_not_position():
    agent = {
        "status": {
            "inventory": {
                "interfaces": [
                    {"name": "ens4", "macAddress": "52:54:09:00:00:03"},
                    {"name": "enp1s0", "macAddress": "52:29:B0:BC:EC:E1"},
                ]
            }
        }
    }

    interfaces = agents.agent_network_interfaces(
        agent, ["52-29-b0-bc-ec-e1"]
    )

    assert interfaces == [
        {"name": "ens4", "macAddress": "52:54:09:00:00:03", "selected": False},
        {"name": "enp1s0", "macAddress": "52:29:B0:BC:EC:E1", "selected": True},
    ]


def test_agent_nmstate_interfaces_preserves_all_mac_selectors():
    agent = {
        "status": {
            "inventory": {
                "interfaces": [
                    {"name": "mgmt0", "macAddress": "aa:bb:cc:dd:ee:01"},
                    {"name": "data0", "macAddress": "aa:bb:cc:dd:ee:02"},
                ]
            }
        }
    }

    assert agents.agent_nmstate_interfaces(agent, ["aa:bb:cc:dd:ee:02"]) == [
        {"name": "mgmt0", "macAddress": "aa:bb:cc:dd:ee:01"},
        {"name": "data0", "macAddress": "aa:bb:cc:dd:ee:02"},
    ]


def test_baremetal_instances_accepts_current_and_legacy_cluster_identity():
    instances = [
        {
            "metadata": {
                "annotations": {"osac.openshift.io/cluster-api-id": "cluster-a"}
            }
        },
        {
            "metadata": {
                "labels": {"osac.openshift.io/clusterorder-uuid": "cluster-a"}
            }
        },
        {"metadata": {"annotations": {"osac.openshift.io/cluster-api-id": "other"}}},
    ]

    assert agents.baremetal_instances_for_cluster(instances, "cluster-a") == instances[:2]
