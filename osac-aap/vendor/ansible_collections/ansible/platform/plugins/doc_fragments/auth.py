# -*- coding: utf-8 -*-

# Copyright: (c) 2023, Sean Sullivan <@sean-m-sullivan>
# GNU General Public License v3.0+ (see COPYING or https://www.gnu.org/licenses/gpl-3.0.txt)

from __future__ import absolute_import, division, print_function

__metaclass__ = type


class ModuleDocFragment(object):
    # Ansible Galaxy documentation fragment
    DOCUMENTATION = r"""
options:
  gateway_hostname:
    description:
    - URL to automation platform gateway.
    - If value not set, will try environment variable C(GATEWAY_HOSTNAME)
    - If value not specified by any means, the value of C(127.0.0.1) will be used
    type: str
  gateway_username:
    description:
    - Username for your automation platform gateway.
    - If value not set, will try environment variable C(GATEWAY_USERNAME)
    type: str
  gateway_password:
    description:
    - Password for your automation platform gateway.
    - If value not set, will try environment variable C(GATEWAY_PASSWORD)
    type: str
  gateway_token:
    description:
    - The automation platform gateway token to use.
    - This value can be in one of two formats.
    - A string which is the token itself. (i.e. bqV5txm97wqJqtkxlMkhQz0pKhRMMX)
    - A dictionary structure as returned by the gateway_token module.
    - If value not set, will try environment variable C(GATEWAY_API_TOKEN)
    type: raw
  gateway_validate_certs:
    description:
    - Whether to allow insecure connections to automation platform gateway.
    - If C(no), SSL certificates will not be validated.
    - This should only be used on personally controlled sites using self-signed certificates.
    - If value not set, will try environment variable C(GATEWAY_VERIFY_SSL)
    type: bool
    aliases: [ validate_certs ]
  gateway_request_timeout:
    description:
    - Specify the timeout Ansible should use in requests to the automation platform gateway.
    - Defaults to 10s, but this is handled by the shared module_utils code
    - If value not set, will try environment variable C(GATEWAY_REQUEST_TIMEOUT)
    type: float
    aliases: [ request_timeout ]
"""
