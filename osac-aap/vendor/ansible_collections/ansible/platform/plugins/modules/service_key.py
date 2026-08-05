#!/usr/bin/python
# coding: utf-8 -*-

# Copyright: (c) 2024, Martin Slemr <@slemrmartin>
# GNU General Public License v3.0+ (see COPYING or https://www.gnu.org/licenses/gpl-3.0.txt)

from __future__ import absolute_import, division, print_function

__metaclass__ = type

DOCUMENTATION = """
---
module: service_key
author: Martin Slemr (@slemrmartin)
short_description: Configure a gateway service key.
description:
    - Configure an automation platform gateway service key.
options:
    name:
      required: true
      type: str
      description: The name of the AAP Service Key, must be unique
    new_name:
      type: str
      description: Setting this option will change the existing name (looked up via the name field)
    is_active:
      type: bool
      description:
      - flag for setting the active state of the Service Key
      - defaults to true by API
    service_cluster:
      description:
      - The name or ID of the Service Cluster
      type: str
    algorithm:
      type: str
      description:
      - algorithm to use for this Service Key
      choices: ["HS256", "HS384", "HS512"]
    secret:
      type: str
      description:
      - secret to use for this Service Key
      - required when creating new Service Key, non-editable
    secret_length:
      type: int
      description:
        - Number of random bytes in the secret
    mark_previous_inactive:
      type: bool
      description:
        - If true any other secret keys for this service will become inactive

extends_documentation_fragment:
- ansible.platform.state
- ansible.platform.auth
"""

EXAMPLES = """
- name: Add service ckey
  ansible.platform.service_key:
    name: Automation Controller Service Key
    is_active: true
    service_cluster: Automation Controller
    algorithm: HS256
    secret: mysecret
    secret_length: 32

- name: Add new controller service key
  ansible.platform.service_key:
    name: Automation Controller Service Key
    new_name: New Automation Controller Service Key
    is_active: true
    service_cluster: Automation Controller
    algorithm: HS256
    secret: mysecret1
    secret_length: 32
    mark_previous_inactive: true
...
"""

from ..module_utils.aap_module import AAPModule  # noqa
from ..module_utils.aap_service_key import AAPServiceKey  # noqa


def main():
    argument_spec = dict(
        name=dict(type="str", required=True),
        new_name=dict(type="str"),
        is_active=dict(type="bool"),
        service_cluster=dict(type="str"),
        algorithm=dict(type="str", choices=["HS256", "HS384", "HS512"]),
        secret=dict(type="str", no_log=True),
        secret_length=dict(type="int", no_log=False),
        mark_previous_inactive=dict(type="bool"),
        state=dict(type="str", choices=["present", "absent", "exists", "enforced"], default="present"),
    )

    # Create a module with spec
    module = AAPModule(argument_spec=argument_spec, supports_check_mode=True)
    AAPServiceKey(module).manage(json_output_fields=['secret'])


if __name__ == "__main__":
    main()
