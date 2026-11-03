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
module: purefb_mgmt_auth_policy
version_added: '1.28.0'
short_description: Manage FlashBlade Management Authentication Policies and their members
description:
- Create, update, or delete a FlashBlade Management Authentication Policy.
- Management authentication policies define which authentication factors
  (password, SSH key/certificate/FIDO2 key) admins must or may present.
  Currently the SSH interface is supported.
- Reconcile the declarative list of members (admins and/or the array) the
  policy is attached to.
- A member can be attached to at most one management authentication policy
  at a time; the array does not move members between policies implicitly.
author:
- Pure Storage Ansible Team (@avk) <pure-ansible-team@everpuredata.com>
notes:
- Changing an attached policy affects subsequent SSH logins for its members
  (existing sessions are unaffected). Misconfiguration can lock
  administrators out of SSH access - review changes with check mode first.
- The built-in C(default-authentication-policy) is not protected by the
  array; it can be modified or deleted like any other policy and is the
  fallback for members without an explicit policy. Change it with care.
- Moving a member between policies (I(replace_existing=true)) is not
  atomic on the array; the member is briefly unattached during the move,
  and if the move fails midway it is left without a policy until the
  task is re-run.
- Deletion safety is enforced by this module, not by the array - the
  array will delete a policy that still has members attached, silently
  detaching them.
- When I(state=absent), configuration and membership options are ignored.
options:
  name:
    description:
    - Name of the management authentication policy.
    type: str
    required: true
  state:
    description:
    - Whether the policy should exist.
    default: present
    choices: [ absent, present ]
    type: str
  enabled:
    description:
    - Whether the policy is active. Members of a disabled policy fall back
      to the platform default authentication behavior.
    - Omit to leave unchanged on an existing policy; defaults to C(true)
      when creating a new policy.
    type: bool
  ssh_allowed_methods:
    description:
    - Authentication methods where any one method is sufficient for SSH
      login.
    - Mutually exclusive with I(ssh_required_methods) - a policy uses one
      mode or the other. Supplying this list switches the policy to allowed
      mode and clears any required methods.
    - C(default) permits whatever methods the FlashBlade supports for the
      interface and version.
    - Omit to leave the current SSH configuration unchanged.
    type: list
    elements: str
    choices: [ password, key, default ]
  ssh_required_methods:
    description:
    - Authentication methods that must all be presented for SSH login.
    - Mutually exclusive with I(ssh_allowed_methods) - a policy uses one
      mode or the other. Supplying this list switches the policy to required
      mode and clears any allowed methods.
    - C(default) is not valid as a required method.
    - Omit to leave the current SSH configuration unchanged.
    type: list
    elements: str
    choices: [ password, key ]
  members:
    description:
    - Declarative list of members the policy is attached to.
    - Setting the list will attach missing members and detach any member
      not listed here.
    - Omit (or set to C(null)) to leave current membership untouched.
    - Provide an empty list to detach every member from the policy.
    - Attaching a member that is attached to a different policy fails
      unless I(replace_existing=true).
    type: list
    elements: dict
    suboptions:
      name:
        description:
        - Name of the member (a local admin name, or the FlashBlade array
          name for I(type=array)).
        type: str
        required: true
      type:
        description:
        - Type of the member.
        type: str
        required: true
        choices: [ admin, array ]
  replace_existing:
    description:
    - Whether a member currently attached to a different management
      authentication policy may be detached from it and attached to this
      policy (detach-then-attach).
    - When C(false) a conflicting assignment fails the task, naming the
      conflicting policy.
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
- name: Create a policy allowing password or key
  everpure.flashblade.purefb_mgmt_auth_policy:
    name: ssh-flexible
    ssh_allowed_methods:
      - password
      - key
    fb_url: 10.10.10.2
    api_token: T-55a68eb5-c785-4720-a2ca-8b03903bf641

- name: Create an MFA policy (password AND key) attached to two admins
  everpure.flashblade.purefb_mgmt_auth_policy:
    name: ssh-mfa
    ssh_required_methods:
      - password
      - key
    members:
      - name: alice
        type: admin
      - name: bob
        type: admin
    fb_url: 10.10.10.2
    api_token: T-55a68eb5-c785-4720-a2ca-8b03903bf641

- name: Switch an existing policy from required to allowed mode
  everpure.flashblade.purefb_mgmt_auth_policy:
    name: ssh-mfa
    ssh_allowed_methods:
      - key
    fb_url: 10.10.10.2
    api_token: T-55a68eb5-c785-4720-a2ca-8b03903bf641

- name: Attach the array baseline, moving it from its current policy
  everpure.flashblade.purefb_mgmt_auth_policy:
    name: ssh-mfa
    members:
      - name: my-flashblade
        type: array
    replace_existing: true
    fb_url: 10.10.10.2
    api_token: T-55a68eb5-c785-4720-a2ca-8b03903bf641

- name: Detach every member from a policy
  everpure.flashblade.purefb_mgmt_auth_policy:
    name: ssh-mfa
    members: []
    fb_url: 10.10.10.2
    api_token: T-55a68eb5-c785-4720-a2ca-8b03903bf641

- name: Delete a policy (fails while members are attached)
  everpure.flashblade.purefb_mgmt_auth_policy:
    name: ssh-mfa
    state: absent
    fb_url: 10.10.10.2
    api_token: T-55a68eb5-c785-4720-a2ca-8b03903bf641
"""

RETURN = r"""
"""

HAS_PYPURECLIENT = True
try:
    from pypureclient.flashblade import (
        ManagementAuthenticationPolicy,
        ManagementAuthenticationPolicyConfig,
        ManagementAuthenticationPolicyPost,
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

MIN_MGMT_AUTH_API_VERSION = "2.22"
CONTEXT_API_VERSION = "2.17"
# Product design exempts these users from authentication-policy membership;
# the module must never attach or detach them.
PROTECTED_ADMINS = frozenset(["ir", "pureeng"])
# Module member type -> REST member_types value.
MEMBER_TYPE_API = {"admin": "admins", "array": "arrays"}
MEMBER_TYPE_MODULE = {"admins": "admin", "arrays": "array"}


def _ctx(module, api_version):
    """Return the context_names kwargs dict when the array supports it."""
    if not module.params.get("context"):
        return {}
    if LooseVersion(CONTEXT_API_VERSION) <= LooseVersion(api_version):
        return {"context_names": [module.params["context"]]}
    return {}


def _validate_methods(module):
    """Reject invalid SSH method lists before any API call.

    The argument spec enforces per-list choices ('default' is only a choice
    of ssh_allowed_methods) and mutual exclusion; this adds the rules the
    spec cannot express.
    """
    for param in ("ssh_allowed_methods", "ssh_required_methods"):
        methods = module.params[param]
        if methods is None:
            continue
        if not methods:
            module.fail_json(
                msg=(
                    "{0} must contain at least one method. To switch modes "
                    "supply the new mode's non-empty list; the other mode is "
                    "cleared automatically."
                ).format(param)
            )
        if len(set(methods)) != len(methods):
            module.fail_json(msg="Duplicate method in {0}".format(param))


def _validate_members(module):
    members = module.params["members"]
    if not members:
        return
    seen = set()
    arrays = 0
    for member in members:
        key = (member["type"], member["name"])
        if key in seen:
            module.fail_json(
                msg="Duplicate member (name={0}, type={1}) in members".format(
                    member["name"], member["type"]
                )
            )
        seen.add(key)
        if member["type"] == "array":
            arrays += 1
        elif member["name"].lower() in PROTECTED_ADMINS:
            module.fail_json(
                msg=(
                    "Admin {0} is exempt from management authentication "
                    "policies and cannot be attached."
                ).format(member["name"])
            )
    if arrays > 1:
        module.fail_json(msg="members may contain at most one array member")


def _get_policy(module, blade, ctx_kwargs):
    """The policy object, or None when it does not exist.

    A missing policy is itself a 400 here ("Management authentication
    policy does not exist."), so only that error reads as None - any
    other failure fails the task rather than masquerading as "absent"
    (which would let state=absent report ok on a transient error).
    """
    res = blade.get_management_authentication_policies(
        names=[module.params["name"]], **ctx_kwargs
    )
    if res.status_code == 200:
        items = list(res.items)
        return items[0] if items else None
    error = get_error_message(res)
    if error and "policy does not exist" in str(error).lower():
        return None
    module.fail_json(
        msg="Failed to read policy {0}. Error: {1}".format(module.params["name"], error)
    )


def _get_members(module, blade, ctx_kwargs):
    """Current membership as a set of (module_type, member_name)."""
    res = blade.get_management_authentication_policies_members(
        policy_names=[module.params["name"]], **ctx_kwargs
    )
    if res.status_code != 200:
        module.fail_json(
            msg="Failed to list members of policy {0}. Error: {1}".format(
                module.params["name"], get_error_message(res)
            )
        )
    return set(
        (MEMBER_TYPE_MODULE[item.member.resource_type], item.member.name)
        for item in res.items
    )


def _get_member_current_policy(module, blade, member_type, member_name, ctx_kwargs):
    """Name of the policy the member is attached to, or None.

    A failed lookup fails the task - treating it as "no conflict" would
    silently skip the preflight this call exists for.
    """
    res = blade.get_management_authentication_policies_members(
        member_names=[member_name],
        member_types=[MEMBER_TYPE_API[member_type]],
        **ctx_kwargs,
    )
    if res.status_code != 200:
        module.fail_json(
            msg=("Failed to look up current policy for {0} {1}. Error: {2}").format(
                member_type, member_name, get_error_message(res)
            )
        )
    items = list(res.items)
    return items[0].policy.name if items else None


def _desired_ssh_config(module):
    """Desired (mode, methods-set) from the params, or None when omitted."""
    if module.params["ssh_allowed_methods"] is not None:
        return ("allowed", set(module.params["ssh_allowed_methods"]))
    if module.params["ssh_required_methods"] is not None:
        return ("required", set(module.params["ssh_required_methods"]))
    return None


def _current_ssh_config(existing):
    """Current (mode, methods-set) of a policy.

    The array reports the inactive mode as an empty list, so exactly one
    mode is non-empty on a configured policy.
    """
    allowed = set(getattr(existing.ssh, "allowed_methods", None) or [])
    required = set(getattr(existing.ssh, "required_methods", None) or [])
    if required:
        return ("required", required)
    return ("allowed", allowed)


def _build_ssh_config(mode, methods):
    """SSH config body with the opposite mode explicitly cleared.

    PATCH replaces the ssh object wholesale (verified on REST 2.28: an
    omitted list is cleared too), but the explicit empty list keeps the
    request self-describing and version-safe.
    """
    if mode == "allowed":
        return ManagementAuthenticationPolicyConfig(
            allowed_methods=sorted(methods), required_methods=[]
        )
    return ManagementAuthenticationPolicyConfig(
        allowed_methods=[], required_methods=sorted(methods)
    )


def _warn_if_live(module, blade, ctx_kwargs):
    """Warn when changing authentication requirements on an attached policy."""
    if _get_members(module, blade, ctx_kwargs):
        module.warn(
            "Policy {0} is attached to one or more members. The new "
            "authentication requirements apply to their subsequent SSH "
            "logins.".format(module.params["name"])
        )


def _validate_members_exist(module, blade, members, ctx_kwargs):
    """Fail when a desired member does not exist.

    The members relationship GET returns 200 with no items for a
    nonexistent member - indistinguishable from "no policy attached" - so
    existence has to come from the admins/arrays endpoints. Runs in check
    mode too, so check mode catches a mistyped member name.
    """
    for member_type, member_name in sorted(members):
        if member_type == "admin":
            res = blade.get_admins(names=[member_name], **ctx_kwargs)
            if res.status_code != 200:
                # A missing admin is a 400 "Admin does not exist." here;
                # passing the array's error through keeps a transient
                # failure from masquerading as a missing member.
                module.fail_json(
                    msg=(
                        "Member admin {0} does not exist or could not be "
                        "verified. Error: {1}"
                    ).format(member_name, get_error_message(res))
                )
        else:
            res = blade.get_arrays(**ctx_kwargs)
            if res.status_code != 200:
                module.fail_json(
                    msg=(
                        "Member array {0} could not be verified. " "Error: {1}"
                    ).format(member_name, get_error_message(res))
                )
            if not any(array.name == member_name for array in res.items):
                module.fail_json(
                    msg="Member array {0} does not exist.".format(member_name)
                )


def _find_member_conflicts(module, blade, members, ctx_kwargs):
    """Read-only lookup of members attached to a different policy.

    Returns a list of (member_type, member_name, other_policy_name).
    """
    conflicts = []
    for member_type, member_name in sorted(members):
        other = _get_member_current_policy(
            module, blade, member_type, member_name, ctx_kwargs
        )
        if other and other != module.params["name"]:
            conflicts.append((member_type, member_name, other))
    return conflicts


def _fail_on_member_conflicts(module, conflicts):
    if conflicts and not module.params["replace_existing"]:
        module.fail_json(
            msg=(
                "Cannot attach members already attached to another "
                "management authentication policy: {0}. Set "
                "replace_existing=true to move them."
            ).format(
                ", ".join(
                    "{0} {1} (attached to {2})".format(mtype, mname, other)
                    for mtype, mname, other in conflicts
                )
            )
        )


def reconcile_members(module, blade, ctx_kwargs):
    """Diff desired members against actual; attach/detach to converge.

    Returns True if any change was made (or would be made in check-mode).
    """
    desired_list = module.params["members"]
    if desired_list is None:
        return False

    desired = set((member["type"], member["name"]) for member in desired_list)
    current = _get_members(module, blade, ctx_kwargs)

    to_add = desired - current
    to_remove = set(
        member
        for member in current - desired
        # Never detach exempt users, even if the array reports them.
        if not (member[0] == "admin" and member[1].lower() in PROTECTED_ADMINS)
    )

    # Existence and conflict checks run in check mode too so that check
    # mode fails exactly where a real run would.
    _validate_members_exist(module, blade, to_add, ctx_kwargs)
    conflicts = _find_member_conflicts(module, blade, to_add, ctx_kwargs)
    _fail_on_member_conflicts(module, conflicts)

    if not to_add and not to_remove:
        return False

    if module.check_mode:
        return True

    for member_type, member_name in sorted(to_remove):
        res = blade.delete_management_authentication_policies_members(
            policy_names=[module.params["name"]],
            member_names=[member_name],
            member_types=[MEMBER_TYPE_API[member_type]],
            **ctx_kwargs,
        )
        if res.status_code != 200:
            module.fail_json(
                msg=("Failed to detach {0} {1} from policy {2}. Error: {3}").format(
                    member_type,
                    member_name,
                    module.params["name"],
                    get_error_message(res),
                )
            )

    # Attach each member as a tight detach->attach pair. The array offers
    # no atomic move (member-side POSTs refuse an attached member just
    # like the policy-side one), so a moved member is unavoidably
    # unattached between its own two calls - but only between those two,
    # and a failure orphans at most that one member.
    conflict_by_member = {(mtype, mname): other for mtype, mname, other in conflicts}
    for member_type, member_name in sorted(to_add):
        other = conflict_by_member.get((member_type, member_name))
        if other:
            res = blade.delete_management_authentication_policies_members(
                policy_names=[other],
                member_names=[member_name],
                member_types=[MEMBER_TYPE_API[member_type]],
                **ctx_kwargs,
            )
            if res.status_code != 200:
                module.fail_json(
                    msg=("Failed to detach {0} {1} from policy {2}. Error: {3}").format(
                        member_type, member_name, other, get_error_message(res)
                    )
                )
        res = blade.post_management_authentication_policies_members(
            policy_names=[module.params["name"]],
            member_names=[member_name],
            member_types=[MEMBER_TYPE_API[member_type]],
            **ctx_kwargs,
        )
        if res.status_code != 200:
            orphan_note = ""
            if other:
                orphan_note = (
                    " {0} {1} is currently attached to no policy; re-run "
                    "the task to attach it."
                ).format(member_type, member_name)
            module.fail_json(
                msg=("Failed to attach {0} {1} to policy {2}. Error: {3}{4}").format(
                    member_type,
                    member_name,
                    module.params["name"],
                    get_error_message(res),
                    orphan_note,
                )
            )

    return True


def delete_policy(module, blade, ctx_kwargs):
    """Guarded delete.

    The array allows deleting an attached policy, so the members check
    must live client-side.
    """
    if _get_members(module, blade, ctx_kwargs):
        module.fail_json(
            msg=(
                "Authentication policy {0} has attached members. Detach "
                "members first (members: [] with state: present)."
            ).format(module.params["name"])
        )
    if not module.check_mode:
        res = blade.delete_management_authentication_policies(
            names=[module.params["name"]], **ctx_kwargs
        )
        if res.status_code != 200:
            module.fail_json(
                msg="Failed to delete policy {0}. Error: {1}".format(
                    module.params["name"], get_error_message(res)
                )
            )
    module.exit_json(changed=True)


def create_policy(module, blade, ctx_kwargs):
    desired_ssh = _desired_ssh_config(module)
    if desired_ssh is None:
        module.fail_json(
            msg=(
                "Creating a policy requires ssh_allowed_methods or "
                "ssh_required_methods."
            )
        )
    enabled = module.params["enabled"]
    if enabled is None:
        enabled = True

    # Preflight member existence and conflicts before the policy POST so
    # a refused attach cannot leave a half-created policy behind, and so
    # check mode fails exactly where a real run would.
    if module.params["members"]:
        desired_members = set(
            (member["type"], member["name"]) for member in module.params["members"]
        )
        _validate_members_exist(module, blade, desired_members, ctx_kwargs)
        _fail_on_member_conflicts(
            module,
            _find_member_conflicts(module, blade, desired_members, ctx_kwargs),
        )

    if module.check_mode:
        module.exit_json(changed=True)

    res = blade.post_management_authentication_policies(
        names=[module.params["name"]],
        policy=ManagementAuthenticationPolicyPost(
            enabled=enabled,
            ssh=_build_ssh_config(*desired_ssh),
        ),
        **ctx_kwargs,
    )
    if res.status_code != 200:
        module.fail_json(
            msg="Failed to create policy {0}. Error: {1}".format(
                module.params["name"], get_error_message(res)
            )
        )
    reconcile_members(module, blade, ctx_kwargs)
    module.exit_json(changed=True)


def update_policy(module, blade, ctx_kwargs, existing):
    changed = False

    patch_kwargs = {}
    if (
        module.params["enabled"] is not None
        and module.params["enabled"] != existing.enabled
    ):
        patch_kwargs["enabled"] = module.params["enabled"]

    desired_ssh = _desired_ssh_config(module)
    if desired_ssh is not None and desired_ssh != _current_ssh_config(existing):
        patch_kwargs["ssh"] = _build_ssh_config(*desired_ssh)

    if patch_kwargs:
        changed = True
        _warn_if_live(module, blade, ctx_kwargs)
        if not module.check_mode:
            res = blade.patch_management_authentication_policies(
                names=[module.params["name"]],
                policy=ManagementAuthenticationPolicy(**patch_kwargs),
                **ctx_kwargs,
            )
            if res.status_code != 200:
                module.fail_json(
                    msg="Failed to update policy {0}. Error: {1}".format(
                        module.params["name"], get_error_message(res)
                    )
                )

    if reconcile_members(module, blade, ctx_kwargs):
        changed = True

    module.exit_json(changed=changed)


def main():
    argument_spec = purefb_argument_spec()
    argument_spec.update(
        dict(
            name=dict(type="str", required=True),
            state=dict(type="str", default="present", choices=["absent", "present"]),
            enabled=dict(type="bool"),
            ssh_allowed_methods=dict(
                type="list",
                elements="str",
                choices=["password", "key", "default"],
            ),
            ssh_required_methods=dict(
                type="list",
                elements="str",
                choices=["password", "key"],
            ),
            members=dict(
                type="list",
                elements="dict",
                options=dict(
                    name=dict(type="str", required=True),
                    type=dict(
                        type="str",
                        required=True,
                        choices=["admin", "array"],
                    ),
                ),
            ),
            replace_existing=dict(type="bool", default=False),
            context=dict(type="str", default=""),
        )
    )

    module = AnsibleModule(
        argument_spec,
        mutually_exclusive=[["ssh_allowed_methods", "ssh_required_methods"]],
        supports_check_mode=True,
    )

    if not HAS_PYPURECLIENT:
        module.fail_json(msg="py-pure-client sdk is required for this module")

    state = module.params["state"]
    if state == "present":
        _validate_methods(module)
        _validate_members(module)

    blade = get_system(module)
    api_version = get_rest_api_version(blade)
    if LooseVersion(MIN_MGMT_AUTH_API_VERSION) > LooseVersion(api_version):
        module.fail_json(
            msg=(
                "FlashBlade REST version {0} does not support management "
                "authentication policies (requires {1}+)."
            ).format(api_version, MIN_MGMT_AUTH_API_VERSION)
        )
    ctx_kwargs = _ctx(module, api_version)

    existing = _get_policy(module, blade, ctx_kwargs)

    if state == "absent":
        if existing is None:
            module.exit_json(changed=False)
        delete_policy(module, blade, ctx_kwargs)
    else:
        if existing is None:
            create_policy(module, blade, ctx_kwargs)
        else:
            update_policy(module, blade, ctx_kwargs, existing)


if __name__ == "__main__":
    main()
