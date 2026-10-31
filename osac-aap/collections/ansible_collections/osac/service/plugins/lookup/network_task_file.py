# Copyright (c) 2026 Red Hat, Inc.
# SPDX-License-Identifier: Apache-2.0
"""Find a network manager task file on the collection search path."""

from __future__ import annotations

import os
import re

from ansible.errors import AnsibleLookupError
from ansible.plugins.lookup import LookupBase
from ansible.utils.collection_loader import AnsibleCollectionConfig

DOCUMENTATION = r"""
name: network_task_file
author: OSAC
short_description: Find the task file of a network manager role
description:
  - Returns the path of C(roles/<role>/tasks/<task>.yaml) or C(.yml) of the
    role named by its fully qualified name, searched on the collection path
    in order, or an empty string when the role or the task file is missing.
options:
  _terms:
    description: The role's fully qualified name and the task name.
    required: true
"""

RETURN = r"""
_raw:
  description: The task file path, or an empty string.
  type: list
  elements: str
"""

ROLE_RE = re.compile(r"^[a-z_][a-z0-9_]*\.[a-z_][a-z0-9_]*\.[a-z_][a-z0-9_]*$")
TASK_RE = re.compile(r"^[a-z][a-z0-9_]*$")


def find_task_file(role, task, search_paths):
    """Return the first task file of role on search_paths, or an empty string."""
    if not ROLE_RE.match(role or "") or not TASK_RE.match(task or ""):
        return ""
    namespace, collection, name = role.split(".")
    for base in search_paths:
        role_dir = os.path.join(base, "ansible_collections", namespace, collection, "roles", name)
        if not os.path.isdir(role_dir):
            continue
        for ext in (".yaml", ".yml"):
            path = os.path.join(role_dir, "tasks", task + ext)
            if os.path.isfile(path):
                return path
        # The first collection that holds the role wins, as Ansible resolves it.
        return ""
    return ""


class LookupModule(LookupBase):
    def run(self, terms, variables=None, **kwargs):
        if len(terms) != 2:
            raise AnsibleLookupError("network_task_file needs the role and the task name")
        role, task = terms
        paths = []
        playbook_dir = (variables or {}).get("playbook_dir")
        if playbook_dir:
            paths.append(os.path.join(playbook_dir, "collections"))
        paths.extend(AnsibleCollectionConfig.collection_paths or [])
        paths.append("/usr/share/ansible/collections")
        seen = []
        for path in paths:
            path = os.path.abspath(os.path.expanduser(path))
            if path not in seen:
                seen.append(path)
        return [find_task_file(role, task, seen)]
