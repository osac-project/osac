#!/usr/bin/python
# -*- coding: utf-8 -*-

# (c) 2017, Simon Dodsley (simon@everpuredata.com)
# GNU General Public License v3.0+ (see COPYING or https://www.gnu.org/licenses/gpl-3.0.txt)

from __future__ import absolute_import, division, print_function

__metaclass__ = type


ANSIBLE_METADATA = {
    "metadata_version": "1.1",
    "status": ["preview"],
    "supported_by": "community",
}


DOCUMENTATION = """
---
module: purefb_network
version_added: "1.0.0"
short_description:  Manage network interfaces in a Everpure FlashBlade
description:
    - This module manages network interfaces on Everpure FlashBlade.
    - When creating a network interface a subnet must already exist with
      a network prefix that covers the IP address of the interface being
      created.
author: Everpure Ansible Team (@sdodsley) <pure-ansible-team@everpuredata.com>
options:
  name:
    description:
      - Interface Name.
    required: true
    type: str
  state:
    description:
      - Create, delete or modifies a network interface.
    required: false
    default: present
    choices: [ "present", "absent" ]
    type: str
  address:
    description:
      - IP address of interface.
    required: false
    type: str
  services:
    description:
      - Define which services are configured for the interfaces.
    required: false
    choices: [ "data", "replication" ]
    default: data
    type: str
  itype:
    description:
      - Type of interface.
    required: false
    choices: [ "vip" ]
    default: vip
    type: str
  attached_server:
    description:
        - Name of the server you want to attach to the interface
        - To attach to a server on a Realm, set the value to realm-1::server-1
        - Only 1 server per Network Interface.
    required: false
    type: str

extends_documentation_fragment:
    - everpure.flashblade.everpure.fb
"""

EXAMPLES = """
- name: Create new network interface named foo
  everpure.flashblade.purefb_network:
    name: foo
    address: 10.21.200.23
    state: present
    fb_url: 10.10.10.2
    api_token: T-55a68eb5-c785-4720-a2ca-8b03903bf641

- name: Create a new network interface named foo with attached server named server-1 in a Realm named realm-1
  everpure.flashblade.purefb_network:
    name: foo
    address: 10.21.200.23
    attached_server: realm-1::server-1
    state: present
    fb_url: 10.10.10.2
    api_token: T-55a68eb5-c785-4720-a2ca-8b03903bf641

- name: Change IP address of network interface named foo
  everpure.flashblade.purefb_network:
    name: foo
    state: present
    address: 10.21.200.123
    fb_url: 10.10.10.2
    api_token: T-55a68eb5-c785-4720-a2ca-8b03903bf641

- name: Delete network interface named foo
  everpure.flashblade.purefb_network:
    name: foo
    state: absent
    fb_url: 10.10.10.2
    api_token: T-55a68eb5-c785-4720-a2ca-8b03903bf641
"""

RETURN = """
"""

HAS_PYPURECLIENT = True
try:
    from pypureclient.flashblade import NetworkInterface, NetworkInterfacePatch
except ImportError:
    HAS_PYPURECLIENT = False

from ansible.module_utils.basic import AnsibleModule
from ansible_collections.everpure.flashblade.plugins.module_utils.purefb import (
    get_system,
    purefb_argument_spec,
)
from ansible_collections.everpure.flashblade.plugins.module_utils.common import (
    get_error_message,
    get_rest_api_version,
)
from ansible_collections.everpure.flashblade.plugins.module_utils.version import (
    LooseVersion,
)

SERVERS_API_VERSION = "2.16"


def get_iface(module, blade):
    """Return Filesystem or None"""
    res = blade.get_network_interfaces(names=[module.params["name"]])
    if res.status_code == 200:
        return list(res.items)[0]
    return None


def _attached_server_name(iface):
    """Name of the server an interface is attached to, or None.

    ``attached_servers`` is a list of references on the API, even though an
    interface can only carry one server.
    """
    attached = getattr(iface, "attached_servers", None) or []
    if not attached:
        return None
    return getattr(attached[0], "name", None)


def create_iface(module, blade):
    """Create Network Interface"""
    changed = True
    if not module.check_mode:
        network_interface = NetworkInterface(
            address=module.params["address"],
            services=[module.params["services"]],
            type=module.params["itype"],
        )
        if module.params["attached_server"]:
            network_interface.attached_servers = [
                {"name": module.params["attached_server"]}
            ]
        res = blade.post_network_interfaces(
            names=[module.params["name"]],
            network_interface=network_interface,
        )
        if res.status_code != 200:
            module.fail_json(
                msg="Interface {0} creation failed. Error: {1}".format(
                    module.params["name"], get_error_message(res)
                )
            )
    module.exit_json(changed=changed)


def modify_iface(module, blade):
    """Modify Network Interface IP address"""
    changed = False
    iface = get_iface(module, blade)
    if module.params["address"] != iface.address:
        changed = True
        if not module.check_mode:
            res = blade.patch_network_interfaces(
                names=[module.params["name"]],
                network_interface=NetworkInterfacePatch(
                    address=module.params["address"]
                ),
            )
            if res.status_code != 200:
                module.fail_json(
                    msg="Failed to modify Interface {0}. Error: {1}".format(
                        module.params["name"], get_error_message(res)
                    )
                )
    # Only move the interface when it is not already on the requested server.
    # An interface carries at most one attached server, but the API field is
    # a list, so the current name has to be pulled out of it.
    wanted_server = module.params["attached_server"]
    if wanted_server is not None and wanted_server != _attached_server_name(iface):
        changed = True
        if not module.check_mode:
            res = blade.patch_network_interfaces(
                names=[module.params["name"]],
                network_interface=NetworkInterfacePatch(
                    attached_servers=[{"name": module.params["attached_server"]}]
                ),
            )
            if res.status_code != 200:
                module.fail_json(
                    msg="Failed to modify Interface {0}. Error: {1}".format(
                        module.params["name"], get_error_message(res)
                    )
                )
    module.exit_json(changed=changed)


def delete_iface(module, blade):
    """Delete Network Interface"""
    changed = True
    if not module.check_mode:
        res = blade.delete_network_interfaces(names=[module.params["name"]])
        if res.status_code != 200:
            module.fail_json(
                msg="Failed to delete network {0}. Error: {1}".format(
                    module.params["name"], get_error_message(res)
                )
            )
    module.exit_json(changed=changed)


def main():
    argument_spec = purefb_argument_spec()
    argument_spec.update(
        dict(
            name=dict(type="str", required=True),
            state=dict(type="str", default="present", choices=["present", "absent"]),
            address=dict(type="str"),
            services=dict(type="str", default="data", choices=["data", "replication"]),
            itype=dict(type="str", default="vip", choices=["vip"]),
            attached_server=dict(type="str", required=False),
        )
    )

    required_if = [["state", "present", ["address"]]]

    module = AnsibleModule(
        argument_spec, required_if=required_if, supports_check_mode=True
    )

    if not HAS_PYPURECLIENT:
        module.fail_json(msg="py-pure-client sdk is required for this module")
    state = module.params["state"]
    blade = get_system(module)
    iface = get_iface(module, blade)

    api_version = get_rest_api_version(blade)
    if module.params["attached_server"] and LooseVersion(
        SERVERS_API_VERSION
    ) > LooseVersion(api_version):
        module.fail_json(
            msg="FlashBlade REST version {0} does not support attached_server "
            "(requires {1}+).".format(api_version, SERVERS_API_VERSION)
        )

    if state == "present" and not iface:
        create_iface(module, blade)
    elif state == "present" and iface:
        modify_iface(module, blade)
    elif state == "absent" and iface:
        delete_iface(module, blade)
    elif state == "absent" and not iface:
        module.exit_json(changed=False)


if __name__ == "__main__":
    main()
