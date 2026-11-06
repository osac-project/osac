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
module: purefb_local_user
version_added: '1.28.0'
short_description: Manage FlashBlade Local Directory Service users
description:
- Create, update, rename or delete a local user in a FlashBlade Local
  Directory Service (LDS). These are the SMB/NFS local identities that live
  inside an LDS attached to a FlashBlade server. They are not the array
  management admins - use M(everpure.flashblade.purefb_user) or
  M(everpure.flashblade.purefb_admin) for those.
- Requires FlashBlade REST API version 2.24 or later (Purity//FB 4.6.8+).
- Group memberships can be managed inline via the I(groups) option. If you
  also drive membership from the group side (via
  M(everpure.flashblade.purefb_local_group)), only manage a given
  (user,group) edge from one side in a single playbook run.
author:
- Pure Storage Ansible Team (@avk) <pure-ansible-team@everpuredata.com>
options:
  name:
    description:
    - Name of the local user inside the LDS.
    type: str
    required: true
  state:
    description:
    - Whether the local user should exist or not.
    type: str
    default: present
    choices: [ absent, present ]
  local_directory_service:
    description:
    - Name of the Local Directory Service that owns this user.
    - Attach LDSes to servers via M(everpure.flashblade.purefb_server).
    type: str
    required: true
  new_name:
    description:
    - New name for the user. Uses the C(LocalUserPatch.name) field.
    - Rejected for built-in users.
    type: str
  uid:
    description:
    - Numeric UID for the user. Auto-assigned by the array if omitted at
      create time. Patchable.
    type: int
  email:
    description:
    - Email address for the user.
    type: str
  enabled:
    description:
    - Whether the user account is enabled.
    - At create time, if unset the user is created enabled.
    - On update, this field is only applied when explicitly set. This lets
      you idempotently manage other attributes of a disabled user without
      accidentally re-enabling the account.
    type: bool
  password:
    description:
    - Password for the user.
    - Required at create time when I(enabled=true).
    - Required on the transition of an existing user from
      I(enabled=false) to I(enabled=true).
    - Otherwise ignored unless I(force_password_reset=true).
    type: str
  force_password_reset:
    description:
    - Force the password to be reset on an existing user, even if unchanged.
    - There is no way to compare the current password on the array, so
      password change is inherently non-idempotent. Set this true to
      explicitly ask for a reset.
    type: bool
    default: false
  primary_group:
    description:
    - Name of the local group that is the user's primary group.
    - Required when I(state=present) - the array will not create a user
      without one.
    - The primary group must already exist in the same LDS.
    type: str
  groups:
    description:
    - List of non-primary local group names the user should belong to.
    - When set, the module reconciles the user's group memberships against
      this list via C(/directory-services/local/users/members).
    - The primary group (either the current one or a newly requested one) is
      never removed via this list - the API disallows removing the primary
      group edge.
    - After a primary-group change the former primary remains a regular
      member; if it is not listed here, a second run removes it.
    type: list
    elements: str
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
- name: Create local user 'alice'
  everpure.flashblade.purefb_local_user:
    name: alice
    local_directory_service: myserver_local
    uid: 5001
    password: "s3cret!"
    primary_group: alice
    email: alice@example.com
    fb_url: 10.10.10.2
    api_token: T-55a68eb5-c785-4720-a2ca-8b03903bf641

- name: Ensure 'alice' also belongs to secondary groups
  everpure.flashblade.purefb_local_user:
    name: alice
    local_directory_service: myserver_local
    primary_group: alice
    groups:
      - developers
      - infra
    fb_url: 10.10.10.2
    api_token: T-55a68eb5-c785-4720-a2ca-8b03903bf641

- name: Disable local user
  everpure.flashblade.purefb_local_user:
    name: alice
    local_directory_service: myserver_local
    primary_group: alice
    enabled: false
    fb_url: 10.10.10.2
    api_token: T-55a68eb5-c785-4720-a2ca-8b03903bf641

- name: Rename local user
  everpure.flashblade.purefb_local_user:
    name: alice
    new_name: alice2
    local_directory_service: myserver_local
    primary_group: alice
    fb_url: 10.10.10.2
    api_token: T-55a68eb5-c785-4720-a2ca-8b03903bf641

- name: Delete local user
  everpure.flashblade.purefb_local_user:
    name: alice
    local_directory_service: myserver_local
    state: absent
    fb_url: 10.10.10.2
    api_token: T-55a68eb5-c785-4720-a2ca-8b03903bf641
"""

RETURN = r"""
"""


HAS_PYPURECLIENT = True
try:
    from pypureclient.flashblade import (
        LocalUserPost,
        LocalUserPatch,
        LocalUserMembershipPost,
        LocalUserMembershipPostGroups,
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

    Local users require REST 2.24, which is well above the 2.17 fleet-context
    floor, so context_names is always accepted here.
    """
    if module.params["context"]:
        return {"context_names": [module.params["context"]]}
    return {}


def _lds_filter(local_ds_name, extra=None):
    """Build a filter expression that scopes to the given LDS.

    The FB GET endpoints for local users / members do not accept
    ``local_directory_service_names``; scoping must go through ``filter``.
    """
    # Per Pure's REST filter grammar, string literals are single-quoted and
    # the backslash is the escape character: escape backslashes first, then
    # single quotes.
    escaped = local_ds_name.replace("\\", "\\\\").replace("'", "\\'")

    expr = "local_directory_service.name='{0}'".format(escaped)
    if extra:
        expr = "({0}) and ({1})".format(expr, extra)
    return expr


def _get_user(module, blade, local_ds_name):
    ctx = _context_kwargs(module)
    res = blade.get_directory_services_local_users(
        names=[module.params["name"]],
        filter=_lds_filter(local_ds_name),
        **ctx,
    )
    if res.status_code == 200:
        items = list(res.items)
        if items:
            return items[0]
    return None


def _get_current_groups(module, blade, local_ds_name):
    """Return the set of current non-primary group names for this user."""
    ctx = _context_kwargs(module)
    res = blade.get_directory_services_local_users_members(
        member_names=[module.params["name"]],
        filter=_lds_filter(local_ds_name),
        **ctx,
    )
    if res.status_code != 200:
        module.fail_json(
            msg="Failed to fetch memberships for user {0}. Error: {1}".format(
                module.params["name"], get_error_message(res)
            )
        )
    result = set()
    for edge in list(res.items):
        group = getattr(edge, "group", None)
        if group is not None:
            gname = getattr(group, "name", None)
            if gname:
                result.add(gname)
    return result


def _add_memberships(module, blade, local_ds_name, groups):
    ctx = _context_kwargs(module)
    body = LocalUserMembershipPost(
        groups=[
            LocalUserMembershipPostGroups(group=ReferenceWritable(name=g))
            for g in groups
        ]
    )
    res = blade.post_directory_services_local_users_members(
        local_membership=body,
        member_names=[module.params["name"]],
        local_directory_service_names=[local_ds_name],
        **ctx,
    )
    if res.status_code != 200:
        module.fail_json(
            msg="Failed to add group memberships {0} for user {1}. Error: {2}".format(
                sorted(groups),
                module.params["name"],
                get_error_message(res),
            )
        )


def _remove_memberships(module, blade, local_ds_name, groups):
    ctx = _context_kwargs(module)
    for g in groups:
        res = blade.delete_directory_services_local_users_members(
            member_names=[module.params["name"]],
            group_names=[g],
            local_directory_service_names=[local_ds_name],
            **ctx,
        )
        if res.status_code != 200:
            module.fail_json(
                msg=("Failed to remove user {0} from group {1}. Error: {2}").format(
                    module.params["name"], g, get_error_message(res)
                )
            )


def _reconcile_memberships(
    module, blade, local_ds_name, desired_primary_group, current_primary_group=None
):
    """Reconcile the user's non-primary group set against module.params['groups'].

    Both the desired and the current primary group are excluded from removal:
    the API disallows deleting the primary-group edge, and until the PATCH has
    run the current primary is still in force.
    """
    desired = set(module.params["groups"] or [])
    current = _get_current_groups(module, blade, local_ds_name)

    exclude = set()
    if desired_primary_group:
        exclude.add(desired_primary_group)
    if current_primary_group:
        exclude.add(current_primary_group)
    desired = desired - exclude
    current_diff = current - exclude

    add = desired - current_diff
    remove = current_diff - desired
    changed = bool(add or remove)
    if changed and not module.check_mode:
        if add:
            _add_memberships(module, blade, local_ds_name, add)
        if remove:
            _remove_memberships(module, blade, local_ds_name, remove)
    return changed


def delete_user(module, blade, local_ds_name, user):
    if getattr(user, "built_in", False):
        module.fail_json(
            msg="Refusing to delete built-in local user {0}".format(
                module.params["name"]
            )
        )
    changed = True
    if not module.check_mode:
        ctx = _context_kwargs(module)
        res = blade.delete_directory_services_local_users(
            names=[module.params["name"]],
            local_directory_service_names=[local_ds_name],
            **ctx,
        )
        if res.status_code != 200:
            module.fail_json(
                msg="Failed to delete local user {0}. Error: {1}".format(
                    module.params["name"], get_error_message(res)
                )
            )
    module.exit_json(changed=changed)


def create_user(module, blade, local_ds_name):
    # enabled is not defaulted in the argument spec so update paths stay
    # idempotent; at create time an unset value means "enabled".
    enabled = module.params["enabled"] if module.params["enabled"] is not None else True
    if enabled and not module.params["password"]:
        module.fail_json(
            msg="'password' is required when creating an enabled local user"
        )
    changed = True
    if not module.check_mode:
        body_kwargs = {}
        if module.params["email"] is not None:
            body_kwargs["email"] = module.params["email"]
        body_kwargs["enabled"] = enabled
        if module.params["password"]:
            body_kwargs["password"] = module.params["password"]
        body_kwargs["primary_group"] = ReferenceWritable(
            name=module.params["primary_group"]
        )
        if module.params["uid"] is not None:
            body_kwargs["uid"] = module.params["uid"]

        ctx = _context_kwargs(module)
        res = blade.post_directory_services_local_users(
            names=[module.params["name"]],
            local_directory_service_names=[local_ds_name],
            local_user=LocalUserPost(**body_kwargs),
            **ctx,
        )
        if res.status_code != 200:
            module.fail_json(
                msg="Failed to create local user {0}. Error: {1}".format(
                    module.params["name"], get_error_message(res)
                )
            )

        if module.params["groups"]:
            _reconcile_memberships(
                module,
                blade,
                local_ds_name,
                module.params["primary_group"],
            )
    module.exit_json(changed=changed)


def update_user(module, blade, local_ds_name, user):
    ctx = _context_kwargs(module)
    built_in = bool(getattr(user, "built_in", False))
    current_pg = getattr(user, "primary_group", None)
    current_pg_name = getattr(current_pg, "name", None) if current_pg else None

    patch_kwargs = {}
    identity_changes = []

    if module.params["new_name"] and module.params["new_name"] != module.params["name"]:
        if built_in:
            module.fail_json(
                msg="Refusing to rename built-in local user {0}".format(
                    module.params["name"]
                )
            )
        # Refuse if target name already exists in this LDS.
        name_conflict_check = blade.get_directory_services_local_users(
            names=[module.params["new_name"]],
            filter=_lds_filter(local_ds_name),
            **ctx,
        )
        if name_conflict_check.status_code == 200 and list(name_conflict_check.items):
            module.fail_json(
                msg=(
                    "Cannot rename local user {0} to {1}: target name already exists"
                ).format(module.params["name"], module.params["new_name"])
            )
        patch_kwargs["name"] = module.params["new_name"]
        identity_changes.append("name")

    if module.params["email"] is not None and module.params["email"] != getattr(
        user, "email", None
    ):
        patch_kwargs["email"] = module.params["email"]
        identity_changes.append("email")

    if module.params["enabled"] is not None and module.params["enabled"] != getattr(
        user, "enabled", None
    ):
        # false -> true transition needs a password.
        if module.params["enabled"] and not getattr(user, "enabled", False):
            if not module.params["password"]:
                module.fail_json(
                    msg=(
                        "'password' is required to enable local user {0} "
                        "(re-enabling a disabled account)"
                    ).format(module.params["name"])
                )
        patch_kwargs["enabled"] = module.params["enabled"]
        identity_changes.append("enabled")

    if module.params["uid"] is not None and module.params["uid"] != getattr(
        user, "uid", None
    ):
        patch_kwargs["uid"] = module.params["uid"]
        identity_changes.append("uid")

    desired_pg_name = module.params["primary_group"]
    pg_changed = False
    if desired_pg_name != current_pg_name:
        patch_kwargs["primary_group"] = ReferenceWritable(name=desired_pg_name)
        identity_changes.append("primary_group")
        pg_changed = True

    if module.params["force_password_reset"]:
        if not module.params["password"]:
            module.fail_json(
                msg=("'password' is required when 'force_password_reset' is true")
            )
        patch_kwargs["password"] = module.params["password"]
        identity_changes.append("password")
    elif (
        "password" not in patch_kwargs
        and patch_kwargs.get("enabled") is True
        and not getattr(user, "enabled", False)
    ):
        # Re-enable transition: include the password once we know it exists.
        patch_kwargs["password"] = module.params["password"]

    if identity_changes and built_in:
        # Rename is already blocked above. Block other identity mutations too;
        # let the array reject anything we didn't foresee, but stop obvious
        # mutations up-front with a clear message.
        module.fail_json(
            msg="Refusing to modify built-in local user {0}".format(
                module.params["name"]
            )
        )

    membership_changed = False
    # Order-of-ops guard: reconcile memberships BEFORE PATCHing primary_group
    # to dodge the known array-side regression where PATCHing primary_group
    # with stale membership edges present has returned 500 on some releases.
    if module.params["groups"] is not None and pg_changed:
        membership_changed = _reconcile_memberships(
            module, blade, local_ds_name, desired_pg_name, current_pg_name
        )

    patch_changed = bool(patch_kwargs)
    if patch_changed and not module.check_mode:
        res = blade.patch_directory_services_local_users(
            names=[module.params["name"]],
            local_directory_service_names=[local_ds_name],
            local_user=LocalUserPatch(**patch_kwargs),
            **ctx,
        )
        if res.status_code != 200:
            module.fail_json(
                msg="Failed to update local user {0}. Error: {1}".format(
                    module.params["name"], get_error_message(res)
                )
            )

    # If we didn't reconcile earlier (no primary-group change), reconcile now.
    if module.params["groups"] is not None and not pg_changed:
        # After a rename the user is known by new_name, and reconciliation
        # uses module.params['name'] - but in check mode the rename was
        # skipped, so the user still answers to the current name.
        if patch_kwargs.get("name") and not module.check_mode:
            module.params["name"] = patch_kwargs["name"]
        membership_changed = _reconcile_memberships(
            module, blade, local_ds_name, desired_pg_name, current_pg_name
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
            uid=dict(type="int"),
            email=dict(type="str"),
            enabled=dict(type="bool"),
            password=dict(type="str", no_log=True),
            force_password_reset=dict(type="bool", default=False, no_log=False),
            primary_group=dict(type="str"),
            groups=dict(type="list", elements="str"),
            context=dict(type="str", default=""),
        )
    )

    module = AnsibleModule(
        argument_spec,
        required_if=[("state", "present", ["primary_group"])],
        supports_check_mode=True,
    )

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
    user = _get_user(module, blade, local_ds_name)
    state = module.params["state"]

    if state == "absent" and user:
        delete_user(module, blade, local_ds_name, user)
    elif state == "absent":
        module.exit_json(changed=False)
    elif state == "present" and not user:
        create_user(module, blade, local_ds_name)
    else:
        update_user(module, blade, local_ds_name, user)


if __name__ == "__main__":
    main()
