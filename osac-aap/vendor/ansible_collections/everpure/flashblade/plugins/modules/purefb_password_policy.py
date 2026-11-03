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
module: purefb_password_policy
version_added: '1.28.0'
short_description: Manage the FlashBlade management password policy
description:
- Configure the array-wide password policy that applies to local FlashBlade
  management users authenticating with a password (Web UI, CLI/SSH).
- The policy is a singleton configuration resource, normally named
  C(management), that always exists on the array. It cannot be created or
  deleted, only modified, so this module has no I(state) option.
- Passwords of AD/LDAP users are governed by the external directory
  service and are not affected by this policy.
author:
- Pure Storage Ansible Team (@avk) <pure-ansible-team@everpuredata.com>
notes:
- Options that are omitted leave the corresponding policy setting unchanged.
- Durations are given in seconds; the array stores them in milliseconds.
- Rules disabled by setting 0 are reported by the array as absent (null)
  values; the GUI displays them as 0.
- The array only allows I(max_login) and I(lockout) to transition between
  enabled (non-zero) and disabled (0) together, so supply both in the same
  task when enabling or disabling the lockout rules. Once both are enabled,
  each can be changed independently.
- Password policies require FlashBlade REST API 2.16 (Purity//FB 4.5.2) or
  higher; I(max_password_age) additionally requires REST API 2.18
  (Purity//FB 4.6.0) or higher.
- The I(min_password), I(max_login) and I(lockout) options use the same
  names and units as the equivalent options of
  M(everpure.flashblade.purefb_admin).
options:
  name:
    description:
    - Name of the password policy.
    - The array provides a single built-in policy named C(management).
    type: str
    default: management
  enabled:
    description:
    - Whether policy enforcement is enabled.
    type: bool
  min_password:
    description:
    - Minimum number of characters required in a password.
    - Range between 1 and 100.
    type: int
  max_login:
    description:
    - Maximum number of failed login attempts allowed before the user is
      locked out.
    - Range between 1 and 100.
    - Set to 0 to disable the maximum login attempts rule.
    type: int
  lockout:
    description:
    - Duration, in seconds, of the account lockout after I(max_login)
      is exceeded.
    - Range between 1 second and 90 days (7776000 seconds).
    - Set to 0 to disable the lockout duration rule.
    type: int
  password_history:
    description:
    - Number of previous passwords tracked to prevent reuse.
    - Range between 0 and 64.
    type: int
  min_password_age:
    description:
    - Minimum time, in seconds, that must pass before a password can be
      changed again.
    - The array stores this with one-hour precision, so the value must be a
      whole number of hours (a multiple of 3600).
    - Range between 0 and 7 days (604800 seconds).
    type: int
  max_password_age:
    description:
    - Maximum time, in seconds, a password remains valid before the user is
      required to change it.
    - Set to 0 to disable password expiration.
    - The array stores this with one-hour precision, so the value must be a
      whole number of hours (a multiple of 3600).
    - Range between 1 day (86400 seconds) and 99999 days (8639913600
      seconds), and must be greater than I(min_password_age).
    - Requires FlashBlade REST API 2.18 (Purity//FB 4.6.0) or higher.
    type: int
  min_character_groups:
    description:
    - Number of character groups (lowercase letters, uppercase letters,
      numbers, special characters) required to be present in a password.
    - Range between 1 and 4.
    - Set to 0 to disable the character group rule.
    type: int
  min_characters_per_group:
    description:
    - Minimum number of characters per group to count the group as present.
    - Must be 1 or greater.
    - Set to 0 to disable the characters per group rule.
    type: int
  enforce_username_check:
    description:
    - Whether the username is prevented from being a substring of the
      password.
    - Only applies to usernames of 4 characters and longer.
    type: bool
  enforce_dictionary_check:
    description:
    - Whether passwords are tested against a dictionary of known leaked
      passwords.
    - Requires passwords longer than 6 characters.
    type: bool
extends_documentation_fragment:
- everpure.flashblade.everpure.fb
"""

EXAMPLES = r"""
- name: Enforce password complexity and length
  everpure.flashblade.purefb_password_policy:
    min_password: 12
    min_character_groups: 3
    min_characters_per_group: 1
    enforce_username_check: true
    enforce_dictionary_check: true
    fb_url: 10.10.10.2
    api_token: T-55a68eb5-c785-4720-a2ca-8b03903bf641

- name: Lock accounts for 30 minutes after 5 failed logins
  everpure.flashblade.purefb_password_policy:
    max_login: 5
    lockout: 1800
    fb_url: 10.10.10.2
    api_token: T-55a68eb5-c785-4720-a2ca-8b03903bf641

- name: Expire passwords after 90 days, at most one change per day
  everpure.flashblade.purefb_password_policy:
    max_password_age: 7776000
    min_password_age: 86400
    password_history: 5
    fb_url: 10.10.10.2
    api_token: T-55a68eb5-c785-4720-a2ca-8b03903bf641

- name: Disable password expiration
  everpure.flashblade.purefb_password_policy:
    max_password_age: 0
    fb_url: 10.10.10.2
    api_token: T-55a68eb5-c785-4720-a2ca-8b03903bf641

- name: Disable password policy enforcement
  everpure.flashblade.purefb_password_policy:
    enabled: false
    fb_url: 10.10.10.2
    api_token: T-55a68eb5-c785-4720-a2ca-8b03903bf641
"""

RETURN = r"""
"""

HAS_PYPURECLIENT = True
try:
    from pypureclient.flashblade import PasswordPolicy
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

MIN_PASSWORD_POLICY_API_VERSION = "2.16"
MAX_PASSWORD_AGE_API_VERSION = "2.18"

# Module option -> API field, value passed unchanged. The settings shared
# with purefb_admin (max_login, min_password) reuse its option names.
PLAIN_PARAMS = {
    "enabled": "enabled",
    "enforce_dictionary_check": "enforce_dictionary_check",
    "enforce_username_check": "enforce_username_check",
    "max_login": "max_login_attempts",
    "min_character_groups": "min_character_groups",
    "min_characters_per_group": "min_characters_per_group",
    "min_password": "min_password_length",
    "password_history": "password_history",
}
# Module option in seconds -> API field in milliseconds.
SECONDS_PARAMS = {
    "lockout": "lockout_duration",
    "min_password_age": "min_password_age",
    "max_password_age": "max_password_age",
}
# Inclusive (min, max) bounds in module units; None means unbounded.
PARAM_RANGES = {
    "max_login": (0, 100),
    "min_password": (1, 100),
    "password_history": (0, 64),
    "min_character_groups": (0, 4),
    "min_characters_per_group": (0, None),
    "lockout": (0, 7776000),
    "min_password_age": (0, 604800),
    "max_password_age": (0, 8639913600),
}
# The array stores password ages with one-hour precision; finer values
# would either be rejected or silently rounded, breaking idempotency.
HOUR_PRECISION_PARAMS = ("min_password_age", "max_password_age")
# A rule set to 0 (disabled) is reported by the array as an absent field,
# so a missing attribute on the current policy compares equal to 0.
NULL_WHEN_DISABLED_FIELDS = (
    "lockout_duration",
    "max_login_attempts",
    "max_password_age",
    "min_character_groups",
    "min_characters_per_group",
    "min_password_age",
    "password_history",
)


def _validate_params(module):
    for param in sorted(PARAM_RANGES):
        value = module.params[param]
        if value is None:
            continue
        low, high = PARAM_RANGES[param]
        if value < low or (high is not None and value > high):
            if high is None:
                module.fail_json(msg="{0} must be {1} or greater".format(param, low))
            module.fail_json(
                msg="{0} must be between {1} and {2}".format(param, low, high)
            )
    for param in HOUR_PRECISION_PARAMS:
        value = module.params[param]
        if value is not None and value % 3600:
            module.fail_json(
                msg=(
                    "{0} must be a whole number of hours (a multiple of "
                    "3600 seconds)"
                ).format(param)
            )
    max_age = module.params["max_password_age"]
    if max_age is not None and max_age != 0 and max_age < 86400:
        module.fail_json(
            msg=(
                "max_password_age must be 0 (expiration disabled) or at "
                "least 86400 seconds (1 day)"
            )
        )
    min_age = module.params["min_password_age"]
    if max_age and min_age is not None and max_age <= min_age:
        module.fail_json(msg="max_password_age must be greater than min_password_age")


def get_policy(module, blade):
    """The singleton policy object.

    The policy always exists on a supported array, so any failure to read
    it fails the task.
    """
    res = blade.get_password_policies(names=[module.params["name"]])
    if res.status_code != 200:
        module.fail_json(
            msg="Failed to read password policy {0}. Error: {1}".format(
                module.params["name"], get_error_message(res)
            )
        )
    items = list(res.items)
    if not items:
        module.fail_json(
            msg="Password policy {0} was not found.".format(module.params["name"])
        )
    return items[0]


def _current_value(current, field):
    value = getattr(current, field, None)
    if value is None and field in NULL_WHEN_DISABLED_FIELDS:
        return 0
    return value


def update_policy(module, blade, current):
    """PATCH only the settings that differ from the current policy."""
    patch_kwargs = {}
    for param, field in PLAIN_PARAMS.items():
        value = module.params[param]
        if value is not None and value != _current_value(current, field):
            patch_kwargs[field] = value
    for param, field in SECONDS_PARAMS.items():
        value = module.params[param]
        if value is not None and value * 1000 != _current_value(current, field):
            patch_kwargs[field] = value * 1000

    changed = bool(patch_kwargs)
    if changed and not module.check_mode:
        res = blade.patch_password_policies(
            names=[module.params["name"]],
            policy=PasswordPolicy(**patch_kwargs),
        )
        if res.status_code != 200:
            module.fail_json(
                msg="Failed to update password policy {0}. Error: {1}".format(
                    module.params["name"], get_error_message(res)
                )
            )
    module.exit_json(changed=changed)


def main():
    argument_spec = purefb_argument_spec()
    argument_spec.update(
        dict(
            name=dict(type="str", default="management"),
            enabled=dict(type="bool"),
            min_password=dict(type="int", no_log=False),
            max_login=dict(type="int"),
            lockout=dict(type="int"),
            password_history=dict(type="int", no_log=False),
            min_password_age=dict(type="int", no_log=False),
            max_password_age=dict(type="int", no_log=False),
            min_character_groups=dict(type="int"),
            min_characters_per_group=dict(type="int"),
            enforce_username_check=dict(type="bool"),
            enforce_dictionary_check=dict(type="bool"),
        )
    )

    module = AnsibleModule(argument_spec, supports_check_mode=True)

    if not HAS_PYPURECLIENT:
        module.fail_json(msg="py-pure-client sdk is required for this module")

    _validate_params(module)

    blade = get_system(module)
    api_version = get_rest_api_version(blade)
    if LooseVersion(MIN_PASSWORD_POLICY_API_VERSION) > LooseVersion(api_version):
        module.fail_json(
            msg=(
                "FlashBlade REST version {0} does not support password "
                "policies (requires {1}+)."
            ).format(api_version, MIN_PASSWORD_POLICY_API_VERSION)
        )
    if module.params["max_password_age"] is not None and LooseVersion(
        MAX_PASSWORD_AGE_API_VERSION
    ) > LooseVersion(api_version):
        module.fail_json(
            msg=(
                "FlashBlade REST version {0} does not support "
                "max_password_age (requires {1}+)."
            ).format(api_version, MAX_PASSWORD_AGE_API_VERSION)
        )

    current = get_policy(module, blade)
    update_policy(module, blade, current)


if __name__ == "__main__":
    main()
