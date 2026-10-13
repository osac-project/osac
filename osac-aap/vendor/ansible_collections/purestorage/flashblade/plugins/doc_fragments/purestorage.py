# -*- coding: utf-8 -*-

# Copyright: (c) 2017, Simon Dodsley <simon@purestorage.com>
# GNU General Public License v3.0+ (see COPYING or https://www.gnu.org/licenses/gpl-3.0.txt)

from __future__ import absolute_import, division, print_function

__metaclass__ = type


class ModuleDocFragment(object):
    # Standard Pure Storage documentation fragment
    DOCUMENTATION = r"""
options:
  - See separate platform section for more details
requirements:
  - See separate platform section for more details
notes:
  - Ansible modules are available for the following Pure Storage products: FlashArray, FlashBlade
"""

    # Documentation fragment for FlashBlade
    FB = r"""
options:
  fb_url:
    description:
      - FlashBlade management IP address or Hostname.
    type: str
  api_token:
    description:
      - FlashBlade API token for admin privileged user.
    type: str
  id_token:
    description:
      - A pre-signed JWT to authenticate with, as an alternative to I(api_token).
      - The token is exchanged by the array for a short-lived access token.
      - Requires a matching API Client to be registered on the array
        (see M(purestorage.flashblade.purefb_apiclient)).
    type: str
    version_added: '1.26.0'
  private_key_file:
    description:
      - Path to the PEM RSA private key used to sign an identity token,
        as an alternative to I(api_token).
      - Requires I(client_id), I(key_id), I(issuer) and I(username).
    type: str
    version_added: '1.26.0'
  private_key_password:
    description:
      - Password protecting I(private_key_file), if encrypted.
    type: str
    version_added: '1.26.0'
  username:
    description:
      - Username the issued token should be granted to.
      - Must be a valid user on the array. Used with I(private_key_file).
    type: str
    version_added: '1.26.0'
  client_id:
    description:
      - ID of the API Client that issues the identity token.
      - Used with I(private_key_file).
    type: str
    version_added: '1.26.0'
  key_id:
    description:
      - Key ID of the API Client that issues the identity token.
      - Used with I(private_key_file).
    type: str
    version_added: '1.26.0'
  issuer:
    description:
      - The API Client's trusted identity issuer registered on the array.
      - Used with I(private_key_file).
    type: str
    version_added: '1.26.0'
  disable_warnings:
    description:
    - Disable insecure certificate warnings
    type: bool
    default: false
    version_added: '1.18.0'
notes:
  - You must set C(PUREFB_URL) and C(PUREFB_API) environment variables
    if I(fb_url) and I(api_token) arguments are not passed to the module directly
  - Token-based authentication (I(id_token), or I(private_key_file) with
    I(client_id), I(key_id), I(issuer) and I(username)) may be used as an
    alternative to I(api_token), and requires a matching API Client registered
    on the array via M(purestorage.flashblade.purefb_apiclient)
requirements:
  - python >= 3.9
  - py-pure-client
  - netaddr
  - datetime
  - pytz
  - distro
  - pycountry
  - urllib3
"""
