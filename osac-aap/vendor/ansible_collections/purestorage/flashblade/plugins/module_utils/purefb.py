# -*- coding: utf-8 -*-

# This code is part of Ansible, but is an independent component.
# This particular file snippet, and this file snippet only, is BSD licensed.
# Modules you write using this snippet, which is embedded dynamically by Ansible
# still belong to the author of the module, and may assign their own license
# to the complete work.
#
# Copyright (c), Simon Dodsley <simon@purestorage.com>,2017
# All rights reserved.
#
# Redistribution and use in source and binary forms, with or without modification,
# are permitted provided that the following conditions are met:
#
#    * Redistributions of source code must retain the above copyright
#      notice, this list of conditions and the following disclaimer.
#    * Redistributions in binary form must reproduce the above copyright notice,
#      this list of conditions and the following disclaimer in the documentation
#      and/or other materials provided with the distribution.
#
# THIS SOFTWARE IS PROVIDED BY THE COPYRIGHT HOLDERS AND CONTRIBUTORS "AS IS" AND
# ANY EXPRESS OR IMPLIED WARRANTIES, INCLUDING, BUT NOT LIMITED TO, THE IMPLIED
# WARRANTIES OF MERCHANTABILITY AND FITNESS FOR A PARTICULAR PURPOSE ARE DISCLAIMED.
# IN NO EVENT SHALL THE COPYRIGHT HOLDER OR CONTRIBUTORS BE LIABLE FOR ANY DIRECT, INDIRECT,
# INCIDENTAL, SPECIAL, EXEMPLARY, OR CONSEQUENTIAL DAMAGES (INCLUDING, BUT NOT LIMITED TO,
# PROCUREMENT OF SUBSTITUTE GOODS OR SERVICES; LOSS OF USE, DATA, OR PROFITS; OR BUSINESS
# INTERRUPTION) HOWEVER CAUSED AND ON ANY THEORY OF LIABILITY, WHETHER IN CONTRACT, STRICT
# LIABILITY, OR TORT (INCLUDING NEGLIGENCE OR OTHERWISE) ARISING IN ANY WAY OUT OF THE
# USE OF THIS SOFTWARE, EVEN IF ADVISED OF THE POSSIBILITY OF SUCH DAMAGE.

from __future__ import absolute_import, division, print_function

__metaclass__ = type

HAS_URLLIB3 = True
try:
    import urllib3
except ImportError:
    HAS_URLLIB3 = False

HAS_DISTRO = True
try:
    import distro
except ImportError:
    HAS_DISTRO = False

HAS_PYPURECLIENT = True
try:
    from pypureclient import flashblade
except ImportError:
    PYPURECLIENT = False

from os import environ
import platform

from ansible_collections.purestorage.flashblade.plugins.module_utils.common import (
    get_error_message,
)

VERSION = "1.5"
USER_AGENT_BASE = "Ansible"
API_AGENT_VERSION = "1.5"


def get_system(module):
    """Get authenticated FlashBlade client connection.

    Creates and returns an authenticated pypureclient FlashBlade client object.
    Credentials can be provided via module parameters or environment variables.

    Args:
        module: Ansible module object with params:
            - fb_url: FlashBlade management URL
            - api_token: API authentication token
            - disable_warnings: Whether to disable urllib3 warnings

    Returns:
        flashblade.Client: Authenticated FlashBlade client object

    Raises:
        AnsibleModule.fail_json: If authentication fails or pypureclient is not installed

    Environment Variables:
        PUREFB_URL: FlashBlade URL (alternative to fb_url parameter)
        PUREFB_API: API token (alternative to api_token parameter)
        PUREFB_ID_TOKEN: Pre-signed JWT (alternative to id_token parameter)
        PUREFB_PRIVATE_KEY_FILE / PUREFB_PRIVATE_KEY_PASSWORD
        PUREFB_USERNAME / PUREFB_CLIENT_ID / PUREFB_KEY_ID / PUREFB_ISSUER

    Note:
        Module parameters take precedence over environment variables.
        Three mutually-exclusive authentication modes are supported:
          1. api_token - static API token (default, backwards compatible).
          2. id_token - a pre-signed JWT, exchanged by the array for an
             access token at /oauth2/1.0/token.
          3. private_key_file with client_id, key_id, issuer and username -
             the SDK signs the JWT locally before exchange.
        Modes 2 and 3 require a matching API Client registered on the array
        (see the purefb_apiclient module).
    """
    if not HAS_PYPURECLIENT:
        module.fail_json(msg="pypureclient SDK not installed.")
    if module.params["disable_warnings"]:
        urllib3.disable_warnings()
    if HAS_DISTRO:
        user_agent = "%(base)s %(class)s/%(version)s (%(platform)s)" % {
            "base": USER_AGENT_BASE,
            "class": __name__,
            "version": VERSION,
            "platform": distro.name(pretty=True),
        }
    else:
        user_agent = "%(base)s %(class)s/%(version)s (%(platform)s)" % {
            "base": USER_AGENT_BASE,
            "class": __name__,
            "version": VERSION,
            "platform": platform.platform(),
        }

    # Module parameters take precedence over the matching PUREFB_* env vars.
    target = module.params["fb_url"] or environ.get("PUREFB_URL")
    api = module.params["api_token"] or environ.get("PUREFB_API")
    id_token = module.params.get("id_token") or environ.get("PUREFB_ID_TOKEN")
    private_key_file = module.params.get("private_key_file") or environ.get(
        "PUREFB_PRIVATE_KEY_FILE"
    )
    private_key_password = module.params.get("private_key_password") or environ.get(
        "PUREFB_PRIVATE_KEY_PASSWORD"
    )
    username = module.params.get("username") or environ.get("PUREFB_USERNAME")
    client_id = module.params.get("client_id") or environ.get("PUREFB_CLIENT_ID")
    key_id = module.params.get("key_id") or environ.get("PUREFB_KEY_ID")
    issuer = module.params.get("issuer") or environ.get("PUREFB_ISSUER")

    common = {"target": target, "user_agent": user_agent}

    if target and api:
        system = flashblade.Client(api_token=api, **common)
    elif target and id_token:
        system = flashblade.Client(id_token=id_token, **common)
    elif target and private_key_file and client_id and key_id and issuer and username:
        system = flashblade.Client(
            private_key_file=private_key_file,
            private_key_password=private_key_password,
            client_id=client_id,
            key_id=key_id,
            issuer=issuer,
            username=username,
            **common,
        )
    else:
        module.fail_json(
            msg="You must set PUREFB_URL and PUREFB_API environment variables "
            "or the fb_url and api_token module arguments. Alternatively, use "
            "token-based authentication via id_token, or private_key_file with "
            "client_id, key_id, issuer and username (or the matching PUREFB_* "
            "environment variables)."
        )
    res = system.get_hardware()
    if res.status_code != 200:
        module.fail_json(
            msg="Pure Storage FlashBlade authentication failed. Error: {0}".format(
                get_error_message(res)
            )
        )
    return system


def purefb_argument_spec():
    """Return standard argument specification for FlashBlade modules.

    Provides the base argument_spec dictionary that should be used by all
    FlashBlade modules for consistent authentication and connection handling.

    Returns:
        dict: Argument specification dictionary containing:
            - fb_url: FlashBlade management URL (optional, can use env var)
            - api_token: API authentication token (optional, can use env var, no_log=True)
            - disable_warnings: Disable urllib3 warnings (bool, default=False)

    Example:
        >>> argument_spec = purefb_argument_spec()
        >>> argument_spec.update(dict(
        ...     name=dict(required=True, type='str'),
        ...     state=dict(default='present', choices=['present', 'absent'])
        ... ))
        >>> module = AnsibleModule(argument_spec=argument_spec)
    """

    return dict(
        fb_url=dict(),
        api_token=dict(no_log=True),
        # OAuth2 / API-client token authentication (alternatives to api_token).
        # See the purefb_apiclient module for registering the trusted key.
        id_token=dict(no_log=True),
        private_key_file=dict(no_log=False),
        private_key_password=dict(no_log=True),
        username=dict(),
        client_id=dict(),
        key_id=dict(no_log=False),
        issuer=dict(),
        disable_warnings=dict(type="bool", default=False),
    )
