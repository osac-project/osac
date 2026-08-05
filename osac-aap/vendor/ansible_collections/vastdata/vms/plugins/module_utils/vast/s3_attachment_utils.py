# -*- coding: utf-8 -*-
# Copyright: (c) 2026, VAST Data
# GNU General Public License v3.0+ (see COPYING or https://www.gnu.org/licenses/gpl-3.0.txt)

"""Shared helpers for S3 policy attachment modules (user/group)."""

from __future__ import absolute_import, division, print_function

__metaclass__ = type


def resolve_s3_policy_id_by_guid(client, policy_guid):
    if not policy_guid:
        return None
    result = client.api.s3policies.get()
    if not result:
        return None
    for p in result:
        if p and p.get("guid") == policy_guid:
            return p.get("id")
    return None


def current_policy_ids(entity):
    raw = entity.get("s3_policies_ids") if entity else None
    if raw is None:
        return []
    if isinstance(raw, (list, dict)):
        return [int(x) for x in raw if x is not None]
    return []
