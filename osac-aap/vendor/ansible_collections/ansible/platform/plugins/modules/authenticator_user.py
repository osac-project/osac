#!/usr/bin/python
# coding: utf-8 -*-

# (c) 2024, Ganesh Nalawade <@ganeshrn>
# GNU General Public License v3.0+ (see COPYING or https://www.gnu.org/licenses/gpl-3.0.txt)

from __future__ import absolute_import, division, print_function

__metaclass__ = type


DOCUMENTATION = """
---
module: authenticator_user
author: Ganesh Nalawade (@ganeshrn)
short_description: Manage user authenticators.
description:
    - Move an authenticator user to a new authenticator..
options:
    authenticator_user_id:
      description:
        - The authenticator user id of the user for which you want to update
          authenticators.
      required: True
      type: str
    authenticator:
      description:
        - The primary key of the authenticator to move the user to. The value
          of this primary key can be retrieved from the `authenticator_users`
          API endpoint.
      required: True
      type: str
    new_uid:
      description:
        - The new UID for the user, used to link the users login with their local
          AAP account. Should match the user identifier from the new authentication
          provider.
          For example, if a user being moved to a new GitHub authenticator, the new UID
          should be the user's GitHub login ID.
      type: str
    keep_memberships:
      description:
        - Decides if the RBAC memberships should be retained or dropped
          and let the authenticator map for the new authenticator manage it instead.
          If set to false it will remove the user's existing memberships and let the
          authenticator map for the new authenticator manage it instead.
      type: bool
      default: false
    merge_with_user:
      description:
        - The user id of another user that you want to merge with the
          current user.
      type: str
    merge_accounts_with_same_uid:
      description:
        - Automatically merge accounts with the same uid.
          Mutually exclusive with merge_with_user option.
      type: bool
      default: false
    remove_other_authenticators:
      description:
        - If true, delete any other authenticator_user entries the user has with other authenticators.
      type: bool
      default: false
    state:
      description:
        - Desired state of the resource.
        - Present state  C(present) will check if the authenticator user exists with the expected authenticator value
          and configure it if needed. If the authenticator user does not exist, it will be created.
        - Exists state  C(exists) will check if the item exists, and the related authenticator is correct.
          If not, the task will fail.
      choices: ["present", "exists"]
      default: "present"
      type: str

extends_documentation_fragment:
- ansible.platform.auth
"""


EXAMPLES = """
- name: Move authenticator users to a new authenticator and merge with another user
  ansible.platform.authenticator_user:
    authenticator_user_id: 9
    authenticator: 2
    new_uid: jdoe
    merge_with_user: 149
    state: present

- name: Merge user with same uid, remove other authenticators and keep memberships
  ansible.platform.authenticator_user:
    authenticator_user_id: 4
    authenticator: 1
    merge_accounts_with_same_uid: true
    keep_memberships: true
    remove_other_authenticators: true
    state: present

- name: Check if authenticator user exists
  ansible.platform.authenticator_user:
    authenticator_user_id: 4
    authenticator: 1
    state: exists
...
"""

from ..module_utils.aap_authenticator_users import AAPAuthenticatorUserMove  # noqa
from ..module_utils.aap_module import AAPModule  # noqa


def main():
    # Any additional arguments that are not fields of the item can be added here
    argument_spec = dict(
        authenticator_user_id=dict(required=True),
        authenticator=dict(required=True),
        new_uid=dict(),
        keep_memberships=dict(type="bool", default=False),
        merge_with_user=dict(),
        merge_accounts_with_same_uid=dict(type="bool", default=False),
        remove_other_authenticators=dict(type="bool", default=False),
        state=dict(default='present', choices=['present', 'exists']),
    )

    # Create a module for ourselves
    module = AAPModule(
        argument_spec=argument_spec,
        mutually_exclusive=[
            ('merge_with_user', 'merge_accounts_with_same_uid'),
        ],
    )
    AAPAuthenticatorUserMove(module).manage()


if __name__ == "__main__":
    main()
