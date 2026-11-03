"""Replace managed-node Linux work with local state fixtures in the target."""

from __future__ import annotations

import os
from pathlib import Path


if os.environ.get("OSAC_AGENTLESS_NET_CONTRACT_TEST_MODE") == "true":
    from ansible.plugins.loader import init_plugin_loader

    init_plugin_loader()

    from ansible_collections.osac.templates.plugins.module_utils import (  # noqa: E402
        agentless_net_network,
        agentless_net_state,
    )

    agentless_net_state.reconcile_virtual_network = lambda *_args, **_kwargs: False
    agentless_net_state.reconcile_virtual_network_dhcp = lambda *_args, **_kwargs: False
    agentless_net_state.preflight_dhcp_supervisor = lambda _uid: "systemd"
    agentless_net_state.ensure_subnet_data_plane = lambda *_args, **_kwargs: False
    agentless_net_state.prepare_subnet_delete_data_plane = lambda *_args, **_kwargs: False
    agentless_net_state.cleanup_virtual_network_dhcp = lambda *_args, **_kwargs: False
    agentless_net_state.delete_virtual_network = lambda *_args, **_kwargs: False
    agentless_net_state.agentless_net_external_ip.reconcile_external_ip_attachment = (
        lambda *_args, **_kwargs: False
    )
    agentless_net_state.agentless_net_external_ip.delete_external_ip_attachment = (
        lambda *_args, **_kwargs: False
    )
    agentless_net_state.dhcp_lease_path = lambda _uid: Path(
        os.environ["OSAC_AGENTLESS_DHCP_LEASE_FILE"]
    )
    agentless_net_network.reconcile_virtual_network = lambda *_args, **_kwargs: False
