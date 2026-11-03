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
module: purefb_snmp_agent
version_added: '1.0.0'
short_description: Configure the FlashBlade SNMP Agent
description:
- Configure the management SNMP Agent on a Everpure FlashBlade.
- The array returns the SNMPv2c community string and the SNMPv3
  passphrases masked, so they cannot be compared. They are only sent when
  the SNMP version or another readable setting changes. A task that
  changes nothing but the community string or a passphrase reports no
  change.
author:
- Everpure Ansible Team (@sdodsley) <pure-ansible-team@everpuredata.com>
options:
  auth_passphrase:
    type: str
    description:
    - SNMPv3 only. Passphrase of 8 - 32 characters.
  auth_protocol:
    type: str
    description:
    - SNMP v3 only. Hash algorithm to use
    choices: [ MD5, SHA ]
  community:
    type: str
    description:
    - SNMP v2c only. Manager community ID. Between 1 and 32 characters long.
  user:
    type: str
    description:
    - SNMP v3 only. User ID recognized by the specified SNMP agent.
      Must be between 1 and 32 characters.
  version:
    type: str
    description:
    - Version of SNMP protocol to use for the agent.
    choices: [ v2c, v3 ]
  privacy_passphrase:
    type: str
    description:
    - SNMPv3 only. Passphrase to encrypt SNMP messages.
      Must be between 8 and 63 non-space ASCII characters.
  privacy_protocol:
    type: str
    description:
    - SNMP v3 only. Encryption protocol to use
    choices: [ AES, DES ]
extends_documentation_fragment:
- everpure.flashblade.everpure.fb
"""

EXAMPLES = r"""
- name: Update v2c SNMP agent
  everpure.flashblade.purefb_snmp_agent:
    community: public
    fb_url: 10.10.10.2
    api_token: T-9f276a18-50ab-446e-8a0c-666a3529a1b6
- name: Update v3 SNMP agent
  everpure.flashblade.purefb_snmp_agent:
    version: v3
    auth_protocol: MD5
    auth_passphrase: password
    fb_url: 10.10.10.2
    api_token: T-9f276a18-50ab-446e-8a0c-666a3529a1b6
"""

RETURN = r"""
"""


HAS_PYPURECLIENT = True
try:
    from pypureclient.flashblade import SnmpAgent, SnmpV2c, SnmpV3
except ImportError:
    HAS_PYPURECLIENT = False


from ansible.module_utils.basic import AnsibleModule
from ansible_collections.everpure.flashblade.plugins.module_utils.purefb import (
    get_system,
    purefb_argument_spec,
)
from ansible_collections.everpure.flashblade.plugins.module_utils.common import (
    get_error_message,
)


def _readable(value):
    """Return value unless the array omitted or masked it.

    The community string and the passphrases are secrets: the array
    returns them as "****" (or not at all), so they can never be compared.
    """
    if value is None:
        return None
    if isinstance(value, str) and value and value.strip("*") == "":
        return None
    return value


def update_agent(module, blade):
    """Update SNMP Agent"""
    changed = False
    res = blade.get_snmp_agents()
    if res.status_code != 200:
        module.fail_json(msg="Failed to get configuration for SNMP agent.")
    agent = list(res.items)[0]
    version = module.params["version"] or agent.version
    # The array returns both the v2c and v3 blocks whatever the active
    # version, with the community string and the passphrases masked as
    # "****". Only the readable attributes of the requested version are
    # compared; an omitted parameter keeps the current value, and the
    # secrets are sent whenever the version or a readable setting changes.
    current_attr = {"version": agent.version}
    new_attr = {"version": version}
    if version == "v2c":
        community = _readable(getattr(getattr(agent, "v2c", None), "community", None))
        if community is not None:
            current_attr["community"] = community
            new_attr["community"] = module.params["community"] or community
    else:
        current_v3 = getattr(agent, "v3", None)
        for attr in ("auth_protocol", "privacy_protocol", "user"):
            current_attr[attr] = getattr(current_v3, attr, None)
            new_attr[attr] = module.params[attr] or current_attr[attr]
    if current_attr != new_attr:
        changed = True
        if not module.check_mode:
            if version == "v2c":
                updated_v2c_attrs = SnmpV2c(community=module.params["community"])
                updated_v2c_agent = SnmpAgent(version="v2c", v2c=updated_v2c_attrs)
                res = blade.patch_snmp_agents(snmp_agent=updated_v2c_agent)
                if res.status_code != 200:
                    module.fail_json(
                        msg="Failed to update v2c SNMP agent. Error: {0}".format(
                            get_error_message(res)
                        )
                    )
            else:
                updated_v3_attrs = SnmpV3(
                    auth_protocol=module.params["auth_protocol"],
                    auth_passphrase=module.params["auth_passphrase"],
                    privacy_protocol=module.params["privacy_protocol"],
                    privacy_passphrase=module.params["privacy_passphrase"],
                    user=module.params["user"],
                )
                updated_v3_agent = SnmpAgent(version="v3", v3=updated_v3_attrs)
                res = blade.patch_snmp_agents(snmp_agent=updated_v3_agent)
                if res.status_code != 200:
                    module.fail_json(
                        msg="Failed to update v3 SNMP agent. Error: {0}".format(
                            get_error_message(res)
                        )
                    )

    module.exit_json(changed=changed)


def main():
    argument_spec = purefb_argument_spec()
    argument_spec.update(
        dict(
            user=dict(type="str"),
            auth_passphrase=dict(type="str", no_log=True),
            auth_protocol=dict(type="str", choices=["MD5", "SHA"]),
            privacy_passphrase=dict(type="str", no_log=True),
            privacy_protocol=dict(type="str", choices=["AES", "DES"]),
            version=dict(type="str", choices=["v2c", "v3"]),
            community=dict(type="str"),
        )
    )

    required_together = [
        ["auth_passphrase", "auth_protocol"],
        ["privacy_passphrase", "privacy_protocol"],
    ]
    required_if = [["version", "v2c", ["community"]], ["version", "v3", ["user"]]]

    module = AnsibleModule(
        argument_spec,
        required_together=required_together,
        required_if=required_if,
        supports_check_mode=True,
    )

    blade = get_system(module)

    if not HAS_PYPURECLIENT:
        module.fail_json(msg="py-pure-client SDK is required for this module")

    if module.params["version"] == "v3":
        if module.params["auth_passphrase"] and not (
            8 <= len(module.params["auth_passphrase"]) <= 32
        ):
            module.fail_json(msg="auth_password must be between 8 and 32 characters")
        if module.params["privacy_passphrase"] and not (
            8 <= len(module.params["privacy_passphrase"]) <= 63
        ):
            module.fail_json(msg="privacy_password must be between 8 and 63 characters")

    update_agent(module, blade)

    module.exit_json(changed=False)


if __name__ == "__main__":
    main()
