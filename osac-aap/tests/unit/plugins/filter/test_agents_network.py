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


def test_bmh_nmstate_interfaces_maps_inspected_nics_before_agent_exists():
    bmh = {
        "status": {
            "hardware": {
                "nics": [
                    {"name": "ens4", "mac": "52:54:09:00:00:03"},
                    {"name": "ens6", "mac": "52:29:B0:BC:EC:E1"},
                    {"name": "ens6", "mac": "52:29:B0:BC:EC:E1"},
                ]
            }
        }
    }

    assert agents.bmh_nmstate_interfaces(bmh, ["52-29-b0-bc-ec-e1"]) == [
        {"name": "ens4", "macAddress": "52:54:09:00:00:03", "selected": False},
        {"name": "ens6", "macAddress": "52:29:B0:BC:EC:E1", "selected": True},
    ]


def test_agents_matching_selected_macs_ignores_stale_disconnected_agents():
    current = {
        "metadata": {"name": "current"},
        "status": {"inventory": {"interfaces": [
            {"name": "ens6", "macAddress": "52:83:8d:0c:40:a9"},
        ]}},
    }
    stale = {
        "metadata": {"name": "stale"},
        "status": {"inventory": {"interfaces": [
            {"name": "ens6", "macAddress": "52:29:b0:bc:ec:e1"},
        ]}},
    }

    assert agents.agents_matching_selected_macs(
        [stale, current], ["52:83:8d:0c:40:a9"]
    ) == [current]


def test_agents_matching_selected_macs_requires_distinct_selected_macs():
    first = {
        "metadata": {"name": "first"},
        "status": {"inventory": {"interfaces": [
            {"name": "ens6", "macAddress": "aa:bb:cc:dd:ee:01"},
        ]}},
    }
    duplicate = {
        "metadata": {"name": "duplicate"},
        "status": {"inventory": {"interfaces": [
            {"name": "ens7", "macAddress": "aa:bb:cc:dd:ee:01"},
        ]}},
    }

    assert agents.agents_matching_selected_macs(
        [first, duplicate], ["aa:bb:cc:dd:ee:01", "aa:bb:cc:dd:ee:02"]
    ) == [first]


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
        {
            "metadata": {
                "labels": {"osac.openshift.io/cluster-order": "cluster-a"}
            }
        },
        {"metadata": {"annotations": {"osac.openshift.io/cluster-api-id": "other"}}},
    ]

    assert agents.baremetal_instances_for_cluster(instances, "cluster-a") == instances[:3]
