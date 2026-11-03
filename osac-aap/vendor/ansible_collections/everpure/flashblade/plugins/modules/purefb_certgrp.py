#!/usr/bin/python
# -*- coding: utf-8 -*-

# (c) 2020, Simon Dodsley (simon@everpuredata.com)
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
module: purefb_certgrp
version_added: '1.4.0'
short_description: Manage FlashBlade Certifcate Groups
description:
- Manage certifcate groups for FlashBlades
author:
- Everpure Ansible Team (@sdodsley) <pure-ansible-team@everpuredata.com>
options:
  state:
    description:
    - Create or delete certifcate group
    default: present
    type: str
    choices: [ absent, present ]
  name:
    description:
    - Name of the certificate group
    type: str
    required: true
  certificates:
    description:
    - List of certificates to add to the group (I(state=present)) or to
      remove from the group (I(state=absent)).
    - Certificates already in the group are left alone, so repeated runs
      report no change.
    - With I(state=absent) and no certificates the whole group is deleted.
    type: list
    elements: str
extends_documentation_fragment:
- everpure.flashblade.everpure.fb
"""

EXAMPLES = r"""
- name: Create a certifcate group
  everpure.flashblade.purefb_certgrp:
    name: test_grp
    fb_url: 10.10.10.2
    api_token: T-9f276a18-50ab-446e-8a0c-666a3529a1b6
- name: Create a certificate group and add existing certificates
  everpure.flashblade.purefb_certgrp:
    name: test_grp
    certificates:
      - cert1
      - cert2
    fb_url: 10.10.10.2
    api_token: T-9f276a18-50ab-446e-8a0c-666a3529a1b6
- name: Delete a certifcate from a group
  everpure.flashblade.purefb_certgrp:
    name: test_grp
    certificates:
      - cert2
    state: absent
    fb_url: 10.10.10.2
    api_token: T-9f276a18-50ab-446e-8a0c-666a3529a1b6
- name: Delete a certifcate group
  everpure.flashblade.purefb_certgrp:
    name: test_grp
    state: absent
    fb_url: 10.10.10.2
    api_token: T-9f276a18-50ab-446e-8a0c-666a3529a1b6
"""

RETURN = r"""
"""

from ansible.module_utils.basic import AnsibleModule
from ansible_collections.everpure.flashblade.plugins.module_utils.purefb import (
    get_system,
    purefb_argument_spec,
)
from ansible_collections.everpure.flashblade.plugins.module_utils.common import (
    get_error_message,
)


def delete_certgrp(module, blade):
    """Delete certificate group"""
    changed = True
    if not module.check_mode:
        res = blade.delete_certificate_groups(names=[module.params["name"]])
        if res.status_code != 200:
            module.fail_json(
                msg="Failed to delete certificate group {0}. Error: {1}".format(
                    module.params["name"], get_error_message(res)
                )
            )
    module.exit_json(changed=changed)


def create_certgrp(module, blade):
    """Create certificate group"""
    changed = True
    if not module.check_mode:
        res = blade.post_certificate_groups(names=[module.params["name"]])
        if res.status_code != 200:
            module.fail_json(
                msg="Failed to create certificate group {0}. Error: {1}".format(
                    module.params["name"], get_error_message(res)
                )
            )
        if module.params["certificates"]:
            res = blade.post_certificate_groups_certificates(
                certificate_names=module.params["certificates"],
                certificate_group_names=[module.params["name"]],
            )
            if res.status_code != 200:
                blade.delete_certificate_groups(names=[module.params["name"]])
                module.fail_json(
                    msg="Failed to add certificates {0}. Error: {1}".format(
                        module.params["certificates"], get_error_message(res)
                    )
                )
    module.exit_json(changed=changed)


def get_group_certificates(module, blade):
    """Return the names of the certificates currently in the group"""
    res = blade.get_certificate_groups_certificates(
        certificate_group_names=[module.params["name"]]
    )
    if res.status_code != 200:
        module.fail_json(
            msg="Failed to get certificate list for group {0}. Error: {1}".format(
                module.params["name"], get_error_message(res)
            )
        )
    return [item.member.name for item in list(res.items)]


def update_certgrp(module, blade):
    """Add certificates to, or remove certificates from, an existing group"""
    changed = False
    current = get_group_certificates(module, blade)
    if module.params["state"] == "present":
        to_add = [cert for cert in module.params["certificates"] if cert not in current]
        if to_add:
            changed = True
            if not module.check_mode:
                res = blade.post_certificate_groups_certificates(
                    certificate_names=to_add,
                    certificate_group_names=[module.params["name"]],
                )
                if res.status_code != 200:
                    module.fail_json(
                        msg="Failed to add certificates {0} to group {1}. Error: {2}".format(
                            to_add, module.params["name"], get_error_message(res)
                        )
                    )
    else:
        to_remove = [cert for cert in module.params["certificates"] if cert in current]
        if to_remove:
            changed = True
            if not module.check_mode:
                res = blade.delete_certificate_groups_certificates(
                    certificate_names=to_remove,
                    certificate_group_names=[module.params["name"]],
                )
                if res.status_code != 200:
                    module.fail_json(
                        msg="Failed to delete certificates {0} from group {1}. Error: {2}".format(
                            to_remove, module.params["name"], get_error_message(res)
                        )
                    )
    module.exit_json(changed=changed)


def main():
    argument_spec = purefb_argument_spec()
    argument_spec.update(
        dict(
            state=dict(type="str", default="present", choices=["absent", "present"]),
            name=dict(type="str", required=True),
            certificates=dict(type="list", elements="str"),
        )
    )

    module = AnsibleModule(argument_spec, supports_check_mode=True)

    state = module.params["state"]
    blade = get_system(module)

    certgrp = bool(
        blade.get_certificate_groups(names=[module.params["name"]]).status_code == 200
    )

    if certgrp and state == "present" and module.params["certificates"]:
        update_certgrp(module, blade)
    elif state == "present" and not certgrp:
        create_certgrp(module, blade)
    elif state == "absent" and certgrp:
        if module.params["certificates"]:
            update_certgrp(module, blade)
        else:
            delete_certgrp(module, blade)

    module.exit_json(changed=False)


if __name__ == "__main__":
    main()
