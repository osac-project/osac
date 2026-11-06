#!/usr/bin/python
# -*- coding: utf-8 -*-

# Copyright: (c) 2026, Simon Dodsley (simon@everpuredata.com)
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
module: purefb_resource_access
version_added: '1.28.0'
short_description: Manage Resource Access on Everpure FlashBlades
description:
    - Create or delete resource access on Everpure FlashBlades.
author:
    - Everpure Ansible Team (@sdodsley) <pure-ansible-team@everpuredata.com>
    - Leo Wahlandt (@valen98) <wahlandtleo@gmail.com>
options:
  resource_type:
    description:
      - The type of resource you want to add, given as the REST collection
        name of the resource.
      - The set of valid resource types is defined by the array and grows
        with the REST API version, so it is not validated by this module.
      - For a realm scope, Purity//FB 4.6 accepts the array-level resources
        that can be delegated to a realm, C(subnets) and C(dns).
      - Policies (for example NFS export policies) live at the array level
        and cannot be shared with a realm through a resource access; the
        array rejects them with C(Invalid resource type).
    type: str
    required: true
  resource_name:
    description:
      - The name of the resource you want to target.
    type: str
    required: true
  state:
    description:
      - Define whether the resource access should exist or not.
    type: str
    default: present
    choices: [ absent, present ]
  scope_type:
    description:
      - The type of scopes.
      - When using Realms, this value will be 'realms'
      - As with I(resource_type), the set of valid scope types is defined
        by the array and is not validated by this module.
    type: str
    required: true
  scope_name:
    description:
      - The name of the scope, that should be using the resource.
      - You can not rename a resource access.
      - Resource access are immutable. You have to delete it and then create a new one.
    type: str
    required: true
extends_documentation_fragment:
- everpure.flashblade.everpure.fb
"""

EXAMPLES = r"""
- name: Delegate subnet subnet_foo to realm realm_foo
  everpure.flashblade.purefb_resource_access:
    resource_type: subnets
    resource_name: subnet_foo
    scope_type: realms
    scope_name: realm_foo
    fb_url: 10.10.10.2
    api_token: T-9f276a18-50ab-446e-8a0c-666a3529a1b6

- name: Share the management DNS configuration with realm realm_foo
  everpure.flashblade.purefb_resource_access:
    resource_type: dns
    resource_name: management
    scope_type: realms
    scope_name: realm_foo
    fb_url: 10.10.10.2
    api_token: T-9f276a18-50ab-446e-8a0c-666a3529a1b6

- name: Revoke the subnet delegation (the subnet itself is not deleted)
  everpure.flashblade.purefb_resource_access:
    resource_type: subnets
    resource_name: subnet_foo
    scope_type: realms
    scope_name: realm_foo
    state: absent
    fb_url: 10.10.10.2
    api_token: T-9f276a18-50ab-446e-8a0c-666a3529a1b6
"""

RETURN = r"""
"""

HAS_PYPURECLIENT = True
try:
    from pypureclient.flashblade import ResourceAccessPost
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

MINIMUM_API_VERSION = "2.19"


def get_resource_access(module, blade):
    """Get resource access"""
    filter_string = (
        "resource.resource_type='"
        + module.params["resource_type"]
        + "' and scope.name='"
        + module.params["scope_name"]
        + "' and scope.resource_type='"
        + module.params["scope_type"]
        + "'"
    )
    res = blade.get_resource_accesses(filter=filter_string)
    if res.status_code == 200 and res.total_item_count:
        items = list(res.items)
        for item in items:
            if item.resource.name == module.params["resource_name"]:
                return True, item.id
    return False, False


def create_resource_access(module, blade):
    """Create a new resource_access"""
    changed = True
    if not module.check_mode:
        resource = ResourceAccessPost(
            resource={
                "name": module.params["resource_name"],
                "resource_type": module.params["resource_type"],
            },
            scope={
                "name": module.params["scope_name"],
                "resource_type": module.params["scope_type"],
            },
        )
        res = blade.post_resource_accesses_batch(items=[resource])
        if res.status_code != 200:
            module.fail_json(
                msg="Create resource access between {0} and {1} failed. Error: {2}".format(
                    module.params["scope_name"],
                    module.params["resource_type"],
                    get_error_message(res),
                )
            )
    module.exit_json(changed=changed)


def delete_resource_access(module, blade, access_id):
    """Delete resource access"""
    changed = True
    if not module.check_mode:
        res = blade.delete_resource_accesses(ids=[access_id])
        if res.status_code != 200:
            module.fail_json(
                msg="Delete resource access between {0} and {1} failed. Error: {2}".format(
                    module.params["scope_name"],
                    module.params["resource_type"],
                    get_error_message(res),
                )
            )
    module.exit_json(changed=changed)


def main():
    argument_spec = purefb_argument_spec()
    argument_spec.update(
        dict(
            resource_type=dict(type="str", required=True),
            resource_name=dict(type="str", required=True),
            state=dict(type="str", default="present", choices=["absent", "present"]),
            scope_type=dict(type="str", required=True),
            scope_name=dict(type="str", required=True),
        )
    )

    module = AnsibleModule(argument_spec, supports_check_mode=True)
    if not HAS_PYPURECLIENT:
        module.fail_json(msg="py-pure-client sdk is required for this module")

    state = module.params["state"]
    blade = get_system(module)
    api_version = get_rest_api_version(blade)
    if LooseVersion(MINIMUM_API_VERSION) > LooseVersion(api_version):
        module.fail_json(
            msg="Resource Access is not supported. Purity//FB 4.6.1, or higher, is required."
        )
    resource, access_id = get_resource_access(module, blade)

    if not resource and state == "present":
        create_resource_access(module, blade)
    elif resource and state == "absent":
        delete_resource_access(module, blade, access_id)

    module.exit_json(changed=False)


if __name__ == "__main__":
    main()
