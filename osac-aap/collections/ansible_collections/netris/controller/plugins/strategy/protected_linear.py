"""Run the linear strategy only after validating original AAP extra vars."""

from __future__ import annotations

import os
from pathlib import Path

from ansible.errors import AnsibleError
from ansible.parsing.dataloader import DataLoader
from ansible.plugins.strategy.linear import StrategyModule as LinearStrategyModule

from ansible_collections.netris.controller.plugins.module_utils.protected_connection import (
    ProtectedInputError,
    validate_extra_vars,
)


_DEFAULT_EXTRA_VARS_PATH = "/runner/env/extravars"


def _validate_launch_extra_vars() -> None:
    """Reject protected keys in Ansible Runner's original launch payload."""

    path = Path(
        os.environ.get("OSAC_LAUNCH_EXTRA_VARS_PATH", _DEFAULT_EXTRA_VARS_PATH)
    )
    if not path.is_file():
        raise AnsibleError("original AAP launch extra_vars are unavailable")

    try:
        extra_vars = DataLoader().load_from_file(
            str(path), cache="none", unsafe=True
        )
    except Exception as error:
        raise AnsibleError("original AAP launch extra_vars are invalid") from error

    try:
        validate_extra_vars(extra_vars)
    except ProtectedInputError as error:
        raise AnsibleError(str(error)) from error


class StrategyModule(LinearStrategyModule):
    """Fail before the first task when AAP launch input is not trusted."""

    def run(self, iterator, play_context):
        _validate_launch_extra_vars()
        return super().run(iterator, play_context)
