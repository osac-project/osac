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
module: purefb_mgmt_policy
version_added: '1.28.0'
short_description: Manage FlashBlade Management Access Policies and their rules
description:
- Create, update, or delete a FlashBlade Management Access Policy.
- Reconcile the declarative list of rules (role + scope) attached to the policy.
- Attachment of policies to admins or directory-service roles is handled by
  M(everpure.flashblade.purefb_mgmt_role).
author:
- Pure Storage Ansible Team (@avk) <pure-ansible-team@everpuredata.com>
options:
  name:
    description:
    - Name of the management access policy.
    - Case-insensitive, case-preserving. This is the handle used for
      attachment and for naming child rules.
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
    - Whether the policy is active. Disabled policies are dropped during
      aggregation on the member.
    - Omit to leave unchanged on an existing policy; defaults to C(true)
      when creating a new policy.
    type: bool
  aggregation_strategy:
    description:
    - How permissions combine across multiple policies attached to the same
      member.
    - C(all-permissions) unions the permissions from every policy (default
      on the array).
    - C(least-common-permission) intersects them.
    - Never affects rule combination within a single policy.
    - Omit to leave unchanged on an existing policy.
    type: str
    choices: [ all-permissions, least-common-permission ]
  rules:
    description:
    - Declarative list of rules (role + scope) that make up the policy.
    - Setting the list will add missing rules and remove any rule not
      listed here. Rule identity is (I(role), I(scope.resource_type),
      I(scope.name)), matched case-insensitively — names are
      case-preserving on the array.
    - Duplicate rules (same role and scope, any case) are rejected.
    - Omit (or set to C(null)) to leave the current rule list untouched.
    - Provide an empty list to remove every rule from the policy.
    - On REST API 2.26 and newer rules are added and removed one at a time.
      Older arrays (REST 2.19 to 2.25) have no per-rule endpoints, so the
      rule list is sent with the policy at creation and replaced as a whole
      when it differs on update.
    - Purity//FB 4.6 (REST 2.21) requires exactly one rule when a policy
      is created.
    type: list
    elements: dict
    suboptions:
      role:
        description:
        - Name of the role granted by this rule (built-in, e.g. C(viewer),
          C(admin), C(storage), C(support); or a custom role created by
          M(everpure.flashblade.purefb_mgmt_role)).
        type: str
        required: true
      scope:
        description:
        - Where the role applies.
        type: dict
        required: true
        suboptions:
          name:
            description:
            - Name of the target (the FlashBlade array name for
              I(resource_type=arrays), or the realm name for
              I(resource_type=realms)).
            type: str
            required: true
          resource_type:
            description:
            - Scope type.
            type: str
            required: true
            choices: [ arrays, realms ]
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
- name: Create an empty policy
  everpure.flashblade.purefb_mgmt_policy:
    name: tenant-a-ops
    enabled: true
    aggregation_strategy: all-permissions
    fb_url: 10.10.10.2
    api_token: T-55a68eb5-c785-4720-a2ca-8b03903bf641

- name: Create a policy with two rules
  everpure.flashblade.purefb_mgmt_policy:
    name: tenant-a-ops
    rules:
      - role: viewer
        scope:
          name: my-flashblade
          resource_type: arrays
      - role: backup-operator
        scope:
          name: realm-a
          resource_type: realms
    fb_url: 10.10.10.2
    api_token: T-55a68eb5-c785-4720-a2ca-8b03903bf641

- name: Disable a policy
  everpure.flashblade.purefb_mgmt_policy:
    name: tenant-a-ops
    enabled: false
    fb_url: 10.10.10.2
    api_token: T-55a68eb5-c785-4720-a2ca-8b03903bf641

- name: Remove every rule from a policy
  everpure.flashblade.purefb_mgmt_policy:
    name: tenant-a-ops
    rules: []
    fb_url: 10.10.10.2
    api_token: T-55a68eb5-c785-4720-a2ca-8b03903bf641

- name: Delete a policy
  everpure.flashblade.purefb_mgmt_policy:
    name: tenant-a-ops
    state: absent
    fb_url: 10.10.10.2
    api_token: T-55a68eb5-c785-4720-a2ca-8b03903bf641
"""

RETURN = r"""
"""

HAS_PYPURECLIENT = True
try:
    from pypureclient.flashblade import (
        ManagementAccessPolicy,
        ManagementAccessPolicyPost,
        ManagementAccessPolicyRule,
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

MIN_POLICY_API_VERSION = "2.19"
# REST version that added the management-access-policies/rules endpoints.
# Older arrays carry the rule list inline on the policy object instead.
MIN_RULE_API_VERSION = "2.26"
CONTEXT_API_VERSION = "2.17"
BUILTIN_POLICIES = frozenset(
    ["array_admin", "management", "ops_admin", "readonly", "storage_admin"]
)


def _ctx(module, api_version):
    """Return the context_names kwargs dict when the array supports it."""
    if not module.params.get("context"):
        return {}
    if LooseVersion(CONTEXT_API_VERSION) <= LooseVersion(api_version):
        return {"context_names": [module.params["context"]]}
    return {}


def _rule_key(role_name, scope_type, scope_name):
    """Case-insensitive rule identity — names are case-preserving on the
    array, so a case variant of an existing rule is the same rule."""
    return (role_name.lower(), scope_type.lower(), scope_name.lower())


def _desired_rules_by_key(rules):
    """Map identity key -> user-supplied rule dict."""
    by_key = {}
    for rule in rules:
        key = _rule_key(
            rule["role"],
            rule["scope"]["resource_type"],
            rule["scope"]["name"],
        )
        by_key.setdefault(key, rule)
    return by_key


def _validate_rules(module):
    """Reject duplicate rules client-side.

    Rule identity is (role, scope.resource_type, scope.name). Without this
    check the create path would forward duplicates to the array while the
    update path silently collapses them.
    """
    rules = module.params["rules"]
    if not rules:
        return
    seen = set()
    for rule in rules:
        key = _rule_key(
            rule["role"],
            rule["scope"]["resource_type"],
            rule["scope"]["name"],
        )
        if key in seen:
            module.fail_json(
                msg=("Duplicate rule (role={0}, scope={1}/{2}) in rules").format(
                    rule["role"],
                    rule["scope"]["resource_type"],
                    rule["scope"]["name"],
                )
            )
        seen.add(key)


def _get_policy(module, blade, ctx_kwargs):
    res = blade.get_management_access_policies(
        names=[module.params["name"]], **ctx_kwargs
    )
    if res.status_code != 200:
        return None
    items = list(res.items)
    return items[0] if items else None


def _get_current_rules(module, blade, ctx_kwargs):
    res = blade.get_management_access_policies_rules(
        policy_names=[module.params["name"]], **ctx_kwargs
    )
    if res.status_code != 200:
        module.fail_json(
            msg="Failed to list rules for policy {0}. Error: {1}".format(
                module.params["name"], get_error_message(res)
            )
        )
    return list(res.items)


def _members_attached(module, blade, ctx_kwargs):
    res = blade.get_management_access_policies_members(
        policy_names=[module.params["name"]], **ctx_kwargs
    )
    if res.status_code != 200:
        return False
    return bool(list(res.items))


def delete_policy(module, blade, ctx_kwargs):
    if _members_attached(module, blade, ctx_kwargs):
        module.fail_json(
            msg=(
                "Policy {0} is attached to one or more members. "
                "Detach it first (see purefb_mgmt_role)."
            ).format(module.params["name"])
        )
    if not module.check_mode:
        res = blade.delete_management_access_policies(
            names=[module.params["name"]], **ctx_kwargs
        )
        if res.status_code != 200:
            module.fail_json(
                msg="Failed to delete policy {0}. Error: {1}".format(
                    module.params["name"], get_error_message(res)
                )
            )
    module.exit_json(changed=True)


def _reconcile_rules(module, blade, ctx_kwargs):
    """Diff desired rule list against actual; add/remove to converge.

    Returns True if any change was made (or would be made in check-mode).
    """
    desired = module.params["rules"]
    if desired is None:
        return False

    current = _get_current_rules(module, blade, ctx_kwargs)
    current_by_key = {}
    for rule in current:
        key = _rule_key(rule.role.name, rule.scope.resource_type, rule.scope.name)
        current_by_key[key] = rule

    desired_by_key = _desired_rules_by_key(desired)

    to_remove = set(current_by_key) - set(desired_by_key)
    to_add = set(desired_by_key) - set(current_by_key)

    if not to_remove and not to_add:
        return False

    if module.check_mode:
        return True

    for key in to_remove:
        # Rule names (<policy>.<index>) renumber whenever a lower-indexed
        # rule is deleted, so a name captured before this loop can point at
        # a different rule (or none) by the time it is used. Rule IDs are
        # immutable — delete by id only.
        rule = current_by_key[key]
        res = blade.delete_management_access_policies_rules(
            ids=[rule.id],
            **ctx_kwargs,
        )
        if res.status_code != 200:
            module.fail_json(
                msg=(
                    "Failed to remove rule (role={0}, scope={1}/{2}) from "
                    "policy {3}. Error: {4}"
                ).format(
                    rule.role.name,
                    rule.scope.resource_type,
                    rule.scope.name,
                    module.params["name"],
                    get_error_message(res),
                )
            )

    for key in to_add:
        spec = desired_by_key[key]
        role_name = spec["role"]
        scope_type = spec["scope"]["resource_type"]
        scope_name = spec["scope"]["name"]
        rule = ManagementAccessPolicyRule(
            role=ReferenceWritable(name=role_name),
            scope=ReferenceWritable(name=scope_name, resource_type=scope_type),
        )
        res = blade.post_management_access_policies_rules(
            rule=rule,
            policy_names=[module.params["name"]],
            **ctx_kwargs,
        )
        if res.status_code != 200:
            module.fail_json(
                msg=(
                    "Failed to add rule (role={0}, scope={1}/{2}) to "
                    "policy {3}. Error: {4}"
                ).format(
                    role_name,
                    scope_type,
                    scope_name,
                    module.params["name"],
                    get_error_message(res),
                )
            )

    return True


def _inline_rule_key(rule):
    """Identity key of a rule as the array returns it inside the policy.

    Tolerates missing role/scope references (a rule whose role was deleted
    comes back with ``role: null``) instead of raising ``AttributeError``.
    """
    role = getattr(getattr(rule, "role", None), "name", None) or ""
    scope = getattr(rule, "scope", None)
    return _rule_key(
        role,
        getattr(scope, "resource_type", None) or "",
        getattr(scope, "name", None) or "",
    )


def _inline_rules_differ(module, existing):
    """Compare the desired rules with the rule list carried on the policy
    object (REST < 2.26, where there are no per-rule endpoints).

    Identity is (role, scope.resource_type, scope.name), case-insensitive,
    so declaring the same rule the policy was created with is not a change.
    """
    current = set()
    for rule in getattr(existing, "rules", None) or []:
        current.add(_inline_rule_key(rule))
    return current != set(_desired_rules_by_key(module.params["rules"]))


def _replace_inline_rules(module, blade, ctx_kwargs, existing):
    """Replace the whole rule list of an existing policy (REST < 2.26).

    Returns True if a change was made (or would be made in check-mode).
    """
    if module.params["rules"] is None or not _inline_rules_differ(module, existing):
        return False
    if module.check_mode:
        return True
    res = blade.patch_management_access_policies(
        names=[module.params["name"]],
        policy=ManagementAccessPolicy(rules=_build_post_rules(module.params["rules"])),
        **ctx_kwargs,
    )
    if res.status_code != 200:
        module.fail_json(
            msg="Failed to replace rules on policy {0}. Error: {1}".format(
                module.params["name"], get_error_message(res)
            )
        )
    return True


def _build_post_rules(rules):
    if rules is None:
        return None
    result = []
    for rule in rules:
        result.append(
            ManagementAccessPolicyRule(
                role=ReferenceWritable(name=rule["role"]),
                scope=ReferenceWritable(
                    name=rule["scope"]["name"],
                    resource_type=rule["scope"]["resource_type"],
                ),
            )
        )
    return result


def create_policy(module, blade, ctx_kwargs):
    enabled = module.params["enabled"]
    if enabled is None:
        enabled = True

    if module.check_mode:
        module.exit_json(changed=True)

    body_kwargs = {"enabled": enabled}
    if module.params["aggregation_strategy"] is not None:
        body_kwargs["aggregation_strategy"] = module.params["aggregation_strategy"]
    post_rules = _build_post_rules(module.params["rules"])
    if post_rules is not None:
        body_kwargs["rules"] = post_rules

    policy = ManagementAccessPolicyPost(**body_kwargs)
    res = blade.post_management_access_policies(
        names=[module.params["name"]], policy=policy, **ctx_kwargs
    )
    if res.status_code != 200:
        module.fail_json(
            msg="Failed to create policy {0}. Error: {1}".format(
                module.params["name"], get_error_message(res)
            )
        )
    module.exit_json(changed=True)


def update_policy(module, blade, ctx_kwargs, existing, api_version=None):
    changed = False

    patch_kwargs = {}
    if (
        module.params["enabled"] is not None
        and module.params["enabled"] != existing.enabled
    ):
        patch_kwargs["enabled"] = module.params["enabled"]
    if (
        module.params["aggregation_strategy"] is not None
        and module.params["aggregation_strategy"] != existing.aggregation_strategy
    ):
        patch_kwargs["aggregation_strategy"] = module.params["aggregation_strategy"]

    if patch_kwargs:
        changed = True
        if not module.check_mode:
            res = blade.patch_management_access_policies(
                names=[module.params["name"]],
                policy=ManagementAccessPolicy(**patch_kwargs),
                **ctx_kwargs,
            )
            if res.status_code != 200:
                module.fail_json(
                    msg="Failed to update policy {0}. Error: {1}".format(
                        module.params["name"], get_error_message(res)
                    )
                )

    if module.params["rules"] is not None:
        if api_version is not None and LooseVersion(
            MIN_RULE_API_VERSION
        ) > LooseVersion(api_version):
            rules_changed = _replace_inline_rules(module, blade, ctx_kwargs, existing)
        else:
            rules_changed = _reconcile_rules(module, blade, ctx_kwargs)
        if rules_changed:
            changed = True

    module.exit_json(changed=changed)


def main():
    argument_spec = purefb_argument_spec()
    argument_spec.update(
        dict(
            name=dict(type="str", required=True),
            state=dict(type="str", default="present", choices=["absent", "present"]),
            enabled=dict(type="bool"),
            aggregation_strategy=dict(
                type="str",
                choices=["all-permissions", "least-common-permission"],
            ),
            rules=dict(
                type="list",
                elements="dict",
                options=dict(
                    role=dict(type="str", required=True),
                    scope=dict(
                        type="dict",
                        required=True,
                        options=dict(
                            name=dict(type="str", required=True),
                            resource_type=dict(
                                type="str",
                                required=True,
                                choices=["arrays", "realms"],
                            ),
                        ),
                    ),
                ),
            ),
            context=dict(type="str", default=""),
        )
    )

    module = AnsibleModule(argument_spec, supports_check_mode=True)

    if not HAS_PYPURECLIENT:
        module.fail_json(msg="py-pure-client sdk is required for this module")

    state = module.params["state"]
    name = module.params["name"]
    # Policy names are case-insensitive/case-preserving on the array, so
    # the guard must match case variants (e.g. "Readonly") too.
    is_builtin = name.lower() in BUILTIN_POLICIES
    mutating = (
        module.params["enabled"] is not None
        or module.params["aggregation_strategy"] is not None
        or module.params["rules"] is not None
        or state == "absent"
    )
    if is_builtin and mutating:
        module.fail_json(
            msg=("Built-in policy {0} cannot be modified or deleted.").format(name)
        )
    _validate_rules(module)

    blade = get_system(module)
    api_version = get_rest_api_version(blade)
    if LooseVersion(MIN_POLICY_API_VERSION) > LooseVersion(api_version):
        module.fail_json(
            msg=(
                "FlashBlade REST version {0} does not support management "
                "access policies (requires {1}+)."
            ).format(api_version, MIN_POLICY_API_VERSION)
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
            update_policy(module, blade, ctx_kwargs, existing, api_version)


if __name__ == "__main__":
    main()
