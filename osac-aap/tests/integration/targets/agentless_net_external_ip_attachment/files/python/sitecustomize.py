"""Provider boundary fixture for the AgentlessNet attachment AAP target."""

from __future__ import annotations

import os


if os.environ.get("OSAC_AGENTLESS_EIA_TEST_MODE") == "true":
    from ansible.plugins.loader import init_plugin_loader  # noqa: E402

    init_plugin_loader()

    from ansible_collections.osac.templates.plugins.module_utils import (  # noqa: E402
        agentless_net_external_ip,
        agentless_net_network,
        agentless_net_state,
    )

    agentless_net_state.reconcile_virtual_network = lambda *_args, **_kwargs: False
    agentless_net_external_ip.reconcile_external_ip_attachment = (
        lambda *_args, **_kwargs: False
    )

    def delete_attachment(_attachment):
        if os.environ.get("OSAC_AGENTLESS_EIA_FAIL_DETACH") == "true":
            raise agentless_net_network.NetworkCommandError(
                "fixture route withdrawal failure"
            )
        return True

    agentless_net_external_ip.delete_external_ip_attachment = delete_attachment
