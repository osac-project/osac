#!/usr/bin/python
# -*- coding: utf-8 -*-

# Copyright: (c) 2026, Simon Dodsley (simon@purestorage.com)
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
module: purefb_realm
version_added: '1.25.0'
short_description: Manage realms on Everpure FlashBlades
description:
- Create, delete or modify realms on Everpure FlashBlades.
author:
- Everpure Ansible Team (@sdodsley) <pure-ansible-team@purestorage.com>
options:
  name:
    description:
    - The name of the realm.
    - This has to be unique and not equal to any existing realm or pod.
    type: str
    required: true
  state:
    description:
    - Define whether the realm should exist or not.
    type: str
    default: present
    choices: [ absent, present ]
  eradicate:
    description:
    - Define whether to eradicate the realm on delete or leave in trash.
    type : bool
    default: false
  qos_policy:
    description:
    - QoS policy to apply to the realm
    type: str
  rename:
    description:
    - Value to rename the specified realm to
    - This has to be unique and not equal to any existing realm or pods.
    type: str
extends_documentation_fragment:
- purestorage.flashblade.purestorage.fb
"""

EXAMPLES = r"""
- name: Create new realm
  purestorage.flashblade.purefb_realm:
    name: foo
    qos_policy: realm1_qos
    iops_qos: 100
    fa_url: 10.10.10.2
    api_token: T-9f276a18-50ab-446e-8a0c-666a3529a1b6

- name: Destroy realm
  purestorage.flashblade.purefb_realm:
    name: foo
    fa_url: 10.10.10.2
    api_token: T-9f276a18-50ab-446e-8a0c-666a3529a1b6
    state: absent

- name: Recover deleted realm
  purestorage.flashblade.purefb_realm:
    name: foo
    fa_url: 10.10.10.2
    api_token: T-9f276a18-50ab-446e-8a0c-666a3529a1b6

- name: Destroy and Eradicate realm
  purestorage.flashblade.purefb_realm:
    name: foo
    eradicate: true
    fa_url: 10.10.10.2
    api_token: T-9f276a18-50ab-446e-8a0c-666a3529a1b6
    state: absent

- name: Rename realm foo to bar
  purestorage.flashblade.purefb_realm:
    name: foo
    rename: bar
    fa_url: 10.10.10.2
    api_token: T-9f276a18-50ab-446e-8a0c-666a3529a1b6
"""

RETURN = r"""
"""

HAS_PURESTORAGE = True
try:
    from pypureclient.flashblade import RealmPatch
except ImportError:
    HAS_PURESTORAGE = False

from ansible.module_utils.basic import AnsibleModule
from ansible_collections.purestorage.flashblade.plugins.module_utils.purefb import (
    get_system,
    purefb_argument_spec,
)
from ansible_collections.purestorage.flashblade.plugins.module_utils.version import (
    LooseVersion,
)
from ansible_collections.purestorage.flashblade.plugins.module_utils.common import (
    get_error_message,
)

MINIMUM_API_VERSION = "2.19"


def get_pending_realm(module, blade):
    """Get Deleted realm"""
    res = blade.get_realms(destroyed=True, names=[module.params["name"]])
    if res.status_code == 200:
        return True
    return None


def get_policy(module, blade):
    """Get QoS Policy"""
    res = blade.get_qos_policies(names=[module.params["qos_policy"]])
    if res.status_code == 200:
        return True
    return None


def get_realm(module, blade):
    """Get Realm"""
    res = blade.get_realms(destroyed=False, names=[module.params["name"]])
    if res.status_code == 200:
        return True
    return None


def rename_realm(module, blade):
    changed = True
    if not module.check_mode:
        res = blade.patch_realm(
            names=[module.params["name"]],
            realm=RealmPatch(name=module.params["rename"]),
        )
        if res.status_code != 200:
            module.fail_json(
                msg="Rename to {0} failed. Error: {1}".format(
                    module.params["rename"], get_error_message(res)
                )
            )
    module.exit_json(changed=changed)


def make_realm(module, blade):
    """Create Realm"""
    changed = True
    if not module.check_mode:
        res = blade.post_realms(names=[module.params["name"]])
        if res.status_code != 200:
            module.fail_json(
                msg="Creation of realm {0} failed. Error: {1}".format(
                    module.params["name"], get_error_message(res)
                )
            )
    if module.params["qos_policy"]:
        if get_policy(module, blade):
            res = blade.post_qos_policies_members(
                policy_names=[module.params["qos_policy"]],
                member_names=[module.params["name"]],
            )
            if res.status_code != 200:
                module.fail_json(
                    msg="QoS policy assignment failed for realm {0}. Error: {1}".format(
                        module.params["name"], get_error_message(res)
                    )
                )
    module.exit_json(changed=changed)


def update_realm(module, blade):
    """Update Realm QoS Policy"""
    changed = False
    # Get current QoS policy for the realm
    policy_members = blade.get_qos_policies_members(
        member_types=["realms"], member_names=[module.params["name"]]
    ).items
    first_member = next(iter(policy_members), None)
    current_policy = getattr(getattr(first_member, "policy", None), "name", None)
    if module.params["qos_policy"] and current_policy != module.params["qos_policy"]:
        changed = True
        if not module.check_mode:
            res = blade.delete_qos_policies_members(
                policy_names=[current_policy],
                member_names=[module.params["name"]],
                member_types=["realms"],
            )
            if res.status_code != 200:
                module.fail_json(
                    msg="Exisitng QoS policy removal failed for realm {0}. Error: {1}".format(
                        module.params["name"], get_error_message(res)
                    )
                )
            res = blade.post_qos_policies_members(
                policy_names=[module.params["qos_policy"]],
                member_names=[module.params["name"]],
                member_types=["realms"],
            )
            if res.status_code != 200:
                module.fail_json(
                    msg="QoS policy assignment failed for realm {0}. Error: {1}".format(
                        module.params["name"], get_error_message(res)
                    )
                )
    module.exit_json(changed=changed)


def recover_realm(module, blade):
    """Recover Realm"""
    changed = True
    if not module.check_mode:
        res = blade.patch_realms(
            names=[module.params["name"]], realm=RealmPatch(destroyed=False)
        )
        if res.status_code != 200:
            module.fail_json(
                msg="Recovery of realm {0} failed. Error: {1}".format(
                    module.params["name"], get_error_message(res)
                )
            )

    module.exit_json(changed=changed)


def eradicate_realm(module, blade):
    """Eradicate Realm"""
    changed = True
    if not module.check_mode:
        res = blade.delete_realms(
            names=[module.params["name"]],
        )
        if res.status_code != 200:
            module.fail_json(
                msg="Eradicating realm {0} failed. Error: {1}".format(
                    module.params["name"], get_error_message(res)
                )
            )
    module.exit_json(changed=changed)


def delete_realm(module, blade):
    """Delete Realm"""
    changed = True
    if not module.check_mode:
        res = blade.patch_realms(
            names=[module.params["name"]],
            realm=RealmPatch(destroyed=True),
        )
        if res.status_code != 200:
            module.fail_json(
                msg="Deleting realm {0} failed. Error: {1}".format(
                    module.params["name"], get_error_message(res)
                )
            )
    if module.params["eradicate"]:
        eradicate_realm(module, blade)

    module.exit_json(changed=changed)


def main():
    argument_spec = purefb_argument_spec()
    argument_spec.update(
        dict(
            name=dict(type="str", required=True),
            state=dict(type="str", default="present", choices=["absent", "present"]),
            qos_policy=dict(type="str"),
            eradicate=dict(type="bool", default=False),
            rename=dict(type="str"),
        )
    )

    module = AnsibleModule(argument_spec, supports_check_mode=True)

    state = module.params["state"]
    blade = get_system(module)
    api_version = list(blade.get_versions().items)
    if LooseVersion(MINIMUM_API_VERSION) > LooseVersion(api_version):
        module.fail_json(
            msg="Realms are not supported. Purity//FB 4.6.1, or higher, is required."
        )
    realm = get_realm(module, blade)
    xrealm = get_pending_realm(module, blade)

    if xrealm and state == "present":
        recover_realm(module, blade)
    elif realm and state == "absent":
        delete_realm(module, blade)
    elif xrealm and state == "absent" and module.params["eradicate"]:
        eradicate_realm(module, blade)
    elif not realm and not xrealm and state == "present":
        make_realm(module, blade)
    elif state == "present" and realm and module.params["rename"] and not xrealm:
        rename_realm(module, blade)
    elif realm and state == "present":
        update_realm(module, blade)
    elif realm is None and state == "absent":
        module.exit_json(changed=False)

    module.exit_json(changed=False)


if __name__ == "__main__":
    main()
