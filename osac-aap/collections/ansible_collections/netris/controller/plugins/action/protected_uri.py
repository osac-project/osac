"""Run the URI module only with installation-owned Netris connection settings."""

from __future__ import annotations

import os
from typing import Any

from ansible.errors import AnsibleActionFail
from ansible.plugins.action import ActionBase


def _expected_validate_certs() -> bool:
    return os.environ.get("NETRIS_VALIDATE_CERTS", "true").strip().lower() != "false"


def _as_bool(value: Any) -> bool:
    if isinstance(value, str):
        return value.strip().lower() != "false"
    return bool(value)


class ActionModule(ActionBase):
    """Enforce process-environment Netris settings before an HTTP request."""

    TRANSFERS_FILES = False

    def run(
        self,
        tmp: str | None = None,
        task_vars: dict[str, Any] | None = None,
    ) -> dict[str, Any]:
        del tmp
        result = super().run(task_vars=task_vars)
        args = dict(self._task.args)
        expected_url = os.environ.get("NETRIS_CONTROLLER_URL", "").rstrip("/")
        actual_url = args.get("url")
        if not expected_url or (
            not isinstance(actual_url, str)
            or (actual_url != expected_url and not actual_url.startswith(f"{expected_url}/"))
        ):
            raise AnsibleActionFail("Netris controller URL is not installation-managed")

        if "validate_certs" not in args or _as_bool(
            args["validate_certs"]
        ) != _expected_validate_certs():
            raise AnsibleActionFail("Netris certificate validation is not installation-managed")

        expected_ca = os.environ.get("NETRIS_CA_PATH", "")
        actual_ca = args.get("ca_path", "") or ""
        if actual_ca != expected_ca:
            raise AnsibleActionFail("Netris CA path is not installation-managed")

        result.update(
            self._execute_module(
                module_name="ansible.builtin.uri",
                module_args=args,
                task_vars=task_vars,
            )
        )
        return result
