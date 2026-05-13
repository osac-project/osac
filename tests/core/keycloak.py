from __future__ import annotations

import json

from tests.core.runner import run


def get_jwt(*, keycloak_url: str, realm: str, client_id: str, username: str, password: str) -> str:
    token_url = f"{keycloak_url}/realms/{realm}/protocol/openid-connect/token"
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
        f"client_id={client_id}",
        "-d",
        f"username={username}",
        "-d",
        f"password={password}",
        "-d",
        "scope=openid",
    )
    response: dict[str, str] = json.loads(stdout)
    token: str | None = response.get("access_token")
    if not token:
        error: str = response.get("error_description", response.get("error", "unknown error"))
        raise RuntimeError(f"Failed to get JWT from Keycloak for user '{username}': {error}")
    return token
