# SPDX-FileCopyrightText: Copyright (c) 2026 Fabien Dupont
# SPDX-License-Identifier: Apache-2.0

from __future__ import absolute_import, division, print_function
__metaclass__ = type

import re


def camel_to_snake(name):
    """Convert camelCase or PascalCase to snake_case."""
    # Handle acronyms like 'VPC' -> 'vpc', 'NVLink' -> 'nv_link'
    s1 = re.sub(r'([A-Z]+)([A-Z][a-z])', r'\1_\2', name)
    s2 = re.sub(r'([a-z0-9])([A-Z])', r'\1_\2', s1)
    return s2.lower()


def snake_to_camel(name):
    """Convert snake_case to camelCase."""
    parts = name.split('_')
    return parts[0] + ''.join(p.capitalize() for p in parts[1:])


# Fields whose dict values have user-defined keys that must not be converted.
# These correspond to OpenAPI schemas with additionalProperties (e.g., Labels).
OPAQUE_DICT_FIELDS = frozenset({'labels'})


def convert_keys(data, converter):
    """Recursively convert all dict keys using the given converter function.

    Keys listed in OPAQUE_DICT_FIELDS are treated as opaque dicts whose
    child keys are user-defined and should not be converted.
    """
    if isinstance(data, dict):
        result = {}
        for k, v in data.items():
            new_key = converter(k)
            if new_key in OPAQUE_DICT_FIELDS or k in OPAQUE_DICT_FIELDS:
                # Preserve child keys as-is for opaque dicts
                result[new_key] = v
            else:
                result[new_key] = convert_keys(v, converter)
        return result
    if isinstance(data, list):
        return [convert_keys(item, converter) for item in data]
    return data


def get_auth_argument_spec():
    """Return auth argument spec with env var fallbacks.

    Separated into a function so the import of env_fallback
    is deferred until Ansible is available.
    """
    from ansible.module_utils.basic import env_fallback
    return dict(
        api_url=dict(
            type='str',
            required=True,
            fallback=(env_fallback, ['NVIDIA_BMM_API_URL']),
        ),
        api_token=dict(
            type='str',
            required=True,
            no_log=True,
            fallback=(env_fallback, ['NVIDIA_BMM_API_TOKEN']),
        ),
        org=dict(
            type='str',
            required=True,
            fallback=(env_fallback, ['NVIDIA_BMM_ORG']),
        ),
        api_path_prefix=dict(
            type='str',
            default='carbide',
            fallback=(env_fallback, ['NVIDIA_BMM_API_PATH_PREFIX']),
        ),
    )
