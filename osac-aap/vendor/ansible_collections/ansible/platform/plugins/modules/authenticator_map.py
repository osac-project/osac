#!/usr/bin/python
# coding: utf-8 -*-

# Copyright: (c) 2024, Martin Slemr <@slemrmartin>
# GNU General Public License v3.0+ (see COPYING or https://www.gnu.org/licenses/gpl-3.0.txt)

from __future__ import absolute_import, division, print_function

__metaclass__ = type


DOCUMENTATION = """
---
module: authenticator_map
author: Red Hat (@RedHatOfficial)
short_description: Configure a gateway authenticator maps.
description:
    - Configure an automation platform gateway authenticator maps.
options:
    name:
      required: true
      type: str
      description: The name of the authenticator mapping, must be unique
    new_name:
      type: str
      description: Setting this option will change the existing name (looked up via the name field)
    authenticator:
      type: str
      required: true
      description: The name of ID referencing the Authenticator
    new_authenticator:
      type: str
      description: Setting this option will change the existing authenticator (looked up via the authenticator field)
    revoke:
      type: bool
      default: false
      description: If a user does not meet this rule should we revoke the permission
    map_type:
      type: str
      description:
      - What does the map work on, a team, a user flag or is this an allow rule
      choices: ["allow", "is_superuser", "team", "organization", "role"]
    team:
      type: str
      description:
      - A team name this rule works on
      - required if map_type is a 'team'
      - required if role's content type is a 'team'
    organization:
      type: str
      description:
      - An organization name this rule works on
      - required if map_type is either 'organization' or 'team'
      - required if role's content type is either 'organization' or 'team'
    role:
      type: str
      description:
      - The name of the RBAC Role Definition to be used for this map
    triggers:
      type: dict
      description:
      - Trigger information for this rule
      - django-ansible-base/ansible_base/authentication/utils/trigger_definition.py
    order:
      type: int
      description:
      - The order in which this rule should be processed, smaller numbers are of higher precedence
      - Items with the same order will be executed in random order
      - Value must be greater or equal to 0
      - Defaults to 0 (by API)
extends_documentation_fragment:
- ansible.platform.state
- ansible.platform.auth
"""

EXAMPLES = """
- name: Add an authenticator map to an existing authenticator
  ansible.platform.authenticator_map:
    name: "Always add users to org 1"
    authenticator: "{{ some_authenticator.name }}"
    revoke: false
    map_type: organization
    role: Organization Member
    organization: "Organization 1"
    triggers:
      always: {}
      never: {}
    order: 10
...
"""

from ..module_utils.aap_authenticator_map import AAPAuthenticatorMap  # noqa
from ..module_utils.aap_module import AAPModule  # noqa


def main():
    argument_spec = dict(
        name=dict(type="str", required=True),
        new_name=dict(type="str"),
        authenticator=dict(type="str", required=True),
        new_authenticator=dict(type="str"),
        revoke=dict(type="bool", default=False),
        map_type=dict(type="str", choices=["allow", "is_superuser", "team", "organization", "role"]),
        team=dict(type="str"),
        role=dict(type="str"),
        organization=dict(type="str"),
        triggers=dict(type="dict"),
        order=dict(type="int"),
        state=dict(choices=["present", "absent", "exists", "enforced"], default="present"),
    )

    # Create a module with spec
    module = AAPModule(argument_spec=argument_spec, supports_check_mode=True)

    AAPAuthenticatorMap(module).manage()


if __name__ == "__main__":
    main()
