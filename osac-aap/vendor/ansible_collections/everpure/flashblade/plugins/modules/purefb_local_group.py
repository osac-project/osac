#!/usr/bin/python
# -*- coding: utf-8 -*-

# (c) 2026, Simon Dodsley (simon@everpuredata.com)
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
module: purefb_local_group
version_added: '1.28.0'
short_description: Manage FlashBlade Local Directory Service groups
description:
- Create, update, rename or delete a local group in a FlashBlade Local
  Directory Service (LDS). These are the SMB/NFS local identities that live
  inside an LDS attached to a FlashBlade server.
- Requires FlashBlade REST API version 2.24 or later (Purity//FB 4.6.8+).
- User memberships can be managed inline via the I(local_users) option. If
  you also drive membership from the user side (via
  M(everpure.flashblade.purefb_local_user)), only manage a given (user,group)
  edge from one side in a single playbook run.
author:
- Pure Storage Ansible Team (@avk) <pure-ansible-team@everpuredata.com>
options:
  name:
    description:
    - Name of the local group inside the LDS.
    type: str
    required: true
  state:
    description:
    - Whether the local group should exist or not.
    type: str
    default: present
    choices: [ absent, present ]
  local_directory_service:
    description:
    - Name of the Local Directory Service that owns this group.
    - Attach LDSes to servers via M(everpure.flashblade.purefb_server).
    type: str
    required: true
  new_name:
    description:
    - New name for the group. Uses the C(LocalGroupPatch.name) field.
    - Rejected for built-in groups.
    type: str
  gid:
    description:
    - Numeric GID for the group. Auto-assigned by the array if omitted at
      create time. Patchable.
    type: int
  email:
    description:
    - Email address for the group.
    type: str
  local_users:
    description:
    - List of local-user names that should be members of this group.
    - When set, the module reconciles the group's user members against this
      list via C(/directory-services/local/groups/members) filtered to user
      members (nested-group and external members are unaffected).
    - The option is named I(local_users) rather than I(members) to reserve
      option space for future I(external_members) and I(group_members)
      without a breaking rename.
    type: list
    elements: str
  force:
    description:
    - Only used when I(state=absent).
    - When the group has members and I(force=false), the module fails with
      a clear message rather than surfacing the raw API error.
    - When I(force=true), the module removes all current memberships before
      deleting the group. Refused on built-in groups regardless.
    type: bool
    default: false
  context:
    description:
    - Name of fleet member on which to perform the operation.
    - This requires the array receiving the request is a member of a fleet
      and the context name to be a member of the same fleet.
    type: str
    default: ""
extends_documentation_fragment:
- everpure.flashblade.everpure.fb
"""

EXAMPLES = r"""
- name: Create local group 'developers'
  everpure.flashblade.purefb_local_group:
    name: developers
    local_directory_service: myserver_local
    gid: 6001
    email: developers@example.com
    fb_url: 10.10.10.2
    api_token: T-55a68eb5-c785-4720-a2ca-8b03903bf641

- name: Ensure 'developers' has these local user members
  everpure.flashblade.purefb_local_group:
    name: developers
    local_directory_service: myserver_local
    local_users:
      - alice
      - bob
    fb_url: 10.10.10.2
    api_token: T-55a68eb5-c785-4720-a2ca-8b03903bf641

- name: Rename local group
  everpure.flashblade.purefb_local_group:
    name: developers
    new_name: devs
    local_directory_service: myserver_local
    fb_url: 10.10.10.2
    api_token: T-55a68eb5-c785-4720-a2ca-8b03903bf641

- name: Delete an empty local group
  everpure.flashblade.purefb_local_group:
    name: devs
    local_directory_service: myserver_local
    state: absent
    fb_url: 10.10.10.2
    api_token: T-55a68eb5-c785-4720-a2ca-8b03903bf641

- name: Force-delete a group that still has members
  everpure.flashblade.purefb_local_group:
    name: devs
    local_directory_service: myserver_local
    state: absent
    force: true
    fb_url: 10.10.10.2
    api_token: T-55a68eb5-c785-4720-a2ca-8b03903bf641
"""

RETURN = r"""
"""


HAS_PYPURECLIENT = True
try:
    from pypureclient.flashblade import (
        LocalGroupPost,
        LocalGroupPatch,
        LocalGroupMembershipPost,
        LocalGroupMembershipPostMembers,
        ReferenceWritable,
    )
except ImportError:
    HAS_PYPURECLIENT = False


from ansible.module_utils.basic import AnsibleModule
from ansible_collections.everpure.flashblade.plugins.module_utils.purefb import (
    get_system,
    purefb_argument_spec,
)
from ansible_collections.everpure.flashblade.plugins.module_utils.common import (
    get_error_message,
    get_rest_api_version,
)
from ansible_collections.everpure.flashblade.plugins.module_utils.version import (
    LooseVersion,
)

MIN_REQUIRED_API_VERSION = "2.24"


def _context_kwargs(module):
    """Return context_names kwargs when a context is set.

    Local groups require REST 2.24, which is well above the 2.17 fleet-context
    floor, so context_names is always accepted here.
    """
    if module.params["context"]:
        return {"context_names": [module.params["context"]]}
    return {}


def _lds_filter(local_ds_name, extra=None):
    """Build a filter expression that scopes to the given LDS.

    The FB 2.24 GET endpoints for local groups / members do not accept
    ``local_directory_service_names``; scoping must go through ``filter``.
    """
    expr = "local_directory_service.name='{0}'".format(
        local_ds_name.replace("'", "\\'")
    )
    if extra:
        expr = "({0}) and ({1})".format(expr, extra)
    return expr


def _get_group(module, blade, local_ds_name):
    ctx = _context_kwargs(module)
    res = blade.get_directory_services_local_groups(
        names=[module.params["name"]],
        filter=_lds_filter(local_ds_name),
        **ctx,
    )
    if res.status_code == 200:
        items = list(res.items)
        if items:
            return items[0]
    return None


def _get_current_user_members(module, blade, local_ds_name, group_name=None):
    """Return the set of user-member names currently attached to this group."""
    ctx = _context_kwargs(module)
    res = blade.get_directory_services_local_groups_members(
        group_names=[group_name or module.params["name"]],
        filter=_lds_filter(local_ds_name),
        member_types=["User"],
        **ctx,
    )
    if res.status_code != 200:
        module.fail_json(
            msg="Failed to fetch memberships for group {0}. Error: {1}".format(
                module.params["name"], get_error_message(res)
            )
        )
    result = set()
    for edge in list(res.items):
        member = getattr(edge, "member", None)
        if member is not None:
            mname = getattr(member, "name", None)
            if mname:
                result.add(mname)
    return result


def _get_all_current_members(module, blade, local_ds_name):
    """Return the full list of member edges for this group (any type)."""
    ctx = _context_kwargs(module)
    res = blade.get_directory_services_local_groups_members(
        group_names=[module.params["name"]],
        filter=_lds_filter(local_ds_name),
        **ctx,
    )
    if res.status_code != 200:
        module.fail_json(
            msg="Failed to fetch memberships for group {0}. Error: {1}".format(
                module.params["name"], get_error_message(res)
            )
        )
    return list(res.items)


def _add_user_members(module, blade, local_ds_name, users, group_name):
    ctx = _context_kwargs(module)
    body = LocalGroupMembershipPost(
        members=[
            LocalGroupMembershipPostMembers(member=ReferenceWritable(name=u))
            for u in users
        ]
    )
    res = blade.post_directory_services_local_groups_members(
        local_membership=body,
        group_names=[group_name],
        local_directory_service_names=[local_ds_name],
        **ctx,
    )
    if res.status_code != 200:
        module.fail_json(
            msg=("Failed to add user members {0} to group {1}. Error: {2}").format(
                sorted(users), group_name, get_error_message(res)
            )
        )


def _remove_user_members(module, blade, local_ds_name, users, group_name):
    ctx = _context_kwargs(module)
    for u in users:
        res = blade.delete_directory_services_local_groups_members(
            group_names=[group_name],
            member_names=[u],
            member_types=["User"],
            local_directory_service_names=[local_ds_name],
            **ctx,
        )
        if res.status_code != 200:
            module.fail_json(
                msg=("Failed to remove user {0} from group {1}. Error: {2}").format(
                    u, group_name, get_error_message(res)
                )
            )


def _reconcile_user_members(module, blade, local_ds_name, group_name):
    """Reconcile user members against module.params['local_users']."""
    desired = set(module.params["local_users"] or [])
    current = _get_current_user_members(
        module, blade, local_ds_name, group_name=group_name
    )
    add = desired - current
    remove = current - desired
    changed = bool(add or remove)
    if changed and not module.check_mode:
        if add:
            _add_user_members(module, blade, local_ds_name, add, group_name)
        if remove:
            _remove_user_members(module, blade, local_ds_name, remove, group_name)
    return changed


def delete_group(module, blade, local_ds_name, group):
    if getattr(group, "built_in", False):
        module.fail_json(
            msg="Refusing to delete built-in local group {0}".format(
                module.params["name"]
            )
        )

    current_edges = _get_all_current_members(module, blade, local_ds_name)
    if current_edges:
        if not module.params["force"]:
            module.fail_json(
                msg=(
                    "Local group {0} has {1} member(s); "
                    "remove them or set force: true to auto-clear before delete."
                ).format(module.params["name"], len(current_edges))
            )
        if not module.check_mode:
            ctx = _context_kwargs(module)
            for edge in current_edges:
                member = getattr(edge, "member", None)
                mname = getattr(member, "name", None) if member else None
                if not mname:
                    continue
                mtype = getattr(member, "resource_type", None)
                delete_kwargs = {
                    "group_names": [module.params["name"]],
                    "member_names": [mname],
                    "local_directory_service_names": [local_ds_name],
                }
                if mtype:
                    # resource_type is a hint like 'local-users' or 'local-groups';
                    # translate to the member_types filter when we can.
                    if "user" in mtype:
                        delete_kwargs["member_types"] = ["User"]
                    elif "group" in mtype:
                        delete_kwargs["member_types"] = ["Group"]
                delete_kwargs.update(ctx)
                res = blade.delete_directory_services_local_groups_members(
                    **delete_kwargs
                )
                if res.status_code != 200:
                    module.fail_json(
                        msg=(
                            "Failed to clear member {0} from group {1}. Error: {2}"
                        ).format(mname, module.params["name"], get_error_message(res))
                    )

    changed = True
    if not module.check_mode:
        ctx = _context_kwargs(module)
        res = blade.delete_directory_services_local_groups(
            names=[module.params["name"]],
            local_directory_service_names=[local_ds_name],
            **ctx,
        )
        if res.status_code != 200:
            module.fail_json(
                msg="Failed to delete local group {0}. Error: {1}".format(
                    module.params["name"], get_error_message(res)
                )
            )
    module.exit_json(changed=changed)


def create_group(module, blade, local_ds_name):
    changed = True
    if not module.check_mode:
        body_kwargs = {}
        if module.params["email"] is not None:
            body_kwargs["email"] = module.params["email"]
        if module.params["gid"] is not None:
            body_kwargs["gid"] = module.params["gid"]

        ctx = _context_kwargs(module)
        res = blade.post_directory_services_local_groups(
            names=[module.params["name"]],
            local_directory_service_names=[local_ds_name],
            local_group=LocalGroupPost(**body_kwargs),
            **ctx,
        )
        if res.status_code != 200:
            module.fail_json(
                msg="Failed to create local group {0}. Error: {1}".format(
                    module.params["name"], get_error_message(res)
                )
            )

        if module.params["local_users"]:
            _reconcile_user_members(module, blade, local_ds_name, module.params["name"])
    module.exit_json(changed=changed)


def update_group(module, blade, local_ds_name, group):
    ctx = _context_kwargs(module)
    built_in = bool(getattr(group, "built_in", False))
    patch_kwargs = {}
    identity_changes = []

    if module.params["new_name"] and module.params["new_name"] != module.params["name"]:
        if built_in:
            module.fail_json(
                msg="Refusing to rename built-in local group {0}".format(
                    module.params["name"]
                )
            )
        clash = blade.get_directory_services_local_groups(
            names=[module.params["new_name"]],
            filter=_lds_filter(local_ds_name),
            **ctx,
        )
        if clash.status_code == 200 and list(clash.items):
            module.fail_json(
                msg=(
                    "Cannot rename local group {0} to {1}: target name already exists"
                ).format(module.params["name"], module.params["new_name"])
            )
        patch_kwargs["name"] = module.params["new_name"]
        identity_changes.append("name")

    if module.params["email"] is not None and module.params["email"] != getattr(
        group, "email", None
    ):
        patch_kwargs["email"] = module.params["email"]
        identity_changes.append("email")

    if module.params["gid"] is not None and module.params["gid"] != getattr(
        group, "gid", None
    ):
        patch_kwargs["gid"] = module.params["gid"]
        identity_changes.append("gid")

    if identity_changes and built_in:
        module.fail_json(
            msg="Refusing to modify built-in local group {0}".format(
                module.params["name"]
            )
        )

    patch_changed = bool(patch_kwargs)
    if patch_changed and not module.check_mode:
        res = blade.patch_directory_services_local_groups(
            names=[module.params["name"]],
            local_directory_service_names=[local_ds_name],
            local_group=LocalGroupPatch(**patch_kwargs),
            **ctx,
        )
        if res.status_code != 200:
            module.fail_json(
                msg="Failed to update local group {0}. Error: {1}".format(
                    module.params["name"], get_error_message(res)
                )
            )

    membership_changed = False
    if module.params["local_users"] is not None:
        # After a successful rename the group is now known by new_name; in
        # check mode the rename was skipped, so it keeps its current name.
        if module.check_mode:
            effective_name = module.params["name"]
        else:
            effective_name = patch_kwargs.get("name") or module.params["name"]
        membership_changed = _reconcile_user_members(
            module, blade, local_ds_name, effective_name
        )

    module.exit_json(changed=patch_changed or membership_changed)


def main():
    argument_spec = purefb_argument_spec()
    argument_spec.update(
        dict(
            name=dict(type="str", required=True),
            state=dict(type="str", default="present", choices=["absent", "present"]),
            local_directory_service=dict(type="str", required=True),
            new_name=dict(type="str"),
            gid=dict(type="int"),
            email=dict(type="str"),
            local_users=dict(type="list", elements="str"),
            force=dict(type="bool", default=False),
            context=dict(type="str", default=""),
        )
    )

    module = AnsibleModule(argument_spec, supports_check_mode=True)

    if not HAS_PYPURECLIENT:
        module.fail_json(msg="py-pure-client sdk is required for this module")

    blade = get_system(module)
    api_version = get_rest_api_version(blade)
    if LooseVersion(MIN_REQUIRED_API_VERSION) > LooseVersion(api_version):
        module.fail_json(
            msg=(
                "FlashBlade REST version not supported. "
                "Minimum version required: {0} (Purity//FB 4.6.8+)"
            ).format(MIN_REQUIRED_API_VERSION)
        )

    if not module.params["context"]:
        # If no context is provided set the context to the local array name
        fleet_res = blade.get_fleets()
        if fleet_res.status_code == 200 and list(fleet_res.items):
            module.params["context"] = list(blade.get_arrays().items)[0].name

    local_ds_name = module.params["local_directory_service"]
    group = _get_group(module, blade, local_ds_name)
    state = module.params["state"]

    if state == "absent" and group:
        delete_group(module, blade, local_ds_name, group)
    elif state == "absent":
        module.exit_json(changed=False)
    elif state == "present" and not group:
        create_group(module, blade, local_ds_name)
    else:
        update_group(module, blade, local_ds_name, group)


if __name__ == "__main__":
    main()
