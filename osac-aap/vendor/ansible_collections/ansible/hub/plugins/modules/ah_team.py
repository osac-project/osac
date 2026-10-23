#!/usr/bin/python
# -*- coding: utf-8 -*-

# Copyright: (c) 2024, Red Hat
# GNU General Public License v3.0+ (see COPYING or https://www.gnu.org/licenses/gpl-3.0.txt)

# You can consult the UI API documentation directly on a running private
# automation hub at https://hub.example.com/pulp/api/v3/docs/


DOCUMENTATION = r"""
---
module: ah_team
short_description: Manage teams in private automation hub
description:
  - Create and delete teams in private automation hub.
  - Uses the Hub UI v2 API for team management.
  - Requires private automation hub version 4.11 or later.
author:
  - Brian McLaughlin (@bmclaughlin)
options:
  name:
    description:
      - Name of the team to create or delete.
    required: true
    type: str
  state:
    description:
      - If V(absent), then the module deletes the team.
      - The module does not fail if the team does not exist because the state is already as expected.
      - If V(present), then the module creates the team if it does not already exist.
    type: str
    default: present
    choices: [absent, present]
seealso:
  - module: ansible.hub.ah_group
  - module: ansible.hub.team_roles
notes:
  - Supports C(check_mode).
  - This module is for private automation hub version 4.11 or later.
  - For earlier versions, use M(ansible.hub.ah_group) instead.
extends_documentation_fragment: ansible.hub.auth_ui
"""

EXAMPLES = r"""
- name: Ensure the team exists
  ansible.hub.ah_team:
    name: my_team
    state: present
    ah_host: hub.example.com
    ah_username: admin
    ah_password: Sup3r53cr3t

- name: Ensure the team is removed
  ansible.hub.ah_team:
    name: my_team
    state: absent
    ah_host: hub.example.com
    ah_username: admin
    ah_password: Sup3r53cr3t
"""

RETURN = r"""
name:
  description: The name of the team.
  type: str
  returned: always
id:
  description: The ID of the team.
  type: int
  returned: when state is present
"""

from ..module_utils.ah_api_module import AHAPIModule, AHAPIModuleError


def get_team(module, team_name):
    """Look up team by name and return full team object."""
    url = module.build_ui_v2_url("teams", query_params={"name": team_name})
    try:
        response = module.make_request("GET", url)
    except AHAPIModuleError as e:
        module.fail_json(msg="Error looking up team: {0}".format(e))

    if response["status_code"] != 200:
        module.fail_json(msg="Failed to look up team: {0}".format(response))

    results = response["json"].get("results", [])
    for team in results:
        if team.get("name") == team_name:
            return team

    return None


def create_team(module, team_name):
    """Create a new team."""
    url = module.build_ui_v2_url("teams")
    data = {"name": team_name}

    try:
        response = module.make_request("POST", url, data=data)
    except AHAPIModuleError as e:
        module.fail_json(msg="Error creating team: {0}".format(e))

    if response["status_code"] not in [200, 201]:
        error_msg = module.extract_error_msg(response)
        module.fail_json(msg="Failed to create team: {0}".format(error_msg or response))

    return response["json"]


def delete_team(module, team_id):
    """Delete an existing team."""
    url = module.build_ui_v2_url("teams/{0}".format(team_id))
    try:
        response = module.make_request("DELETE", url)
    except AHAPIModuleError as e:
        module.fail_json(msg="Error deleting team: {0}".format(e))

    if response["status_code"] not in [200, 202, 204]:
        error_msg = module.extract_error_msg(response)
        module.fail_json(msg="Failed to delete team: {0}".format(error_msg or response))

    return True


def main():
    argument_spec = dict(
        name=dict(required=True),
        state=dict(choices=["present", "absent"], default="present"),
    )

    # Create a module for ourselves
    module = AHAPIModule(argument_spec=argument_spec, supports_check_mode=True)

    # Extract our parameters
    name = module.params.get("name")
    state = module.params.get("state")

    # Authenticate
    module.authenticate()

    # Check server version - this module requires 4.11 or later
    vers = module.get_server_version()
    if vers < "4.11":
        module.fail_json(
            msg="This module requires private automation hub version 4.11 or later. "
                "Your version is {vers}. Use ah_group module instead.".format(vers=vers)
        )

    # Initialize output
    module.json_output = {
        'changed': False,
        'name': name,
    }

    # Look up the team
    existing_team = get_team(module, name)

    if state == 'absent':
        if existing_team is not None:
            # Delete the team
            if not module.check_mode:
                delete_team(module, existing_team['id'])
            module.json_output['changed'] = True
    else:  # state == 'present'
        if existing_team is None:
            # Create the team
            if not module.check_mode:
                new_team = create_team(module, name)
                module.json_output['id'] = new_team.get('id')
            module.json_output['changed'] = True
        else:
            module.json_output['id'] = existing_team.get('id')

    module.exit_json(**module.json_output)


if __name__ == "__main__":
    main()
