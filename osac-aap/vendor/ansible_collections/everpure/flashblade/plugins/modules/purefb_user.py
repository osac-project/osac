#!/usr/bin/python
# -*- coding: utf-8 -*-

# (c) 2019, Simon Dodsley (simon@everpuredata.com)
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
module: purefb_user
version_added: '1.0.0'
short_description: Create, modify or delete FlashBlade user accounts
description:
- Modify user on a Pure Stoage FlashBlade.
author:
- Everpure Ansible Team (@sdodsley) <pure-ansible-team@everpuredata.com>
options:
  state:
    description:
    - Create, delete or update local user account
    default: present
    type: str
    choices: [ absent, present ]
    version_added: "1.21.0"
  name:
    description:
    - The name of the user account
    type: str
    required: true
  role:
    description:
    - Sets the local user's access level to the system
    - A new local user is created with I(readonly) when no role is given.
    - When omitted for an existing user the current role is left unchanged.
    type: str
    choices: [ readonly, ops_admin, storage_admin, array_admin ]
    version_added: "1.21.0"
  password:
    description:
    - Password for the local user.
    type: str
  old_password:
    description:
    - If changing an existing password, you must provide the old password for security
    type: str
  api:
    description:
    - Define whether to create an API token for this user
    - An existing token is replaced, so this is not idempotent.
    - Token can be exposed using the I(debug) module
    type: bool
    default: false
    version_added: "1.21.0"
  timeout:
    description:
      - The duration of API token validity.
      - Valid values are weeks (w), days(d), hours(h), minutes(m) and seconds(s).
      - The default of C("0") creates a token that does not expire.
    type: str
    default: "0"
    version_added: "1.21.0"
  public_key:
    description:
    - SSH public key for the user in OpenSSH C(authorized_keys) format, for
      example C(ssh-rsa AAAA... comment) or C(ssh-ed25519 AAAA... comment).
    - The array accepts at most two keys, one per line.
    - PEM blocks (I(-----BEGIN PUBLIC KEY-----)) are rejected. That is the
      key format of M(everpure.flashblade.purefb_apiclient); the array treats
      every line of a PEM block as a separate SSH key.
    - Keys are compared by type and key material, so a different comment or
      whitespace is not a change. When the array masks the stored key
      (reported as C(****)) only its presence can be checked; clear it with
      C("") before setting a different key.
    - An empty string removes the user's public key.
    type: str
    version_added: "1.8.0"
  clear_lock:
    description:
    - Clear user lockout flag
    type: bool
    default: false
    version_added: "1.8.0"
  ad_user:
    description:
      - Whether the user is in the AD system
      - Not required for local users
    type: bool
    default: false
    version_added: "1.21.0"
extends_documentation_fragment:
- everpure.flashblade.everpure.fb
"""

EXAMPLES = r"""
- name: Change password for local user (NOT IDEMPOTENT)
  everpure.flashblade.purefb_user:
    name: pureuser
    password: anewpassword
    old_password: apassword
    fb_url: 10.10.10.2
    api_token: T-9f276a18-50ab-446e-8a0c-666a3529a1b6

- name: Set public key for user
  everpure.flashblade.purefb_user:
    name: fred
    public_key: "{{lookup('file', 'public_pem_file') }}"
    fb_url: 10.10.10.2
    api_token: T-9f276a18-50ab-446e-8a0c-666a3529a1b6

- name: Clear user lockout
  everpure.flashblade.purefb_user:
    name: fred
    clear_lock: true
    fb_url: 10.10.10.2
    api_token: T-9f276a18-50ab-446e-8a0c-666a3529a1b6

- name: Create an API token (TTL of 2 days) and assign a public key to an AD user
  everpure.flashblade.purefb_user:
    name: ansible-ad
    ad_user: true
    public_key: "{{lookup('file', 'id_rsa.pub') }}"
    api: true
    timeout: 2d
    fb_url: 10.10.10.2
    api_token: T-9f276a18-50ab-446e-8a0c-666a3529a1b6
"""

RETURN = r"""
"""

HAS_PYPURECLIENT = True
try:
    from pypureclient.flashblade import (
        AdminPatch,
        AdminPost,
        ReferenceWritable,
    )
except ImportError:
    HAS_PYPURECLIENT = False

from ansible.module_utils.basic import AnsibleModule
from ansible_collections.everpure.flashblade.plugins.module_utils.time_utils import (
    time_to_milliseconds,
    TimeConversionError,
)
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
import re

MIN_LOCAL_USER = "2.15"
MIN_EXPOSE_API = "2.18"
DEFAULT_ROLE = "readonly"
# Value the array reports for a stored public key when it masks it.
PUBLIC_KEY_MASK = "****"
MAX_PUBLIC_KEYS = 2
# One OpenSSH authorized_keys entry: "<type> <base64 key> [comment]"
OPENSSH_KEY_LINE = re.compile(r"^(\S+)\s+([A-Za-z0-9+/=]+)(?:\s+.*)?$")


def parse_public_keys(text):
    """Return the (type, key) pairs found in an OpenSSH authorized_keys string.

    Blank lines are ignored. Returns None when any non-empty line is not an
    OpenSSH public key entry (for example a PEM block).
    """
    keys = []
    for line in (text or "").splitlines():
        line = line.strip()
        if not line:
            continue
        match = OPENSSH_KEY_LINE.match(line)
        if not match:
            return None
        keys.append((match.group(1), match.group(2)))
    return keys


def validate_public_key(module):
    """Fail early when public_key is not in the format the array accepts.

    The array wants OpenSSH format (``ssh-rsa AAAA...``) and reads one key
    per line, so a PEM block is seen as many keys and rejected with the
    unhelpful "At most 2 public keys can be uploaded for a user".
    """
    text = module.params["public_key"]
    if not text or not text.strip():
        return
    keys = parse_public_keys(text)
    if keys is None:
        module.fail_json(
            msg=(
                "public_key must be in OpenSSH authorized_keys format "
                "('ssh-rsa AAAA...' or 'ssh-ed25519 AAAA...', one key per "
                "line). PEM blocks are the purefb_apiclient key format and "
                "are not accepted for SSH access."
            )
        )
    if len(keys) > MAX_PUBLIC_KEYS:
        module.fail_json(
            msg="At most {0} public keys can be set for a user".format(MAX_PUBLIC_KEYS)
        )


def public_key_changed(module, user):
    """Return True when the requested public key differs from the user's.

    Keys are compared by type and key material only, so comments and
    whitespace do not count. An empty request clears the key and is a
    change only while a key is set. A masked value (``****``) means the
    array does not return stored keys; only presence can be compared then.
    """
    desired = module.params["public_key"]
    if desired is None:
        return False
    current = getattr(user, "public_key", None) or ""
    if not desired.strip():
        return bool(current.strip())
    if current.strip() == PUBLIC_KEY_MASK:
        return False
    return sorted(parse_public_keys(desired)) != sorted(
        parse_public_keys(current) or []
    )


def set_public_key(module, blade, action="change"):
    """PATCH the user's public key (an empty string clears it)"""
    res = blade.patch_admins(
        names=[module.params["name"]],
        admin=AdminPatch(public_key=module.params["public_key"].strip()),
    )
    if res.status_code != 200:
        module.fail_json(
            msg="Failed to {0} SSH key. Error: {1}".format(
                action, get_error_message(res)
            )
        )


def get_user(module, blade):
    """Return Local User Account or None"""
    user = None
    res = blade.get_admins(names=[module.params["name"]])
    if res.status_code != 200:
        return None
    else:
        return list(res.items)[0]


def has_api_token(user):
    """Return True if the admin currently holds an API token.

    The token value itself is never returned by GET admins, only its
    ``created_at``/``expires_at`` timestamps, so test those.
    """
    token = getattr(user, "api_token", None)
    return bool(
        getattr(token, "created_at", None) or getattr(token, "expires_at", None)
    )


def get_token_ttl(module):
    """Return the API token validity in milliseconds, or None for no expiry"""
    timeout = module.params["timeout"]
    if timeout in (None, "", "0", 0):
        return None
    try:
        return time_to_milliseconds(timeout)
    except TimeConversionError as err:
        module.fail_json(msg="Invalid timeout {0}: {1}".format(timeout, err))


def delete_api_token(module, blade):
    """Delete the admin's API token"""
    res = blade.delete_admins_api_tokens(admin_names=[module.params["name"]])
    if res.status_code != 200:
        module.fail_json(
            msg="Failed to delete original API token. Error: {0}".format(
                get_error_message(res)
            )
        )


def create_api_token(module, blade):
    """Create an API token for the admin and return it"""
    res = blade.post_admins_api_tokens(
        admin_names=[module.params["name"]], timeout=get_token_ttl(module)
    )
    if res.status_code != 200:
        module.fail_json(
            msg="Failed to create API token. Error: {0}".format(get_error_message(res))
        )
    return list(res.items)[0].api_token.token


def create_local_user(module, blade, user):
    """Create or Update Local User Account"""
    changed = key_changed = api_changed = role_changed = passwd_changed = False
    lock_changed = False
    role = module.params["role"] or DEFAULT_ROLE
    api_token = "No API token created"
    if not user:
        changed = True
        if not module.check_mode:
            res = blade.post_admins(
                names=[module.params["name"]],
                admin=AdminPost(
                    role=ReferenceWritable(name=role),
                    password=module.params["password"],
                ),
            )
            if res.status_code != 200:
                module.fail_json(
                    msg="Failed to create user {0}. Error: {1}".format(
                        module.params["name"], get_error_message(res)
                    )
                )
            if module.params["api"]:
                api_token = create_api_token(module, blade)
            if module.params["public_key"] and module.params["public_key"].strip():
                set_public_key(module, blade, action="add")
    else:
        if module.params["password"] and not module.params["old_password"]:
            module.exit_json(changed=changed)
        if (
            module.params["password"]
            and module.params["old_password"]
            and module.params["password"] != module.params["old_password"]
        ):
            passwd_changed = True
            if not module.check_mode:
                res = blade.patch_admins(
                    names=[module.params["name"]],
                    admin=AdminPatch(
                        password=module.params["password"],
                        old_password=module.params["old_password"],
                    ),
                )
                if res.status_code != 200:
                    module.fail_json(
                        msg="Local User {0} password reset failed. Error: {1}"
                        "Check old password.".format(
                            module.params["name"], get_error_message(res)
                        )
                    )
        if module.params["api"]:
            api_changed = True
            if not module.check_mode:
                if has_api_token(user):
                    delete_api_token(module, blade)
                api_token = create_api_token(module, blade)
        if (
            module.params["role"]
            and module.params["role"] != getattr(user.role, "name", None)
            and user.is_local
        ):
            if module.params["name"] != "pureuser":
                role_changed = True
                if not module.check_mode:
                    res = blade.patch_admins(
                        names=[module.params["name"]],
                        admin=AdminPatch(
                            role=ReferenceWritable(name=module.params["role"])
                        ),
                    )
                    if res.status_code != 200:
                        module.fail_json(
                            msg="Local User {0} role changed failed. Error: {1}".format(
                                module.params["name"], get_error_message(res)
                            )
                        )
            else:
                module.warn("Role for 'pureuser' cannot be modified.")
        if public_key_changed(module, user):
            key_changed = True
            if not module.check_mode:
                set_public_key(module, blade)
        if module.params["clear_lock"] and getattr(user, "locked", False):
            lock_changed = True
            if not module.check_mode:
                res = blade.patch_admins(
                    names=[module.params["name"]],
                    admin=AdminPatch(locked=False),
                )
                if res.status_code != 200:
                    module.fail_json(
                        msg="Failed to unlock user {0}. Error: {1}".format(
                            module.params["name"], get_error_message(res)
                        )
                    )
        changed = bool(
            passwd_changed or role_changed or api_changed or key_changed or lock_changed
        )
    module.exit_json(changed=changed, user_info=api_token)


def update_ad_user(module, blade, user):
    """Update AD user API token and/or SSH key"""
    api_token = "No API token created"
    api_changed = ssh_changed = False
    if module.params["api"]:
        api_changed = True
        if not module.check_mode:
            if user and has_api_token(user):
                delete_api_token(module, blade)
            api_token = create_api_token(module, blade)
    if module.params["public_key"] is not None:
        if user is None:
            # An AD user with no array-side state yet: nothing to compare
            # against, and nothing to clear.
            ssh_changed = bool(module.params["public_key"].strip())
        else:
            ssh_changed = public_key_changed(module, user)
        if ssh_changed and not module.check_mode:
            set_public_key(module, blade, action="add")
    changed = bool(api_changed or ssh_changed)
    module.exit_json(changed=changed, user_info=api_token)


def delete_ad_user(module, blade, user):
    """Delete AD User Account references"""
    changed = False
    if not module.check_mode:
        if user:
            changed = True
            if has_api_token(user):
                res = blade.delete_admins_api_tokens(
                    admin_names=[module.params["name"]]
                )
                if res.status_code != 200:
                    module.fail_json(
                        msg="AD Account {0} API token deletion failed. Error: {1}".format(
                            module.params["name"], get_error_message(res)
                        )
                    )
            if getattr(user, "public_key", None):
                res = blade.patch_admins(
                    names=[module.params["name"]],
                    admin=AdminPatch(public_key=""),
                )
                if res.status_code != 200:
                    module.fail_json(
                        msg="AD Account {0} public key deletion failed. Error: {1}".format(
                            module.params["name"], get_error_message(res)
                        )
                    )
        res = blade.delete_admins_cache(names=[module.params["name"]])
        if res.status_code != 200:
            module.fail_json(
                msg="Admin Cache deleteion failed for AD user {0}. Error: {1}".format(
                    module.params["name"], get_error_message(res)
                )
            )
    module.exit_json(changed=changed)


def delete_local_user(module, blade):
    """Delete Local User Account"""
    changed = True
    if not module.check_mode:
        res = blade.delete_admins(names=[module.params["name"]])
        if res.status_code != 200:
            module.fail_json(
                msg="User Account {0} deletion failed. Error: {1}".format(
                    module.params["name"], get_error_message(res)
                )
            )
    module.exit_json(changed=changed)


def main():
    argument_spec = purefb_argument_spec()
    argument_spec.update(
        dict(
            name=dict(type="str", required=True),
            public_key=dict(type="str", no_log=True),
            password=dict(type="str", no_log=True),
            old_password=dict(type="str", no_log=True),
            clear_lock=dict(type="bool", default=False),
            role=dict(
                type="str",
                choices=["readonly", "ops_admin", "storage_admin", "array_admin"],
            ),
            state=dict(type="str", default="present", choices=["absent", "present"]),
            api=dict(type="bool", default=False),
            ad_user=dict(type="bool", default=False),
            timeout=dict(type="str", default="0"),
        )
    )

    module = AnsibleModule(
        argument_spec,
        supports_check_mode=True,
    )

    validate_public_key(module)
    blade = get_system(module)
    api_version = get_rest_api_version(blade)
    if LooseVersion(MIN_LOCAL_USER) > LooseVersion(api_version):
        module.fail_json(
            msg="Purity//FB must be upgraded to support managing local users."
        )

    state = module.params["state"]
    pattern = re.compile("^[a-z0-9]([a-z0-9-]{0,30}[a-z0-9])?$")
    user = get_user(module, blade)
    local_user = getattr(user, "is_local", False)
    if not pattern.match(module.params["name"]):
        module.fail_json(
            msg="name must contain a minimum of 1 and a maximum of 32 characters "
            "(alphanumeric or `-`). All letters must be lowercase."
        )

    if state == "present" and not local_user and module.params["ad_user"]:
        update_ad_user(module, blade, user)
    if state == "absent" and local_user:
        delete_local_user(module, blade)
    if state == "absent" and not local_user:
        if user is None and not module.params["ad_user"]:
            # Nothing to delete: the local user does not exist
            module.exit_json(changed=False)
        delete_ad_user(module, blade, user)
    elif state == "present" and not module.params["ad_user"]:
        create_local_user(module, blade, user)
    else:
        module.exit_json(changed=False)


if __name__ == "__main__":
    main()
