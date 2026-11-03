#!/usr/bin/python
# -*- coding: utf-8 -*-

# (c) 2020, Simon Dodsley (simon@everpuredata.com)
# GNU General Public License v3.0+ (see COPYING or https://www.gnu.org/licenses/gpl-3.0.txt)

from __future__ import absolute_import, division, print_function

__metaclass__ = type

ANSIBLE_METADATA = {
    "metadata_version": "1.1",
    "status": ["preview"],
    "supported_by": "community",
}

DOCUMENTATION = r"""
---
module: purefb_syslog
version_added: '1.4.0'
short_description: Configure Everpure FlashBlade syslog settings
description:
- Configure syslog configuration for Everpure FlashBlades.
- Add or delete an individual syslog server to the existing
  list of serves.
author:
- Everpure Ansible Team (@sdodsley) <pure-ansible-team@everpuredata.com>
options:
  name:
    description:
    - Unique identifier for the syslog server address
    type: str
    required: true
  state:
    description:
    - Create update, delete or test syslog servers configuration
    default: present
    type: str
    choices: [ absent, present, test ]
  protocol:
    description:
    - Protocol which server uses
    type: str
    choices: [ tcp, tls, udp ]
  port:
    description:
    - Port at which the server is listening. If no port is specified
      the system will use 514
    type: str
  address:
    description:
    - Syslog server address.
      This field supports IPv4 or FQDN.
      An invalid IP addresses will cause the module to fail.
      No validation is performed for FQDNs.
    type: str
  services:
    description:
    - Syslog service type(s)
    type: list
    elements: str
    choices: [ management, data-audit ]
    default: management
extends_documentation_fragment:
- everpure.flashblade.everpure.fb
"""

EXAMPLES = r"""
- name: Delete exisitng syslog server entries
  everpure.flashblade.purefb_syslog:
    name: syslog1
    state: absent
    fb_url: 10.10.10.2
    api_token: T-55a68eb5-c785-4720-a2ca-8b03903bf641

- name: Set array syslog servers
  everpure.flashblade.purefb_syslog:
    state: present
    name: syslog1
    services:
      - data-audit
    address: syslog1.com
    protocol: udp
    fb_url: 10.10.10.2
    api_token: T-55a68eb5-c785-4720-a2ca-8b03903bf641
"""

RETURN = r"""
"""


HAS_PYPURECLIENT = True
try:
    from pypureclient.flashblade import SyslogServerPost, SyslogServerPatch
except ImportError:
    HAS_PYPURECLIENT = False


from ansible.module_utils.basic import AnsibleModule
from ansible_collections.everpure.flashblade.plugins.module_utils.purefb import (
    get_system,
    purefb_argument_spec,
)
from ansible_collections.everpure.flashblade.plugins.module_utils.version import (
    LooseVersion,
)
from ansible_collections.everpure.flashblade.plugins.module_utils.common import (
    get_error_message,
    get_rest_api_version,
)

SYSLOG_SERVICES_API = "2.14"


def delete_syslog(module, blade):
    """Delete Syslog Server"""
    changed = True
    if not module.check_mode:
        res = blade.delete_syslog_servers(names=[module.params["name"]])
        if res.status_code != 200:
            module.fail_json(
                msg="Failed to remove syslog server {0}. Error: {1}".format(
                    module.params["name"], get_error_message(res)
                )
            )

    module.exit_json(changed=changed)


def add_syslog(module, blade):
    """Add Syslog Server"""
    changed = False
    noport_address = module.params["protocol"] + "://" + module.params["address"]

    if module.params["port"]:
        full_address = noport_address + ":" + module.params["port"]
    else:
        full_address = noport_address
    api_version = get_rest_api_version(blade)

    changed = True
    if not module.check_mode:
        if LooseVersion(SYSLOG_SERVICES_API) <= LooseVersion(api_version):
            res = blade.post_syslog_servers(
                names=[module.params["name"]],
                syslog_server=SyslogServerPost(
                    uri=full_address, services=module.params["services"]
                ),
            )
            if res.status_code != 200:
                module.fail_json(
                    msg="Failed to add syslog server. Error: {0}".format(
                        get_error_message(res)
                    )
                )
        else:
            res = blade.post_syslog_servers(
                syslog_server=SyslogServerPost(uri=full_address),
                names=[module.params["name"]],
            )
            if res.status_code != 200:
                module.fail_json(
                    msg="Failed to add syslog server {0} - {1}. Error: {2}".format(
                        module.params["name"], full_address, get_error_message(res)
                    )
                )

    module.exit_json(changed=changed)


def update_syslog(module, blade):
    """Update Syslog Server"""
    changed = False
    api_version = get_rest_api_version(blade)
    if LooseVersion(SYSLOG_SERVICES_API) > LooseVersion(api_version):
        module.exit_json(
            msg="Purity//FB needs upgrading to support modification of existing syslog server"
        )
    syslog_config = list(blade.get_syslog_servers(names=[module.params["name"]]).items)[
        0
    ]
    noport_address = module.params["protocol"] + "://" + module.params["address"]

    if module.params["port"]:
        full_address = noport_address + ":" + module.params["port"]
    else:
        full_address = noport_address
    new_uri = syslog_config.uri
    if full_address != new_uri:
        changed = True
        new_uri = full_address
    new_services = syslog_config.services
    if module.params["services"] != new_services:
        changed = True
        new_services = module.params["services"]
    if changed and not module.check_mode:
        res = blade.patch_syslog_servers(
            names=[module.params["name"]],
            syslog_server=SyslogServerPatch(uri=new_uri, services=new_services),
        )
        if res.status_code != 200:
            module.fail_json(
                msg="Updating syslog server {0} failed. Error: {1}".format(
                    module.params["name"], get_error_message(res)
                )
            )
    module.exit_json(changed=changed)


def test_syslog(module, blade):
    """Test syslog configuration"""
    test_response = []
    # The syslog test endpoint takes no names filter, so the array tests
    # every configured server; only the results for I(name) are returned.
    res = blade.get_syslog_servers_test()
    if res.status_code != 200:
        module.fail_json(
            msg="Failed to test syslog server {0}. Error: {1}".format(
                module.params["name"], get_error_message(res)
            )
        )
    for component in list(res.items):
        if getattr(component.resource, "name", None) != module.params["name"]:
            continue
        if component.enabled:
            enabled = "true"
        else:
            enabled = "false"
        if component.success:
            success = "true"
        else:
            success = "false"
        test_response.append(
            {
                "component_address": component.component_address,
                "component_name": component.component_name,
                "description": component.description,
                "destination": component.destination,
                "enabled": enabled,
                "result_details": getattr(component, "result_details", ""),
                "success": success,
                "test_type": component.test_type,
                "resource_name": component.resource.name,
            }
        )
    module.exit_json(changed=False, test_response=test_response)


def main():
    argument_spec = purefb_argument_spec()
    argument_spec.update(
        dict(
            address=dict(type="str"),
            protocol=dict(type="str", choices=["tcp", "tls", "udp"]),
            port=dict(type="str"),
            name=dict(type="str", required=True),
            services=dict(
                type="list",
                elements="str",
                choices=["management", "data-audit"],
                default=["management"],
            ),
            state=dict(
                type="str", default="present", choices=["absent", "present", "test"]
            ),
        )
    )

    required_if = [["state", "present", ["address", "protocol"]]]

    module = AnsibleModule(
        argument_spec, required_if=required_if, supports_check_mode=True
    )

    blade = get_system(module)

    res = blade.get_syslog_servers(names=[module.params["name"]])
    exists = bool(res.status_code == 200)

    if module.params["state"] == "absent" and exists:
        delete_syslog(module, blade)
    elif module.params["state"] == "present" and exists:
        update_syslog(module, blade)
    elif module.params["state"] == "present" and not exists:
        add_syslog(module, blade)
    elif module.params["state"] == "test" and exists:
        test_syslog(module, blade)

    module.exit_json(changed=False)


if __name__ == "__main__":
    main()
