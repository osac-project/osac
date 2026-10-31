from __future__ import annotations

import base64
import binascii
import hmac
import json
import os
import re
import ssl
import subprocess
import time
from collections.abc import Callable, Generator
from typing import Any
from urllib.error import HTTPError, URLError
from urllib.parse import quote, urlencode, urlparse
from urllib.request import HTTPRedirectHandler, HTTPSHandler, Request, build_opener

import pytest

from tests.e2e.core.grpc_client import PRIVATE_API, PUBLIC_API, GRPCClient
from tests.e2e.core.helpers import unique_name
from tests.e2e.core.keycloak import get_jwt
from tests.e2e.core.runner import poll_until

pytestmark = [pytest.mark.regression, pytest.mark.iam]

_READY = "SERVICE_ACCOUNT_STATE_READY"
_READY_OR_FAILED = {_READY, "SERVICE_ACCOUNT_STATE_FAILED"}
_ROTATION_PENDING = "SERVICE_ACCOUNT_SECRET_ROTATION_STATE_PENDING"
_ROTATION_COMPLETED = "SERVICE_ACCOUNT_SECRET_ROTATION_STATE_COMPLETED"
_ROTATION_ERROR = "SERVICE_ACCOUNT_SECRET_ROTATION_STATE_ERROR"
_GRPC_CODE = re.compile(r"Code:\s*([A-Za-z]+)")
_SERVICE_ACCOUNT_CLIENT_ID_PREFIX = "osac-sa-"
_RPC_TIMEOUT_SECONDS = 300
_HTTP_TIMEOUT_SECONDS = 10
_STREAM_RPC_TIMEOUT_SECONDS = 10
_POLL_DELAY_SECONDS = 2
_SHORT_POLL_RETRIES = 30
_LONG_POLL_RETRIES = 60


class _NoRedirectHandler(HTTPRedirectHandler):
    def redirect_request(
        self,
        request: Request,
        response: Any,  # noqa: ANN401
        code: int,
        message: str,
        headers: Any,  # noqa: ANN401
        new_url: str,
    ) -> None:
        return None


def _camel_case(name: str) -> str:
    head, *tail = name.split("_")
    return head + "".join(part.title() for part in tail)


def _field(mapping: dict[str, Any], name: str, default: Any = None) -> Any:  # noqa: ANN401
    return mapping.get(_camel_case(name), mapping.get(name, default))


def _grpc_error_code(output: str) -> str | None:
    match = _GRPC_CODE.search(output)
    return match.group(1) if match else None


def _call(client: GRPCClient, *, service: str, data: dict[str, Any] | None = None) -> dict[str, Any]:
    try:
        return client.call(service=service, data=data)
    except subprocess.TimeoutExpired:
        raise RuntimeError(f"Fulfillment {service} timed out; response details redacted") from None
    except subprocess.CalledProcessError as exc:
        code = _grpc_error_code((exc.stderr or "") + (exc.stdout or ""))
        suffix = f" with gRPC {code}" if code else ""
        raise RuntimeError(f"Fulfillment {service} failed{suffix}; response details redacted") from None


def _call_unchecked(
    client: GRPCClient, *, service: str, data: dict[str, Any] | None = None, timeout: int = _RPC_TIMEOUT_SECONDS
) -> tuple[dict[str, Any] | None, str | None]:
    try:
        output, returncode = client.call_unchecked(service=service, data=data, timeout=timeout)
    except subprocess.TimeoutExpired:
        raise RuntimeError(f"Fulfillment {service} timed out; response details redacted") from None
    if returncode != 0:
        return None, _grpc_error_code(output) or "Unknown"
    try:
        response: dict[str, Any] = json.loads(output)
    except json.JSONDecodeError:
        raise RuntimeError(f"Fulfillment {service} returned invalid JSON; response details redacted") from None
    return response, None


def _assert_grpc_rejected(
    client: GRPCClient,
    *,
    service: str,
    data: dict[str, Any] | None,
    expected_code: str,
    timeout: int = _RPC_TIMEOUT_SECONDS,
) -> None:
    response, code = _call_unchecked(client, service=service, data=data, timeout=timeout)
    assert response is None and code == expected_code, f"Expected gRPC {expected_code}; got {code or 'success'}"


def _authorized_response(
    client: GRPCClient, *, service: str, data: dict[str, Any] | None = None
) -> dict[str, Any] | None:
    response, code = _call_unchecked(client, service=service, data=data)
    if code == "PermissionDenied":
        return None
    if code:
        raise RuntimeError(f"{service} failed with gRPC {code}; response details redacted")
    return response


def _get_service_account(client: GRPCClient, account_id: str) -> dict[str, Any] | None:
    response, code = _call_unchecked(client, service=f"{PUBLIC_API}.ServiceAccounts/Get", data={"id": account_id})
    if code == "NotFound":
        return None
    if code:
        raise RuntimeError(f"ServiceAccounts/Get failed with gRPC {code}; response details redacted")
    if response is None:
        return None
    account = response.get("object")
    if not isinstance(account, dict):
        raise RuntimeError("ServiceAccounts/Get returned no object")
    return account


def _account_client_id(account: dict[str, Any]) -> str:
    metadata = account.get("metadata", {})
    tenant = metadata.get("tenant")
    account_id = account.get("id")
    if not isinstance(tenant, str) or not tenant or not isinstance(account_id, str) or not account_id:
        raise RuntimeError("Service account is missing its tenant or ID")
    return f"{_SERVICE_ACCOUNT_CLIENT_ID_PREFIX}{tenant}-{account_id}"


def _contains_credential_bytes(value: Any) -> bool:  # noqa: ANN401
    if isinstance(value, dict):
        for key, child in value.items():
            if (
                key in {"clientId", "client_id", "clientSecret", "client_secret"}
                and isinstance(child, (str, bytes))
                and child
            ):
                return True
            if _contains_credential_bytes(child):
                return True
    elif isinstance(value, list):
        return any(_contains_credential_bytes(child) for child in value)
    return False


def _contains_secret_value(value: Any, secrets: tuple[bytes, ...]) -> bool:  # noqa: ANN401
    if isinstance(value, dict):
        return any(_contains_secret_value(child, secrets) for child in value.values())
    if isinstance(value, list):
        return any(_contains_secret_value(child, secrets) for child in value)
    if isinstance(value, (str, bytes)):
        content = value.encode("utf-8") if isinstance(value, str) else value
        return any(secret and (secret in content or base64.b64encode(secret) in content) for secret in secrets)
    return False


def _decode_secret_data(secret: dict[str, Any]) -> dict[str, bytes]:
    data = secret.get("data")
    if not isinstance(data, dict):
        raise RuntimeError("Credential Secret returned no data")
    decoded: dict[str, bytes] = {}
    for key in ("client_id", "client_secret"):
        encoded = data.get(key, data.get(_camel_case(key)))
        if not isinstance(encoded, str) or not encoded:
            raise RuntimeError(f"Credential Secret is missing {key}")
        try:
            decoded[key] = base64.b64decode(encoded, validate=True)
        except (binascii.Error, ValueError):
            raise RuntimeError(f"Credential Secret has invalid {key} data") from None
        if not decoded[key]:
            raise RuntimeError(f"Credential Secret has empty {key} data")
    return decoded


def _credentials_match(left: dict[str, bytes], right: dict[str, bytes]) -> bool:
    keys = ("client_id", "client_secret")
    return all(
        isinstance(left.get(key), bytes)
        and isinstance(right.get(key), bytes)
        and hmac.compare_digest(left[key], right[key])
        for key in keys
    )


def _ssl_context() -> ssl.SSLContext:
    cafile = os.environ.get("SSL_CERT_FILE") or os.environ.get("REQUESTS_CA_BUNDLE")
    return ssl.create_default_context(cafile=cafile)


def _https_json(
    *, url: str, method: str = "GET", form: dict[str, str] | None = None, bearer_token: str | None = None
) -> tuple[int, Any]:
    parsed_url = urlparse(url)
    if parsed_url.scheme != "https" or not parsed_url.hostname:
        raise RuntimeError("Keycloak requests require a valid HTTPS URL")
    headers = {"Accept": "application/json"}
    body: bytes | None = None
    if form is not None:
        headers["Content-Type"] = "application/x-www-form-urlencoded"
        body = urlencode(form).encode("utf-8")
    if bearer_token is not None:
        headers["Authorization"] = f"Bearer {bearer_token}"
    request = Request(url, data=body, headers=headers, method=method)
    opener = build_opener(_NoRedirectHandler(), HTTPSHandler(context=_ssl_context()))
    try:
        with opener.open(request, timeout=_HTTP_TIMEOUT_SECONDS) as response:
            status = response.status
            content = response.read()
    except HTTPError as exc:
        exc.close()
        return exc.code, None
    except (URLError, TimeoutError, ssl.SSLError) as exc:
        raise RuntimeError(f"Keycloak HTTPS request failed ({type(exc).__name__})") from None
    try:
        return status, json.loads(content)
    except (UnicodeDecodeError, json.JSONDecodeError):
        raise RuntimeError(f"Keycloak returned invalid JSON (HTTP {status})") from None


def _keycloak_admin_token(*, keycloak_url: str, password: str) -> str:
    token_url = f"{keycloak_url.rstrip('/')}/realms/master/protocol/openid-connect/token"
    status, body = _https_json(
        url=token_url,
        method="POST",
        form={"grant_type": "password", "client_id": "admin-cli", "username": "admin", "password": password},
    )
    token = body.get("access_token") if isinstance(body, dict) else None
    if status != 200 or not isinstance(token, str) or not token:
        raise RuntimeError(f"Keycloak admin token request failed (HTTP {status})")
    return token


def _keycloak_client_exists(*, keycloak_url: str, admin_token: str, client_id: str) -> bool:
    query = urlencode({"clientId": client_id})
    url = f"{keycloak_url.rstrip('/')}/admin/realms/osac/clients?{query}"
    status, clients = _https_json(url=url, bearer_token=admin_token)
    if status != 200 or not isinstance(clients, list):
        raise RuntimeError(f"Keycloak client lookup failed (HTTP {status})")
    return any(isinstance(client, dict) and client.get("clientId") == client_id for client in clients)


def _service_account_credential(client: GRPCClient, account: dict[str, Any]) -> dict[str, bytes]:
    status = account.get("status", {})
    secret_reference = _field(status, "client_secret")
    if not isinstance(secret_reference, dict) or not secret_reference.get("id"):
        raise RuntimeError("Ready service account has no credential Secret reference")
    response = _call(client, service=f"{PUBLIC_API}.Secrets/Get", data={"id": secret_reference["id"]})
    return _decode_secret_data(response.get("object", {}))


def _service_account_token_request(
    *, keycloak_url: str, realm: str, client_id: str, client_secret: bytes
) -> tuple[int, str | None]:
    realm_path = quote(realm, safe="")
    discovery_url = f"{keycloak_url.rstrip('/')}/realms/{realm_path}/.well-known/openid-configuration"
    status, discovery = _https_json(url=discovery_url)
    token_endpoint = discovery.get("token_endpoint") if isinstance(discovery, dict) else None
    if status != 200 or not isinstance(token_endpoint, str):
        raise RuntimeError(f"Keycloak OIDC discovery failed (HTTP {status})")
    endpoint_url = urlparse(token_endpoint)
    configured_host = urlparse(keycloak_url).hostname
    if endpoint_url.scheme != "https" or endpoint_url.hostname != configured_host:
        raise RuntimeError("Keycloak OIDC token endpoint must use the configured HTTPS host")
    try:
        client_secret_text = client_secret.decode("utf-8")
    except UnicodeDecodeError:
        raise RuntimeError("Keycloak client secret is not valid UTF-8") from None

    status, response = _https_json(
        url=token_endpoint,
        method="POST",
        form={"grant_type": "client_credentials", "client_id": client_id, "client_secret": client_secret_text},
    )
    token = response.get("access_token") if isinstance(response, dict) else None
    return status, token if isinstance(token, str) and token else None


def _service_account_access_token(*, keycloak_url: str, realm: str, client_id: str, client_secret: bytes) -> str:
    status, token = _service_account_token_request(
        keycloak_url=keycloak_url, realm=realm, client_id=client_id, client_secret=client_secret
    )
    if status != 200 or token is None:
        raise RuntimeError(f"Keycloak client-credentials request failed (HTTP {status})")
    return token


def _jwt_claims(token: str) -> dict[str, Any]:
    parts = token.split(".")
    if len(parts) != 3:
        raise RuntimeError("Keycloak returned a malformed access token")
    payload = parts[1]
    try:
        decoded = base64.urlsafe_b64decode(payload + "=" * (-len(payload) % 4))
        claims = json.loads(decoded)
    except (binascii.Error, UnicodeDecodeError, json.JSONDecodeError):
        raise RuntimeError("Keycloak returned an unreadable access-token payload") from None
    if not isinstance(claims, dict):
        raise RuntimeError("Keycloak returned an unreadable access-token payload")
    return claims


def _tenant_id(account: dict[str, Any]) -> str:
    tenant = account.get("metadata", {}).get("tenant")
    if not isinstance(tenant, str) or not tenant:
        raise RuntimeError("Service account is missing its tenant")
    return tenant


def _create_project(client: GRPCClient, name: str) -> str:
    response = _call(client, service=f"{PUBLIC_API}.Projects/Create", data={"object": {"metadata": {"name": name}}})
    project_id = response.get("object", {}).get("id")
    if not isinstance(project_id, str) or not project_id:
        raise RuntimeError("Projects/Create returned no project ID")
    return project_id


def _get_project(client: GRPCClient, project_id: str) -> dict[str, Any] | None:
    response, code = _call_unchecked(client, service=f"{PUBLIC_API}.Projects/Get", data={"id": project_id})
    if code == "NotFound":
        return None
    if code:
        raise RuntimeError(f"Projects/Get failed with gRPC {code}; response details redacted")
    if response is None:
        return None
    project = response.get("object")
    if not isinstance(project, dict):
        raise RuntimeError("Projects/Get returned no object")
    return project


def _delete_project_if_present(client: GRPCClient, project_id: str) -> None:
    _, code = _call_unchecked(client, service=f"{PUBLIC_API}.Projects/Delete", data={"id": project_id})
    if code not in (None, "NotFound"):
        raise RuntimeError(f"Projects/Delete failed with gRPC {code}; response details redacted")


def _project_is_absent(client: GRPCClient, project_id: str) -> bool:
    if _get_project(client, project_id) is not None:
        return False
    response = _call(client, service=f"{PUBLIC_API}.Projects/List")
    return not any(project.get("id") == project_id for project in response.get("items", []))


def _wait_for_project_absent(client: GRPCClient, project_id: str) -> None:
    poll_until(
        fn=lambda: _project_is_absent(client, project_id),
        until=bool,
        retries=_LONG_POLL_RETRIES,
        delay=_POLL_DELAY_SECONDS,
        description="project cleanup",
    )


def _create_role_binding(client: GRPCClient, *, name: str, role_name: str, account_id: str) -> str:
    response = _call(
        client,
        service=f"{PUBLIC_API}.RoleBindings/Create",
        data={
            "object": {
                "metadata": {"name": name},
                "spec": {"role": {"name": role_name}, "serviceAccounts": [{"id": account_id}]},
            }
        },
    )
    role_binding_id = response.get("object", {}).get("id")
    if not isinstance(role_binding_id, str) or not role_binding_id:
        raise RuntimeError("RoleBindings/Create returned no role-binding ID")
    return role_binding_id


def _update_role_binding_role(client: GRPCClient, *, role_binding_id: str, role_name: str) -> dict[str, Any]:
    return _call(
        client,
        service=f"{PUBLIC_API}.RoleBindings/Update",
        data={
            "object": {"id": role_binding_id, "spec": {"role": {"name": role_name}}},
            "updateMask": {"paths": ["spec.role"]},
        },
    )


def _delete_role_binding_if_present(client: GRPCClient, role_binding_id: str) -> None:
    _, code = _call_unchecked(client, service=f"{PUBLIC_API}.RoleBindings/Delete", data={"id": role_binding_id})
    if code not in (None, "NotFound"):
        raise RuntimeError(f"RoleBindings/Delete failed with gRPC {code}; response details redacted")


def _role_binding_is_absent(client: GRPCClient, role_binding_id: str) -> bool:
    _, code = _call_unchecked(client, service=f"{PUBLIC_API}.RoleBindings/Get", data={"id": role_binding_id})
    if code == "NotFound":
        return True
    if code:
        raise RuntimeError(f"RoleBindings/Get failed with gRPC {code}; response details redacted")
    return False


def _wait_for_role_binding_absent(client: GRPCClient, role_binding_id: str) -> None:
    poll_until(
        fn=lambda: _role_binding_is_absent(client, role_binding_id),
        until=bool,
        retries=_LONG_POLL_RETRIES,
        delay=_POLL_DELAY_SECONDS,
        description="RoleBinding cleanup",
    )


def _create_test_secret(client: GRPCClient, *, name: str) -> str:
    payload = base64.b64encode(b"e2e-cross-tenant-secret").decode("ascii")
    response = _call(
        client,
        service=f"{PUBLIC_API}.Secrets/Create",
        data={"object": {"metadata": {"name": name}, "type": "SECRET_TYPE_OPAQUE", "data": {"test-data": payload}}},
    )
    secret_id = response.get("object", {}).get("id")
    if not isinstance(secret_id, str) or not secret_id:
        raise RuntimeError("Secrets/Create returned no secret ID")
    return secret_id


def _delete_secret_if_present(client: GRPCClient, secret_id: str) -> None:
    _, code = _call_unchecked(client, service=f"{PUBLIC_API}.Secrets/Delete", data={"id": secret_id})
    if code not in (None, "NotFound"):
        raise RuntimeError(f"Secrets/Delete failed with gRPC {code}; response details redacted")


def _secret_is_absent(client: GRPCClient, secret_id: str) -> bool:
    _, code = _call_unchecked(client, service=f"{PUBLIC_API}.Secrets/Get", data={"id": secret_id})
    if code == "NotFound":
        listed = _call(client, service=f"{PUBLIC_API}.Secrets/List")
        return not any(secret.get("id") == secret_id for secret in listed.get("items", []))
    if code:
        raise RuntimeError(f"Secrets/Get failed with gRPC {code}; response details redacted")
    return False


def _wait_for_secret_absent(client: GRPCClient, secret_id: str) -> None:
    poll_until(
        fn=lambda: _secret_is_absent(client, secret_id),
        until=bool,
        retries=_LONG_POLL_RETRIES,
        delay=_POLL_DELAY_SECONDS,
        description="Secret cleanup",
    )


def _run_cleanup(actions: list[Callable[[], None]]) -> None:
    errors: list[Exception] = []
    for action in actions:
        try:
            action()
        except Exception as exc:
            errors.append(exc)
    if errors:
        raise ExceptionGroup("E2E resource cleanup failed", errors)


def _projects_list_with_ids(client: GRPCClient, expected_ids: set[str]) -> dict[str, Any] | None:
    response, code = _call_unchecked(client, service=f"{PUBLIC_API}.Projects/List")
    if code == "PermissionDenied":
        return None
    if code:
        raise RuntimeError(f"Projects/List failed with gRPC {code}; response details redacted")
    if response is None:
        return None
    project_ids = {project.get("id") for project in response.get("items", [])}
    return response if expected_ids.issubset(project_ids) else None


def _service_account_is_absent(client: GRPCClient, account_id: str) -> bool:
    if _get_service_account(client, account_id) is not None:
        return False
    listed = _call(client, service=f"{PUBLIC_API}.ServiceAccounts/List")
    return not any(item.get("id") == account_id for item in listed.get("items", []))


def _wait_for_account_absent(client: GRPCClient, account_id: str) -> None:
    poll_until(
        fn=lambda: _service_account_is_absent(client, account_id),
        until=bool,
        retries=_LONG_POLL_RETRIES,
        delay=_POLL_DELAY_SECONDS,
        description="service account archival",
    )


def _account_deletion_started(client: GRPCClient, account_id: str) -> bool:
    account = _get_service_account(client, account_id)
    return account is not None and _field(account.get("metadata", {}), "deletion_timestamp") is not None


def _wait_for_account_deletion_started(client: GRPCClient, account_id: str) -> None:
    poll_until(
        fn=lambda: _account_deletion_started(client, account_id),
        until=bool,
        retries=_LONG_POLL_RETRIES,
        delay=_POLL_DELAY_SECONDS,
        description="service-account deletion timestamp",
    )


def _wait_for_keycloak_client_absent(*, keycloak_url: str, keycloak_admin_password: str, client_id: str) -> None:
    admin_token = _keycloak_admin_token(keycloak_url=keycloak_url, password=keycloak_admin_password)
    poll_until(
        fn=lambda: _keycloak_client_exists(keycloak_url=keycloak_url, admin_token=admin_token, client_id=client_id),
        until=lambda exists: not exists,
        retries=_LONG_POLL_RETRIES,
        delay=_POLL_DELAY_SECONDS,
        description="Keycloak service-account client cleanup",
    )


def _delete_service_account_if_present(client: GRPCClient, account_id: str) -> None:
    _, code = _call_unchecked(client, service=f"{PUBLIC_API}.ServiceAccounts/Delete", data={"id": account_id})
    if code not in (None, "NotFound"):
        raise RuntimeError(f"ServiceAccounts/Delete failed with gRPC {code}; response details redacted")


@pytest.fixture
def service_account_lifecycle_resources(
    jwt_grpc_tenant1_admin: GRPCClient, keycloak_url: str, keycloak_admin_password: str
) -> Generator[dict[str, Any], None, None]:
    name = unique_name("e2e-service-account")
    create_response = _call(
        jwt_grpc_tenant1_admin,
        service=f"{PUBLIC_API}.ServiceAccounts/Create",
        data={"object": {"metadata": {"name": name}}},
    )
    created = create_response.get("object")
    if not isinstance(created, dict):
        raise RuntimeError("ServiceAccounts/Create returned no object")
    account_id = created.get("id")
    if not isinstance(account_id, str) or not account_id:
        raise RuntimeError("ServiceAccounts/Create returned no account ID")

    try:
        ready = poll_until(
            fn=lambda: _get_service_account(jwt_grpc_tenant1_admin, account_id) or {},
            until=lambda account: _field(account.get("status", {}), "state") in _READY_OR_FAILED,
            retries=_LONG_POLL_RETRIES,
            delay=_POLL_DELAY_SECONDS,
            description="service-account provisioning",
        )
        if _field(ready.get("status", {}), "state") != _READY:
            raise RuntimeError("Service-account provisioning failed")
        yield {
            "account_id": account_id,
            "client": jwt_grpc_tenant1_admin,
            "create_response": create_response,
            "keycloak_admin_password": keycloak_admin_password,
            "keycloak_url": keycloak_url,
            "name": name,
            "ready": ready,
        }
    finally:
        _run_cleanup(
            [
                lambda: _delete_service_account_if_present(jwt_grpc_tenant1_admin, account_id),
                lambda: _wait_for_account_absent(jwt_grpc_tenant1_admin, account_id),
                lambda: _wait_for_keycloak_client_absent(
                    keycloak_url=keycloak_url,
                    keycloak_admin_password=keycloak_admin_password,
                    client_id=_account_client_id(created),
                ),
            ]
        )


@pytest.fixture
def service_account_role_resources(
    service_account_lifecycle_resources: dict[str, Any],
) -> Generator[dict[str, str], None, None]:
    client: GRPCClient = service_account_lifecycle_resources["client"]
    account_id: str = service_account_lifecycle_resources["account_id"]
    role_binding_id = _create_role_binding(
        client, name=unique_name("e2e-sa-lifecycle-binding"), role_name="tenant-automation", account_id=account_id
    )
    try:
        yield {"role_binding_id": role_binding_id}
    finally:
        _run_cleanup(
            [
                lambda: _delete_role_binding_if_present(client, role_binding_id),
                lambda: _wait_for_role_binding_absent(client, role_binding_id),
            ]
        )


@pytest.fixture
def jwt_grpc_tenant2_admin(
    fulfillment_address: str, keycloak_url: str, keycloak_realm: str, keycloak_client_id: str, jwt_password: str
) -> GRPCClient:
    return GRPCClient(
        address=fulfillment_address,
        token_factory=lambda: get_jwt(
            keycloak_url=keycloak_url,
            realm=keycloak_realm,
            client_id=keycloak_client_id,
            username="tenant2_admin",
            password=jwt_password,
        ),
    )


@pytest.fixture
def tenant2_service_account_resources(
    jwt_grpc_tenant2_admin: GRPCClient, keycloak_url: str, keycloak_admin_password: str
) -> Generator[dict[str, Any], None, None]:
    name = unique_name("e2e-tenant2-service-account")
    create_response = _call(
        jwt_grpc_tenant2_admin,
        service=f"{PUBLIC_API}.ServiceAccounts/Create",
        data={"object": {"metadata": {"name": name}}},
    )
    created = create_response.get("object")
    if not isinstance(created, dict):
        raise RuntimeError("ServiceAccounts/Create returned no object")
    account_id = created.get("id")
    if not isinstance(account_id, str) or not account_id:
        raise RuntimeError("ServiceAccounts/Create returned no account ID")

    try:
        ready = poll_until(
            fn=lambda: _get_service_account(jwt_grpc_tenant2_admin, account_id) or {},
            until=lambda account: _field(account.get("status", {}), "state") in _READY_OR_FAILED,
            retries=_LONG_POLL_RETRIES,
            delay=_POLL_DELAY_SECONDS,
            description="Tenant 2 service-account provisioning",
        )
        if _field(ready.get("status", {}), "state") != _READY:
            raise RuntimeError("Tenant 2 service-account provisioning failed")
        yield {"account_id": account_id, "client": jwt_grpc_tenant2_admin, "created": created, "ready": ready}
    finally:
        _run_cleanup(
            [
                lambda: _delete_service_account_if_present(jwt_grpc_tenant2_admin, account_id),
                lambda: _wait_for_account_absent(jwt_grpc_tenant2_admin, account_id),
                lambda: _wait_for_keycloak_client_absent(
                    keycloak_url=keycloak_url,
                    keycloak_admin_password=keycloak_admin_password,
                    client_id=_account_client_id(created),
                ),
            ]
        )


@pytest.fixture
def automation_access_resources(
    service_account_lifecycle_resources: dict[str, Any], jwt_grpc_tenant2_admin: GRPCClient
) -> Generator[dict[str, Any], None, None]:
    tenant1_admin: GRPCClient = service_account_lifecycle_resources["client"]
    created_projects: list[tuple[GRPCClient, str]] = []
    resources: dict[str, Any] = {"role_binding_id": None}
    try:
        tenant1_projects: list[dict[str, str]] = []
        for suffix in ("a1", "a2"):
            name = unique_name(f"e2e-sa-{suffix}")
            project_id = _create_project(tenant1_admin, name)
            tenant1_projects.append({"id": project_id, "name": name})
            created_projects.append((tenant1_admin, project_id))

        tenant2_project_name = unique_name("e2e-sa-b1")
        tenant2_project_id = _create_project(jwt_grpc_tenant2_admin, tenant2_project_name)
        created_projects.append((jwt_grpc_tenant2_admin, tenant2_project_id))

        resources.update(
            {
                "tenant1_projects": tenant1_projects,
                "tenant2_project": {"id": tenant2_project_id, "name": tenant2_project_name},
                "tenant2_admin": jwt_grpc_tenant2_admin,
            }
        )
        yield resources
    finally:
        cleanup_actions: list[Callable[[], None]] = []
        role_binding_id = resources.get("role_binding_id")
        if isinstance(role_binding_id, str):
            cleanup_actions.append(
                lambda role_binding_id=role_binding_id: _delete_role_binding_if_present(tenant1_admin, role_binding_id)
            )
            cleanup_actions.append(lambda: _wait_for_role_binding_absent(tenant1_admin, role_binding_id))
        for project_client, project_id in reversed(created_projects):
            cleanup_actions.append(
                lambda project_client=project_client, project_id=project_id: _delete_project_if_present(
                    project_client, project_id
                )
            )
            cleanup_actions.append(
                lambda project_client=project_client, project_id=project_id: _wait_for_project_absent(
                    project_client, project_id
                )
            )
        _run_cleanup(cleanup_actions)


def test_service_account_lifecycle_updates_and_deletes_account(
    service_account_lifecycle_resources: dict[str, Any], service_account_role_resources: dict[str, str]
) -> None:
    client: GRPCClient = service_account_lifecycle_resources["client"]
    account_id: str = service_account_lifecycle_resources["account_id"]
    name: str = service_account_lifecycle_resources["name"]
    create_response: dict[str, Any] = service_account_lifecycle_resources["create_response"]
    ready: dict[str, Any] = service_account_lifecycle_resources["ready"]

    assert not _contains_credential_bytes(create_response), "Create response exposed credential bytes"
    assert _field(ready.get("metadata", {}), "name") == name
    assert _field(ready.get("status", {}), "state") == _READY
    secret_reference = _field(ready.get("status", {}), "client_secret")
    assert isinstance(secret_reference, dict) and secret_reference.get("id"), "READY account has no credential Secret"
    role_binding_id = service_account_role_resources["role_binding_id"]
    role_binding_response = _call(client, service=f"{PUBLIC_API}.RoleBindings/Get", data={"id": role_binding_id})
    role_binding = role_binding_response.get("object", {})
    role_binding_spec = role_binding.get("spec", {})
    assert _field(_field(role_binding_spec, "role", {}), "name") == "tenant-automation"
    role_subjects = _field(role_binding_spec, "service_accounts", [])
    assert any(subject.get("id") == account_id for subject in role_subjects)

    fetched = _call(client, service=f"{PUBLIC_API}.ServiceAccounts/Get", data={"id": account_id})
    assert not _contains_credential_bytes(fetched), "Get response exposed credential bytes"
    fetched_account = fetched.get("object", {})
    assert fetched_account.get("id") == account_id

    listed = _call(client, service=f"{PUBLIC_API}.ServiceAccounts/List")
    assert not _contains_credential_bytes(listed), "List response exposed credential bytes"
    assert any(item.get("id") == account_id for item in listed.get("items", []))

    secret_response = _call(client, service=f"{PUBLIC_API}.Secrets/Get", data={"id": secret_reference["id"]})
    credential = _decode_secret_data(secret_response.get("object", {}))
    client_id = credential["client_id"].decode("utf-8")
    assert client_id == _account_client_id(ready), "Credential Secret client ID does not match the managed account"

    updated_response = _call(
        client,
        service=f"{PUBLIC_API}.ServiceAccounts/Update",
        data={
            "object": {"id": account_id, "metadata": {"labels": {"e2e-test": "service-account"}}},
            "updateMask": {"paths": ["metadata.labels"]},
        },
    )
    assert not _contains_credential_bytes(updated_response), "Update response exposed credential bytes"
    updated_account = updated_response.get("object", {})
    assert updated_account.get("metadata", {}).get("labels", {}).get("e2e-test") == "service-account"

    fetched_after_update = _call(client, service=f"{PUBLIC_API}.ServiceAccounts/Get", data={"id": account_id})
    assert not _contains_credential_bytes(fetched_after_update), "Updated Get response exposed credential bytes"
    fetched_account_after_update = fetched_after_update.get("object", {})
    assert fetched_account_after_update.get("metadata", {}).get("labels", {}).get("e2e-test") == "service-account"

    listed_after_update = _call(client, service=f"{PUBLIC_API}.ServiceAccounts/List")
    assert not _contains_credential_bytes(listed_after_update), "Updated List response exposed credential bytes"
    listed_account = next(item for item in listed_after_update.get("items", []) if item.get("id") == account_id)
    assert listed_account.get("metadata", {}).get("labels", {}).get("e2e-test") == "service-account"

    _delete_role_binding_if_present(client, role_binding_id)
    _wait_for_role_binding_absent(client, role_binding_id)
    _delete_service_account_if_present(client, account_id)
    _wait_for_account_absent(client, account_id)
    _wait_for_keycloak_client_absent(
        keycloak_url=service_account_lifecycle_resources["keycloak_url"],
        keycloak_admin_password=service_account_lifecycle_resources["keycloak_admin_password"],
        client_id=client_id,
    )


def test_tenant_automation_role_grants_and_revokes_tenant_access(
    service_account_lifecycle_resources: dict[str, Any],
    automation_access_resources: dict[str, Any],
    keycloak_url: str,
    keycloak_realm: str,
    fulfillment_private_address: str,
) -> None:
    admin_client: GRPCClient = service_account_lifecycle_resources["client"]
    ready_account: dict[str, Any] = service_account_lifecycle_resources["ready"]
    account_id: str = service_account_lifecycle_resources["account_id"]
    tenant = _tenant_id(ready_account)
    credential = _service_account_credential(admin_client, ready_account)
    client_id = credential["client_id"].decode("utf-8")
    token = _service_account_access_token(
        keycloak_url=keycloak_url, realm=keycloak_realm, client_id=client_id, client_secret=credential["client_secret"]
    )
    claims = _jwt_claims(token)
    assert claims.get("azp") == client_id, "Access token authorized the wrong Keycloak client"
    assert claims.get("osac_tenant") == tenant, "Access token has the wrong tenant claim"
    audience = claims.get("aud")
    audiences = audience if isinstance(audience, list) else [audience]
    assert "osac-api" in audiences, "Access token is missing the OSAC API audience"

    service_client = GRPCClient(address=admin_client.address, token=token)
    private_service_client = GRPCClient(address=fulfillment_private_address, token=token)
    _assert_grpc_rejected(
        service_client, service=f"{PUBLIC_API}.Projects/List", data=None, expected_code="PermissionDenied"
    )

    tenant1_projects: list[dict[str, str]] = automation_access_resources["tenant1_projects"]
    tenant2_project: dict[str, str] = automation_access_resources["tenant2_project"]
    role_binding_id = _create_role_binding(
        admin_client,
        name=unique_name("e2e-sa-automation-binding"),
        role_name="tenant-automation",
        account_id=account_id,
    )
    automation_access_resources["role_binding_id"] = role_binding_id

    expected_project_ids = {project["id"] for project in tenant1_projects}
    projects_response = poll_until(
        fn=lambda: _projects_list_with_ids(service_client, expected_project_ids),
        until=lambda response: response is not None,
        retries=_SHORT_POLL_RETRIES,
        delay=_POLL_DELAY_SECONDS,
        description="tenant-automation project access",
    )
    visible_project_ids = {project.get("id") for project in projects_response.get("items", [])}
    assert expected_project_ids.issubset(visible_project_ids), "Tenant Admin's projects were not visible"
    assert tenant2_project["id"] not in visible_project_ids, "Projects/List exposed a Tenant B project"

    for service_name in ("BareMetalInstances", "Clusters", "ComputeInstances"):
        for project in tenant1_projects:
            response = _call(
                service_client,
                service=f"{PUBLIC_API}.{service_name}/List",
                data={"filter": f'this.metadata.project == "{project["name"]}"'},
            )
            for item in response.get("items", []):
                metadata = item.get("metadata", {})
                assert metadata.get("tenant") == tenant
                assert metadata.get("project") == project["name"]

    _assert_grpc_rejected(
        service_client,
        service=f"{PUBLIC_API}.Projects/Get",
        data={"id": tenant2_project["id"]},
        expected_code="NotFound",
    )
    secret_reference = _field(ready_account.get("status", {}), "client_secret")
    assert isinstance(secret_reference, dict) and isinstance(secret_reference.get("id"), str)
    _assert_grpc_rejected(
        service_client,
        service=f"{PUBLIC_API}.Secrets/Get",
        data={"id": secret_reference["id"]},
        expected_code="PermissionDenied",
    )

    secret_list = _call(service_client, service=f"{PUBLIC_API}.Secrets/List")
    secret_list_exposes_data = any(bool(item.get("data")) for item in secret_list.get("items", []))
    assert not secret_list_exposes_data, "Secrets/List exposed data bytes; response details redacted"
    _assert_grpc_rejected(
        service_client, service=f"{PUBLIC_API}.ServiceAccounts/List", data=None, expected_code="PermissionDenied"
    )
    _assert_grpc_rejected(
        service_client, service=f"{PUBLIC_API}.IdentityProviders/List", data=None, expected_code="PermissionDenied"
    )
    _assert_grpc_rejected(
        private_service_client, service=f"{PRIVATE_API}.Volumes/List", data=None, expected_code="PermissionDenied"
    )

    _delete_role_binding_if_present(admin_client, role_binding_id)
    _wait_for_role_binding_absent(admin_client, role_binding_id)
    _assert_grpc_rejected(
        service_client, service=f"{PUBLIC_API}.Projects/List", data=None, expected_code="PermissionDenied"
    )


def test_service_account_idp_manager_and_tenant_admin_role_boundaries(
    service_account_lifecycle_resources: dict[str, Any],
    jwt_grpc_tenant2_admin: GRPCClient,
    keycloak_url: str,
    keycloak_realm: str,
) -> None:
    admin_client: GRPCClient = service_account_lifecycle_resources["client"]
    ready_account: dict[str, Any] = service_account_lifecycle_resources["ready"]
    account_id: str = service_account_lifecycle_resources["account_id"]
    credential = _service_account_credential(admin_client, ready_account)
    client_id = credential["client_id"].decode("utf-8")
    secret_reference = _field(ready_account.get("status", {}), "client_secret")
    if not isinstance(secret_reference, dict) or not isinstance(secret_reference.get("id"), str):
        raise RuntimeError("Ready service account has no credential Secret reference")

    role_binding_id: str | None = None
    tenant2_secret_id: str | None = None
    try:
        tenant2_secret_id = _create_test_secret(jwt_grpc_tenant2_admin, name=unique_name("e2e-sa-tenant2-secret"))
        role_binding_id = _create_role_binding(
            admin_client,
            name=unique_name("e2e-sa-role-tier-binding"),
            role_name="tenant-automation",
            account_id=account_id,
        )
        token = _service_account_access_token(
            keycloak_url=keycloak_url,
            realm=keycloak_realm,
            client_id=client_id,
            client_secret=credential["client_secret"],
        )
        service_client = GRPCClient(address=admin_client.address, token=token)
        _assert_grpc_rejected(
            service_client,
            service=f"{PUBLIC_API}.Events/Watch",
            data=None,
            expected_code="PermissionDenied",
            timeout=_STREAM_RPC_TIMEOUT_SECONDS,
        )
        _assert_grpc_rejected(
            service_client,
            service=f"{PUBLIC_API}.ConsoleSessions/Create",
            data={"object": {}},
            expected_code="PermissionDenied",
        )

        manager_binding = _update_role_binding_role(
            admin_client, role_binding_id=role_binding_id, role_name="tenant-idp-manager"
        )
        manager_subjects = _field(manager_binding.get("object", {}).get("spec", {}), "service_accounts", [])
        assert any(subject.get("id") == account_id for subject in manager_subjects), (
            "RoleBinding role update removed its service-account subject"
        )

        idp_list = poll_until(
            fn=lambda: _authorized_response(service_client, service=f"{PUBLIC_API}.IdentityProviders/List"),
            until=lambda response: response is not None,
            retries=_SHORT_POLL_RETRIES,
            delay=_POLL_DELAY_SECONDS,
            description="tenant-idp-manager IdentityProviders/List access",
        )
        assert isinstance(idp_list.get("items", []), list)
        _assert_grpc_rejected(
            service_client, service=f"{PUBLIC_API}.ServiceAccounts/List", data=None, expected_code="PermissionDenied"
        )
        _assert_grpc_rejected(
            service_client,
            service=f"{PUBLIC_API}.Secrets/Get",
            data={"id": secret_reference["id"]},
            expected_code="PermissionDenied",
        )

        admin_binding = _update_role_binding_role(
            admin_client, role_binding_id=role_binding_id, role_name="tenant-admin"
        )
        admin_subjects = _field(admin_binding.get("object", {}).get("spec", {}), "service_accounts", [])
        assert any(subject.get("id") == account_id for subject in admin_subjects), (
            "RoleBinding role update removed its service-account subject"
        )

        service_accounts = poll_until(
            fn=lambda: _authorized_response(service_client, service=f"{PUBLIC_API}.ServiceAccounts/List"),
            until=lambda response: response is not None,
            retries=_SHORT_POLL_RETRIES,
            delay=_POLL_DELAY_SECONDS,
            description="tenant-admin ServiceAccounts/List access",
        )
        assert not _contains_credential_bytes(service_accounts), "ServiceAccounts/List exposed credential bytes"
        assert any(item.get("id") == account_id for item in service_accounts.get("items", []))

        own_secret = _call(service_client, service=f"{PUBLIC_API}.Secrets/Get", data={"id": secret_reference["id"]})
        _decode_secret_data(own_secret.get("object", {}))
        _assert_grpc_rejected(
            service_client,
            service=f"{PUBLIC_API}.Secrets/Get",
            data={"id": tenant2_secret_id},
            expected_code="NotFound",
        )

        _delete_role_binding_if_present(admin_client, role_binding_id)
        _wait_for_role_binding_absent(admin_client, role_binding_id)
        role_binding_id = None
        _assert_grpc_rejected(
            service_client, service=f"{PUBLIC_API}.Projects/List", data=None, expected_code="PermissionDenied"
        )
    finally:
        cleanup_actions: list[Callable[[], None]] = []
        if role_binding_id is not None:
            cleanup_actions.append(
                lambda role_binding_id=role_binding_id: _delete_role_binding_if_present(admin_client, role_binding_id)
            )
            cleanup_actions.append(lambda: _wait_for_role_binding_absent(admin_client, role_binding_id))
        if tenant2_secret_id is not None:
            cleanup_actions.append(
                lambda tenant2_secret_id=tenant2_secret_id: _delete_secret_if_present(
                    jwt_grpc_tenant2_admin, tenant2_secret_id
                )
            )
            cleanup_actions.append(
                lambda tenant2_secret_id=tenant2_secret_id: _wait_for_secret_absent(
                    jwt_grpc_tenant2_admin, tenant2_secret_id
                )
            )
        _run_cleanup(cleanup_actions)


def test_tenant_admin_cannot_manage_another_tenants_service_account(
    jwt_grpc_tenant1_admin: GRPCClient, jwt_grpc_tenant1: GRPCClient, tenant2_service_account_resources: dict[str, Any]
) -> None:
    tenant2_admin: GRPCClient = tenant2_service_account_resources["client"]
    account_id: str = tenant2_service_account_resources["account_id"]

    before = _get_service_account(tenant2_admin, account_id)
    assert before is not None, "Tenant 2 admin could not read the created account"
    before_metadata = before.get("metadata", {})
    before_version = before_metadata.get("version")
    assert isinstance(before_version, int) and before_version > 0, "Created account has no metadata version"
    assert _field(before_metadata, "deletion_timestamp") is None, "Created account is already deleting"
    before_status = before.get("status", {})
    before_secret_reference = _field(before_status, "client_secret")
    assert isinstance(before_secret_reference, dict) and before_secret_reference.get("id"), (
        "READY account has no credential Secret reference"
    )
    before_credentials = _service_account_credential(tenant2_admin, before)
    assert before_credentials["client_id"].decode("utf-8") == _account_client_id(before), (
        "Credential Secret client ID does not match the managed account"
    )

    _assert_grpc_rejected(
        jwt_grpc_tenant1_admin,
        service=f"{PUBLIC_API}.ServiceAccounts/Get",
        data={"id": account_id},
        expected_code="NotFound",
    )
    _assert_grpc_rejected(
        jwt_grpc_tenant1_admin,
        service=f"{PUBLIC_API}.ServiceAccounts/Update",
        data={
            "object": {"id": account_id, "metadata": {"labels": {"e2e-cross-tenant": "must-not-apply"}}},
            "updateMask": {"paths": ["metadata.labels"]},
        },
        expected_code="NotFound",
    )
    _assert_grpc_rejected(
        jwt_grpc_tenant1_admin,
        service=f"{PUBLIC_API}.ServiceAccounts/Delete",
        data={"id": account_id},
        expected_code="NotFound",
    )
    _assert_grpc_rejected(
        jwt_grpc_tenant1_admin,
        service=f"{PUBLIC_API}.ServiceAccounts/RegenerateSecret",
        data={"id": account_id},
        expected_code="NotFound",
    )
    _assert_grpc_rejected(
        jwt_grpc_tenant1,
        service=f"{PUBLIC_API}.ServiceAccounts/Get",
        data={"id": account_id},
        expected_code="PermissionDenied",
    )

    after = _get_service_account(tenant2_admin, account_id)
    assert after is not None, "Cross-tenant management changed or deleted the Tenant 2 account"
    after_metadata = after.get("metadata", {})
    assert after_metadata.get("version") == before_version, "Cross-tenant management changed account metadata"
    assert _field(after_metadata, "deletion_timestamp") is None, "Cross-tenant management began account deletion"
    after_status = after.get("status", {})
    assert _field(after_status, "state") == _field(before_status, "state") == _READY
    after_secret_reference = _field(after_status, "client_secret")
    assert after_secret_reference == before_secret_reference, "Cross-tenant management changed the Secret reference"
    after_credentials = _service_account_credential(tenant2_admin, after)
    assert _credentials_match(before_credentials, after_credentials), (
        "Cross-tenant management changed the Tenant 2 account credentials"
    )


def test_deleting_service_account_revokes_its_unexpired_token(
    service_account_lifecycle_resources: dict[str, Any], keycloak_url: str, keycloak_realm: str
) -> None:
    admin_client: GRPCClient = service_account_lifecycle_resources["client"]
    account_id: str = service_account_lifecycle_resources["account_id"]
    ready_account: dict[str, Any] = service_account_lifecycle_resources["ready"]
    credential = _service_account_credential(admin_client, ready_account)
    client_id = credential["client_id"].decode("utf-8")
    role_binding_id: str | None = None
    try:
        role_binding_id = _create_role_binding(
            admin_client,
            name=unique_name("e2e-sa-delete-token-binding"),
            role_name="tenant-automation",
            account_id=account_id,
        )
        token = _service_account_access_token(
            keycloak_url=keycloak_url,
            realm=keycloak_realm,
            client_id=client_id,
            client_secret=credential["client_secret"],
        )
        claims = _jwt_claims(token)
        token_expiry = claims.get("exp")
        assert isinstance(token_expiry, int) and token_expiry > time.time(), "Issued service-account token is expired"
        service_client = GRPCClient(address=admin_client.address, token=token)
        projects = poll_until(
            fn=lambda: _projects_list_with_ids(service_client, set()),
            until=lambda response: response is not None,
            retries=_SHORT_POLL_RETRIES,
            delay=_POLL_DELAY_SECONDS,
            description="service-account access before deletion",
        )
        assert isinstance(projects.get("items", []), list)

        _delete_service_account_if_present(admin_client, account_id)
        _wait_for_account_deletion_started(admin_client, account_id)
        assert token_expiry > time.time(), "Service-account token expired before the revocation check"
        _assert_grpc_rejected(
            service_client, service=f"{PUBLIC_API}.Projects/List", data=None, expected_code="PermissionDenied"
        )
    finally:
        if role_binding_id is not None:
            _run_cleanup(
                [
                    lambda: _delete_role_binding_if_present(admin_client, role_binding_id),
                    lambda: _wait_for_role_binding_absent(admin_client, role_binding_id),
                ]
            )


def test_service_account_secret_rotation_preserves_tokens_and_rejects_old_secret(
    service_account_lifecycle_resources: dict[str, Any], keycloak_url: str, keycloak_realm: str
) -> None:
    admin_client: GRPCClient = service_account_lifecycle_resources["client"]
    account_id: str = service_account_lifecycle_resources["account_id"]
    ready_account: dict[str, Any] = service_account_lifecycle_resources["ready"]
    original_credentials = _service_account_credential(admin_client, ready_account)
    client_id = original_credentials["client_id"].decode("utf-8")
    secret_reference = _field(ready_account.get("status", {}), "client_secret")
    if not isinstance(secret_reference, dict) or not isinstance(secret_reference.get("id"), str):
        raise RuntimeError("Ready service account has no credential Secret reference")

    role_binding_id: str | None = None
    try:
        role_binding_id = _create_role_binding(
            admin_client,
            name=unique_name("e2e-sa-rotation-binding"),
            role_name="tenant-automation",
            account_id=account_id,
        )
        original_token = _service_account_access_token(
            keycloak_url=keycloak_url,
            realm=keycloak_realm,
            client_id=client_id,
            client_secret=original_credentials["client_secret"],
        )
        original_token_expiry = _jwt_claims(original_token).get("exp")
        assert isinstance(original_token_expiry, int) and original_token_expiry > time.time(), (
            "Issued service-account token is expired"
        )
        service_client = GRPCClient(address=admin_client.address, token=original_token)
        projects = poll_until(
            fn=lambda: _projects_list_with_ids(service_client, set()),
            until=lambda response: response is not None,
            retries=_SHORT_POLL_RETRIES,
            delay=_POLL_DELAY_SECONDS,
            description="service-account access before rotation",
        )
        assert isinstance(projects.get("items", []), list)

        rotation_response = _call(
            admin_client, service=f"{PUBLIC_API}.ServiceAccounts/RegenerateSecret", data={"id": account_id}
        )
        assert not _contains_credential_bytes(rotation_response), "RegenerateSecret response exposed credential bytes"
        queued_account = rotation_response.get("object", {})
        queued_status = queued_account.get("status", {})
        assert queued_account.get("id") == account_id
        assert _field(queued_status, "secret_rotation_state") == _ROTATION_PENDING
        queued_secret_reference = _field(queued_status, "client_secret")
        assert queued_secret_reference == secret_reference, "Rotation changed the credential Secret reference"

        rotated_account = poll_until(
            fn=lambda: _get_service_account(admin_client, account_id) or {},
            until=lambda account: (
                _field(account.get("status", {}), "secret_rotation_state") in {_ROTATION_COMPLETED, _ROTATION_ERROR}
            ),
            retries=_LONG_POLL_RETRIES,
            delay=_POLL_DELAY_SECONDS,
            description="service-account credential rotation",
        )
        rotated_status = rotated_account.get("status", {})
        if _field(rotated_status, "secret_rotation_state") != _ROTATION_COMPLETED:
            raise RuntimeError("Service-account credential rotation failed")
        assert _field(rotated_status, "client_secret") == secret_reference, (
            "Rotation changed the credential Secret reference"
        )

        replacement_credentials = _service_account_credential(admin_client, rotated_account)
        assert replacement_credentials["client_id"] == original_credentials["client_id"]
        assert not hmac.compare_digest(
            original_credentials["client_secret"], replacement_credentials["client_secret"]
        ), "Rotation did not replace the service-account secret"

        assert original_token_expiry > time.time(), "Service-account token expired before the post-rotation check"
        _call(service_client, service=f"{PUBLIC_API}.Projects/List")

        old_status, old_token = _service_account_token_request(
            keycloak_url=keycloak_url,
            realm=keycloak_realm,
            client_id=client_id,
            client_secret=original_credentials["client_secret"],
        )
        assert old_status in {400, 401} and old_token is None, "Keycloak did not reject the old client secret"
        replacement_status, replacement_token = _service_account_token_request(
            keycloak_url=keycloak_url,
            realm=keycloak_realm,
            client_id=client_id,
            client_secret=replacement_credentials["client_secret"],
        )
        assert replacement_status == 200 and replacement_token is not None, (
            "The replacement secret could not obtain a Keycloak token"
        )
        replacement_client = GRPCClient(address=admin_client.address, token=replacement_token)
        _call(replacement_client, service=f"{PUBLIC_API}.Projects/List")

        get_response = _call(admin_client, service=f"{PUBLIC_API}.ServiceAccounts/Get", data={"id": account_id})
        list_response = _call(admin_client, service=f"{PUBLIC_API}.ServiceAccounts/List")
        update_response = _call(
            admin_client,
            service=f"{PUBLIC_API}.ServiceAccounts/Update",
            data={
                "object": {"id": account_id, "metadata": {"labels": {"e2e-secret-rotation": "completed"}}},
                "updateMask": {"paths": ["metadata.labels"]},
            },
        )
        secret_values = (original_credentials["client_secret"], replacement_credentials["client_secret"])
        for response in (get_response, list_response, update_response):
            assert not _contains_credential_bytes(response), "Account API response exposed credential fields"
            assert not _contains_secret_value(response, secret_values), (
                "Account API response exposed a service-account secret"
            )
        assert update_response.get("object", {}).get("metadata", {}).get("labels", {}).get("e2e-secret-rotation") == (
            "completed"
        )
    finally:
        if role_binding_id is not None:
            _run_cleanup(
                [
                    lambda: _delete_role_binding_if_present(admin_client, role_binding_id),
                    lambda: _wait_for_role_binding_absent(admin_client, role_binding_id),
                ]
            )
