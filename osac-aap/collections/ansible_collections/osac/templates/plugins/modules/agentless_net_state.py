#!/usr/bin/python
"""Provision and remove AgentlessNet VirtualNetwork and Subnet state."""

from __future__ import annotations

from ansible.module_utils.basic import AnsibleModule

from ansible_collections.osac.templates.plugins.module_utils.agentless_net_state import (
    StateError,
    StateStore,
)
from ansible_collections.osac.templates.plugins.module_utils.agentless_net_subnet import (
    SubnetProviderError,
    parse_switch_vlan_exclusions,
    preflight_dhcp_supervisor,
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
    choices: [ensure_virtual_network, delete_virtual_network, get_subnet, preflight_dhcp_supervisor, reserve_subnet, ensure_subnet, prepare_delete_subnet, release_subnet]
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
  virtual_network_uid:
    description: Stable Kubernetes UID of the parent VirtualNetwork.
    type: str
  subnet_cidr:
    description: Canonical IPv4 CIDR requested for this Subnet.
    type: str
  trunk_interface:
    description: Managed-node trunk interface used by the saved Subnet VLAN.
    type: str
  observed_switch_vlan_state:
    description: Raw bridge VLAN membership and NVUE reserved-range output from every configured switch.
    type: list
    elements: dict
  vip_cidr:
    description: Optional upper-end VIP block excluded from the DHCP range.
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
found:
  description: Whether a saved Subnet reservation was found.
  returned: for Subnet actions
  type: bool
subnet:
  description: Validated Subnet provider state when found.
  returned: for Subnet actions when found
  type: dict
"""


def main() -> None:
    module = AnsibleModule(
        argument_spec={
            "action": {
                "type": "str",
                "required": True,
            "choices": [
                "ensure_virtual_network",
                "delete_virtual_network",
                "get_subnet",
                "preflight_dhcp_supervisor",
                "reserve_subnet",
                "ensure_subnet",
                "prepare_delete_subnet",
                "release_subnet",
            ],
            },
            "state_file": {"type": "path", "required": True},
            "uid": {"type": "str", "required": True},
            "tenant_id": {"type": "str", "required": True},
            "virtual_network_cidr": {"type": "str"},
            "virtual_network_uid": {"type": "str"},
            "subnet_cidr": {"type": "str"},
            "trunk_interface": {"type": "str"},
            "observed_switch_vlan_state": {
                "type": "list",
                "elements": "dict",
                "default": [],
            },
            "vip_cidr": {"type": "str", "default": ""},
        },
        required_if=[
            (
                "action",
                "ensure_virtual_network",
                ["virtual_network_cidr"],
            ),
            (
                "action",
                "reserve_subnet",
                [
                    "virtual_network_uid",
                    "subnet_cidr",
                    "trunk_interface",
                    "observed_switch_vlan_state",
                ],
            ),
        ],
        supports_check_mode=True,
    )
    params = module.params
    store = StateStore(params["state_file"])

    try:
        if params["action"] == "ensure_virtual_network":
            if module.check_mode:
                existing = store.get_virtual_network(
                    params["uid"], params["tenant_id"], check_mode=True
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

        if params["action"] == "get_subnet":
            subnet = store.get_subnet(
                params["uid"], params["tenant_id"], check_mode=module.check_mode
            )
            module.exit_json(changed=False, found=subnet is not None, subnet=subnet)

        if params["action"] == "preflight_dhcp_supervisor":
            module.exit_json(
                changed=False,
                dhcp_supervisor=preflight_dhcp_supervisor(params["uid"]),
            )

        if params["action"] == "reserve_subnet":
            existing = store.get_subnet(
                params["uid"], params["tenant_id"], check_mode=module.check_mode
            )
            switch_state = params["observed_switch_vlan_state"]
            if existing is None and not switch_state:
                raise StateError("Cumulus VLAN discovery is required before Subnet allocation")
            observed_vlan_ids: set[int] = set()
            try:
                for switch in switch_state:
                    if not isinstance(switch, dict):
                        raise SubnetProviderError("Cumulus VLAN discovery result is malformed")
                    observed_vlan_ids.update(
                        parse_switch_vlan_exclusions(
                            switch.get("bridge_vlan_output"),
                            switch.get("reserved_vlan_output"),
                        )
                    )
            except SubnetProviderError as error:
                raise StateError(str(error)) from error
            subnet, changed = store.reserve_subnet(
                params["uid"],
                params["virtual_network_uid"],
                params["tenant_id"],
                params["subnet_cidr"],
                params["trunk_interface"],
                observed_vlan_ids=observed_vlan_ids,
                vip_cidr=params["vip_cidr"],
                check_mode=module.check_mode,
            )
            module.exit_json(changed=changed, found=True, subnet=subnet)

        if params["action"] == "ensure_subnet":
            subnet, state_changed, provider_changed = store.ensure_and_reconcile_subnet(
                params["uid"],
                params["tenant_id"],
                check_mode=module.check_mode,
            )
            module.exit_json(
                changed=state_changed or provider_changed,
                found=True,
                subnet=subnet,
            )

        if params["action"] == "prepare_delete_subnet":
            subnet, changed = store.prepare_delete_subnet(
                params["uid"],
                params["tenant_id"],
                check_mode=module.check_mode,
            )
            module.exit_json(
                changed=changed,
                found=subnet is not None,
                subnet=subnet,
            )

        if params["action"] == "release_subnet":
            subnet, changed = store.release_subnet(
                params["uid"],
                params["tenant_id"],
                check_mode=module.check_mode,
            )
            module.exit_json(
                changed=changed,
                found=subnet is not None,
                subnet=subnet,
            )

        if module.check_mode:
            existing = store.get_virtual_network(
                params["uid"], params["tenant_id"], check_mode=True
            )
            module.exit_json(changed=existing is not None)
        module.exit_json(
            changed=store.delete_and_remove_virtual_network(
                params["uid"],
                params["tenant_id"],
            )
        )
    except (StateError, SubnetProviderError) as error:
        if params["action"] in {
            "get_subnet",
            "preflight_dhcp_supervisor",
            "reserve_subnet",
            "ensure_subnet",
            "prepare_delete_subnet",
            "release_subnet",
        }:
            module.fail_json(msg=str(error))
        module.fail_json(
            msg="AgentlessNet VirtualNetwork operation failed; inspect the network node and retry."
        )


if __name__ == "__main__":
    main()
