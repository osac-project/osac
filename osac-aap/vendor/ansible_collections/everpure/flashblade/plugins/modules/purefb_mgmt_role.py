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
module: purefb_mgmt_role
version_added: '1.28.0'
short_description: Manage FlashBlade custom management roles, permissions, and policy attachment
description:
- Manage FlashBlade Management Access Roles and the permissions they grant
  (I(object=role)).
- Attach or detach management access policies to a local admin
  (I(object=admin_attach)) or to a directory-service role mapping
  (I(object=ds_attach)).
- Policies and their rules are managed by
  M(everpure.flashblade.purefb_mgmt_policy).
- API-client policy attachment is not covered here; it is managed via the
  I(access_policies) field on M(everpure.flashblade.purefb_apiclient).
author:
- Pure Storage Ansible Team (@avk) <pure-ansible-team@everpuredata.com>
options:
  object:
    description:
    - What the module operates on.
    - C(role) manages a custom role and its permissions.
    - C(admin_attach) attaches or detaches policies on a local admin.
    - C(ds_attach) attaches or detaches policies on a directory-service role
      mapping.
    type: str
    required: true
    choices: [ role, admin_attach, ds_attach ]
  state:
    description:
    - For I(object=role), whether the role should exist.
    - For attachment modes, whether the listed policies should be attached
      (C(present)) or detached (C(absent)). Other policies on the subject
      are untouched.
    - Detaching from a subject that does not exist is a no-op; attaching
      to one fails.
    default: present
    choices: [ absent, present ]
    type: str
  name:
    description:
    - Name of the custom role. Required for I(object=role).
    - Maximum 63 characters. Cannot begin with an underscore (reserved).
    type: str
  description:
    description:
    - Free-form description of the role. Applied at creation time only;
      the FlashBlade REST API does not support editing a role's description.
    type: str
  permissions:
    description:
    - Declarative list of permissions on the role. Setting this list adds
      missing entries and removes any not listed. Identity key is
      I(resource); changing I(actions) on an existing resource is a PATCH.
    - Omit (or set to C(null)) to leave the current permission list
      untouched.
    - Provide an empty list to remove every permission from the role.
    - Built-in (Pure-defined) roles cannot be modified; supplying this
      option for one fails. Omit it to use the task as a bare existence
      check on a built-in role.
    - Only meaningful with I(object=role).
    type: list
    elements: dict
    suboptions:
      resource:
        description:
        - Target API resource. Accepted as either the short name (e.g.
          C(file-system-snapshots)) or the fully-qualified form
          (e.g. C(purestorage.com/v2/file-system-snapshots)).
        type: str
        required: true
      actions:
        description:
        - Allowed verbs on the resource. Any subset of C(get), C(post),
          C(patch), C(delete), or the special value C(all).
        - C(all) cannot be combined with specific verbs.
        - Duplicates are rejected.
        - The array stores C(all) as a grant distinct from the four
          explicit verbs, even though the effective access is identical
          today. Re-declaring an existing permission in the other spelling
          is a real state change and reports C(changed=true).
        - C(all) is forward-compatible - verbs added by future Purity
          releases are included automatically, while an enumerated list
          stays frozen at the verbs listed.
        type: list
        elements: str
        required: true
  admin:
    description:
    - Name of the local admin whose policy list should be reconciled.
    - Required for I(object=admin_attach).
    - The array refuses policy changes on built-in users (such as
      C(pureuser)); use a local admin created by
      M(everpure.flashblade.purefb_user).
    type: str
  ds_role:
    description:
    - Name of the directory-service role mapping whose policy list should
      be reconciled.
    - Required for I(object=ds_attach).
    type: str
  policies:
    description:
    - List of management access policy names to attach (with I(state=present))
      or detach (with I(state=absent)) from the subject.
    - Names are matched against the attached policies case-insensitively.
    - Required for I(object=admin_attach) and I(object=ds_attach).
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
- name: Create a custom role with one permission
  everpure.flashblade.purefb_mgmt_role:
    object: role
    name: backup-operator
    description: Manages file system snapshots and restores in a realm
    permissions:
      - resource: file-system-snapshots
        actions: [get, post, patch, delete]
    fb_url: 10.10.10.2
    api_token: T-55a68eb5-c785-4720-a2ca-8b03903bf641

- name: Grant a role read-only on a second resource, remove any other permissions
  everpure.flashblade.purefb_mgmt_role:
    object: role
    name: backup-operator
    permissions:
      - resource: file-system-snapshots
        actions: [get]
      - resource: file-systems
        actions: [get]
    fb_url: 10.10.10.2
    api_token: T-55a68eb5-c785-4720-a2ca-8b03903bf641

- name: Delete a custom role
  everpure.flashblade.purefb_mgmt_role:
    object: role
    name: backup-operator
    state: absent
    fb_url: 10.10.10.2
    api_token: T-55a68eb5-c785-4720-a2ca-8b03903bf641

- name: Attach a policy to a local admin
  everpure.flashblade.purefb_mgmt_role:
    object: admin_attach
    admin: svc-automation
    policies: [tenant-a-ops]
    fb_url: 10.10.10.2
    api_token: T-55a68eb5-c785-4720-a2ca-8b03903bf641

- name: Detach policies from a DS role mapping
  everpure.flashblade.purefb_mgmt_role:
    object: ds_attach
    ds_role: tenant-a-admins
    policies: [tenant-a-ops, tenant-a-readonly]
    state: absent
    fb_url: 10.10.10.2
    api_token: T-55a68eb5-c785-4720-a2ca-8b03903bf641
"""

RETURN = r"""
"""

HAS_PYPURECLIENT = True
try:
    from pypureclient.flashblade import (
        ManagementAccessPolicyRolePost,
        ManagementAccessPolicyRolePermissionPatch,
        ManagementAccessPolicyRolePermissionPost,
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

MIN_ATTACH_API_VERSION = "2.19"
MIN_ROLE_API_VERSION = "2.26"
CONTEXT_API_VERSION = "2.17"
RESOURCE_PREFIX = "purestorage.com/v2/"
VALID_VERB_SET = frozenset(["get", "post", "patch", "delete"])


def _ctx(module, api_version):
    if not module.params.get("context"):
        return {}
    if LooseVersion(CONTEXT_API_VERSION) <= LooseVersion(api_version):
        return {"context_names": [module.params["context"]]}
    return {}


def _normalize_resource(resource):
    """Accept both short and fully-qualified resource names."""
    if resource.startswith(RESOURCE_PREFIX):
        return resource
    return RESOURCE_PREFIX + resource


def _normalize_actions(module, actions):
    """Lowercase, dedupe, validate. Returns a sorted list for comparison."""
    seen = []
    for verb in actions:
        v = verb.lower()
        if v in seen:
            module.fail_json(msg="Duplicate action {0} in permission".format(verb))
        seen.append(v)
    if "all" in seen and len(seen) > 1:
        module.fail_json(msg="Action 'all' cannot be combined with specific verbs")
    if "all" not in seen:
        invalid = [v for v in seen if v not in VALID_VERB_SET]
        if invalid:
            module.fail_json(
                msg="Invalid action(s) {0}; allowed: get, post, patch, delete, all".format(
                    invalid
                )
            )
    return sorted(seen)


def _validate_role_name(module, name):
    if len(name) > 63:
        module.fail_json(msg="Role name must be 63 characters or fewer")
    if name.startswith("_"):
        module.fail_json(msg="Role names beginning with '_' are reserved")


def _get_role(module, blade, ctx_kwargs):
    res = blade.get_management_access_policies_roles(
        names=[module.params["name"]], **ctx_kwargs
    )
    if res.status_code != 200:
        return None
    items = list(res.items)
    return items[0] if items else None


def _get_permissions(module, blade, ctx_kwargs):
    res = blade.get_management_access_policies_roles_permissions(
        role_names=[module.params["name"]], **ctx_kwargs
    )
    if res.status_code != 200:
        module.fail_json(
            msg="Failed to list permissions for role {0}. Error: {1}".format(
                module.params["name"], get_error_message(res)
            )
        )
    return list(res.items)


def _role_referenced_by_rule(blade, role_name, ctx_kwargs):
    """Return True if any policy has a rule referencing this role."""
    res = blade.get_management_access_policies_rules(**ctx_kwargs)
    if res.status_code != 200:
        return False
    for rule in list(res.items):
        if getattr(rule.role, "name", None) == role_name:
            return True
    return False


def _delete_role(module, blade, ctx_kwargs, existing):
    if getattr(existing, "pure_defined", False):
        module.fail_json(
            msg="Built-in role {0} cannot be deleted".format(module.params["name"])
        )
    if _role_referenced_by_rule(blade, module.params["name"], ctx_kwargs):
        module.fail_json(
            msg=(
                "Role {0} is referenced by one or more policy rules. Remove "
                "the referencing rules first (see purefb_mgmt_policy)."
            ).format(module.params["name"])
        )
    if not module.check_mode:
        res = blade.delete_management_access_policies_roles(
            names=[module.params["name"]], **ctx_kwargs
        )
        if res.status_code != 200:
            module.fail_json(
                msg="Failed to delete role {0}. Error: {1}".format(
                    module.params["name"], get_error_message(res)
                )
            )
    module.exit_json(changed=True)


def _create_role(module, blade, ctx_kwargs):
    # Validate every permission before any mutating call so a rejected
    # create leaves nothing behind on the array.
    validated = []
    seen_resources = set()
    for perm in module.params["permissions"] or []:
        actions = _normalize_actions(module, perm["actions"])
        resource = _normalize_resource(perm["resource"])
        if resource in seen_resources:
            module.fail_json(
                msg="Duplicate resource {0} in permissions".format(resource)
            )
        seen_resources.add(resource)
        validated.append((resource, actions))

    if module.check_mode:
        module.exit_json(changed=True)

    body_kwargs = {}
    if module.params["description"] is not None:
        body_kwargs["description"] = module.params["description"]
    role_post = ManagementAccessPolicyRolePost(**body_kwargs)
    res = blade.post_management_access_policies_roles(
        names=[module.params["name"]], role=role_post, **ctx_kwargs
    )
    if res.status_code != 200:
        module.fail_json(
            msg="Failed to create role {0}. Error: {1}".format(
                module.params["name"], get_error_message(res)
            )
        )

    for resource, actions in validated:
        res = blade.post_management_access_policies_roles_permissions(
            permission=ManagementAccessPolicyRolePermissionPost(
                actions=actions, resource=resource
            ),
            role_names=[module.params["name"]],
            **ctx_kwargs,
        )
        if res.status_code != 200:
            module.fail_json(
                msg=(
                    "Failed to add permission on {0} to role {1}. " "Error: {2}"
                ).format(resource, module.params["name"], get_error_message(res))
            )

    module.exit_json(changed=True)


def _reconcile_permissions(module, blade, ctx_kwargs):
    """Diff desired permissions against actual. Returns True if changed."""
    desired = module.params["permissions"]
    if desired is None:
        return False

    desired_by_resource = {}
    for perm in desired:
        resource = _normalize_resource(perm["resource"])
        actions = _normalize_actions(module, perm["actions"])
        if resource in desired_by_resource:
            module.fail_json(
                msg="Duplicate resource {0} in permissions".format(resource)
            )
        desired_by_resource[resource] = actions

    current = _get_permissions(module, blade, ctx_kwargs)
    current_by_resource = {perm.resource: perm for perm in current}

    to_remove = set(current_by_resource) - set(desired_by_resource)
    to_add = set(desired_by_resource) - set(current_by_resource)
    to_check = set(desired_by_resource) & set(current_by_resource)
    to_patch = []
    for resource in to_check:
        current_actions = sorted(
            [a.lower() for a in list(current_by_resource[resource].actions or [])]
        )
        if current_actions != desired_by_resource[resource]:
            to_patch.append(resource)

    if not to_remove and not to_add and not to_patch:
        return False

    if module.check_mode:
        return True

    for resource in to_remove:
        perm_name = current_by_resource[resource].name
        res = blade.delete_management_access_policies_roles_permissions(
            names=[perm_name],
            **ctx_kwargs,
        )
        if res.status_code != 200:
            module.fail_json(
                msg=(
                    "Failed to remove permission {0} from role {1}. Error: {2}"
                ).format(perm_name, module.params["name"], get_error_message(res))
            )

    for resource in to_add:
        res = blade.post_management_access_policies_roles_permissions(
            permission=ManagementAccessPolicyRolePermissionPost(
                actions=desired_by_resource[resource], resource=resource
            ),
            role_names=[module.params["name"]],
            **ctx_kwargs,
        )
        if res.status_code != 200:
            module.fail_json(
                msg=("Failed to add permission on {0} to role {1}. Error: {2}").format(
                    resource, module.params["name"], get_error_message(res)
                )
            )

    for resource in to_patch:
        perm_name = current_by_resource[resource].name
        res = blade.patch_management_access_policies_roles_permissions(
            permission=ManagementAccessPolicyRolePermissionPatch(
                actions=desired_by_resource[resource]
            ),
            names=[perm_name],
            **ctx_kwargs,
        )
        if res.status_code != 200:
            module.fail_json(
                msg=("Failed to update permission {0} on role {1}. Error: {2}").format(
                    perm_name, module.params["name"], get_error_message(res)
                )
            )

    return True


def _handle_role(module, blade, api_version, ctx_kwargs):
    if LooseVersion(MIN_ROLE_API_VERSION) > LooseVersion(api_version):
        module.fail_json(
            msg=(
                "FlashBlade REST version {0} does not support custom roles "
                "and permissions (requires {1}+)."
            ).format(api_version, MIN_ROLE_API_VERSION)
        )
    _validate_role_name(module, module.params["name"])

    existing = _get_role(module, blade, ctx_kwargs)
    state = module.params["state"]

    if state == "absent":
        if existing is None:
            module.exit_json(changed=False)
        _delete_role(module, blade, ctx_kwargs, existing)
    else:
        if existing is None:
            _create_role(module, blade, ctx_kwargs)
        else:
            # Guard the mutation only: a bare existence check (no
            # permissions supplied) on a built-in role stays a no-op.
            if module.params["permissions"] is not None and getattr(
                existing, "pure_defined", False
            ):
                module.fail_json(
                    msg="Built-in role {0} cannot be modified".format(
                        module.params["name"]
                    )
                )
            changed = _reconcile_permissions(module, blade, ctx_kwargs)
            module.exit_json(changed=changed)


def _current_attached_policy_names(module, blade, kind, ctx_kwargs):
    if kind == "admin":
        subject = module.params["admin"]
        res = blade.get_management_access_policies_admins(
            member_names=[subject], **ctx_kwargs
        )
    else:
        subject = module.params["ds_role"]
        res = blade.get_management_access_policies_directory_services_roles(
            member_names=[subject], **ctx_kwargs
        )
    if res.status_code != 200:
        module.fail_json(
            msg="Failed to list policies attached to {0} {1}. Error: {2}".format(
                kind, subject, get_error_message(res)
            )
        )
    names = set()
    for mapping in list(res.items):
        pol = getattr(mapping, "policy", None)
        if pol is not None and getattr(pol, "name", None):
            names.add(pol.name)
    return names


def _subject_exists(module, blade, kind, ctx_kwargs):
    """Return True if the attach subject (admin or ds_role) exists."""
    if kind == "admin":
        res = blade.get_admins(names=[module.params["admin"]], **ctx_kwargs)
    else:
        res = blade.get_directory_services_roles(
            names=[module.params["ds_role"]], **ctx_kwargs
        )
    return res.status_code == 200 and bool(list(res.items))


def _handle_attach(module, blade, api_version, ctx_kwargs, kind):
    if LooseVersion(MIN_ATTACH_API_VERSION) > LooseVersion(api_version):
        module.fail_json(
            msg=(
                "FlashBlade REST version {0} does not support management "
                "access policy attachment (requires {1}+)."
            ).format(api_version, MIN_ATTACH_API_VERSION)
        )

    state = module.params["state"]
    if not _subject_exists(module, blade, kind, ctx_kwargs):
        # A missing subject trivially satisfies state=absent: nothing can
        # be attached to it, so detaching is an idempotent no-op.
        if state == "absent":
            module.exit_json(changed=False)
        subject = (
            module.params["admin"] if kind == "admin" else module.params["ds_role"]
        )
        module.fail_json(msg="{0} {1} does not exist".format(kind, subject))
    current = _current_attached_policy_names(module, blade, kind, ctx_kwargs)
    # Policy names are case-insensitive, case-preserving on the array, so
    # diff on a lowercased key. Detach uses the array's canonical spelling.
    current_by_key = {name.lower(): name for name in current}
    requested_by_key = {}
    for name in module.params["policies"]:
        requested_by_key.setdefault(name.lower(), name)

    if state == "present":
        to_add = [
            name for key, name in requested_by_key.items() if key not in current_by_key
        ]
        to_remove = []
    else:
        to_add = []
        to_remove = [
            current_by_key[key] for key in requested_by_key if key in current_by_key
        ]

    if not to_add and not to_remove:
        module.exit_json(changed=False)
    if module.check_mode:
        module.exit_json(changed=True)

    if kind == "admin":
        subject = module.params["admin"]
        add_fn = blade.post_management_access_policies_admins
        del_fn = blade.delete_management_access_policies_admins
    else:
        subject = module.params["ds_role"]
        add_fn = blade.post_management_access_policies_directory_services_roles
        del_fn = blade.delete_management_access_policies_directory_services_roles

    if to_add:
        res = add_fn(
            member_names=[subject],
            policy_names=sorted(to_add),
            **ctx_kwargs,
        )
        if res.status_code != 200:
            module.fail_json(
                msg="Failed to attach policies {0} to {1} {2}. Error: {3}".format(
                    sorted(to_add), kind, subject, get_error_message(res)
                )
            )
    if to_remove:
        res = del_fn(
            member_names=[subject],
            policy_names=sorted(to_remove),
            **ctx_kwargs,
        )
        if res.status_code != 200:
            module.fail_json(
                msg="Failed to detach policies {0} from {1} {2}. Error: {3}".format(
                    sorted(to_remove), kind, subject, get_error_message(res)
                )
            )

    module.exit_json(changed=True)


def main():
    argument_spec = purefb_argument_spec()
    argument_spec.update(
        dict(
            object=dict(
                type="str",
                required=True,
                choices=["role", "admin_attach", "ds_attach"],
            ),
            state=dict(type="str", default="present", choices=["absent", "present"]),
            name=dict(type="str"),
            description=dict(type="str"),
            permissions=dict(
                type="list",
                elements="dict",
                options=dict(
                    resource=dict(type="str", required=True),
                    actions=dict(type="list", elements="str", required=True),
                ),
            ),
            admin=dict(type="str"),
            ds_role=dict(type="str"),
            policies=dict(type="list", elements="str"),
            context=dict(type="str", default=""),
        )
    )

    required_if = [
        ("object", "role", ["name"]),
        ("object", "admin_attach", ["admin", "policies"]),
        ("object", "ds_attach", ["ds_role", "policies"]),
    ]

    module = AnsibleModule(
        argument_spec, required_if=required_if, supports_check_mode=True
    )

    if not HAS_PYPURECLIENT:
        module.fail_json(msg="py-pure-client sdk is required for this module")

    blade = get_system(module)
    api_version = get_rest_api_version(blade)
    ctx_kwargs = _ctx(module, api_version)

    obj = module.params["object"]
    if obj == "role":
        _handle_role(module, blade, api_version, ctx_kwargs)
    elif obj == "admin_attach":
        _handle_attach(module, blade, api_version, ctx_kwargs, "admin")
    else:
        _handle_attach(module, blade, api_version, ctx_kwargs, "ds_role")


if __name__ == "__main__":
    main()
