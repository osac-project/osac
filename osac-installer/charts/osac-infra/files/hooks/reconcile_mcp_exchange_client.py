#!/usr/bin/env python3
"""Ensure the OSAC API audience scope and MCP exchange client are configured."""

from __future__ import annotations

import json
import os
import ssl
import sys
import urllib.error
import urllib.parse
import urllib.request
from typing import Any

REALM = "osac"
SCOPE_NAME = "osac-api"
SCOPE_ATTRIBUTES = {
    "include.in.token.scope": "false",
    "display.on.consent.screen": "false",
    "gui.order": "",
    "consent.screen.text": "",
    "include.in.openid.provider.metadata": "false",
}
AUDIENCE_MAPPER = {
    "name": "osac-api-aud",
    "protocol": "openid-connect",
    "protocolMapper": "oidc-audience-mapper",
    "consentRequired": False,
    "config": {
        "id.token.claim": "false",
        "lightweight.claim": "false",
        "access.token.claim": "true",
        "introspection.token.claim": "true",
        "included.custom.audience": SCOPE_NAME,
    },
}
_HTTPS_CONTEXT: ssl.SSLContext | None = None
DEFAULT_CLIENT_SCOPES = ["groups", "basic", "username", "organization", "roles", SCOPE_NAME]
# Keep these clients aligned with the osac-api default scopes in realm.json. When
# the scope is deleted and recreated, Keycloak drops its existing client links.
API_SCOPE_CLIENT_IDS = ("osac-cli", "osac-ui", "osac-admin", "osac-ui-backend", "osac-csi-driver")


class KeycloakError(RuntimeError):
    """A Keycloak request failed without exposing credentials or response bodies."""


def _open_request(
    url: str,
    method: str,
    body: bytes | None = None,
    headers: dict[str, str] | None = None,
) -> bytes:
    request = urllib.request.Request(url, data=body, headers=headers or {}, method=method)
    options: dict[str, Any] = {"timeout": 30}
    if url.startswith("https://"):
        options["context"] = _https_context()
    try:
        with urllib.request.urlopen(request, **options) as response:
            return response.read()
    except urllib.error.HTTPError as error:
        status = error.code
        error.close()
        raise KeycloakError(f"Keycloak {method} {urllib.parse.urlsplit(url).path} returned HTTP {status}") from None
    except urllib.error.URLError as error:
        host = urllib.parse.urlsplit(url).netloc
        raise KeycloakError(f"could not reach Keycloak at {host}: {error.reason}") from None
    except (TimeoutError, OSError) as error:
        raise KeycloakError(f"could not reach Keycloak at {urllib.parse.urlsplit(url).netloc}: {error}") from None


def _https_context() -> ssl.SSLContext:
    global _HTTPS_CONTEXT
    if _HTTPS_CONTEXT is not None:
        return _HTTPS_CONTEXT
    ca_path = os.environ.get("KEYCLOAK_CA_PATH")
    if not ca_path:
        raise KeycloakError("KEYCLOAK_CA_PATH is required for HTTPS Keycloak requests")
    try:
        _HTTPS_CONTEXT = ssl.create_default_context(cafile=ca_path)
        return _HTTPS_CONTEXT
    except (OSError, ssl.SSLError) as error:
        raise KeycloakError(f"could not load the Keycloak CA certificate from {ca_path}: {error}") from None


def _decode_json(body: bytes, description: str) -> Any:  # noqa: ANN401
    try:
        return json.loads(body) if body else None
    except json.JSONDecodeError as error:
        raise KeycloakError(f"Keycloak returned invalid JSON while {description}: {error.msg}") from None


def get_admin_token(keycloak_url: str, username: str, password: str) -> str:
    """Get an admin token using the credentials supplied to the installer hook."""
    form = urllib.parse.urlencode(
        {"grant_type": "password", "client_id": "admin-cli", "username": username, "password": password}
    ).encode()
    body = _open_request(
        f"{keycloak_url.rstrip('/')}/realms/master/protocol/openid-connect/token",
        "POST",
        form,
        {"Content-Type": "application/x-www-form-urlencoded"},
    )
    payload = _decode_json(body, "requesting an admin token")
    token = payload.get("access_token") if isinstance(payload, dict) else None
    if not isinstance(token, str) or not token:
        raise KeycloakError("Keycloak did not return an admin access token; verify the installer admin credentials")
    return token


def admin_request(
    keycloak_url: str,
    token: str,
    method: str,
    path: str,
    payload: dict[str, Any] | None = None,
) -> Any:  # noqa: ANN401
    """Call the Keycloak Admin API and decode its optional JSON response."""
    headers = {"Authorization": f"Bearer {token}"}
    body = None
    if payload is not None:
        body = json.dumps(payload).encode()
        headers["Content-Type"] = "application/json"
    response = _open_request(f"{keycloak_url.rstrip('/')}{path}", method, body, headers)
    return _decode_json(response, f"{method} {path}")


def _list(response: Any, description: str) -> list[dict[str, Any]]:  # noqa: ANN401
    if not isinstance(response, list) or any(not isinstance(item, dict) for item in response):
        raise KeycloakError(f"Keycloak returned an invalid {description} list")
    return response


def _find_scope(scopes: list[dict[str, Any]]) -> dict[str, Any] | None:
    return next((item for item in scopes if item.get("name") == SCOPE_NAME), None)


def _find_client(clients: list[dict[str, Any]], client_id: str) -> dict[str, Any] | None:
    return next((item for item in clients if item.get("clientId") == client_id), None)


def _find_audience_mapper(mappers: list[dict[str, Any]]) -> dict[str, Any] | None:
    return next((item for item in mappers if item.get("name") == AUDIENCE_MAPPER["name"]), None)


def _scope_is_current(scope: dict[str, Any]) -> bool:
    attributes = scope.get("attributes")
    return (
        scope.get("protocol") == "openid-connect"
        and isinstance(attributes, dict)
        and all(attributes.get(key) == value for key, value in SCOPE_ATTRIBUTES.items())
    )


def _audience_mapper_is_current(mapper: dict[str, Any]) -> bool:
    config = mapper.get("config")
    return (
        mapper.get("protocol") == AUDIENCE_MAPPER["protocol"]
        and mapper.get("protocolMapper") == AUDIENCE_MAPPER["protocolMapper"]
        and mapper.get("consentRequired") == AUDIENCE_MAPPER["consentRequired"]
        and isinstance(config, dict)
        and all(config.get(key) == value for key, value in AUDIENCE_MAPPER["config"].items())
    )


def _ensure_scope(keycloak_url: str, token: str) -> dict[str, Any]:
    scopes_path = f"/admin/realms/{REALM}/client-scopes"
    scopes = _list(admin_request(keycloak_url, token, "GET", scopes_path), "client scope")
    scope = _find_scope(scopes)
    scope_payload = {
        "name": SCOPE_NAME,
        "description": "OSAC API",
        "protocol": "openid-connect",
        "attributes": SCOPE_ATTRIBUTES,
    }
    if scope is None:
        try:
            admin_request(keycloak_url, token, "POST", scopes_path, scope_payload)
        except KeycloakError as error:
            raise KeycloakError(
                f"could not create the {SCOPE_NAME} client scope; verify Keycloak realm-management permissions: {error}"
            ) from None
        scopes = _list(admin_request(keycloak_url, token, "GET", scopes_path), "client scope")
        scope = _find_scope(scopes)
    if scope is None or not isinstance(scope.get("id"), str) or not scope["id"]:
        raise KeycloakError(f"Keycloak did not expose the {SCOPE_NAME} client scope after reconciliation")
    if not _scope_is_current(scope):
        scope_id = urllib.parse.quote(scope["id"], safe="")
        update_payload = {**scope_payload, "id": scope["id"]}
        try:
            admin_request(keycloak_url, token, "PUT", f"{scopes_path}/{scope_id}", update_payload)
        except KeycloakError as error:
            raise KeycloakError(
                f"could not update the {SCOPE_NAME} client scope; verify Keycloak realm-management permissions: {error}"
            ) from None
        scope = _find_scope(_list(admin_request(keycloak_url, token, "GET", scopes_path), "client scope"))
        if scope is None or not _scope_is_current(scope):
            raise KeycloakError(
                f"the {SCOPE_NAME} client scope is not configured for OpenID Connect after reconciliation"
            )

    scope_id = urllib.parse.quote(scope["id"], safe="")
    mappers_path = f"{scopes_path}/{scope_id}/protocol-mappers/models"
    mappers = _list(admin_request(keycloak_url, token, "GET", mappers_path), "protocol mapper")
    mapper = _find_audience_mapper(mappers)
    mapper_payload = dict(AUDIENCE_MAPPER)
    mapper_was_current = mapper is not None and _audience_mapper_is_current(mapper)
    mapper_protocol_is_wrong = mapper is not None and mapper.get("protocol") != AUDIENCE_MAPPER["protocol"]
    if mapper is None or mapper_protocol_is_wrong:
        if mapper is not None:
            if not mapper.get("id"):
                raise KeycloakError(f"Keycloak returned the existing {SCOPE_NAME} audience mapper without an ID")
            mapper_id = urllib.parse.quote(str(mapper.get("id", "")), safe="")
            try:
                admin_request(keycloak_url, token, "DELETE", f"{mappers_path}/{mapper_id}")
            except KeycloakError as error:
                raise KeycloakError(
                    f"could not replace the {SCOPE_NAME} audience mapper with an incompatible protocol: {error}"
                ) from None
        try:
            admin_request(keycloak_url, token, "POST", mappers_path, mapper_payload)
        except KeycloakError as error:
            raise KeycloakError(
                f"could not create the {SCOPE_NAME} audience mapper; verify Keycloak realm-management "
                f"permissions: {error}"
            ) from None
    elif not _audience_mapper_is_current(mapper):
        if not mapper.get("id"):
            raise KeycloakError(f"Keycloak returned the existing {SCOPE_NAME} audience mapper without an ID")
        mapper_payload["id"] = mapper.get("id")
        mapper_id = urllib.parse.quote(str(mapper.get("id", "")), safe="")
        try:
            admin_request(keycloak_url, token, "PUT", f"{mappers_path}/{mapper_id}", mapper_payload)
        except KeycloakError as error:
            raise KeycloakError(
                f"could not update the {SCOPE_NAME} audience mapper; verify Keycloak realm-management "
                f"permissions: {error}"
            ) from None

    if not mapper_was_current:
        mappers = _list(admin_request(keycloak_url, token, "GET", mappers_path), "protocol mapper")
        mapper = _find_audience_mapper(mappers)
        if mapper is None or not _audience_mapper_is_current(mapper):
            raise KeycloakError(
                f"the {SCOPE_NAME} audience mapper is not configured to issue the required API audience"
            )
    return scope


def _ensure_default_scope_assignment(
    keycloak_url: str,
    token: str,
    scope: dict[str, Any],
    client_id: str,
    client_description: str,
    client: dict[str, Any] | None = None,
) -> None:
    clients_path = f"/admin/realms/{REALM}/clients"
    if client is None:
        query = urllib.parse.urlencode({"clientId": client_id})
        clients = _list(admin_request(keycloak_url, token, "GET", f"{clients_path}?{query}"), "client")
        client = _find_client(clients, client_id)
    if client is None or not isinstance(client.get("id"), str) or not client["id"]:
        raise KeycloakError(f"Keycloak did not expose {client_description} while assigning {SCOPE_NAME}")

    client_uuid = urllib.parse.quote(client["id"], safe="")
    scope_uuid = urllib.parse.quote(scope["id"], safe="")
    assignment_path = f"{clients_path}/{client_uuid}/default-client-scopes/{scope_uuid}"
    default_scopes = _list(
        admin_request(keycloak_url, token, "GET", f"{clients_path}/{client_uuid}/default-client-scopes"),
        "default client scope",
    )
    if any(item.get("id") == scope["id"] and item.get("name") == SCOPE_NAME for item in default_scopes):
        return

    optional_scopes = _list(
        admin_request(keycloak_url, token, "GET", f"{clients_path}/{client_uuid}/optional-client-scopes"),
        "optional client scope",
    )
    if any(item.get("id") == scope["id"] and item.get("name") == SCOPE_NAME for item in optional_scopes):
        optional_assignment_path = f"{clients_path}/{client_uuid}/optional-client-scopes/{scope_uuid}"
        try:
            admin_request(keycloak_url, token, "DELETE", optional_assignment_path)
        except KeycloakError as error:
            raise KeycloakError(
                f"could not promote {SCOPE_NAME} from optional to default scope on {client_description}: {error}"
            ) from None

    try:
        admin_request(keycloak_url, token, "PUT", assignment_path)
    except KeycloakError as error:
        raise KeycloakError(
            f"could not assign {SCOPE_NAME} as a default client scope to {client_description}: {error}"
        ) from None

    default_scopes = _list(
        admin_request(keycloak_url, token, "GET", f"{clients_path}/{client_uuid}/default-client-scopes"),
        "default client scope",
    )
    if not any(item.get("id") == scope["id"] and item.get("name") == SCOPE_NAME for item in default_scopes):
        raise KeycloakError(f"Keycloak did not attach {SCOPE_NAME} to {client_description} default scopes")


def _ensure_api_client_scope_assignments(keycloak_url: str, token: str, scope: dict[str, Any]) -> None:
    for client_id in API_SCOPE_CLIENT_IDS:
        _ensure_default_scope_assignment(keycloak_url, token, scope, client_id, f"API client '{client_id}'")


def _ensure_exchange_client(
    keycloak_url: str,
    token: str,
    scope: dict[str, Any],
    resource_url: str,
    exchange_secret: str,
) -> None:
    clients_path = f"/admin/realms/{REALM}/clients"
    query = urllib.parse.urlencode({"clientId": resource_url})

    def find_client() -> dict[str, Any] | None:
        clients = _list(admin_request(keycloak_url, token, "GET", f"{clients_path}?{query}"), "client")
        return _find_client(clients, resource_url)


    client_payload = {
        "clientId": resource_url,
        "name": "OSAC MCP resource and exchange client",
        "enabled": True,
        "protocol": "openid-connect",
        "publicClient": False,
        "secret": exchange_secret,
        "standardFlowEnabled": False,
        "directAccessGrantsEnabled": False,
        "serviceAccountsEnabled": False,
        "fullScopeAllowed": True,
        "defaultClientScopes": DEFAULT_CLIENT_SCOPES,
        "attributes": {
            "standard.token.exchange.enabled": "true",
            "resource_url": resource_url,
        },
    }
    client = find_client()
    if client is None:
        try:
            admin_request(keycloak_url, token, "POST", clients_path, client_payload)
        except KeycloakError as error:
            raise KeycloakError(f"could not create the MCP exchange client: {error}") from None
        client = find_client()
    else:
        client_id = urllib.parse.quote(str(client.get("id", "")), safe="")
        if not client_id:
            raise KeycloakError("Keycloak returned an MCP exchange client without an ID")
        try:
            admin_request(keycloak_url, token, "PUT", f"{clients_path}/{client_id}", client_payload)
        except KeycloakError as error:
            raise KeycloakError(f"could not update the MCP exchange client: {error}") from None
    if client is None or not isinstance(client.get("id"), str) or not client["id"]:
        raise KeycloakError("Keycloak did not expose the MCP exchange client after reconciliation")

    _ensure_default_scope_assignment(
        keycloak_url,
        token,
        scope,
        resource_url,
        f"MCP exchange client '{resource_url}'",
        client,
    )


def reconcile(
    keycloak_url: str,
    admin_username: str,
    admin_password: str,
    resource_url: str | None = None,
    exchange_secret: str | None = None,
) -> None:
    """Create/repair the audience scope and its API clients, then configure the MCP client."""
    token = get_admin_token(keycloak_url, admin_username, admin_password)
    scope = _ensure_scope(keycloak_url, token)
    if bool(resource_url) != bool(exchange_secret):
        raise KeycloakError(
            "MCP_RESOURCE_URL and MCP_EXCHANGE_SECRET must both be configured for client reconciliation"
        )
    _ensure_api_client_scope_assignments(keycloak_url, token, scope)
    if resource_url and exchange_secret:
        _ensure_exchange_client(keycloak_url, token, scope, resource_url, exchange_secret)


def main() -> int:
    try:
        reconcile(
            os.environ.get("KEYCLOAK_URL", "https://keycloak:443"),
            os.environ["ADMIN_USERNAME"],
            os.environ["ADMIN_PASSWORD"],
            os.environ.get("MCP_RESOURCE_URL"),
            os.environ.get("MCP_EXCHANGE_SECRET"),
        )
    except (KeyError, KeycloakError) as error:
        print(f"ERROR: MCP Keycloak reconciliation failed: {error}", file=sys.stderr)
        return 1
    print(f"Keycloak {SCOPE_NAME} audience scope is reconciled")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
