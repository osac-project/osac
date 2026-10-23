#!/usr/bin/python
# -*- coding: utf-8 -*-

# Copyright: (c) 2024, Red Hat
# GNU General Public License v3.0+ (see COPYING or https://www.gnu.org/licenses/gpl-3.0.txt)

# You can consult the UI API documentation directly on a running private
# automation hub at https://hub.example.com/pulp/api/v3/docs/


DOCUMENTATION = """
---
module: team_roles
short_description: Assign roles to teams in private automation hub
description:
  - Assign roles to existing teams in private automation hub.
  - Teams and roles must already exist.
  - Uses the Hub UI v2 API for role assignments.
  - Requires private automation hub version 4.11 or later.
author:
  - Brian McLaughlin (@bmclaughlin)
options:
  team:
    description:
      - Name of the team to assign roles to.
      - The team must already exist.
    required: true
    type: str
  organization:
    description:
      - Name or ID of the organization containing the team.
      - Optional when the team name is unique.
      - Required when teams with the same name exist in multiple organizations.
    required: false
    type: str
  role:
    description:
      - Role name to assign to the team (e.g., galaxy.collection_namespace_owner).
      - The role must already exist.
    required: true
    type: str
  targets:
    description:
      - Resource targets to scope the role to.
      - Required for object-level roles.
      - When multiple namespaces are specified, a separate role assignment is created for each.
    type: dict
    suboptions:
      collection_namespaces:
        description:
          - List of collection namespaces to limit permissions to.
          - Each namespace will receive its own role assignment.
        type: list
        elements: str
  state:
    description:
      - If V(present), assigns the role to the team.
      - If V(absent), removes the role from the team.
    type: str
    default: present
    choices: [present, absent]
extends_documentation_fragment: ansible.hub.auth_ui
"""

EXAMPLES = """
- name: Assign namespace-scoped role to a team
  ansible.hub.team_roles:
    organization: my_org
    team: my_team
    role: galaxy.collection_namespace_owner
    targets:
      collection_namespaces:
        - my_namespace
    state: present
    ah_host: hub.example.com
    ah_username: admin
    ah_password: Sup3r53cr3t

- name: Assign role to a team for multiple namespaces
  ansible.hub.team_roles:
    team: my_team
    role: galaxy.collection_namespace_owner
    targets:
      collection_namespaces:
        - namespace_one
        - namespace_two
    state: present
    ah_host: hub.example.com
    ah_username: admin
    ah_password: Sup3r53cr3t

- name: Remove a role from a team
  ansible.hub.team_roles:
    team: my_team
    role: galaxy.collection_namespace_owner
    targets:
      collection_namespaces:
        - my_namespace
    state: absent
    ah_host: hub.example.com
    ah_username: admin
    ah_password: Sup3r53cr3t
"""

RETURN = """
team:
  description: The name of the team.
  type: str
  returned: always
role:
  description: The role that was assigned or removed.
  type: str
  returned: always
object_id:
  description:
    - The target object ID if a scoped role was used.
    - When multiple namespaces are specified, only the last processed object ID is returned.
  type: str
  returned: when targets are specified and a change was made
"""

from ..module_utils.ah_api_module import AHAPIModule, AHAPIModuleError


def get_team_id(module, team_name, organization=None):
    """Look up a team by name and optional organization, then return its ID."""
    query_params = {"name": team_name}
    if organization is not None:
        query_params["organization"] = organization
    url = module.build_ui_v2_url("teams", query_params=query_params)
    try:
        response = module.make_request("GET", url)
    except AHAPIModuleError as e:
        module.fail_json(msg="Error looking up team: {0}".format(e))

    if response["status_code"] != 200:
        module.fail_json(msg="Failed to look up team: {0}".format(response))

    matching_teams = []
    results = response["json"].get("results", [])
    for team in results:
        if team.get("name") != team_name:
            continue
        if organization is None:
            matching_teams.append(team)
            continue

        team_organization = team.get("organization")
        if team_organization is None:
            # The organization query already scopes the API response. Some
            # server versions omit the organization field from team results.
            matching_teams.append(team)
        elif isinstance(team_organization, dict):
            if str(organization) in (
                str(team_organization.get("id")),
                str(team_organization.get("name")),
            ):
                matching_teams.append(team)
        elif str(team_organization) == str(organization):
            matching_teams.append(team)

    if len(matching_teams) > 1:
        if organization is None:
            module.fail_json(
                msg=(
                    "Multiple teams named `{0}` were found. Specify the "
                    "`organization` parameter to select the intended team."
                ).format(team_name)
            )
        else:
            module.fail_json(
                msg=(
                    "Multiple teams named `{0}` matched organization `{1}`. "
                    "The API response was ambiguous."
                ).format(team_name, organization)
            )
        return None

    if organization is not None and matching_teams and matching_teams[0].get("organization") is None:
        module.fail_json(
            msg=(
                "Team `{0}` matched organization `{1}`, but the API response "
                "did not include organization metadata, so the team could not be verified."
            ).format(team_name, organization)
        )
        return None

    if matching_teams:
        return matching_teams[0].get("id")

    return None


def get_role_definition_id(module, role_name):
    """Look up role definition by name and return its ID."""
    url = module.build_ui_v2_url("role_definitions", query_params={"name": role_name})
    try:
        response = module.make_request("GET", url)
    except AHAPIModuleError as e:
        module.fail_json(msg="Error looking up role definition: {0}".format(e))

    if response["status_code"] != 200:
        module.fail_json(msg="Failed to look up role definition: {0}".format(response))

    results = response["json"].get("results", [])
    for role_def in results:
        if role_def.get("name") == role_name:
            return role_def.get("id")

    return None


def get_namespace_id(module, namespace_name):
    """Look up namespace by name and return its ID (uses v1 API)."""
    # Namespaces are in v1 API, not v2
    url = module.build_ui_url("namespaces", query_params={"name": namespace_name})
    try:
        response = module.make_request("GET", url)
    except AHAPIModuleError as e:
        module.fail_json(msg="Error looking up namespace: {0}".format(e))

    if response["status_code"] != 200:
        module.fail_json(msg="Failed to look up namespace: {0}".format(response))

    results = response["json"].get("data", [])
    for ns in results:
        if ns.get("name") == namespace_name:
            return ns.get("id")

    return None


def get_existing_assignment(module, team_id, role_definition_id, object_id):
    """Check if a role assignment already exists."""
    query_params = {
        "team": team_id,
        "role_definition": role_definition_id,
    }
    if object_id:
        query_params["object_id"] = str(object_id)

    url = module.build_ui_v2_url("role_team_assignments", query_params=query_params)
    try:
        response = module.make_request("GET", url)
    except AHAPIModuleError as e:
        module.fail_json(msg="Error checking existing assignments: {0}".format(e))

    if response["status_code"] != 200:
        module.fail_json(msg="Failed to check existing assignments: {0}".format(response))

    results = response["json"].get("results", [])
    for assignment in results:
        # Normalize object_id comparison - treat None and empty string as equivalent
        assignment_object_id = assignment.get("object_id")
        if assignment_object_id is None:
            assignment_object_id = ""
        if object_id is None:
            target_object_id = ""
        else:
            target_object_id = str(object_id)

        if (assignment.get("team") == team_id and
                assignment.get("role_definition") == role_definition_id and
                str(assignment_object_id) == target_object_id):
            return assignment

    return None


def create_assignment(module, team_id, role_definition_id, object_id):
    """Create a new role team assignment."""
    url = module.build_ui_v2_url("role_team_assignments")
    data = {
        "team": team_id,
        "role_definition": role_definition_id,
    }
    if object_id:
        data["object_id"] = str(object_id)

    try:
        response = module.make_request("POST", url, data=data)
    except AHAPIModuleError as e:
        module.fail_json(msg="Error creating role assignment: {0}".format(e))

    if response["status_code"] not in [200, 201]:
        error_msg = module.extract_error_msg(response)
        module.fail_json(msg="Failed to create role assignment: {0}".format(error_msg or response))

    return response["json"]


def delete_assignment(module, assignment_id):
    """Delete an existing role team assignment."""
    url = module.build_ui_v2_url("role_team_assignments/{0}".format(assignment_id))
    try:
        response = module.make_request("DELETE", url)
    except AHAPIModuleError as e:
        module.fail_json(msg="Error deleting role assignment: {0}".format(e))

    if response["status_code"] not in [200, 202, 204]:
        error_msg = module.extract_error_msg(response)
        module.fail_json(msg="Failed to delete role assignment: {0}".format(error_msg or response))

    return True


def main():
    argument_spec = dict(
        team=dict(type='str', required=True),
        organization=dict(type='str', required=False),
        role=dict(type='str', required=True),
        targets=dict(type='dict', default=None),
        state=dict(choices=["present", "absent"], default="present"),
    )

    # Create a module for ourselves
    module = AHAPIModule(argument_spec=argument_spec, supports_check_mode=True)

    # Check server version - this module requires 4.11 or later
    vers = module.get_server_version()
    if vers < "4.11":
        module.fail_json(
            msg="This module requires private automation hub version 4.11 or later. Your version is {vers}".format(vers=vers)
        )

    # Extract our parameters
    team_name = module.params.get("team")
    organization = module.params.get("organization")
    role_name = module.params.get("role")
    targets = module.params.get("targets")
    state = module.params.get("state")

    # Look up the team
    team_id = get_team_id(module, team_name, organization)
    if team_id is None:
        if organization is None:
            module.fail_json(msg="Team `{0}` was not found".format(team_name))
        module.fail_json(
            msg="Team `{0}` was not found in organization `{1}`".format(team_name, organization)
        )

    # Look up the role definition
    role_definition_id = get_role_definition_id(module, role_name)
    if role_definition_id is None:
        module.fail_json(msg="Role `{0}` was not found".format(role_name))

    # Initialize output
    module.json_output = {
        'changed': False,
        'team': team_name,
        'role': role_name,
    }

    # Process targets
    object_ids = []
    if targets and targets.get("collection_namespaces"):
        for namespace_name in targets["collection_namespaces"]:
            ns_id = get_namespace_id(module, namespace_name)
            if ns_id is None:
                module.fail_json(msg="Namespace `{0}` was not found".format(namespace_name))
            object_ids.append(ns_id)
    else:
        # No targets means global/org-level assignment (object_id = None)
        object_ids.append(None)

    # Process each object_id
    for object_id in object_ids:
        # Check if assignment already exists
        existing = get_existing_assignment(module, team_id, role_definition_id, object_id)

        if state == 'present':
            if existing is None:
                # Create the assignment
                if not module.check_mode:
                    create_assignment(module, team_id, role_definition_id, object_id)
                module.json_output['changed'] = True
                if object_id:
                    module.json_output['object_id'] = str(object_id)
        elif state == 'absent':
            if existing is not None:
                # Delete the assignment
                if not module.check_mode:
                    delete_assignment(module, existing['id'])
                module.json_output['changed'] = True
                if object_id:
                    module.json_output['object_id'] = str(object_id)

    module.exit_json(**module.json_output)


if __name__ == "__main__":
    main()
