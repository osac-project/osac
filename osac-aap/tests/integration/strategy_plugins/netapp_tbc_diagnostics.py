"""Report sanitized API status for the NetApp integration target's failed TBC update."""

import ast
import json
import warnings

from ansible.plugins.strategy.linear import StrategyModule as LinearStrategy
from ansible.utils.display import Display

UPDATE_TASK = (
    "osac.templates.netapp_storage : Update the TridentBackendConfig "
    "for dispatched backend"
)
PATCH_ERROR_PREFIX = "Failed to patch object: "
MAX_STATUS_LENGTH = 16 * 1024
STATUS_REASONS = frozenset(
    {
        "AlreadyExists",
        "BadRequest",
        "Conflict",
        "Expired",
        "Forbidden",
        "Gone",
        "InternalError",
        "Invalid",
        "MethodNotAllowed",
        "NotAcceptable",
        "NotFound",
        "RequestEntityTooLarge",
        "ServerTimeout",
        "ServiceUnavailable",
        "StoreReadError",
        "Timeout",
        "TooManyRequests",
        "Unauthorized",
        "UnsupportedMediaType",
    }
)

display = Display()


def _patch_status(message):
    if not isinstance(message, str) or not message.startswith(PATCH_ERROR_PREFIX):
        return {}

    body = message[len(PATCH_ERROR_PREFIX) :]
    if len(body) > MAX_STATUS_LENGTH:
        return {}

    try:
        # kubernetes.core 5.5.0 formats the Python client's raw response bytes
        # into the message. The same message can also contain plain JSON.
        if body.startswith(("b'", 'b"')):
            # Parser warnings can quote part of a malformed response.
            with warnings.catch_warnings(action="ignore"):
                body = ast.literal_eval(body)
        status = json.loads(body)
    except (SyntaxError, ValueError, TypeError, RecursionError):
        return {}

    if (
        isinstance(status, dict)
        and status.get("apiVersion") == "v1"
        and status.get("kind") == "Status"
        and status.get("status") == "Failure"
    ):
        return status
    return {}


def diagnostic_for_result(result):
    task = result._task
    if (
        not result.is_failed()
        or task.action != "kubernetes.core.k8s"
        or task.get_name() != UPDATE_TASK
    ):
        return None

    status = _patch_status(result._result.get("msg"))
    code = status.get("code")
    reason = status.get("reason")
    safe_code = code if type(code) is int and 400 <= code <= 599 else "unavailable"
    safe_reason = (
        reason if isinstance(reason, str) and reason in STATUS_REASONS else "unavailable"
    )
    return f"NetApp TBC update failed (HTTP status={safe_code}, Kubernetes reason={safe_reason})"


class StrategyModule(LinearStrategy):
    def _process_pending_results(self, iterator, one_pass=False, max_passes=None):
        # The strategy retains the final failed_when decision and module result.
        # Ansible passes only censored copies to callbacks for no_log tasks.
        results = super()._process_pending_results(
            iterator, one_pass=one_pass, max_passes=max_passes
        )
        for result in results:
            diagnostic = diagnostic_for_result(result)
            if diagnostic is not None:
                display.display(diagnostic)
        return results
