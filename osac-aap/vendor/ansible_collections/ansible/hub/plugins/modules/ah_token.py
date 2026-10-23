#!/usr/bin/python
# -*- coding: utf-8 -*-

# Copyright: (c) 2020, John Westcott IV <john.westcott.iv@redhat.com>
# Copyright: (c) 2021, Sean Sullivan <@sean-m-sullivan>
# GNU General Public License v3.0+ (see COPYING or https://www.gnu.org/licenses/gpl-3.0.txt)


DOCUMENTATION = """
---
module: ah_token
author:
  - John Westcott IV (@john-westcott-iv)
  - Sean Sullivan (@sean-m-sullivan)
short_description: Create, update, or destroy Automation Hub tokens
description:
    - Create or destroy Automation Hub tokens.
    - In addition, the module sets an Ansible fact which can be passed into other
      ah_* modules as the parameter ah_oauthtoken. See examples for usage.
    - Because of the sensitive nature of tokens, the created token value is only available once
      through the Ansible fact. (See RETURN for details).
    - Due to the nature of tokens in Automation Hub this module is not idempotent. A second will
      with the same parameters will create a new token.
    - If you are creating a temporary token for use with modules you should delete the token
      when you are done with it. See the example for how to do it.
    - B(Deprecated) when used with AAP 2.5 or 2.6. This module will be removed in AAP 2.7.
      In AAP 2.7, all authentication is handled through the AAP Gateway using JWT and personal
      access tokens are no longer supported.
options:
    state:
      description:
        - Desired state of the resource.
      choices: ["present", "absent"]
      default: "present"
      type: str
extends_documentation_fragment: ansible.hub.auth
"""

EXAMPLES = """
- name: Create a new token using an existing token
  ansible.hub.ah_token:
    ah_token: "{{ my_existing_token }}"
  no_log: true

- name: Delete this token
  ansible.hub.ah_token:
    ah_token: "{{ ah_token }}"
    state: absent
  no_log: true

- name: Create a new token using username/password
  ansible.hub.ah_token:
    state: present
    ah_username: "{{ my_username }}"
    ah_password: "{{ my_password }}"
  no_log: true

- name: Use our new token to make another call
  ansible.hub.namespace:
    ah_token: "{{ ah_token }}"
  no_log: true
"""

RETURN = """
ah_token:
  type: dict
  description: An Ansible Fact variable representing a Automation Hub token object which can be used for auth in subsequent modules. See examples for usage.
  contains:
    token:
      description: The token that was generated. This token can never be accessed again, make sure this value is noted before it is lost.
      type: str
    id:
      description: The numeric ID of the token created
      type: str
  returned: on successful create
"""

import base64

from json import loads

from urllib.error import HTTPError

from ansible.module_utils.compat.version import LooseVersion
from ansible.module_utils.urls import ConnectionError

from ..module_utils.ah_module import AHModule


def check_deprecation(module):
    """Check if this module is deprecated or unsupported based on Hub version.

    Detects whether Hub is behind an AAP Gateway (resource server) and checks
    the Hub version to determine if the ah_token module should warn or fail.

    Unlike AHAPIModule (used by ah_user), AHModule does not have built-in
    resource server detection or get_server_version(), so we make lightweight
    API calls to detect the environment.
    """
    # Build auth headers from module params so the version-check request is
    # authenticated. Without this, galaxy_ng strips server_version from
    # unauthenticated responses and the version gates below are silently skipped.
    headers = {"Content-Type": "application/json", "Accept": "application/json"}
    if module.oauth_token:
        headers["Authorization"] = "Token {0}".format(module.oauth_token)
    elif module.username and module.password:
        basic_str = base64.b64encode(
            "{0}:{1}".format(module.username, module.password).encode("ascii")
        )
        headers["Authorization"] = "Basic {0}".format(basic_str.decode("ascii"))

    # Check if behind a resource server by hitting /api/
    try:
        response = module.session.open(
            "GET",
            module.build_url("/api/").geturl(),
            validate_certs=module.verify_ssl,
            timeout=module.request_timeout,
            headers=headers,
            follow_redirects=True,
        )
        data = loads(response.read())
    except (HTTPError, ConnectionError, ValueError):
        # HTTPError/ConnectionError: standalone install, endpoint doesn't exist, or network issue
        # ValueError: malformed JSON response, not a standard AAP/Galaxy endpoint
        return

    if "apis" not in data or "galaxy" not in data.get("apis", {}):
        # Not behind a resource server (standalone Galaxy/Hub), no action needed
        return

    # Behind a resource server (AAP Gateway)
    galaxy_prefix = data["apis"]["galaxy"].strip("/")
    try:
        vers_response = module.session.open(
            "GET",
            module.url._replace(path="/{0}/".format(galaxy_prefix)).geturl(),
            validate_certs=module.verify_ssl,
            timeout=module.request_timeout,
            headers=headers,
            follow_redirects=True,
        )
        vers_data = loads(vers_response.read())
    except (HTTPError, ConnectionError) as e:
        module.warn("Unable to determine Hub version: {0}".format(e))
        return
    except (ValueError, KeyError):
        module.warn("Unable to parse Hub version response")
        return

    server_version = vers_data.get("server_version", "0").replace("dev", "")
    vers = LooseVersion(server_version)

    if vers >= LooseVersion("4.12"):
        module.fail_json(
            msg=(
                "The ah_token module is not supported in AAP 2.7+ (Hub {vers}). "
                "In AAP 2.7, all authentication is handled through the AAP Gateway "
                "using JWT. Personal access tokens created by this module are not "
                "compatible with Gateway authentication. "
                "Use the ansible.platform collection for token management instead."
            ).format(vers=server_version)
        )
    elif vers >= LooseVersion("4.10"):
        module.warn(
            "The ah_token module is deprecated when used with AAP 2.5+ (Hub {vers}) "
            "and will be removed in AAP 2.7. In AAP 2.7, all authentication will be "
            "handled through the AAP Gateway using JWT. "
            "Use the ansible.platform collection for token management instead."
            .format(vers=server_version)
        )


def return_token(module, last_response):
    # A token is special because you can never get the actual token ID back from the API.
    # So the default module return would give you an ID but then the token would forever be masked on you.
    # This method will return the entire token object we got back so that a user has access to the token

    module.json_output["ansible_facts"] = {
        "ah_token": last_response,
    }
    module.exit_json(**module.json_output)


def main():
    # Any additional arguments that are not fields of the item can be added here
    argument_spec = dict(
        state=dict(choices=["present", "absent"], default="present"),
    )

    # Create a module for ourselves
    module = AHModule(argument_spec=argument_spec)

    # Check for deprecation/removal before proceeding
    check_deprecation(module)

    # Extract our parameters
    state = module.params.get("state")

    # Delete an existing token
    if state == "absent":
        # If the state was absent we can let the module delete it if needed, the module will handle exiting from this
        existing_item = {}
        existing_item["endpoint"] = "auth/token/"
        existing_item["type"] = "token"
        module.delete_if_needed(existing_item)

    # If the state was present and we can let the module build or update the existing item, this will return on its own
    module.create_or_update_if_needed(
        None,
        None,
        endpoint="auth/token/",
        item_type="token",
        on_create=return_token,
    )


if __name__ == "__main__":
    main()
