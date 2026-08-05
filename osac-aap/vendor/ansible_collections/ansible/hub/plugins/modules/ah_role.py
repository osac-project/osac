#!/usr/bin/python
# -*- coding: utf-8 -*-

# Copyright: (c) 2020, Sean Sullivan <@sean-m-sullivan>
# GNU General Public License v3.0+ (see COPYING or https://www.gnu.org/licenses/gpl-3.0.txt)

# You can consult the UI API documentation directly on a running private
# automation hub at https://hub.example.com/pulp/api/v3/docs/

from __future__ import absolute_import, division, print_function

__metaclass__ = type


DOCUMENTATION = r"""
---
module: ah_role
short_description: Manage a role of group permissions
description:
  - Manage a role of group permissions.
author:
  - Sean Sullivan (@sean-m-sullivan)
options:
  name:
    description:
      - Name of the role.
      - Must start with 'galaxy.'.
    required: true
    type: str
  description:
    description:
      - Description to use for the role.
    type: str
  perms:
    description:
      - The list of permissions to add to or remove from the given group.
      - The module accepts the following roles.
      - For user management, V(add_user), V(change_user), V(delete_user), and V(view_user).
      - For group management, V(add_group), V(change_group), V(delete_group), and V(view_group).
      - For collection namespace management, V(add_namespace), V(change_namespace), V(upload_to_namespace), and V(delete_namespace).
      - For collection content management, V(modify_ansible_repo_content), V(delete_collection), and V(sign_ansiblerepository).
      - For remote repository configuration, V(change_collectionremote), V(view_collectionremote),
        V(add_collectionremote), V(delete_collectionremote), and V(manage_roles_collectionremote).
      - For Ansible Repository management, only with private automation hub v4.7.0
        V(add_ansiblerepository), V(change_ansiblerepository), V(delete_ansiblerepository), V(manage_roles_ansiblerepository),
        V(repair_ansiblerepository), V(view_ansiblerepository),
      - For container image management, only with private automation hub v4.3.2 or later,
        V(change_containernamespace_perms), V(change_container), V(change_image_tag), V(create_container),
        Push existing container V(push_container), V(namespace_add_containerdistribution), V(manage_roles_containernamespace),
        and V(delete_containerrepository).
      - For remote registry management, V(add_containerregistryremote), V(change_containerregistryremote), and V(delete_containerregistryremote).
      - For task management, V(change_task), V(view_task), and V(delete_task).
      - You can also grant or revoke all permissions with V(*) or V(all).
    type: list
    elements: str
  state:
    description:
      - If V(absent), then the module removes the listed permissions from the
        group. If the group has permissions that are not listed in O(perms),
        then the module does not remove those pre-existing permissions.
      - If V(present), then the module adds the listed permissions to the group. The module does not remove the permissions that the group already has.
    type: str
    default: present
    choices: [absent, present]
notes:
  - Supports V(check_mode).
  - This module only works up to Automation Hub version 4.6 (AAP 2.3).
extends_documentation_fragment: ansible.hub.auth_ui
"""


EXAMPLES = r"""
- name: Ensure the operators have the correct permissions to manage users
  ansible.hub.ah_role:
    name: galaxy.operators
    perms:
      - add_user
      - change_user
      - delete_user
      - view_user
    state: present
    ah_host: hub.example.com
    ah_username: admin
    ah_password: Sup3r53cr3t
  no_log: True

- name: Ensure the administrators have all the permissions
  ansible.hub.ah_role:
    name: galaxy.administrators
    perms: "*"
    state: present
    ah_host: hub.example.com
    ah_username: admin
    ah_password: Sup3r53cr3t
  no_log: True

- name: Ensure the developers cannot manage groups nor users
  ansible.hub.ah_role:
    name: galaxy.developers
    perms:
      - add_user
      - change_user
      - delete_user
      - add_group
      - change_group
      - delete_group
    state: absent
    ah_host: hub.example.com
    ah_username: admin
    ah_password: Sup3r53cr3t
  no_log: True
"""

RETURN = r""" # """

from ..module_utils.ah_api_module import AHAPIModule
from ..module_utils.ah_pulp_object import AHPulpRolePerm

# Mapping between the permission names that the user provides in perms and the
# private automation hub internal names.
FRIENDLY_PERM_NAMES = {
    "*": "all",
    # Namespaces
    "add_namespace": "galaxy.add_namespace",
    "change_namespace": "galaxy.change_namespace",
    "upload_to_namespace": "galaxy.upload_to_namespace",
    "delete_namespace": "galaxy.delete_namespace",
    # Collections
    "modify_ansible_repo_content": "ansible.modify_ansible_repo_content",
    "delete_collection": "ansible.delete_collection",
    "sign_ansiblerepository": "ansible.sign_ansiblerepository",
    # Users
    "add_user": "galaxy.add_user",
    "change_user": "galaxy.change_user",
    "delete_user": "galaxy.delete_user",
    "view_user": "galaxy.view_user",
    # Groups
    "add_group": "galaxy.add_group",
    "change_group": "galaxy.change_group",
    "delete_group": "galaxy.delete_group",
    "view_group": "galaxy.view_group",
    # Remotes (Collections)
    "change_collectionremote": "ansible.change_collectionremote",
    "view_collectionremote": "ansible.view_collectionremote",
    "add_collectionremote": "ansible.add_collectionremote",
    "delete_collectionremote": "ansible.delete_collectionremote",
    "manage_roles_collectionremote": "ansible.manage_roles_collectionremote",
    # Ansible Repository (Collections)
    "add_ansiblerepository": "ansible.add_ansiblerepository",
    "change_ansiblerepository": "ansible.change_ansiblerepository",
    "delete_ansiblerepository": "ansible.delete_ansiblerepository",
    "manage_roles_ansiblerepository": "ansible.manage_roles_ansiblerepository",
    "repair_ansiblerepository": "ansible.repair_ansiblerepository",
    "view_ansiblerepository": "ansible.view_ansiblerepository",
    # Containers
    "change_containernamespace_perms": "container.change_containernamespace",
    "change_container": "container.namespace_change_containerdistribution",
    "change_image_tag": "container.namespace_modify_content_containerpushrepository",
    "create_container": "container.add_containernamespace",
    "push_container": "container.namespace_push_containerdistribution",
    "namespace_add_containerdistribution": "container.namespace_add_containerdistribution",
    "manage_roles_containernamespace": "container.manage_roles_containernamespace",
    "delete_containerrepository": "container.delete_containerrepository",
    # Remote Registries
    "add_containerregistryremote": "galaxy.add_containerregistryremote",
    "change_containerregistryremote": "galaxy.change_containerregistryremote",
    "delete_containerregistryremote": "galaxy.delete_containerregistryremote",
    # Tasks
    "change_task": "core.change_task",
    "delete_task": "core.delete_task",
    "view_task": "core.view_task",
}


def main():
    argument_spec = dict(
        name=dict(required=True),
        description=dict(),
        perms=dict(type="list", elements="str"),
        state=dict(choices=["present", "absent"], default="present"),
    )

    # Create a module for ourselves
    module = AHAPIModule(argument_spec=argument_spec, supports_check_mode=True)

    # Extract our parameters
    name = module.params.get("name")
    description = module.params.get("description")
    perms = module.params.get("perms")
    state = module.params.get("state")

    # Authenticate
    module.authenticate()

    # Only recent versions support execution environment
    vers = module.get_server_version()
    if vers < "4.6":
        module.fail_json(msg="This module requires private automation hub version 4.6 or later. Your version is {vers}".format(vers=vers))

    # Process the object from the Pulp API (delete or create)
    role_pulp = AHPulpRolePerm(module)
    role_pulp.get_object(name)

    # Remove the role
    if state == "absent":
        role_pulp.delete()

    # Convert the given permission list to a list of internal names
    role_perms = []
    perm_ah_names = [v for v in FRIENDLY_PERM_NAMES.values() if v != "all"]
    if perms is not None:
        for perm in perms:
            if perm == "*" or perm == "all":
                role_perms = perm_ah_names
                break
            if perm in FRIENDLY_PERM_NAMES:
                role_perms.append(FRIENDLY_PERM_NAMES[perm])
            elif perm in perm_ah_names:
                role_perms.append(perm)
            else:
                module.fail_json(msg="Unknown perm ({perm}) defined".format(perm=perm))
    else:
        module.fail_json(msg="Permsions are required if not using state absent.")

    if not role_pulp.exists:
        role_data = {
            'name': name,
            'description': description,
            'permissions': role_perms,
        }
        role_pulp.create(role_data)
    else:
        role_data = {
            'name': name,
            'description': description,
            'permissions': role_perms,
        }
        role_pulp.update(role_data)


if __name__ == "__main__":
    main()
