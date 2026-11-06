#!/usr/bin/python
# -*- coding: utf-8 -*-

# (c) 2021, Simon Dodsley (simon@everpuredata.com)
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
module: purefb_admin
version_added: '1.8.0'
short_description: Configure Everpure FlashBlade Global Admin settings
description:
- Set global admin settings for the FlashBlade
- Options that are omitted leave the corresponding setting unchanged.
author:
- Everpure Ansible Team (@sdodsley) <pure-ansible-team@everpuredata.com>
options:
  max_login:
    description:
    - Maximum number of failed logins before account is locked
    type: int
  min_password:
    description:
    - Minimum user password length
    - Range between 1 and 100
    type: int
  lockout:
    description:
    - Account lockout duration, in seconds, after max_login exceeded
    - Range between 1 second and 90 days (7776000 seconds)
    type: int
extends_documentation_fragment:
- everpure.flashblade.everpure.fb
"""

EXAMPLES = r"""
- name: Set global login parameters
  everpure.flashblade.purefb_admin:
    max_login: 5
    min_password: 10
    lockout: 300
    fb_url: 10.10.10.2
    api_token: T-55a68eb5-c785-4720-a2ca-8b03903bf641
"""

RETURN = r"""
"""

HAS_PYPURECLIENT = True
try:
    from pypureclient.flashblade import AdminSetting
except ImportError:
    HAS_PYPURECLIENT = False


from ansible.module_utils.basic import AnsibleModule
from ansible_collections.everpure.flashblade.plugins.module_utils.purefb import (
    get_system,
    purefb_argument_spec,
)
from ansible_collections.everpure.flashblade.plugins.module_utils.common import (
    get_error_message,
)


def main():
    argument_spec = purefb_argument_spec()
    argument_spec.update(
        dict(
            max_login=dict(type="int"),
            min_password=dict(type="int", no_log=False),
            lockout=dict(type="int"),
        )
    )

    module = AnsibleModule(argument_spec, supports_check_mode=True)

    if not HAS_PYPURECLIENT:
        module.fail_json(msg="py-pure-client sdk is required for this module")
    if (
        module.params["lockout"] is not None
        and not 1 <= module.params["lockout"] <= 7776000
    ):
        module.fail_json(msg="Lockout must be between 1 and 7776000 seconds")
    if (
        module.params["min_password"] is not None
        and not 1 <= module.params["min_password"] <= 100
    ):
        module.fail_json(msg="Minimum password length must be between 1 and 100")
    blade = get_system(module)
    changed = False
    # Settings that are omitted keep their current value; the array reports
    # a disabled lockout rule as an absent (null) field.
    current_settings = list(blade.get_admins_settings().items)[0]
    lockout = getattr(current_settings, "lockout_duration", None)
    max_login = getattr(current_settings, "max_login_attempts", None)
    min_password = getattr(current_settings, "min_password_length", None)
    if (
        module.params["min_password"] is not None
        and min_password != module.params["min_password"]
    ):
        changed = True
        min_password = module.params["min_password"]
    if (
        module.params["lockout"] is not None
        and lockout != module.params["lockout"] * 1000
    ):
        changed = True
        lockout = module.params["lockout"] * 1000
    if (
        module.params["max_login"] is not None
        and max_login != module.params["max_login"]
    ):
        changed = True
        max_login = module.params["max_login"]

    if changed and not module.check_mode:
        admin = AdminSetting(
            min_password_length=min_password,
            max_login_attempts=max_login,
            lockout_duration=lockout,
        )

        res = blade.patch_admins_settings(admin_setting=admin)
        if res.status_code != 200:
            module.fail_json(
                msg="Failed to change Global Admin settings. Error: {0}".format(
                    get_error_message(res)
                )
            )
    module.exit_json(changed=changed)


if __name__ == "__main__":
    main()
