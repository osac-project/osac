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
module: purefb_s3acc_export
version_added: '1.26.0'
short_description: Manage FlashBlade Object Store Account exports
description:
- Create, update or delete a FlashBlade object-store account export.
- An account export binds an object-store account to a server using an
  S3 export policy, making the account's buckets visible for S3 through
  that server.
- The account export is uniquely identified by the (I(account), I(server))
  pair.
author:
- Pure Storage Ansible Team (@sdodsley) <pure-ansible-team@purestorage.com>
options:
  state:
    description:
    - Define whether the account export should exist or not.
    default: present
    choices: [ absent, present ]
    type: str
  account:
    description:
    - Name of the object store account to export.
    type: str
    required: true
  server:
    description:
    - Name of the server the account will be exported on.
    type: str
    required: true
  policy:
    description:
    - Name of the S3 export policy used for the export.
    - Required when creating a new account export.
    - When supplied on update, the export will be re-pointed at the
      named policy.
    type: str
  enabled:
    description:
    - If C(true) the account export is enabled.
    - Defaults to C(true) on creation if not specified.
    type: bool
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
- name: Export account acme on server fb-01 using policy acme-export
  purestorage.flashblade.purefb_s3acc_export:
    account: acme
    server: fb-01
    policy: acme-export
    fb_url: 10.10.10.2
    api_token: T-68618f31-0c9e-4e57-aa44-5306a2cf10e3

- name: Disable an existing account export
  purestorage.flashblade.purefb_s3acc_export:
    account: acme
    server: fb-01
    enabled: false
    fb_url: 10.10.10.2
    api_token: T-68618f31-0c9e-4e57-aa44-5306a2cf10e3

- name: Re-point an existing account export at a different policy
  purestorage.flashblade.purefb_s3acc_export:
    account: acme
    server: fb-01
    policy: acme-export-v2
    fb_url: 10.10.10.2
    api_token: T-68618f31-0c9e-4e57-aa44-5306a2cf10e3

- name: Delete an account export
  purestorage.flashblade.purefb_s3acc_export:
    account: acme
    server: fb-01
    state: absent
    fb_url: 10.10.10.2
    api_token: T-68618f31-0c9e-4e57-aa44-5306a2cf10e3
"""

RETURN = r"""
"""

HAS_PYPURECLIENT = True
try:
    from pypureclient.flashblade import (
        ObjectStoreAccountExportPost,
        ObjectStoreAccountExportPatch,
        Reference,
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


def _get_export(module, blade):
    """Return the existing account export object or None."""
    filter_string = "member.name='{0}' and server.name='{1}'".format(
        module.params["account"], module.params["server"]
    )
    res = blade.get_object_store_account_exports(
        filter=filter_string, **_context_kwargs(module)
    )
    if res.status_code != 200:
        return None
    items = list(res.items)
    return items[0] if items else None


def delete_export(module, blade, export):
    """Delete an account export."""
    changed = True
    if not module.check_mode:
        res = blade.delete_object_store_account_exports(
            names=[getattr(export, "name", None)], **_context_kwargs(module)
        )
        if res.status_code != 200:
            module.fail_json(
                msg="Failed to delete account export for account {0} on server {1}. Error: {2}".format(
                    module.params["account"],
                    module.params["server"],
                    get_error_message(res),
                )
            )
    module.exit_json(changed=changed)


def create_export(module, blade):
    """Create a new account export."""
    if not module.params["policy"]:
        module.fail_json(msg="policy is required when creating a new account export.")
    changed = True
    post_kwargs = {"server": Reference(name=module.params["server"])}
    if module.params["enabled"] is not None:
        post_kwargs["export_enabled"] = module.params["enabled"]
    if not module.check_mode:
        res = blade.post_object_store_account_exports(
            member_names=[module.params["account"]],
            policy_names=[module.params["policy"]],
            object_store_account_export=ObjectStoreAccountExportPost(**post_kwargs),
            **_context_kwargs(module),
        )
        if res.status_code != 200:
            module.fail_json(
                msg="Failed to create account export for account {0} on server {1}. Error: {2}".format(
                    module.params["account"],
                    module.params["server"],
                    get_error_message(res),
                )
            )
    module.exit_json(changed=changed)


def update_export(module, blade, export):
    """Update an existing account export."""
    changed = False
    patch_kwargs = {}

    if module.params["enabled"] is not None and module.params["enabled"] != getattr(
        export, "enabled", None
    ):
        patch_kwargs["export_enabled"] = module.params["enabled"]

    if module.params["policy"]:
        current_policy = getattr(getattr(export, "policy", None), "name", None)
        if module.params["policy"] != current_policy:
            patch_kwargs["policy"] = Reference(name=module.params["policy"])

    if patch_kwargs:
        changed = True
        if not module.check_mode:
            res = blade.patch_object_store_account_exports(
                names=[getattr(export, "name", None)],
                object_store_account_export=ObjectStoreAccountExportPatch(
                    **patch_kwargs
                ),
                **_context_kwargs(module),
            )
            if res.status_code != 200:
                module.fail_json(
                    msg="Failed to update account export for account {0} on server {1}. Error: {2}".format(
                        module.params["account"],
                        module.params["server"],
                        get_error_message(res),
                    )
                )

    module.exit_json(changed=changed)


def main():
    argument_spec = purefb_argument_spec()
    argument_spec.update(
        dict(
            state=dict(type="str", default="present", choices=["absent", "present"]),
            account=dict(type="str", required=True),
            server=dict(type="str", required=True),
            policy=dict(type="str"),
            enabled=dict(type="bool"),
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
    export = _get_export(module, blade)

    if state == "present" and export is None:
        create_export(module, blade)
    elif state == "present" and export is not None:
        update_export(module, blade, export)
    elif state == "absent" and export is not None:
        delete_export(module, blade, export)

    module.exit_json(changed=False)


if __name__ == "__main__":
    main()
