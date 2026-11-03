#!/usr/bin/python
# -*- coding: utf-8 -*-

# (c) 2018, Simon Dodsley (simon@everpuredata.com)
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
module: purefb_smtp
version_added: '1.0.0'
short_description: Configure SMTP for Everpure FlashBlade
description:
- Configure SMTP for a Everpure FlashBlade.
- Whilst there can be no relay host, a sender domain must be configured.
author:
- Everpure Ansible Team (@sdodsley) <pure-ansible-team@everpuredata.com>
options:
  host:
    description:
    - Relay server name
    - Use "" to clear.
    type: str
  domain:
    description:
    - Domain name for alert messages
    required: true
    type: str
  encryption:
    description:
    - Enforces an encryption mode when sending alert email messages.
    - Use "" to clear.
    type: str
    choices: [ "starttls", "" ]
    version_added: 1.19.0
extends_documentation_fragment:
- everpure.flashblade.everpure.fb
"""

EXAMPLES = r"""
- name: Configure SMTP settings
  everpure.flashblade.purefb_smtp:
    host: hostname
    encryption: starttls
    domain: xyz.com
    fb_url: 10.10.10.2
    api_token: T-9f276a18-50ab-446e-8a0c-666a3529a1b6
"""

RETURN = r"""
"""

HAS_PYPURECLIENT = True
try:
    from pypureclient.flashblade import SmtpServer
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

SMTP_ENCRYPT_API_VERSION = "2.15"


def set_smtp(module, blade):
    """Configure SMTP settings"""
    changed = False
    current_smtp = list(blade.get_smtp_servers(names=["management"]).items)[0]
    # The array reports an unset relay host or encryption mode as null; the
    # module takes "" for "unset", so compare both sides normalised to "".
    relay_host = getattr(current_smtp, "relay_host", None) or ""
    domain = current_smtp.sender_domain
    encrypt = getattr(current_smtp, "encryption_mode", None) or ""
    if module.params["host"] is not None and module.params["host"] != relay_host:
        relay_host = module.params["host"]
        changed = True
    if module.params["domain"] and module.params["domain"] != domain:
        domain = module.params["domain"]
        changed = True
    if (
        module.params["encryption"] is not None
        and module.params["encryption"] != encrypt
    ):
        encrypt = module.params["encryption"]
        changed = True
    if changed and not module.check_mode:
        smtp_attrs = {"sender_domain": domain}
        # Only send a field that was requested or that the array already
        # holds, so an unset field is not turned into an explicit "".
        if module.params["host"] is not None or relay_host:
            smtp_attrs["relay_host"] = relay_host
        if LooseVersion(SMTP_ENCRYPT_API_VERSION) <= LooseVersion(
            get_rest_api_version(blade)
        ) and (module.params["encryption"] is not None or encrypt):
            smtp_attrs["encryption_mode"] = encrypt
        res = blade.patch_smtp_servers(smtp=SmtpServer(**smtp_attrs))
        if res.status_code != 200:
            module.fail_json(
                msg="Failed to set SMTP configuration. Error: {0}".format(
                    get_error_message(res)
                )
            )
    module.exit_json(changed=changed)


def main():
    argument_spec = purefb_argument_spec()
    argument_spec.update(
        dict(
            host=dict(type="str"),
            domain=dict(type="str", required=True),
            encryption=dict(type="str", choices=["starttls", ""]),
        )
    )

    module = AnsibleModule(argument_spec, supports_check_mode=True)

    blade = get_system(module)
    api_version = get_rest_api_version(blade)
    if not HAS_PYPURECLIENT:
        module.fail_json(msg="py-pure-client SDK is required for this module")
    if (
        LooseVersion(SMTP_ENCRYPT_API_VERSION) > LooseVersion(api_version)
        and module.params["encryption"]
    ):
        module.fail_json(msg="Purity//FB must be upgraded to support encryption.")

    set_smtp(module, blade)
    module.exit_json(changed=False)


if __name__ == "__main__":
    main()
