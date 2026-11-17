# -*- coding: utf-8 -*-
# Copyright: (c) 2026, VAST Data
# Apache License 2.0 (see LICENSE or https://www.apache.org/licenses/LICENSE-2.0)
# SPDX-License-Identifier: Apache-2.0

from __future__ import absolute_import, division, print_function

__metaclass__ = type


class ModuleDocFragment:
    """Standard VAST VMS connection documentation fragment.

    Provides the shared ``vms:`` connection block used by every module in the
    collection. Modules pull it in via:

        extends_documentation_fragment:
          - vastdata.vms.conn
    """

    DOCUMENTATION = r"""
options:
  vms:
    description: VAST VMS connection parameters.
    type: dict
    required: true
    suboptions:
      host:
        description: VAST VMS hostname or IP address.
        required: true
        type: str
      validate_certs:
        description: Validate SSL certificates.
        type: bool
        default: true
      timeout:
        description: Request timeout in seconds.
        type: int
      token:
        description: API token (VAST 5.3+). Mutually exclusive with username/password.
        type: str
      username:
        description: Username for authentication. Mutually exclusive with token.
        type: str
      password:
        description: Password for authentication. Mutually exclusive with token.
        type: str
      tenant:
        description: Tenant name (optional).
        type: str
      api_version:
        description: API version (optional). Defaults to 'latest' if not specified.
        type: str
      debug:
        description: Enable HTTP debug traces. Traces are emitted as warnings on failure.
        type: bool
        default: false
"""
