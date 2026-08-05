#!/usr/bin/python
# coding: utf-8 -*-
# Copyright: (c) 2017, Wayne Witzel III <wayne@riotousliving.com>
# Copyright: (c) 2024, Martin Slemr <@slemrmartin>
# GNU General Public License v3.0+ (see COPYING or https://www.gnu.org/licenses/gpl-3.0.txt)

from __future__ import absolute_import, division, print_function

__metaclass__ = type


DOCUMENTATION = """
---
module: team
author: Red Hat (@RedHatOfficial)
short_description: Configure a gateway team.
description:
    - Configure an automation platform gateway team.
options:
    name:
      required: true
      type: str
      description: The name of the team, must be unique
    new_name:
      type: str
      description: Setting this option will change the existing name (looked up via the name field)
    description:
      description: The description of the Team
      type: str
    organization:
      type: str
      required: true
      description: The name or ID referencing the Organization
    new_organization:
      type: str
      description: Setting this option will change the existing organization (looked up via the organization field)

extends_documentation_fragment:
- ansible.platform.state
- ansible.platform.auth
"""

EXAMPLES = """
- name: Create Team
  ansible.platform.team:
    name: Gateway Developers
    description: AAP Gateway Developers Team
    organization: Ansible Product Development

- name: Update Team
  ansible.platform.team:
    name: Gateway Developers
    organization: "1"
    new_organization: "Red Hat Ansible"

- name: Delete Team
  ansible.platform.team:
    name: Gateway Developers
    organization: "Red Hat Ansible"
    state: absent
...
"""

from ..module_utils.aap_module import AAPModule  # noqa
from ..module_utils.aap_team import AAPTeam  # noqa


def main():
    argument_spec = dict(
        name=dict(type="str", required=True),
        new_name=dict(type="str"),
        description=dict(type="str"),
        organization=dict(type="str", required=True),
        new_organization=dict(type="str"),
        state=dict(choices=["present", "absent", "exists", "enforced"], default="present"),
    )

    # Create a module with spec
    module = AAPModule(argument_spec=argument_spec, supports_check_mode=True)

    AAPTeam(module).manage()


if __name__ == "__main__":
    main()
