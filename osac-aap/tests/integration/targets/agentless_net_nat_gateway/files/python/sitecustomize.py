"""Provider boundary fixture for the AgentlessNet NATGateway AAP target."""

from __future__ import annotations

import os


if os.environ.get("OSAC_AGENTLESS_NATGW_TEST_MODE") == "true":
    from ansible.plugins.loader import init_plugin_loader  # noqa: E402

    init_plugin_loader()

    from ansible_collections.osac.templates.plugins.module_utils import (  # noqa: E402
        agentless_net_nat_gateway,
        agentless_net_network,
        agentless_net_state,
    )

    agentless_net_state.reconcile_virtual_network = lambda *_args, **_kwargs: False
    agentless_net_nat_gateway.reconcile_nat_gateway = lambda *_args, **_kwargs: False

    def delete_nat_gateway(_parent, _gateway):
        if os.environ.get("OSAC_AGENTLESS_NATGW_FAIL_DELETE") == "true":
            raise agentless_net_network.NetworkCommandError(
                "fixture route withdrawal failure"
            )
        return True

    agentless_net_nat_gateway.delete_nat_gateway = delete_nat_gateway
