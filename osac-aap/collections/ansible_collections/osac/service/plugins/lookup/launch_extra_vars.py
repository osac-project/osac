# Copyright (c) 2026 Red Hat, Inc.
# SPDX-License-Identifier: Apache-2.0
"""List the names of the extra vars the job was launched with."""

from __future__ import annotations

from ansible.plugins.lookup import LookupBase
from ansible.utils.vars import load_extra_vars

DOCUMENTATION = r"""
name: launch_extra_vars
author: OSAC
short_description: List the names of the job's launch extra vars
description:
  - Returns the sorted top-level names of every C(--extra-vars) argument of
    the running C(ansible-playbook), as AAP passes them (C(-e @/runner/env/extravars)).
    Only names are returned, never values.
"""

RETURN = r"""
_raw:
  description: The extra var names.
  type: list
  elements: str
"""


class LookupModule(LookupBase):
    def run(self, terms: list, variables: dict | None = None, **kwargs: object) -> list[str]:
        return sorted(str(name) for name in load_extra_vars(self._loader))
