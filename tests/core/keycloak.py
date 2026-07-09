from __future__ import annotations

import json

from tests.core.runner import poll_until, run_unchecked


def get_jwt(*, keycloak_url: str, realm: str, client_id: str, username: str, password: str) -> str:
    """Get JWT token from Keycloak with retry logic for startup flakiness."""
    token_url = f"{keycloak_url}/realms/{realm}/protocol/openid-connect/token"

    def try_get_token() -> str | None:
        stdout, returncode = run_unchecked(
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
            "scope=openid organization",
        )

        if returncode != 0:
            return None

        try:
            response: dict[str, str] = json.loads(stdout)
            token: str | None = response.get("access_token")
            if not token:
                error: str = response.get("error_description", response.get("error", "unknown error"))
                raise RuntimeError(f"Failed to get JWT from Keycloak for user '{username}': {error}")
            return token
        except json.JSONDecodeError:
            # Malformed response - Keycloak might still be starting
            return None

    return poll_until(
        fn=try_get_token,
        until=lambda token: token is not None,
        retries=24,
        delay=5,
        description=f"Keycloak JWT token for user '{username}'",
    )
