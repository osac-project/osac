# -*- coding: utf-8 -*-
# Copyright: (c) 2026, VAST Data
# Apache License 2.0 (see LICENSE or https://www.apache.org/licenses/LICENSE-2.0)
# SPDX-License-Identifier: Apache-2.0

from __future__ import absolute_import, division, print_function

__metaclass__ = type

DOCUMENTATION = r"""
name: vast_target_cluster_to_remote_target_id
short_description: Resolve a target cluster to a native replication remote target id
description:
  - Select the native replication peer on the source cluster that points at the target cluster
    (either by an explicit C(target_cluster) on the peer or by matching the peer's C(leading_vip)
    against the target cluster's replication VIP ranges) and return its remote target id.
  - Requires exactly one matching peer.
options:
  _terms:
    description:
      - "Five positional terms: source_cluster, target_cluster, the C(vms) connection dict,
        the list of replicationpeers, and the list of vippools."
    required: true
"""

EXAMPLES = r"""
- name: Resolve the remote target id for a source/target cluster pair
  ansible.builtin.set_fact:
    remote_target_id: >-
      {{ lookup('vastdata.vms.vast_target_cluster_to_remote_target_id',
                source_cluster, target_cluster, vms, replicationpeers, vippools) }}
"""

RETURN = r"""
_raw:
  description: The resolved remote target id (single-element list).
  type: list
"""

from ansible.errors import AnsibleError
from ansible.plugins.lookup import LookupBase
from ansible_collections.vastdata.vms.plugins.module_utils.vast.errors import VastError
from ansible_collections.vastdata.vms.plugins.module_utils.vast.name_resolver import (
    target_cluster_to_remote_target_id,
)


class LookupModule(LookupBase):
    def run(self, terms, variables=None, **kwargs):
        if len(terms) != 5:
            raise AnsibleError(
                "vast_target_cluster_to_remote_target_id expects exactly 5 positional terms: "
                f"source_cluster, target_cluster, vms, replicationpeers, vippools (got {len(terms)})"
            )

        source_cluster, target_cluster, vms, replicationpeers, vippools = terms
        try:
            value = target_cluster_to_remote_target_id(
                source_cluster,
                target_cluster,
                vms,
                replicationpeers,
                vippools,
            )
        except VastError as exc:
            raise AnsibleError(str(exc)) from exc

        return [value]
