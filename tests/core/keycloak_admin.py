from __future__ import annotations

import json
import time
from typing import Any
from urllib.parse import urlencode

from tests.core.runner import run


def get_admin_token(*, keycloak_url: str, username: str, password: str) -> str:
    """Get an admin access token for Keycloak admin API calls."""
    token_url = f"{keycloak_url}/realms/master/protocol/openid-connect/token"
    stdout: str = run(
        "curl",
        "-sk",
        "--fail-with-body",
        "-X",
        "POST",
        token_url,
        "-d",
        "grant_type=password",
        "-d",
        "client_id=admin-cli",
        "-d",
        f"username={username}",
        "-d",
        f"password={password}",
    )
    response: dict[str, str] = json.loads(stdout)
    token: str | None = response.get("access_token")
    if not token:
        error: str = response.get("error_description", response.get("error", "unknown error"))
        raise RuntimeError(f"Failed to get admin token from Keycloak: {error}")
    return token


def keycloak_admin_request(
    *, keycloak_url: str, admin_token: str, method: str, path: str, data: Any = None
) -> tuple[int, bytes]:
    """
    Make an authenticated request to the Keycloak admin API for the 'osac' realm.
    The path is relative to /admin/realms/osac (e.g., "/organizations", "/users/{id}").
    Returns (status_code, response_body).
    """
    url = f"{keycloak_url}/admin/realms/osac{path}"
    args = [
        "curl",
        "-sk",
        "-w",
        "\n%{http_code}",
        "-X",
        method,
        "-H",
        f"Authorization: Bearer {admin_token}",
        "-H",
        "Content-Type: application/json",
    ]
    if data is not None:
        if isinstance(data, str):
            args.extend(["-d", data])
        else:
            args.extend(["-d", json.dumps(data)])

    args.append(url)

    output: str = run(*args)
    lines = output.strip().split("\n")
    status_code = int(lines[-1])
    body = "\n".join(lines[:-1]).encode("utf-8")

    return status_code, body


def wait_for_organization(
    *, keycloak_url: str, admin_token: str, org_name: str, timeout_seconds: int = 60
) -> str:
    """
    Wait for an organization to be synced to Keycloak and return its ID.
    Polls with exponential backoff until the organization exists or timeout is reached.
    """
    start_time = time.time()
    interval = 1.0
    max_interval = 10.0

    while time.time() - start_time < timeout_seconds:
        query = urlencode({"exact": "true", "search": org_name})
        status, body = keycloak_admin_request(
            keycloak_url=keycloak_url,
            admin_token=admin_token,
            method="GET",
            path=f"/organizations?{query}",
        )

        if status != 200:
            raise RuntimeError(f"Failed to query organizations: status={status} body={body.decode()}")

        orgs: list[dict[str, Any]] = json.loads(body)
        if len(orgs) > 0:
            org_id: str = orgs[0]["id"]
            return org_id

        time.sleep(interval)
        interval = min(interval * 2, max_interval)

    raise RuntimeError(f"Organization '{org_name}' not found in Keycloak after {timeout_seconds}s")


def get_user_id(*, keycloak_url: str, admin_token: str, username: str) -> str:
    """Get a user's ID by username."""
    query = urlencode({"username": username, "exact": "true"})
    status, body = keycloak_admin_request(
        keycloak_url=keycloak_url, admin_token=admin_token, method="GET", path=f"/users?{query}"
    )

    if status != 200:
        raise RuntimeError(f"Failed to get user '{username}': status={status} body={body.decode()}")

    users: list[dict[str, Any]] = json.loads(body)
    if len(users) == 0:
        raise RuntimeError(f"User '{username}' not found in Keycloak")

    return users[0]["id"]


def add_user_to_organization(
    *, keycloak_url: str, admin_token: str, org_id: str, user_id: str, username: str, org_name: str
) -> None:
    """Add a user to a Keycloak organization."""
    status, body = keycloak_admin_request(
        keycloak_url=keycloak_url,
        admin_token=admin_token,
        method="POST",
        path=f"/organizations/{org_id}/members",
        data=user_id,
    )

    # 201 Created, 204 No Content, or 409 Conflict (already a member) are all acceptable
    # 400 with "Duplicate resource error" is also acceptable (Keycloak returns this when user is already a member)
    if status == 400:
        error_msg = body.decode().lower()
        if "duplicate" not in error_msg:
            raise RuntimeError(
                f"Failed to add user '{username}' to organization '{org_name}': status={status} body={body.decode()}"
            )
    elif status not in (201, 204, 409):
        raise RuntimeError(
            f"Failed to add user '{username}' to organization '{org_name}': status={status} body={body.decode()}"
        )


def ensure_organization_group(*, keycloak_url: str, admin_token: str, org_id: str, org_name: str) -> str:
    """
    Ensure a /members group exists in the organization and return its ID.
    Creates the group if it doesn't exist.
    """
    group_name = "/members"
    group_payload = {"name": group_name}

    status, body = keycloak_admin_request(
        keycloak_url=keycloak_url,
        admin_token=admin_token,
        method="POST",
        path=f"/organizations/{org_id}/groups",
        data=group_payload,
    )

    # 201 Created or 409 Conflict (already exists) are acceptable
    if status == 201:
        group_resp: dict[str, Any] = json.loads(body)
        return group_resp["id"]
    elif status == 409:
        # Group already exists, need to fetch it
        status, body = keycloak_admin_request(
            keycloak_url=keycloak_url, admin_token=admin_token, method="GET", path=f"/organizations/{org_id}/groups"
        )

        if status != 200:
            raise RuntimeError(
                f"Failed to get groups for organization '{org_name}': status={status} body={body.decode()}"
            )

        groups: list[dict[str, Any]] = json.loads(body)
        for g in groups:
            if g.get("name") == group_name:
                return g["id"]

        raise RuntimeError(f"Failed to find group '{group_name}' in organization '{org_name}'")
    else:
        raise RuntimeError(
            f"Failed to create group '{group_name}' in organization '{org_name}': status={status} body={body.decode()}"
        )


def add_user_to_organization_group(
    *, keycloak_url: str, admin_token: str, org_id: str, group_id: str, user_id: str, username: str, org_name: str
) -> None:
    """Add a user to a group within a Keycloak organization."""
    status, body = keycloak_admin_request(
        keycloak_url=keycloak_url,
        admin_token=admin_token,
        method="PUT",
        path=f"/organizations/{org_id}/groups/{group_id}/members/{user_id}",
    )

    # 200 OK, 201 Created, 204 No Content, or 409 Conflict are all acceptable
    # 400 with "Duplicate resource error" is also acceptable (Keycloak returns this when user is already a member)
    if status == 400:
        error_msg = body.decode().lower()
        if "duplicate" not in error_msg:
            raise RuntimeError(
                f"Failed to add user '{username}' to group in organization '{org_name}': "
                f"status={status} body={body.decode()}"
            )
    elif status not in (200, 201, 204, 409):
        raise RuntimeError(
            f"Failed to add user '{username}' to group in organization '{org_name}': "
            f"status={status} body={body.decode()}"
        )
