#!/usr/bin/python
# -*- coding: utf-8 -*-

# (c) 2025, Simon Dodsley (simon@everpuredata.com)
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
module: purefb_bucket_access
version_added: '1.20.0'
short_description: Manage FlashBlade bucket access policies
description:
- Manage object store bucket policies.
- This modules allows the management of both bucket access and cross-origin
  resource sharing policies and their associated rules.
author:
- Everpure Ansible Team (@sdodsley) <pure-ansible-team@everpuredata.com>
options:
  state:
    description:
    - Create or delete policy or rule.
    default: present
    type: str
    choices: [ absent, present ]
  policy_type:
    description:
    - Type of policy
    type: str
    choices: [ access, cors ]
    default: access
  name:
    description:
    - Name of Object Store bucket the policy applies to.
    type: str
    required: true
  rule:
    description:
    - Name of the rule in the Bucket Policy
    - Required if I(state) is I(present)
    type: str
  effect:
    description:
    - Allow S3 requests that match all of the I(actions) item selected.
      Rules are additive.
    - The FlashBlade REST API only supports I(allow) for bucket access
      policy rules. I(deny) is kept as a choice for backwards compatibility
      but is rejected with an error, because the array would silently store
      the rule as I(allow).
    type: str
    default: allow
    choices: [ allow, deny ]
  actions:
    description:
    - List of permissions to grant.
    - System-wide policy rules cannot be deleted or modified
    - The FlashBlade REST API currently only supports I(s3:GetObject) in a
      bucket access policy rule. A list that also contains other actions is
      accepted by the array but stored as I(s3:GetObject) alone, and a list
      without I(s3:GetObject) is rejected by the array. When the stored rule
      differs from the request the module fails and reports what the array
      kept, rather than replacing the rule on every run.
    type: list
    elements: str
    default: [ "s3:GetObject" ]
    choices:
      - s3:*
      - s3:AbortMultipartUpload
      - s3:BypassGovernanceRetention
      - s3:CreateBucket
      - s3:DeleteBucket
      - s3:DeleteObject
      - s3:DeleteObjectVersion
      - s3:ExtendSafemodeRetentionPeriod
      - s3:GetBucketAcl
      - s3:GetBucketLocation
      - s3:GetBucketVersioning
      - s3:GetLifecycleConfiguration
      - s3:GetObject
      - s3:GetObjectAcl
      - s3:GetObjectLegalHold
      - s3:GetObjectLockConfiguration
      - s3:GetObjectRetention
      - s3:GetObjectTagging
      - s3:GetObjectVersion
      - s3:GetObjectVersionTagging
      - s3:ListAllMyBuckets
      - s3:ListBucket
      - s3:ListBucketMultipartUploads
      - s3:ListBucketVersions
      - s3:ListMultipartUploadParts
      - s3:PutBucketVersioning
      - s3:PutLifecycleConfiguration
      - s3:PutObject
      - s3:PutObjectLegalHold
      - s3:PutObjectLockConfiguration
      - s3:PutObjectRetention
      - s3:ResolveSafemodeConflicts
  methods:
    description:
    - A list of HTTP methods that are permitted for cross-origin requests to access a bucket.
    - The only currently supported combination of allowed methods is all methods.
    choices:
      - GET
      - PUT
      - HEAD
      - POST
      - DELETE
    type: list
    elements: str
    default: ["GET", "PUT", "HEAD", "POST", "DELETE"]
  headers:
    description:
    - A list of headers that are permitted to be included in cross-origin requests to access a bucket.
    - The only currently supported allowed header is '*'.
    type: list
    elements: str
    default: ["*"]
  origins:
    description:
    - A list of origins (domains) that are permitted to make cross-origin requests to access a bucket.
    - The only currently supported allowed origin is '*'.
    type: list
    elements: str
    default: ["*"]
  resources:
    description:
    - The list of resources which this rule applies to.
    - The only currently supported resource is all objects in a bucket to
      which the parent policy belongs.
    elements: str
    type: list
    default: ["*"]
  principals:
    description:
    - Defines if the rule will apply to all object store users
      regardless of their origin or principal.
    type: bool
    default: true
  context:
    description:
    - Name of fleet member on which to perform the operation.
    - This requires the array receiving the request is a member of a fleet
      and the context name to be a member of the same fleet.
    type: str
    default: ""
    version_added: "1.22.0"
extends_documentation_fragment:
- everpure.flashblade.everpure.fb
"""

EXAMPLES = r"""
- name: Create a bucket access policy rule for bucket bar
  everpure.flashblade.purefb_bucket_policy:
    rule: foo
    name: bar
    policy_type: access
    fb_url: 10.10.10.2
    api_token: T-9f276a18-50ab-446e-8a0c-666a3529a1b6
- name: Create a CORS policy rule for bucket bar
  everpure.flashblade.purefb_bucket_policy:
    rule: foo
    name: bar
    policy_type: cors
    fb_url: 10.10.10.2
    api_token: T-9f276a18-50ab-446e-8a0c-666a3529a1b6
- name: Delete bucket policy rule foo from bucket bar
  everpure.flashblade.purefb_bucket_policy:
    rule: foo
    name: bar
    policy_type: access
    state: absent
    fb_url: 10.10.10.2
    api_token: T-9f276a18-50ab-446e-8a0c-666a3529a1b6
- name: Delete all bucket policy rules from bucket bar
  everpure.flashblade.purefb_bucket_policy:
    name: bar
    policy_type: access
    state: absent
    fb_url: 10.10.10.2
    api_token: T-9f276a18-50ab-446e-8a0c-666a3529a1b6
"""

RETURN = r"""
"""

HAS_PYPURECLIENT = True
try:
    from pypureclient.flashblade import (
        BucketAccessPolicyRulePost,
        BucketAccessPolicyRulePrincipal,
        CrossOriginResourceSharingPolicyRulePost,
    )
except ImportError:
    HAS_PYPURECLIENT = False


from ansible.module_utils.basic import AnsibleModule
from ansible_collections.everpure.flashblade.plugins.module_utils.purefb import (
    get_system,
    purefb_argument_spec,
)
from ansible_collections.everpure.flashblade.plugins.module_utils.version import (
    LooseVersion,
)
from ansible_collections.everpure.flashblade.plugins.module_utils.common import (
    get_error_message,
    get_rest_api_version,
)

MIN_API_VERSION = "2.12"
CONTEXT_API_VERSION = "2.17"


def _context_kwargs(module, api_version):
    """Return context_names kwargs when a fleet context applies, else {}"""
    if (
        LooseVersion(CONTEXT_API_VERSION) <= LooseVersion(api_version)
        and module.params["context"]
    ):
        return {"context_names": [module.params["context"]]}
    return {}


def _access_rule_matches(module, rule):
    """Compare a live bucket access policy rule with the requested rule.

    effect is not compared: the REST API only supports allow, and deny is
    rejected before this point.
    """
    desired = {
        "actions": sorted(module.params["actions"]),
        "resources": sorted(
            module.params["name"] + "/" + resource
            for resource in module.params["resources"]
        ),
        "all_principals": bool(module.params["principals"]),
    }
    current = {
        "actions": sorted(getattr(rule, "actions", None) or []),
        "resources": sorted(getattr(rule, "resources", None) or []),
        "all_principals": bool(
            getattr(getattr(rule, "principals", None), "all", False)
        ),
    }
    return desired == current


def _describe_access_rule(rule):
    """Return the stored content of a bucket access policy rule for messages"""
    return "actions={0}, resources={1}, all_principals={2}".format(
        sorted(getattr(rule, "actions", None) or []),
        sorted(getattr(rule, "resources", None) or []),
        bool(getattr(getattr(rule, "principals", None), "all", False)),
    )


def _describe_cors_rule(rule):
    """Return the stored content of a CORS policy rule for messages"""
    return "methods={0}, origins={1}, headers={2}".format(
        sorted(getattr(rule, "allowed_methods", None) or []),
        sorted(getattr(rule, "allowed_origins", None) or []),
        sorted(getattr(rule, "allowed_headers", None) or []),
    )


def _cors_rule_matches(module, rule):
    """Compare a live CORS policy rule with the requested rule"""
    desired = {
        "methods": sorted(module.params["methods"]),
        "origins": sorted(module.params["origins"]),
        "headers": sorted(module.params["headers"]),
    }
    current = {
        "methods": sorted(getattr(rule, "allowed_methods", None) or []),
        "origins": sorted(getattr(rule, "allowed_origins", None) or []),
        "headers": sorted(getattr(rule, "allowed_headers", None) or []),
    }
    return desired == current


def delete_cors_policy(module, blade):
    """Delete cross-origin resource sharing policy or rule"""
    api_version = get_rest_api_version(blade)
    changed = True
    if not module.check_mode:
        if module.params["rule"]:
            if (
                LooseVersion(CONTEXT_API_VERSION) <= LooseVersion(api_version)
                and module.params["context"]
            ):
                res = blade.get_buckets_cross_origin_resource_sharing_policies_rules(
                    bucket_names=[module.params["name"]],
                    names=[module.params["rule"]],
                    context_names=[module.params["context"]],
                )
            else:
                res = blade.get_buckets_cross_origin_resource_sharing_policies_rules(
                    bucket_names=[module.params["name"]], names=[module.params["rule"]]
                )
            if res.status_code != 200:
                # Rule does not exist, nothing to delete
                changed = False
                module.exit_json(changed=changed)

            if (
                LooseVersion(CONTEXT_API_VERSION) <= LooseVersion(api_version)
                and module.params["context"]
            ):
                res = blade.delete_buckets_cross_origin_resource_sharing_policies_rules(
                    names=module.params["rule"],
                    bucket_names=module.params["name"],
                    context_names=[module.params["context"]],
                )
            else:
                res = blade.delete_buckets_cross_origin_resource_sharing_policies_rules(
                    names=module.params["rule"], bucket_names=module.params["name"]
                )
            if res.status_code != 200:
                module.fail_json(
                    msg="Failed to delete CORS rule {0} for bucket {1}. Error: {2}".format(
                        module.params["rule"],
                        module.params["name"],
                        get_error_message(res),
                    )
                )
        else:
            if (
                LooseVersion(CONTEXT_API_VERSION) <= LooseVersion(api_version)
                and module.params["context"]
            ):
                res = blade.get_buckets_cross_origin_resource_sharing_policies(
                    bucket_names=[module.params["name"]],
                    context_names=[module.params["context"]],
                )
            else:
                res = blade.get_buckets_cross_origin_resource_sharing_policies(
                    bucket_names=[module.params["name"]],
                )
            if res.total_item_count == 0:
                changed = False
                module.exit_json(changed=changed)

            if (
                LooseVersion(CONTEXT_API_VERSION) <= LooseVersion(api_version)
                and module.params["context"]
            ):
                res = blade.delete_buckets_cross_origin_resource_sharing_policies(
                    bucket_names=module.params["name"],
                    context_names=[module.params["context"]],
                )
            else:
                res = blade.delete_buckets_cross_origin_resource_sharing_policies(
                    bucket_names=module.params["name"]
                )
            if res.status_code != 200:
                module.fail_json(
                    msg="Failed to delete CORS policy for bucket {0}. Error: {1}".format(
                        module.params["name"], get_error_message(res)
                    )
                )

    module.exit_json(changed=changed)


def delete_access_policy(module, blade):
    """Delete bucket access policy or rule"""

    changed = True
    api_version = get_rest_api_version(blade)
    if not module.check_mode:
        if module.params["rule"]:
            if (
                LooseVersion(CONTEXT_API_VERSION) <= LooseVersion(api_version)
                and module.params["context"]
            ):
                res = blade.get_buckets_bucket_access_policies_rules(
                    bucket_names=[module.params["name"]],
                    names=[module.params["rule"]],
                    context_names=[module.params["context"]],
                )
            else:
                res = blade.get_buckets_bucket_access_policies_rules(
                    bucket_names=[module.params["name"]], names=[module.params["rule"]]
                )
            if res.status_code != 200:
                changed = False
                module.exit_json(changed=changed)

            if (
                LooseVersion(CONTEXT_API_VERSION) <= LooseVersion(api_version)
                and module.params["context"]
            ):
                res = blade.delete_buckets_bucket_access_policies_rules(
                    names=module.params["rule"],
                    bucket_names=module.params["name"],
                    context_names=[module.params["context"]],
                )
            else:
                res = blade.delete_buckets_bucket_access_policies_rules(
                    names=module.params["rule"], bucket_names=module.params["name"]
                )
            if res.status_code != 200:
                module.fail_json(
                    msg="Failed to delete access rule {0} for bucket {1}. Error: {2}".format(
                        module.params["rule"],
                        module.params["name"],
                        get_error_message(res),
                    )
                )
        else:
            if (
                LooseVersion(CONTEXT_API_VERSION) <= LooseVersion(api_version)
                and module.params["context"]
            ):
                res = blade.get_buckets_bucket_access_policies(
                    bucket_names=[module.params["name"]],
                    context_names=[module.params["context"]],
                )
            else:
                res = blade.get_buckets_bucket_access_policies(
                    bucket_names=[module.params["name"]]
                )
            if res.total_item_count == 0:
                changed = False
                module.exit_json(changed=changed)

            if (
                LooseVersion(CONTEXT_API_VERSION) <= LooseVersion(api_version)
                and module.params["context"]
            ):
                res = blade.delete_buckets_bucket_access_policies(
                    bucket_names=module.params["name"],
                    context_names=[module.params["context"]],
                )
            else:
                res = blade.delete_buckets_bucket_access_policies(
                    bucket_names=module.params["name"]
                )
            if res.status_code != 200:
                module.fail_json(
                    msg="Failed to delete access policy for bucket {0}. Error: {1}".format(
                        module.params["name"], get_error_message(res)
                    )
                )

    module.exit_json(changed=changed)


def create_access_policy(module, blade):
    """Create or update a bucket access policy rule (and the policy if needed)"""
    changed = False
    api_version = get_rest_api_version(blade)
    ctx = _context_kwargs(module, api_version)
    bucket = module.params["name"]
    rule_name = module.params["rule"]

    res = blade.get_buckets_bucket_access_policies_rules(
        bucket_names=[bucket], names=[rule_name], **ctx
    )
    if res.status_code == 200:
        if _access_rule_matches(module, list(res.items)[0]):
            module.exit_json(changed=False)
        # The REST API has no PATCH for bucket access policy rules, so a rule
        # whose content differs is replaced: delete it, then recreate it.
        changed = True
        if not module.check_mode:
            res = blade.delete_buckets_bucket_access_policies_rules(
                bucket_names=[bucket], names=[rule_name], **ctx
            )
            if res.status_code != 200:
                module.fail_json(
                    msg="Failed to replace access policy rule {0} "
                    "in policy {1}. Error: {2}".format(
                        rule_name, bucket, get_error_message(res)
                    )
                )
    else:
        changed = True

    if not module.check_mode:
        # The policy itself may be missing (never created, or removed with its
        # last rule), in which case it has to exist before the rule is posted.
        res = blade.get_buckets_bucket_access_policies(bucket_names=[bucket], **ctx)
        if res.status_code != 200 or res.total_item_count == 0:
            res = blade.post_buckets_bucket_access_policies(
                bucket_names=[bucket], **ctx
            )
            if res.status_code != 200:
                module.fail_json(
                    msg="Failed to create initial bucket access policy for {0}. "
                    "Error: {1}".format(bucket, get_error_message(res))
                )
        all_resources = []
        for resource in module.params["resources"]:
            all_resources.append(bucket + "/" + resource)
        res = blade.post_buckets_bucket_access_policies_rules(
            bucket_names=[bucket],
            names=[rule_name],
            rule=BucketAccessPolicyRulePost(
                effect=module.params["effect"],
                resources=all_resources,
                actions=module.params["actions"],
                principals=BucketAccessPolicyRulePrincipal(
                    all=module.params["principals"]
                ),
            ),
            **ctx
        )
        if res.status_code != 200:
            module.fail_json(
                msg="Failed to create access policy rule {0} "
                "in policy {1}. Error: {2}".format(
                    rule_name, bucket, get_error_message(res)
                )
            )
        # The array accepts a rule body it cannot honour and stores what it
        # supports instead (Purity//FB 4.6 keeps s3:GetObject on all objects
        # for all principals). Left alone, every later run would see the
        # stored rule differ from the request and replace it again, so the
        # stored rule is checked here and a coerced rule is an error. The
        # POST response only echoes the request body (verified on 4.6.4: it
        # returns the extra actions that a GET immediately afterwards does
        # not), so the rule has to be read back rather than taken from the
        # POST response.
        res = blade.get_buckets_bucket_access_policies_rules(
            bucket_names=[bucket], names=[rule_name], **ctx
        )
        stored = list(res.items) if res.status_code == 200 else []
        if stored and not _access_rule_matches(module, stored[0]):
            module.fail_json(
                changed=True,
                msg="Access policy rule {0} in policy {1} was not stored as "
                "requested: the array kept {2}. The FlashBlade REST API "
                "currently only supports s3:GetObject on all objects in the "
                "bucket for all principals.".format(
                    rule_name, bucket, _describe_access_rule(stored[0])
                ),
            )
    module.exit_json(changed=changed)


def create_cors_policy(module, blade):
    """Create or update a CORS policy rule (and the policy if needed)"""
    changed = False
    api_version = get_rest_api_version(blade)
    ctx = _context_kwargs(module, api_version)
    bucket = module.params["name"]
    rule_name = module.params["rule"]

    res = blade.get_buckets_cross_origin_resource_sharing_policies_rules(
        bucket_names=[bucket], names=[rule_name], **ctx
    )
    if res.status_code == 200:
        if _cors_rule_matches(module, list(res.items)[0]):
            module.exit_json(changed=False)
        # The REST API has no PATCH for CORS policy rules, so a rule whose
        # content differs is replaced: delete it, then recreate it.
        changed = True
        if not module.check_mode:
            res = blade.delete_buckets_cross_origin_resource_sharing_policies_rules(
                bucket_names=[bucket], names=[rule_name], **ctx
            )
            if res.status_code != 200:
                module.fail_json(
                    msg="Failed to replace CORS policy rule {0} "
                    "in policy {1}. Error: {2}".format(
                        rule_name, bucket, get_error_message(res)
                    )
                )
    else:
        changed = True

    if not module.check_mode:
        res = blade.get_buckets_cross_origin_resource_sharing_policies(
            bucket_names=[bucket], **ctx
        )
        if res.status_code != 200 or res.total_item_count == 0:
            res = blade.post_buckets_cross_origin_resource_sharing_policies(
                bucket_names=[bucket], **ctx
            )
            if res.status_code != 200:
                module.fail_json(
                    msg="Failed to create initial CORS policy for {0}. "
                    "Error: {1}".format(bucket, get_error_message(res))
                )
        res = blade.post_buckets_cross_origin_resource_sharing_policies_rules(
            bucket_names=[bucket],
            names=[rule_name],
            rule=CrossOriginResourceSharingPolicyRulePost(
                allowed_methods=module.params["methods"],
                allowed_origins=module.params["origins"],
                allowed_headers=module.params["headers"],
            ),
            **ctx
        )
        if res.status_code != 200:
            module.fail_json(
                msg="Failed to create CORS policy rule {0} "
                "in policy {1}. Error: {2}".format(
                    rule_name, bucket, get_error_message(res)
                )
            )
        # As for access rules: fail if the array stored something other than
        # what was asked for, instead of replacing the rule on every run. The
        # POST response echoes the request, so read the rule back.
        res = blade.get_buckets_cross_origin_resource_sharing_policies_rules(
            bucket_names=[bucket], names=[rule_name], **ctx
        )
        stored = list(res.items) if res.status_code == 200 else []
        if stored and not _cors_rule_matches(module, stored[0]):
            module.fail_json(
                changed=True,
                msg="CORS policy rule {0} in policy {1} was not stored as "
                "requested: the array kept {2}. The FlashBlade REST API "
                "currently only supports all methods, origin '*' and "
                "header '*'.".format(rule_name, bucket, _describe_cors_rule(stored[0])),
            )
    module.exit_json(changed=changed)


def main():
    argument_spec = purefb_argument_spec()
    argument_spec.update(
        dict(
            state=dict(type="str", default="present", choices=["absent", "present"]),
            context=dict(type="str", default=""),
            policy_type=dict(
                type="str",
                default="access",
                choices=[
                    "access",
                    "cors",
                ],
            ),
            name=dict(type="str", required=True),
            rule=dict(type="str"),
            resources=dict(type="list", elements="str", default=["*"]),
            effect=dict(
                type="str",
                default="allow",
                choices=[
                    "allow",
                    "deny",
                ],
            ),
            methods=dict(
                type="list",
                elements="str",
                default=["GET", "PUT", "HEAD", "POST", "DELETE"],
                choices=[
                    # Only currently allowed option is:
                    # GET, PUT, HEAD, POST, DELETE
                    "GET",
                    "PUT",
                    "HEAD",
                    "POST",
                    "DELETE",
                ],
            ),
            principals=dict(type="bool", default="True"),
            origins=dict(type="list", elements="str", default=["*"]),
            headers=dict(type="list", elements="str", default=["*"]),
            actions=dict(
                type="list",
                elements="str",
                default="s3:GetObject",
                choices=[
                    "s3:*",
                    "s3:AbortMultipartUpload",
                    "s3:BypassGovernanceRetention",
                    "s3:CreateBucket",
                    "s3:DeleteBucket",
                    "s3:DeleteObject",
                    "s3:DeleteObjectVersion",
                    "s3:ExtendSafemodeRetentionPeriod",
                    "s3:GetBucketAcl",
                    "s3:GetBucketLocation",
                    "s3:GetBucketVersioning",
                    "s3:GetLifecycleConfiguration",
                    "s3:GetObject",
                    "s3:GetObjectAcl",
                    "s3:GetObjectLegalHold",
                    "s3:GetObjectLockConfiguration",
                    "s3:GetObjectRetention",
                    "s3:GetObjectTagging",
                    "s3:GetObjectVersion",
                    "s3:GetObjectVersionTagging",
                    "s3:ListAllMyBuckets",
                    "s3:ListBucket",
                    "s3:ListBucketMultipartUploads",
                    "s3:ListBucketVersions",
                    "s3:ListMultipartUploadParts",
                    "s3:PutBucketVersioning",
                    "s3:PutLifecycleConfiguration",
                    "s3:PutObject",
                    "s3:PutObjectLegalHold",
                    "s3:PutObjectLockConfiguration",
                    "s3:PutObjectRetention",
                    "s3:ResolveSafemodeConflicts",
                ],
            ),
        )
    )

    required_if = [["state", "present", ["rule"]]]
    module = AnsibleModule(
        argument_spec,
        required_if=required_if,
        supports_check_mode=True,
    )

    state = module.params["state"]
    if not HAS_PYPURECLIENT:
        module.fail_json(msg="py-pure-client sdk is required for this module")
    if (
        state == "present"
        and module.params["policy_type"] == "access"
        and module.params["effect"] != "allow"
    ):
        # The REST API documents allow as the only supported effect and the
        # array stores any other value as allow, so refuse rather than
        # silently create a rule that does the opposite of what was asked.
        module.fail_json(
            msg="effect '{0}' is not supported for bucket access policy rules; "
            "the FlashBlade REST API only supports 'allow'.".format(
                module.params["effect"]
            )
        )
    blade = get_system(module)
    api_version = get_rest_api_version(blade)
    if (
        LooseVersion(CONTEXT_API_VERSION) <= LooseVersion(api_version)
        and not module.params["context"]
    ):
        # If no context is provided set the context to the local array name
        fleet_res = blade.get_fleets()
        if fleet_res.status_code == 200 and list(fleet_res.items):
            module.params["context"] = list(blade.get_arrays().items)[0].name
    if LooseVersion(MIN_API_VERSION) > LooseVersion(api_version):
        module.fail_json(
            msg=(
                "Minimum FlashBlade REST version required: {0}".format(MIN_API_VERSION)
            )
        )
    if (
        LooseVersion(CONTEXT_API_VERSION) <= LooseVersion(api_version)
        and module.params["context"]
    ):
        res = blade.get_buckets(
            names=[module.params["name"]],
            destroyed=False,
            context_names=[module.params["context"]],
        )
    else:
        res = blade.get_buckets(names=[module.params["name"]], destroyed=False)
    if res.status_code != 200:
        module.fail_json(
            msg="Bucket {0} does not exist, or is destroyed.".format(
                module.params["name"]
            )
        )
    if module.params["policy_type"] == "access":
        if state == "present":
            create_access_policy(module, blade)
        else:
            delete_access_policy(module, blade)
    else:
        if state == "present":
            create_cors_policy(module, blade)
        else:
            delete_cors_policy(module, blade)

    module.exit_json(changed=False)


if __name__ == "__main__":
    main()
