# SPDX-FileCopyrightText: Copyright (c) 2026 Fabien Dupont
# SPDX-License-Identifier: Apache-2.0

from __future__ import absolute_import, division, print_function
__metaclass__ = type


class ModuleDocFragment(object):

    DOCUMENTATION = r'''
options:
    api_url:
        description:
            - URL of the NVIDIA Bare Metal Manager API.
            - When using the NVIDIA proxy, set this to the proxy base URL.
        type: str
        required: true
        environment:
            - name: NVIDIA_BMM_API_URL
    api_token:
        description:
            - JWT bearer token for API authentication.
            - Obtain by exchanging SSA client credentials at the NVIDIA SSA token endpoint.
        type: str
        required: true
        environment:
            - name: NVIDIA_BMM_API_TOKEN
    org:
        description:
            - Organization name for API requests.
        type: str
        required: true
        environment:
            - name: NVIDIA_BMM_ORG
    api_path_prefix:
        description:
            - API path prefix used in request URLs.
            - Use C(carbide) (default) for direct API access.
            - Use C(forge) when connecting through the NVIDIA proxy.
        type: str
        default: carbide
        environment:
            - name: NVIDIA_BMM_API_PATH_PREFIX
'''
