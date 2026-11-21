"""Authentication validation and connection builder."""

from .client import VastConnection
from .errors import VastAuthError


def validate_auth(params: dict) -> None:
    """
    Ensure token XOR (username + password) is provided.

    Raises VastAuthError if invalid.
    """
    vms = params.get("vms", {})
    token = vms.get("token")
    username = vms.get("username")
    password = vms.get("password")

    has_token = token is not None and token != ""
    has_user_pass = (username is not None and username != "") and (password is not None and password != "")

    if has_token and has_user_pass:
        raise VastAuthError("Provide either token OR username+password, not both")
    if not has_token and not has_user_pass:
        raise VastAuthError("Provide either token OR username+password")


def build_connection(params: dict) -> VastConnection:
    """Build VastConnection from nested vms params."""
    vms = params.get("vms") or {}
    host = vms.get("host")
    if not host:
        raise VastAuthError("vms.host is required")
    return VastConnection(
        host=host,
        token=vms.get("token") or None,
        username=vms.get("username") or None,
        password=vms.get("password") or None,
        validate_certs=vms.get("validate_certs", True),
        timeout=vms.get("timeout"),
        tenant=vms.get("tenant") or None,
        api_version=vms.get("api_version"),
        debug=vms.get("debug", False),
    )
