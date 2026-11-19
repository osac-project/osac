#!/usr/bin/python
# -*- coding: utf-8 -*-

# (c) 2026
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
module: purefb_s3_export_policy
version_added: '1.26.0'
short_description: Manage FlashBlade S3 Export Policies
description:
- Create, update, rename or delete FlashBlade S3 Export Policies and their rules.
- An S3 export policy is a reusable object-store policy that controls which
  buckets in an object-store account are visible for S3 through an
  object-store account export.
- The policy shell (name, enabled flag, inline rules) is managed via the
  C(/s3-export-policies) endpoint. The set of rules is reconciled against
  the C(/s3-export-policies/rules) sub-resource on update.
author:
- Pure Storage Ansible Team (@sdodsley) <pure-ansible-team@purestorage.com>
options:
  name:
    description:
    - Name of the S3 export policy.
    type: str
    required: true
  state:
    description:
    - Define whether the S3 export policy should exist or not.
    default: present
    choices: [ absent, present ]
    type: str
  enabled:
    description:
    - If C(true) the policy is enabled.
    - Defaults to C(true) on creation if not specified.
    type: bool
  rename:
    description:
    - New name for the S3 export policy.
    - Only takes effect when the policy already exists.
    type: str
  rules:
    description:
    - Ordered list of rules to apply to the policy.
    - When supplied on create, rules are materialised in a single round trip.
    - When supplied on update, rules are reconciled against the current
      state - missing rules are added, divergent rules are patched, and
      rules not in this list are removed.
    - Omit this option entirely to leave existing rules untouched.
    type: list
    elements: dict
    suboptions:
      name:
        description:
        - Name of the rule. Used as the idempotency key.
        type: str
        required: true
      actions:
        description:
        - List of actions granted by this rule.
        - Currently only C(pure:S3Access) is supported.
        type: list
        elements: str
      effect:
        description:
        - Effect of this rule.
        type: str
        choices: [ allow, deny ]
      resources:
        description:
        - List of bucket resources from the account that this rule applies to.
        - Glob patterns are supported (for example C(my-bucket*) or C(*)).
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
- purestorage.flashblade.purestorage.fb
"""

EXAMPLES = r"""
- name: Create an S3 export policy with two rules
  purestorage.flashblade.purefb_s3_export_policy:
    name: my_export_policy
    enabled: true
    rules:
      - name: allow_all_buckets
        actions:
          - "pure:S3Access"
        effect: allow
        resources:
          - "*"
      - name: deny_finance
        actions:
          - "pure:S3Access"
        effect: deny
        resources:
          - "finance-*"
    fb_url: 10.10.10.2
    api_token: T-68618f31-0c9e-4e57-aa44-5306a2cf10e3

- name: Disable an existing S3 export policy
  purestorage.flashblade.purefb_s3_export_policy:
    name: my_export_policy
    enabled: false
    fb_url: 10.10.10.2
    api_token: T-68618f31-0c9e-4e57-aa44-5306a2cf10e3

- name: Rename an S3 export policy
  purestorage.flashblade.purefb_s3_export_policy:
    name: my_export_policy
    rename: tenant_a_export_policy
    fb_url: 10.10.10.2
    api_token: T-68618f31-0c9e-4e57-aa44-5306a2cf10e3

- name: Delete an S3 export policy
  purestorage.flashblade.purefb_s3_export_policy:
    name: tenant_a_export_policy
    state: absent
    fb_url: 10.10.10.2
    api_token: T-68618f31-0c9e-4e57-aa44-5306a2cf10e3
"""

RETURN = r"""
"""

HAS_PYPURECLIENT = True
try:
    from pypureclient.flashblade import (
        S3ExportPolicyPost,
        S3ExportPolicyPatch,
        S3ExportPolicyRulePost,
    )
except ImportError:
    HAS_PYPURECLIENT = False

from ansible.module_utils.basic import AnsibleModule
from ansible_collections.purestorage.flashblade.plugins.module_utils.purefb import (
    get_system,
    purefb_argument_spec,
)
from ansible_collections.purestorage.flashblade.plugins.module_utils.common import (
    get_error_message,
)
from ansible_collections.purestorage.flashblade.plugins.module_utils.version import (
    LooseVersion,
)

MIN_REQUIRED_API_VERSION = "2.20"


def _context_kwargs(module):
    """Build context_names kwargs only when a context is supplied."""
    if module.params["context"]:
        return {"context_names": [module.params["context"]]}
    return {}


def _normalize_rule(rule):
    """Normalize a desired-rule dict for order-insensitive comparison."""
    return {
        "name": rule.get("name"),
        "actions": sorted(rule.get("actions") or []),
        "effect": rule.get("effect"),
        "resources": sorted(rule.get("resources") or []),
    }


def _normalize_existing_rule(rule_obj):
    """Normalize a rule object returned by the SDK for comparison."""
    return {
        "name": getattr(rule_obj, "name", None),
        "actions": sorted(list(getattr(rule_obj, "actions", None) or [])),
        "effect": getattr(rule_obj, "effect", None),
        "resources": sorted(list(getattr(rule_obj, "resources", None) or [])),
    }


def _get_policy(module, blade):
    """Return the existing policy object or None."""
    res = blade.get_s3_export_policies(
        names=[module.params["name"]], **_context_kwargs(module)
    )
    if res.status_code != 200:
        return None
    items = list(res.items)
    return items[0] if items else None


def _get_policy_rules(module, blade):
    """Return the list of existing rule objects for the policy."""
    res = blade.get_s3_export_policies_rules(
        policy_names=[module.params["name"]], **_context_kwargs(module)
    )
    if res.status_code != 200:
        return []
    return list(res.items)


def _reconcile_rules(module, blade, desired_rules):
    """Add/modify/remove rules to match desired_rules. Returns True if changed."""
    current_rules = _get_policy_rules(module, blade)
    current_by_name = {
        getattr(r, "name", None): _normalize_existing_rule(r) for r in current_rules
    }
    desired_by_name = {r["name"]: _normalize_rule(r) for r in desired_rules}

    to_add = [n for n in desired_by_name if n not in current_by_name]
    to_remove = [n for n in current_by_name if n not in desired_by_name]
    to_modify = [
        n
        for n in desired_by_name
        if n in current_by_name and desired_by_name[n] != current_by_name[n]
    ]

    if not (to_add or to_remove or to_modify):
        return False

    if module.check_mode:
        return True

    for name in to_add:
        d = desired_by_name[name]
        res = blade.post_s3_export_policies_rules(
            names=[name],
            policy_names=[module.params["name"]],
            rule=S3ExportPolicyRulePost(
                actions=d["actions"] or None,
                effect=d["effect"],
                resources=d["resources"] or None,
            ),
            **_context_kwargs(module),
        )
        if res.status_code != 200:
            module.fail_json(
                msg="Failed to add rule {0} to S3 export policy {1}. Error: {2}".format(
                    name, module.params["name"], get_error_message(res)
                )
            )

    for name in to_modify:
        d = desired_by_name[name]
        res = blade.patch_s3_export_policies_rules(
            names=[name],
            policy_names=[module.params["name"]],
            rule=S3ExportPolicyRulePost(
                actions=d["actions"] or None,
                effect=d["effect"],
                resources=d["resources"] or None,
            ),
            **_context_kwargs(module),
        )
        if res.status_code != 200:
            module.fail_json(
                msg="Failed to update rule {0} on S3 export policy {1}. Error: {2}".format(
                    name, module.params["name"], get_error_message(res)
                )
            )

    if to_remove:
        res = blade.delete_s3_export_policies_rules(
            names=to_remove,
            policy_names=[module.params["name"]],
            **_context_kwargs(module),
        )
        if res.status_code != 200:
            module.fail_json(
                msg="Failed to remove rules {0} from S3 export policy {1}. Error: {2}".format(
                    to_remove, module.params["name"], get_error_message(res)
                )
            )

    return True


def delete_policy(module, blade):
    """Delete an S3 export policy."""
    changed = True
    if not module.check_mode:
        res = blade.delete_s3_export_policies(
            names=[module.params["name"]], **_context_kwargs(module)
        )
        if res.status_code != 200:
            module.fail_json(
                msg="Failed to delete S3 export policy {0}. Error: {1}".format(
                    module.params["name"], get_error_message(res)
                )
            )
    module.exit_json(changed=changed)


def create_policy(module, blade):
    """Create a new S3 export policy."""
    changed = True
    policy_kwargs = {}
    if module.params["enabled"] is not None:
        policy_kwargs["enabled"] = module.params["enabled"]
    if module.params["rules"]:
        policy_kwargs["rules"] = [
            {
                "name": r["name"],
                "actions": r.get("actions"),
                "effect": r.get("effect"),
                "resources": r.get("resources"),
            }
            for r in module.params["rules"]
        ]
    if not module.check_mode:
        res = blade.post_s3_export_policies(
            names=[module.params["name"]],
            policy=S3ExportPolicyPost(**policy_kwargs),
            **_context_kwargs(module),
        )
        if res.status_code != 200:
            module.fail_json(
                msg="Failed to create S3 export policy {0}. Error: {1}".format(
                    module.params["name"], get_error_message(res)
                )
            )
    module.exit_json(changed=changed)


def update_policy(module, blade, policy):
    """Update an existing S3 export policy."""
    changed = False
    patch_kwargs = {}

    if module.params["enabled"] is not None and module.params["enabled"] != getattr(
        policy, "enabled", None
    ):
        patch_kwargs["enabled"] = module.params["enabled"]

    if module.params["rename"] and module.params["rename"] != getattr(
        policy, "name", None
    ):
        patch_kwargs["name"] = module.params["rename"]

    if patch_kwargs:
        changed = True
        if not module.check_mode:
            res = blade.patch_s3_export_policies(
                names=[module.params["name"]],
                policy=S3ExportPolicyPatch(**patch_kwargs),
                **_context_kwargs(module),
            )
            if res.status_code != 200:
                module.fail_json(
                    msg="Failed to update S3 export policy {0}. Error: {1}".format(
                        module.params["name"], get_error_message(res)
                    )
                )

    if module.params["rules"] is not None:
        # Reconcile against the *current* name (rename, if any, is applied after).
        if _reconcile_rules(module, blade, module.params["rules"]):
            changed = True

    module.exit_json(changed=changed)


def main():
    argument_spec = purefb_argument_spec()
    argument_spec.update(
        dict(
            state=dict(type="str", default="present", choices=["absent", "present"]),
            name=dict(type="str", required=True),
            enabled=dict(type="bool"),
            rename=dict(type="str"),
            rules=dict(
                type="list",
                elements="dict",
                options=dict(
                    name=dict(type="str", required=True),
                    actions=dict(type="list", elements="str"),
                    effect=dict(type="str", choices=["allow", "deny"]),
                    resources=dict(type="list", elements="str"),
                ),
            ),
            context=dict(type="str", default=""),
        )
    )

    module = AnsibleModule(argument_spec, supports_check_mode=True)

    if not HAS_PYPURECLIENT:
        module.fail_json(msg="py-pure-client sdk is required for this module")

    blade = get_system(module)
    api_version = list(blade.get_versions().items)
    if LooseVersion(MIN_REQUIRED_API_VERSION) > LooseVersion(api_version):
        module.fail_json(
            msg="FlashBlade REST version not supported. "
            "Minimum version required: {0}".format(MIN_REQUIRED_API_VERSION)
        )

    if not module.params["context"]:
        # If no context is provided set the context to the local array name
        module.params["context"] = list(blade.get_arrays().items)[0].name

    state = module.params["state"]
    policy = _get_policy(module, blade)

    if module.params["rename"] and policy is None:
        module.fail_json(
            msg="Cannot rename S3 export policy {0}: policy does not exist.".format(
                module.params["name"]
            )
        )

    if state == "present" and policy is None:
        create_policy(module, blade)
    elif state == "present" and policy is not None:
        update_policy(module, blade, policy)
    elif state == "absent" and policy is not None:
        delete_policy(module, blade)

    module.exit_json(changed=False)


if __name__ == "__main__":
    main()
