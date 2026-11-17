#!/usr/bin/env python
# -*- coding: utf-8 -*-
# Copyright: (c) 2026, VAST Data
# Apache License 2.0 (see LICENSE or https://www.apache.org/licenses/LICENSE-2.0)
# SPDX-License-Identifier: Apache-2.0
"""Unit tests for VMS network-settings response and request shaping."""

import sys
from pathlib import Path

collection_root = Path(__file__).parent.parent.parent.parent.parent
sys.path.insert(0, str(collection_root))

from plugins.module_utils.vast.network_settings import (  # noqa: E402
    build_write_body,
    hosts_from_boxes,
    network_settings_response_normalizer,
    unwrap_data_envelope,
)
from plugins.module_utils.vast.schema_overrides import get_overrides  # noqa: E402

CURRENT_SETTINGS = {
    "management_vips": ["10.0.0.10"],
    "external_gateways": ["10.0.0.1"],
    "ntp": ["1.1.1.1"],
    "dns": ["10.0.0.53"],
    "ext_netmask": "255.255.255.0",
    "auto_ports_ext_iface": "outband",
    "b2b_ipmi": False,
    "ipmi_gateway": "10.0.0.1",
    "ipmi_netmask": "255.255.255.0",
    "eth_mtu": 9000,
    "ib_mtu": 2044,
    "boxes": [
        {
            "hosts": [
                {
                    "id": 2,
                    "hostname": "cn2",
                    "mgmt_ip": "10.0.0.3",
                    "ipmi_ip": "10.0.0.4",
                    "nb_eth_mtu": 9000,
                    "nb_ib_mtu": 2044,
                    "vast_install_info": {"version": "5.5.0"},
                }
            ]
        }
    ],
}


def test_unwrap_data_envelope_preserves_plain_resources():
    assert unwrap_data_envelope(CURRENT_SETTINGS) is CURRENT_SETTINGS


def test_response_normalizer_unwraps_and_derives_hosts_without_losing_boxes():
    normalized = network_settings_response_normalizer([{"data": CURRENT_SETTINGS}])

    assert normalized[0]["eth_mtu"] == 9000
    assert normalized[0]["boxes"] == CURRENT_SETTINGS["boxes"]
    assert normalized[0]["hosts"] == [
        {
            "id": 2,
            "hostname": "cn2",
            "mgmt_ip": "10.0.0.3",
            "ipmi_ip": "10.0.0.4",
            "nb_eth_mtu": 9000,
            "nb_ib_mtu": 2044,
        }
    ]


def test_response_normalizer_keeps_explicit_hosts_from_summary():
    response = {"data": {"hosts": [{"id": 9, "hostname": "cn9"}]}}
    assert network_settings_response_normalizer([response]) == [{"hosts": [{"id": 9, "hostname": "cn9"}]}]


def test_hosts_from_boxes_keeps_all_writable_fields_and_omits_read_only_fields():
    assert hosts_from_boxes(CURRENT_SETTINGS) == [
        {
            "id": 2,
            "hostname": "cn2",
            "mgmt_ip": "10.0.0.3",
            "ipmi_ip": "10.0.0.4",
            "nb_eth_mtu": 9000,
            "nb_ib_mtu": 2044,
        }
    ]


def test_build_write_body_echoes_required_fields_for_partial_patch():
    body = build_write_body({"data": CURRENT_SETTINGS}, {"ntp": ["2.2.2.2"]})

    assert body == {
        "management_vips": ["10.0.0.10"],
        "external_gateways": ["10.0.0.1"],
        "ntp": ["2.2.2.2"],
        "dns": ["10.0.0.53"],
        "ext_netmask": "255.255.255.0",
        "auto_ports_ext_iface": "outband",
        "b2b_ipmi": False,
        "ipmi_gateway": "10.0.0.1",
        "ipmi_netmask": "255.255.255.0",
        "eth_mtu": 9000,
        "ib_mtu": 2044,
        "hosts": [
            {
                "id": 2,
                "hostname": "cn2",
                "mgmt_ip": "10.0.0.3",
                "ipmi_ip": "10.0.0.4",
                "nb_eth_mtu": 9000,
                "nb_ib_mtu": 2044,
            }
        ],
    }


def test_build_write_body_prefers_user_hosts():
    desired_hosts = [{"id": 7, "hostname": "replacement"}]
    assert build_write_body(CURRENT_SETTINGS, {"hosts": desired_hosts})["hosts"] == desired_hosts


def test_manage_and_info_endpoint_override_wires_read_and_write_shaping():
    overrides = get_overrides("vms_network_settings", (5, 5))
    assert overrides["response_normalizer"] is network_settings_response_normalizer
    assert overrides["write_body_builder"] is build_write_body


def test_summary_override_reads_network_settings_and_reports_preview_unchanged():
    overrides = get_overrides("vms_network_settings_summary", (5, 5))
    assert overrides["action_response_normalizer"] is network_settings_response_normalizer
    assert "response_normalizer" not in overrides
    assert overrides["write_body_builder"] is build_write_body
    assert overrides["write_source_sub_path"] == "network_settings"
    assert overrides["action_reports_changed"] is False
