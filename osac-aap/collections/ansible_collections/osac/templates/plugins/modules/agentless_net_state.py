#!/usr/bin/python
"""Provision and remove AgentlessNet VirtualNetwork state."""

from __future__ import annotations

from ansible.module_utils.basic import AnsibleModule

from ansible_collections.osac.templates.plugins.module_utils.agentless_net_state import (
    StateError,
    StateStore,
)


DOCUMENTATION = r"""
---
module: agentless_net_state
short_description: Provision or remove an AgentlessNet VirtualNetwork
description:
  - Reserves a tenant-owned UID-keyed VirtualNetwork mapping before applying network state.
  - Uses SQLite indexes and a per-UID operation lock so
    provider commands do not block unrelated state updates.
  - Retains an allocation until provider cleanup has completed.
options:
  action:
    description: State operation to perform.
    type: str
    required: true
    choices: [ensure_virtual_network, delete_virtual_network]
  state_file:
    description: Path to the AgentlessNet API state file on the managed node.
    type: path
    required: true
  uid:
    description: Stable VirtualNetwork UID.
    type: str
    required: true
  tenant_id:
    description: Tenant name from the VirtualNetwork tenant annotation.
    type: str
    required: true
  virtual_network_cidr:
    description: VirtualNetwork IPv4 supernet.
    type: str
author:
  - OSAC project
version_added: "1.0.0"
"""

EXAMPLES = r"""
- name: Provision a VirtualNetwork
  osac.templates.agentless_net_state:
    action: ensure_virtual_network
    state_file: /etc/osac/agentless_network_state.sqlite3
    uid: 01234567-89ab-cdef-0123-456789abcdef
    tenant_id: tenant-a
    virtual_network_cidr: 10.20.0.0/16
"""

RETURN = r"""
backend_network_id:
  description: Stable provider identifier, equal to the OSAC resource UID.
  returned: when action is ensure_virtual_network
  type: str
"""


def main() -> None:
    module = AnsibleModule(
        argument_spec={
            "action": {
                "type": "str",
                "required": True,
                "choices": ["ensure_virtual_network", "delete_virtual_network"],
            },
            "state_file": {"type": "path", "required": True},
            "uid": {"type": "str", "required": True},
            "tenant_id": {"type": "str", "required": True},
            "virtual_network_cidr": {"type": "str"},
        },
        required_if=[
            (
                "action",
                "ensure_virtual_network",
                ["virtual_network_cidr"],
            )
        ],
        supports_check_mode=True,
    )
    params = module.params
    store = StateStore(params["state_file"])

    try:
        if params["action"] == "ensure_virtual_network":
            if module.check_mode:
                existing = store.get_virtual_network(
                    params["uid"], params["tenant_id"]
                )
                module.exit_json(
                    changed=existing is None,
                    backend_network_id=params["uid"],
                )
            _, state_changed, network_changed = store.ensure_and_reconcile_virtual_network(
                params["uid"],
                params["virtual_network_cidr"],
                params["tenant_id"],
            )
            module.exit_json(
                changed=state_changed or network_changed,
                backend_network_id=params["uid"],
            )

        if module.check_mode:
            module.exit_json(
                changed=(
                    store.get_virtual_network(params["uid"], params["tenant_id"])
                    is not None
                )
            )
        module.exit_json(
            changed=store.delete_and_remove_virtual_network(
                params["uid"], params["tenant_id"]
            )
        )
    except StateError:
        module.fail_json(
            msg="AgentlessNet VirtualNetwork operation failed; inspect the network node and retry."
        )


if __name__ == "__main__":
    main()
