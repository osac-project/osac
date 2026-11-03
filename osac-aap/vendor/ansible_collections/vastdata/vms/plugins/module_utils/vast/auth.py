"""Authentication validation and connection builder.

``connection_from_vms`` is the single source of truth for turning a ``vms``
connection dict into a :class:`VastConnection`. Both the module base classes
(via :func:`build_connection`) and the lookup plugins (via
``name_resolver.build_connection``) delegate to it so auth validation, alias
handling, and error types stay consistent.
"""

import os
from typing import Optional, Tuple

from .client import VastConnection
from .errors import VastAuthError

# Alias keys accepted from lookup-plugin terms (dataprotection-style ``vms_*``).
_ALIASES = {
    "host": ("host", "vms_host", "hostname"),
    "token": ("token", "vms_token"),
    "username": ("username", "vms_username"),
    "password": ("password", "vms_password"),
    "tenant": ("tenant", "vms_tenant"),
}


def _mtls_paths(vms: dict) -> Tuple[Optional[str], Optional[str]]:
    """Resolve optional mTLS client certificate paths for the HTTP session.

    Paths may be supplied via the process environment (``VAST_CLIENT_CERT`` /
    ``VAST_CLIENT_KEY``) so integration tests and ad-hoc recovery scripts can
    present the VMS-installed client certificate without extending every
    module's ``vms`` argument spec.
    """
    cert = os.environ.get("VAST_CLIENT_CERT")
    key = os.environ.get("VAST_CLIENT_KEY")
    if cert and not key:
        raise VastAuthError("VAST_CLIENT_KEY is required when VAST_CLIENT_CERT is set")
    if key and not cert:
        raise VastAuthError("VAST_CLIENT_CERT is required when VAST_CLIENT_KEY is set")
    return cert, key


def _pick(vms: dict, field: str, allow_aliases: bool) -> Optional[str]:
    keys = _ALIASES[field] if allow_aliases else (field,)
    for key in keys:
        value = vms.get(key)
        if value is not None and value != "":
            return value
    return None


def connection_from_vms(vms: dict, allow_aliases: bool = False) -> VastConnection:
    """Build (and validate) a VastConnection from a ``vms`` connection dict.

    Enforces token XOR username+password and a required host. Raises
    :class:`VastAuthError` (a ``VastError``) on any problem.
    """
    if not isinstance(vms, dict):
        raise VastAuthError("vms must be a dictionary")

    host = _pick(vms, "host", allow_aliases)
    token = _pick(vms, "token", allow_aliases)
    username = _pick(vms, "username", allow_aliases)
    password = _pick(vms, "password", allow_aliases)

    if not host:
        raise VastAuthError("vms.host is required")

    has_token = token is not None
    has_user_pass = username is not None and password is not None
    if has_token and has_user_pass:
        raise VastAuthError("Provide either token OR username+password, not both")
    if not has_token and not has_user_pass:
        raise VastAuthError("Provide either token OR username+password")

    client_cert, client_key = _mtls_paths(vms)

    return VastConnection(
        host=host,
        token=token,
        username=username,
        password=password,
        validate_certs=vms.get("validate_certs", True),
        timeout=vms.get("timeout"),
        tenant=_pick(vms, "tenant", allow_aliases),
        api_version=vms.get("api_version"),
        debug=vms.get("debug", False),
        client_cert=client_cert,
        client_key=client_key,
    )


def validate_auth(params: dict) -> None:
    """Ensure token XOR (username + password) is provided.

    Kept for the module base classes, which validate before constructing the
    client. Delegates to :func:`connection_from_vms` so the rules live in one
    place. Raises VastAuthError if invalid.
    """
    connection_from_vms(params.get("vms") or {})


def build_connection(params: dict) -> VastConnection:
    """Build VastConnection from nested ``vms`` module params."""
    return connection_from_vms(params.get("vms") or {})
