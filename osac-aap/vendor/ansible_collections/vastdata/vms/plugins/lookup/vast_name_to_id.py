# -*- coding: utf-8 -*-
# Copyright: (c) 2026, VAST Data
# Apache License 2.0 (see LICENSE or https://www.apache.org/licenses/LICENSE-2.0)
# SPDX-License-Identifier: Apache-2.0

from __future__ import absolute_import, division, print_function

__metaclass__ = type

DOCUMENTATION = r"""
name: vast_name_to_id
short_description: Resolve a VAST resource name to its id
description:
  - Query a VAST VMS endpoint for a resource matching a given name and return its id.
  - Expects exactly one match unless C(strict=false), in which case it returns C(None).
options:
  _terms:
    description:
      - "Three positional terms: the resource name, the C(vms) connection dict, and the endpoint."
    required: true
  name_field:
    description: Field used to match the name.
    type: str
    default: name
  id_field:
    description: Field whose value is returned as the id.
    type: str
    default: id
  query:
    description: Extra query parameters merged into the lookup request.
    type: dict
  strict:
    description: Require exactly one match. When false, return C(None) on zero/many matches.
    type: bool
    default: true
"""

EXAMPLES = r"""
- name: Resolve a tenant name to its id
  ansible.builtin.set_fact:
    tenant_id: "{{ lookup('vastdata.vms.vast_name_to_id', 'default', vms, 'tenants') }}"

- name: Resolve a tenant name to its guid
  ansible.builtin.set_fact:
    tenant_guid: "{{ lookup('vastdata.vms.vast_name_to_id', 'default', vms, 'tenants', id_field='guid') }}"
"""

RETURN = r"""
_raw:
  description: The resolved id value (single-element list).
  type: list
"""

from ansible.errors import AnsibleError
from ansible.plugins.lookup import LookupBase
from ansible_collections.vastdata.vms.plugins.module_utils.vast.errors import VastError
from ansible_collections.vastdata.vms.plugins.module_utils.vast.name_resolver import name_to_id


class LookupModule(LookupBase):
    def run(self, terms, variables=None, **kwargs):
        if len(terms) != 3:
            raise AnsibleError("vast_name_to_id expects exactly 3 positional terms: name, vms, endpoint " f"(got {len(terms)})")

        name, vms, endpoint = terms
        try:
            value = name_to_id(
                name,
                vms,
                endpoint,
                name_field=kwargs.get("name_field", "name"),
                id_field=kwargs.get("id_field", "id"),
                query=kwargs.get("query"),
                strict=kwargs.get("strict", True),
            )
        except VastError as exc:
            raise AnsibleError(str(exc)) from exc

        return [value]
