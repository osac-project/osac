#!/usr/bin/python
"""Provision or remove AgentlessNet provider state."""

from __future__ import annotations

from ansible.module_utils.basic import AnsibleModule

from ansible_collections.osac.templates.plugins.module_utils.agentless_net_state import (
    PublicStateError,
    RetryableStateError,
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
short_description: Provision or remove AgentlessNet network state
description:
  - Reserves API-ID-owned VirtualNetwork and Subnet provider state.
  - Registers shared ExternalIPPools and tenant-owned ExternalIPs by API ID.
  - Uses SQLite indexes and per-resource operation locks for state updates.
  - Retains an allocation until provider cleanup has completed.
options:
  action:
    description: State operation to perform.
    type: str
    required: true
    choices:
      - ensure_virtual_network
      - delete_virtual_network
      - get_subnet
      - query_dhcp_lease
      - preflight_dhcp_supervisor
      - reserve_subnet
      - ensure_subnet
      - prepare_delete_subnet
      - release_subnet
      - ensure_external_ip_pool
      - delete_external_ip_pool
      - reserve_external_ip
      - release_external_ip
      - ensure_external_ip_attachment
      - delete_external_ip_attachment
      - ensure_nat_gateway
      - delete_nat_gateway
  state_file:
    description: Path to the AgentlessNet API state file on the managed node.
    type: path
    required: true
  uid:
    description: Stable OSAC resource API ID.
    type: str
    required: true
  tenant_id:
    description: Tenant name from the resource tenant annotation.
    type: str
    required: true
  fulfillment_id:
    description: API ID retained in existing indexed state columns.
    type: str
  ip_family:
    description: ExternalIPPool address family; only IPv4 is supported.
    type: str
  cidrs:
    description: ExternalIPPool CIDR list; exactly one canonical IPv4 CIDR is supported.
    type: list
    elements: str
  pool_uid:
    description: API ID of the registered ExternalIPPool.
    type: str
  pool_fulfillment_id:
    description: Fulfillment UUID of the registered ExternalIPPool.
    type: str
  pool_tenant_id:
    description: Tenant owner of the registered ExternalIPPool.
    type: str
  pool_cidr:
    description: Canonical IPv4 CIDR of the registered ExternalIPPool.
    type: str
  virtual_network_cidr:
    description: VirtualNetwork IPv4 supernet.
    type: str
  virtual_network_uid:
    description: API ID of the parent VirtualNetwork.
    type: str
  subnet_uid:
    description: API ID of the attached Subnet.
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
  ipam:
    description: OSAC-recorded Subnet address plan.
    type: dict
  host_name:
    description: Bare-metal inventory host name from the attachment target.
    type: str
  interface_name:
    description: Bare-metal host interface name from the attachment.
    type: str
  ports:
    description: Saved Cumulus switch ports owned by the NetworkAttachment.
    type: list
    elements: dict
  dhcp_supervisor:
    description: Preflighted DHCP service supervisor for the VirtualNetwork.
    type: str
  attachment_context:
    description: Controller-resolved ExternalIPAttachment dependency snapshot.
    type: dict
  nat_gateway_context:
    description: Controller-resolved NATGateway dependency snapshot.
    type: dict
  external_interface:
    description: Provider uplink used for ExternalIP and NATGateway rules.
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
- name: Register an ExternalIPPool
  osac.templates.agentless_net_state:
    action: ensure_external_ip_pool
    state_file: /etc/osac/agentless_network_state.sqlite3
    uid: 01234567-89ab-cdef-0123-456789abcdef
    fulfillment_id: 11234567-89ab-cdef-0123-456789abcdef
    tenant_id: tenant-a
    ip_family: IPv4
    cidrs:
      - 198.51.100.0/24
"""

RETURN = r"""
backend_network_id:
  description: Stable provider identifier, equal to the OSAC resource API ID.
  returned: Internal module output; networking tasks set only contract results.
  type: str
found:
  description: Whether a saved Subnet reservation was found.
  returned: for Subnet actions
  type: bool
subnet:
  description: Validated Subnet provider state when found.
  returned: for Subnet actions when found
  type: dict
lease:
  description: Active DHCP lease matching the requested Subnet and NIC MAC.
  returned: when a matching active lease is found
  type: dict
pool:
  description: Validated ExternalIPPool provider identity.
  returned: for ExternalIPPool actions when the pool exists or would be registered
  type: dict
external_ip:
  description: API-ID-owned ExternalIP reservation.
  returned: for ExternalIP reservation actions when one exists or would be allocated
  type: dict
"""

EXTERNAL_IP_POOL_ACTIONS = {
    "ensure_external_ip_pool",
    "delete_external_ip_pool",
}
EXTERNAL_IP_ACTIONS = {"reserve_external_ip", "release_external_ip"}
EXTERNAL_IP_ATTACHMENT_ACTIONS = {
    "ensure_external_ip_attachment",
    "delete_external_ip_attachment",
}


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
                    "query_dhcp_lease",
                    "preflight_dhcp_supervisor",
                    "reserve_subnet",
                    "ensure_subnet",
                    "prepare_delete_subnet",
                    "release_subnet",
                    "ensure_external_ip_pool",
                    "delete_external_ip_pool",
                    "reserve_external_ip",
                    "release_external_ip",
                    "ensure_external_ip_attachment",
                    "delete_external_ip_attachment",
                    "ensure_nat_gateway",
                    "delete_nat_gateway",
                    "prepare_network_attachment",
                    "mark_network_attachment_ready",
                    "begin_delete_network_attachment",
                    "finish_delete_network_attachment",
                    "get_network_attachment",
                ],
            },
            "state_file": {"type": "path", "required": True},
            "uid": {"type": "str", "required": True},
            "tenant_id": {"type": "str", "required": True},
            "fulfillment_id": {"type": "str"},
            "ip_family": {"type": "str"},
            "cidrs": {"type": "list", "elements": "str"},
            "pool_uid": {"type": "str"},
            "pool_fulfillment_id": {"type": "str"},
            "pool_tenant_id": {"type": "str"},
            "pool_cidr": {"type": "str"},
            "virtual_network_cidr": {"type": "str"},
            "virtual_network_uid": {"type": "str"},
            "subnet_uid": {"type": "str"},
            "subnet_cidr": {"type": "str"},
            "trunk_interface": {"type": "str"},
            "observed_switch_vlan_state": {
                "type": "list",
                "elements": "dict",
                "default": [],
            },
            "ipam": {"type": "dict"},
            "mac_address": {"type": "str"},
            "host_name": {"type": "str"},
            "interface_name": {"type": "str"},
            "ports": {"type": "list", "elements": "dict"},
            "dhcp_supervisor": {"type": "str"},
            "attachment_context": {"type": "dict"},
            "nat_gateway_context": {"type": "dict"},
            "external_interface": {"type": "str"},
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
                    "ipam",
                ],
            ),
            (
                "action",
                "ensure_external_ip_pool",
                ["fulfillment_id", "ip_family", "cidrs"],
            ),
            (
                "action",
                "delete_external_ip_pool",
                ["fulfillment_id", "ip_family", "cidrs"],
            ),
            (
                "action",
                "reserve_external_ip",
                ["fulfillment_id", "pool_uid", "pool_fulfillment_id", "pool_tenant_id", "pool_cidr"],
            ),
            (
                "action",
                "release_external_ip",
                ["fulfillment_id", "pool_uid"],
            ),
            (
                "action",
                "ensure_external_ip_attachment",
                ["fulfillment_id", "attachment_context", "external_interface"],
            ),
            ("action", "delete_external_ip_attachment", ["fulfillment_id", "attachment_context"]),
            (
                "action",
                "ensure_nat_gateway",
                ["fulfillment_id", "nat_gateway_context", "external_interface"],
            ),
            ("action", "delete_nat_gateway", ["fulfillment_id", "nat_gateway_context"]),
            (
                "action",
                "prepare_network_attachment",
                ["virtual_network_uid", "subnet_uid", "host_name", "interface_name", "mac_address", "ports"],
            ),
            ("action", "begin_delete_network_attachment", ["virtual_network_uid"]),
        ],
        supports_check_mode=True,
    )
    params = module.params
    store = StateStore(params["state_file"])

    try:
        if params["action"] in EXTERNAL_IP_POOL_ACTIONS:
            if params["action"] == "ensure_external_ip_pool":
                pool, changed = store.ensure_external_ip_pool(
                    params["uid"],
                    params["fulfillment_id"],
                    params["tenant_id"],
                    params["ip_family"],
                    params["cidrs"],
                    check_mode=module.check_mode,
                )
            else:
                pool, changed = store.delete_external_ip_pool(
                    params["uid"],
                    params["fulfillment_id"],
                    params["tenant_id"],
                    params["ip_family"],
                    params["cidrs"],
                    check_mode=module.check_mode,
                )
            result = {
                "changed": changed,
                "backend_network_id": params["uid"],
            }
            if pool is not None:
                result["pool"] = pool
            module.exit_json(**result)
            return

        if params["action"] in EXTERNAL_IP_ACTIONS:
            if params["action"] == "reserve_external_ip":
                external_ip, changed = store.reserve_external_ip(
                    params["uid"],
                    params["fulfillment_id"],
                    params["tenant_id"],
                    params["pool_uid"],
                    params["pool_fulfillment_id"],
                    params["pool_tenant_id"],
                    params["pool_cidr"],
                    check_mode=module.check_mode,
                )
            else:
                external_ip, changed = store.release_external_ip(
                    params["uid"],
                    params["fulfillment_id"],
                    params["tenant_id"],
                    params["pool_uid"],
                    check_mode=module.check_mode,
                )
            result = {
                "changed": changed,
                "backend_network_id": params["uid"],
            }
            if external_ip is not None:
                result["external_ip"] = external_ip
            module.exit_json(**result)
            return

        if params["action"] in EXTERNAL_IP_ATTACHMENT_ACTIONS:
            if params["action"] == "ensure_external_ip_attachment":
                _attachment, changed = store.ensure_external_ip_attachment(
                    params["uid"],
                    params["fulfillment_id"],
                    params["tenant_id"],
                    params["attachment_context"],
                    params["external_interface"],
                    check_mode=module.check_mode,
                )
            else:
                _attachment, changed = store.delete_external_ip_attachment(
                    params["uid"],
                    params["fulfillment_id"],
                    params["tenant_id"],
                    params["attachment_context"],
                    check_mode=module.check_mode,
                )
            module.exit_json(changed=changed, backend_network_id=params["uid"])
            return

        if params["action"] in {"ensure_nat_gateway", "delete_nat_gateway"}:
            if params["action"] == "ensure_nat_gateway":
                _gateway, state_changed, provider_changed = store.ensure_nat_gateway(
                    params["uid"],
                    params["fulfillment_id"],
                    params["tenant_id"],
                    params["nat_gateway_context"],
                    params["external_interface"],
                    check_mode=module.check_mode,
                )
                module.exit_json(
                    changed=state_changed or provider_changed,
                    backend_network_id=params["uid"],
                )
            else:
                _gateway, changed = store.delete_nat_gateway(
                    params["uid"],
                    params["fulfillment_id"],
                    params["tenant_id"],
                    params["nat_gateway_context"],
                    check_mode=module.check_mode,
                )
                module.exit_json(changed=changed, backend_network_id=params["uid"])
            return

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

        if params["action"] == "query_dhcp_lease":
            lease = store.query_dhcp_lease(
                params["uid"],
                params["tenant_id"],
            )
            module.exit_json(
                changed=False,
                found=lease is not None,
                **({"lease": lease} if lease is not None else {}),
            )

        if params["action"] == "get_network_attachment":
            attachment = store.get_network_attachment(
                params["uid"], params["tenant_id"]
            )
            module.exit_json(
                changed=False,
                found=attachment is not None,
                attachment=attachment or {},
            )

        if params["action"] == "prepare_network_attachment":
            entry, state_changed, dhcp_changed = store.prepare_network_attachment(
                params["uid"],
                params["tenant_id"],
                params["virtual_network_uid"],
                params["subnet_uid"],
                params["host_name"],
                params["interface_name"],
                params["mac_address"],
                params["ports"],
                params.get("dhcp_supervisor"),
                check_mode=module.check_mode,
            )
            module.exit_json(
                changed=state_changed or dhcp_changed,
                attachment=entry,
            )

        if params["action"] == "mark_network_attachment_ready":
            entry, changed = store.mark_network_attachment_ready(
                params["uid"], params["tenant_id"], check_mode=module.check_mode
            )
            module.exit_json(changed=changed, attachment=entry or {})

        if params["action"] == "begin_delete_network_attachment":
            entry = store.begin_delete_network_attachment(
                params["uid"],
                params["tenant_id"],
                params["virtual_network_uid"],
                check_mode=module.check_mode,
            )
            module.exit_json(changed=entry is not None, found=entry is not None, attachment=entry or {})

        if params["action"] == "finish_delete_network_attachment":
            entry, state_changed, dhcp_changed = store.finish_delete_network_attachment(
                params["uid"],
                params["tenant_id"],
                params.get("dhcp_supervisor"),
                check_mode=module.check_mode,
            )
            module.exit_json(changed=state_changed or dhcp_changed, found=entry is not None)

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
                raise StateError("Switch VLAN discovery is required before Subnet allocation")
            observed_vlan_ids: set[int] = set()
            try:
                for switch in switch_state:
                    observed_vlan_ids.update(parse_switch_vlan_exclusions(switch))
            except SubnetProviderError as error:
                raise StateError(str(error)) from error
            subnet, changed = store.reserve_subnet(
                params["uid"],
                params["virtual_network_uid"],
                params["tenant_id"],
                params["subnet_cidr"],
                params["trunk_interface"],
                params["ipam"],
                observed_vlan_ids=observed_vlan_ids,
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
                params.get("virtual_network_uid"),
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
        retryable = bool(getattr(error, "retryable", isinstance(error, SubnetProviderError)))
        if isinstance(error, PublicStateError):
            module.fail_json(
                msg=error.public_message,
                public_message=error.public_message,
                retryable=retryable,
            )
            return
        if params["action"] in EXTERNAL_IP_ATTACHMENT_ACTIONS:
            module.fail_json(
                msg="AgentlessNet ExternalIPAttachment operation failed; inspect the network node and retry.",
                retryable=retryable,
            )
            return

        if params["action"] in EXTERNAL_IP_POOL_ACTIONS:
            module.fail_json(
                msg="AgentlessNet ExternalIPPool operation failed; inspect the network node and retry.",
                retryable=retryable,
            )
            return
        if params["action"] in {
            "get_subnet",
            "get_network_attachment",
            "query_dhcp_lease",
            "preflight_dhcp_supervisor",
            "reserve_subnet",
            "ensure_subnet",
            "prepare_delete_subnet",
            "release_subnet",
        }:
            module.fail_json(msg=str(error), retryable=retryable)
        module.fail_json(
            msg="AgentlessNet VirtualNetwork operation failed; inspect the network node and retry.",
            retryable=retryable,
        )


if __name__ == "__main__":
    main()
