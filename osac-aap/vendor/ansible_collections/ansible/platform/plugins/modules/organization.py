#!/usr/bin/python
# coding: utf-8 -*-

# Copyright: (c) 2017, Wayne Witzel III <wayne@riotousliving.com>
# Copyright: (c) 2024, Martin Slemr <@slemrmartin>
# GNU General Public License v3.0+ (see COPYING or https://www.gnu.org/licenses/gpl-3.0.txt)

from __future__ import absolute_import, division, print_function

__metaclass__ = type


DOCUMENTATION = """
---
module: organization
author: Red Hat (@RedHatOfficial)
short_description: Configure a gateway organization.
description:
    - Configure an automation platform gateway organizations.
options:
    name:
      required: true
      type: str
      description: The name of the organization, must be unique
    new_name:
      type: str
      description: Setting this option will change the existing name (looked up via the name field)
    description:
      description: The description of the Organization
      type: str
extends_documentation_fragment:
- ansible.platform.state
- ansible.platform.auth
"""

EXAMPLES = """
- name: Create Organization
  ansible.platform.organization:
    name: Ansible Product Development
    description: Organization for ansible developers

- name: Update Organization
  ansible.platform.organization:
    name: Ansible Product Development

- name: Delete Organization
  ansible.platform.organization:
    name: Ansible Product Development
    state: absent
...
"""

from ..module_utils.aap_module import AAPModule
from ..module_utils.aap_organization import AAPOrganization


def main():
    argument_spec = dict(
        name=dict(type="str", required=True),
        new_name=dict(type="str"),
        description=dict(type="str"),
        state=dict(choices=["present", "absent", "exists", "enforced"], default="present"),
    )

    # Create a module with spec
    module = AAPModule(argument_spec=argument_spec, supports_check_mode=True)

    AAPOrganization(module).manage()


if __name__ == "__main__":
    main()
