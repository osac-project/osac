#!/usr/bin/env python
# -*- coding: utf-8 -*-
# Copyright: (c) 2026, VAST Data
# Apache License 2.0 (see LICENSE or https://www.apache.org/licenses/LICENSE-2.0)
# SPDX-License-Identifier: Apache-2.0
"""Runtime tests for generated VMS network-settings modules."""

from unittest.mock import MagicMock

from ansible_collections.vastdata.vms.plugins.module_utils.vast.schema_overrides import get_overrides
from ansible_collections.vastdata.vms.plugins.modules.vms_network_settings import VmsNetworkSettings
from ansible_collections.vastdata.vms.plugins.modules.vms_network_settings_info import VmsNetworkSettingsInfo
from ansible_collections.vastdata.vms.plugins.modules.vms_network_settings_summary import (
    VmsNetworkSettingsSummary,
)

CURRENT = {
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
    "boxes": [{"hosts": [{"id": 2, "hostname": "cn2", "mgmt_ip": "10.0.0.3"}]}],
}


def _resource(module_class, overrides_key, params):
    resource = module_class.__new__(module_class)
    resource.module = MagicMock()
    resource.module.exit_json.side_effect = SystemExit
    resource.module.fail_json.side_effect = SystemExit
    resource.params = params
    resource.check_mode = False
    if hasattr(resource, "_EXCLUDE_KEYS"):
        resource._exclude = set(resource._EXCLUDE_KEYS) | {"vms_id"}
    resource.overrides = get_overrides(overrides_key, (5, 5))
    resource.client = MagicMock()
    return resource


def _path(resource, sub_path):
    parent = resource.client.api.__getitem__.return_value
    parent_item = parent.__getitem__.return_value
    return parent_item.__getitem__(sub_path)


def test_manage_identical_value_is_idempotent_with_enveloped_get():
    resource = _resource(
        VmsNetworkSettings,
        "vms_network_settings",
        {"vms_id": 1, "eth_mtu": 9000, "state": "present", "wait": True, "wait_timeout": 300},
    )
    endpoint = _path(resource, "network_settings")
    endpoint.get.return_value = [{"data": CURRENT}]

    try:
        resource.run()
    except SystemExit:
        pass

    assert resource.module.exit_json.call_args.kwargs["changed"] is False
    endpoint.patch.assert_not_called()


def test_manage_partial_patch_echoes_live_required_fields():
    resource = _resource(
        VmsNetworkSettings,
        "vms_network_settings",
        {
            "vms_id": 1,
            "ntp": ["2.2.2.2"],
            "state": "present",
            "wait": False,
            "wait_timeout": 300,
        },
    )
    endpoint = _path(resource, "network_settings")
    endpoint.get.return_value = [{"data": CURRENT}]
    endpoint.patch.return_value = {}

    try:
        resource.run()
    except SystemExit:
        pass

    body = endpoint.patch.call_args.kwargs
    assert body["management_vips"] == CURRENT["management_vips"]
    assert body["external_gateways"] == CURRENT["external_gateways"]
    assert body["hosts"] == [{"id": 2, "hostname": "cn2", "mgmt_ip": "10.0.0.3"}]
    assert body["ntp"] == ["2.2.2.2"]


def test_info_returns_unwrapped_top_level_settings():
    resource = _resource(VmsNetworkSettingsInfo, "vms_network_settings", {"vms_id": 1})
    endpoint = _path(resource, "network_settings")
    endpoint.get.return_value = [{"data": CURRENT}]

    try:
        resource.run()
    except SystemExit:
        pass

    result = resource.module.exit_json.call_args.kwargs
    assert result["changed"] is False
    assert result["vms_network_settings"][0]["eth_mtu"] == 9000
    assert "data" not in result["vms_network_settings"][0]


def test_summary_builds_full_preview_and_reports_unchanged():
    resource = _resource(
        VmsNetworkSettingsSummary,
        "vms_network_settings_summary",
        {"vms_id": 1, "ntp": ["2.2.2.2"]},
    )
    _path(resource, "network_settings").first.return_value = {"data": CURRENT}
    summary = _path(resource, "network_settings_summary")
    summary.post.return_value = {"data": {"hosts": []}}

    try:
        resource.run()
    except SystemExit:
        pass

    assert summary.post.call_args.kwargs["management_vips"] == CURRENT["management_vips"]
    assert summary.post.call_args.kwargs["ntp"] == ["2.2.2.2"]
    result = resource.module.exit_json.call_args.kwargs
    assert result == {"changed": False, "result": {"hosts": []}}
