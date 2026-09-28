#!/usr/bin/python
"""Manage shared AgentlessNet namespace and veth operations."""

from __future__ import annotations

from ansible.module_utils.basic import AnsibleModule

from ansible_collections.osac.templates.plugins.module_utils.agentless_net_network import (
    NetworkCommandError,
    delete_uplink,
    ensure_ipv4_forwarding,
    ensure_namespace,
    ensure_uplink,
)


DOCUMENTATION = r"""
---
module: agentless_net_network
short_description: Manage an AgentlessNet network namespace and veth uplink
description:
  - Provides the shared namespace and uplink operations used by AgentlessNet
    VirtualNetworks and legacy namespace routers.
options:
  action:
    description: Networking operation to perform.
    type: str
    required: true
    choices: [ensure_namespace, ensure_uplink, delete_uplink]
  namespace:
    description: Linux network namespace name.
    type: str
    required: true
  host_interface:
    description: Veth endpoint in the host network namespace.
    type: str
  namespace_interface:
    description: Veth endpoint inside the network namespace.
    type: str
  namespace_ip:
    description: IPv4 address and prefix for the namespace endpoint.
    type: str
  host_ip:
    description: IPv4 address and prefix for the host endpoint.
    type: str
  gateway:
    description: Default route gateway inside the network namespace.
    type: str
  enable_namespace_forwarding:
    description: Enable IPv4 forwarding inside the namespace after uplink setup.
    type: bool
    default: false
"""


def main() -> None:
    module = AnsibleModule(
        argument_spec={
            "action": {
                "type": "str",
                "required": True,
                "choices": ["ensure_namespace", "ensure_uplink", "delete_uplink"],
            },
            "namespace": {"type": "str", "required": True},
            "host_interface": {"type": "str"},
            "namespace_interface": {"type": "str"},
            "namespace_ip": {"type": "str"},
            "host_ip": {"type": "str"},
            "gateway": {"type": "str"},
            "enable_namespace_forwarding": {"type": "bool", "default": False},
        },
        required_if=[
            (
                "action",
                "ensure_uplink",
                ["host_interface", "namespace_interface", "namespace_ip", "host_ip", "gateway"],
            ),
            ("action", "delete_uplink", ["host_interface"]),
        ],
    )
    params = module.params
    try:
        if params["action"] == "ensure_namespace":
            changed = ensure_namespace(params["namespace"])
        elif params["action"] == "ensure_uplink":
            changed = ensure_uplink(
                params["namespace"],
                params["namespace_interface"],
                params["host_interface"],
                params["namespace_ip"],
                params["host_ip"],
                params["gateway"],
            )
            if params["enable_namespace_forwarding"]:
                changed = ensure_ipv4_forwarding(params["namespace"]) or changed
        else:
            changed = delete_uplink(params["namespace"], params["host_interface"])
        module.exit_json(changed=changed)
    except NetworkCommandError as error:
        module.fail_json(msg=str(error))


if __name__ == "__main__":
    main()
