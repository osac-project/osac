#!/usr/bin/python
# coding: utf-8 -*-

# (c) 2020, John Westcott IV <john.westcott.iv@redhat.com>
# (c) 2023, Sean Sullivan <@sean-m-sullivan>
# GNU General Public License v3.0+ (see COPYING or https://www.gnu.org/licenses/gpl-3.0.txt)

from __future__ import absolute_import, division, print_function

__metaclass__ = type


DOCUMENTATION = """
---
module: user
author: Sean Sullivan (@sean-m-sullivan)
short_description: Configure a gateway user.
description:
    - Configure an automation platform gateway user.
options:
    username:
      description:
        - Required. 150 characters or fewer. Letters, digits and @/./+/-/_ only.
      required: True
      type: str
    first_name:
      description:
        - First name of the user.
      type: str
    last_name:
      description:
        - Last name of the user.
      type: str
    email:
      description:
        - Email address of the user.
      type: str
    is_superuser:
      description:
        - Designates that this user has all permissions without explicitly assigning them.
      type: bool
      aliases: ['superuser']
    password:
      description:
        - Write-only field used to change the password.
      type: str
    organizations:
      description:
        - List of organizations IDs to associate with the user
      type: list
      elements: str
    update_secrets:
      description:
        - C(true) will always change password if user specifies password, even if API gives $encrypted$ for password.
        - C(false) will only set the password if other values change too.
      type: bool
      default: true
    authenticators:
      description:
        - A list of authenticators to associate the user with
      type: list
      elements: str
    authenticator_uid:
      description:
        - The UID to associate with this users authenticators
      type: str

extends_documentation_fragment:
- ansible.platform.state
- ansible.platform.auth
"""


EXAMPLES = """
- name: Add user
  ansible.platform.user:
    username: jdoe
    password: foobarbaz
    email: jdoe@example.org
    first_name: John
    last_name: Doe
    state: present

- name: Add user as a system administrator
  ansible.platform.user:
    username: jdoe
    password: foobarbaz
    email: jdoe@example.org
    superuser: true
    state: present

- name: Delete user
  ansible.platform.user:
    username: jdoe
    email: jdoe@example.org
    state: absent
...
"""

from ..module_utils.aap_module import AAPModule  # noqa
from ..module_utils.aap_user import AAPUser  # noqa


def main():
    # Any additional arguments that are not fields of the item can be added here
    argument_spec = dict(
        username=dict(required=True),
        first_name=dict(),
        last_name=dict(),
        email=dict(),
        is_superuser=dict(type="bool", aliases=["superuser"]),
        password=dict(no_log=True),
        organizations=dict(type="list", elements='str'),
        update_secrets=dict(type="bool", default=True, no_log=False),
        authenticators=dict(type="list", elements='str'),
        authenticator_uid=dict(),
        state=dict(choices=["present", "absent", "exists", "enforced"], default="present"),
    )

    # Create a module for ourselves
    module = AAPModule(argument_spec=argument_spec, supports_check_mode=True)

    AAPUser(module).manage()


if __name__ == "__main__":
    main()
