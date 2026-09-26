from __future__ import annotations

import json
import logging
import re
import shutil
import subprocess
import tempfile
from collections.abc import Generator
from urllib.parse import urlencode
from uuid import uuid4

import pytest

from tests.e2e.core.grpc_client import PRIVATE_API, GRPCClient
from tests.e2e.core.helpers import wait_for_tenant_deletion
from tests.e2e.core.k8s_client import K8sClient
from tests.e2e.core.keycloak_admin import delete_user_by_username, get_admin_token, keycloak_admin_request
from tests.e2e.core.runner import env, poll_until, run_unchecked

logger = logging.getLogger(__name__)

_NOT_FOUND = re.compile(r"Code:\s*NotFound")


def pytest_configure(config: pytest.Config) -> None:
    """Register the iam marker without changing the shared e2e conftest."""
    config.addinivalue_line("markers", "iam: hub identity and access management tests (tenant onboarding, IdP, RBAC)")


_SECRET_VALUE = re.compile(r"(?i)((?:password|token|secret|authorization:\s*bearer|authorization|bearer)[:\s=]+)\S+")


def _redact_secrets(text: str) -> str:
    """Strip credential values from CLI/API text used in logs."""
    return _SECRET_VALUE.sub(r"\1[REDACTED]", text)


def _safe_exception(exc: BaseException) -> str:
    """Summarize an exception for logs without argv, tokens, or a traceback.

    ``subprocess.CalledProcessError`` from Keycloak curl calls embeds the full
    command, including ``Authorization: Bearer`` and ``password=`` arguments.
    """
    if isinstance(exc, subprocess.CalledProcessError):
        output = ((exc.stderr or "") + "\n" + (exc.stdout or "")).strip()
        summary = f"{type(exc).__name__} rc={exc.returncode}"
        if output:
            return f"{summary}: {_redact_secrets(output)}"
        return summary
    return type(exc).__name__


# Placeholder OIDC config for IdentityProviders/Create. IT tests reach READY with
# these URLs; a live mock IdP is not part of a clean hub install.
_PLACEHOLDER_OIDC = {
    "issuer": "https://oidc.example.com",
    "authorization_url": "https://oidc.example.com/authorize",
    "token_url": "https://oidc.example.com/token",
    "client_id": "e2e-onboarding",
}


def _delete_private(grpc: GRPCClient, *, service: str, resource_id: str, label: str) -> None:
    """Delete a private-API resource, warning on NotFound and other teardown errors."""
    try:
        grpc.call(service=service, data={"id": resource_id})
    except subprocess.CalledProcessError as exc:
        combined = (exc.stderr or "") + (exc.stdout or "")
        if _NOT_FOUND.search(combined):
            logger.warning("%s %s already deleted via API", label, resource_id)
        else:
            logger.warning("%s %s teardown delete failed: %s", label, resource_id, _redact_secrets(combined.strip()))


def _wait_private_absent(
    grpc: GRPCClient, *, service: str, resource_id: str, label: str, retries: int = 12, delay: int = 2
) -> bool:
    """Wait until a private-API Get returns NotFound so tenant delete is not raced.

    Returns False on timeout so the fixture can finish remaining cleanup, then fail.
    """

    def _gone() -> bool:
        """Return True when Get for this id reports NotFound."""
        combined, rc = grpc.call_unchecked(service=service, data={"id": resource_id})
        return rc != 0 and "NotFound" in combined

    try:
        poll_until(
            fn=_gone,
            until=lambda gone: gone is True,
            retries=retries,
            delay=delay,
            description=f"{label} {resource_id} gone",
        )
    except TimeoutError:
        logger.warning("%s %s still present before tenant delete", label, resource_id)
        return False
    return True


def _namespace_absent(name: str) -> bool:
    """Return True when kubectl reports the tenant namespace is NotFound."""
    combined, rc = run_unchecked("kubectl", "--as", "system:admin", "get", "ns", name)
    if rc == 0:
        return False
    return "NotFound" in combined


def _organization_absent(*, keycloak_url: str, admin_token: str, org_name: str) -> bool:
    """Return True when Keycloak lists no organization with this exact name."""
    query = urlencode({"exact": "true", "search": org_name})
    status, body = keycloak_admin_request(
        keycloak_url=keycloak_url, admin_token=admin_token, method="GET", path=f"/organizations?{query}"
    )
    if status != 200:
        logger.warning("Keycloak org query for %s failed: status=%s", org_name, status)
        return False
    try:
        orgs = json.loads(body)
    except ValueError:
        logger.warning("Keycloak org query for %s returned non-JSON", org_name)
        return False
    return isinstance(orgs, list) and len(orgs) == 0


@pytest.fixture
def onboarding_resources(
    private_grpc: GRPCClient,
    k8s_hub_client: K8sClient,
    keycloak_url: str,
    keycloak_admin_password: str,
    fulfillment_address: str,
    jwt_password: str,
) -> Generator[dict[str, str], None, None]:
    """Unique tenant/project names plus teardown of Demo 1 onboarding resources.

    The test body creates the tenant (so create stdout can capture break-glass
    credentials) and records IDs on the yielded dict. Cleanup always runs.
    Alice/Bob are unique Keycloak users created by the test, not installer
    fixtures and not a pre-deployed mock OIDC.
    """
    tag = uuid4().hex[:8]
    tenant_name = f"test-onboard-{tag}"
    project_name = f"onboard-{tag}"
    alice_config_dir = tempfile.mkdtemp(prefix="osac-config-alice-")
    bob_config_dir = tempfile.mkdtemp(prefix="osac-config-bob-")
    resources: dict[str, str] = {
        "tenant_name": tenant_name,
        "project_name": project_name,
        "idp_name": f"oidc-{tag}",
        "role_binding_name": f"tenant-admin-{tag}",
        "membership_name": f"viewer-{tag}",
        "cli_binary": env("OSAC_CLI_PATH", "osac"),
        "public_address": f"https://{fulfillment_address.rsplit(':', 1)[0]}",
        "alice_config_dir": alice_config_dir,
        "bob_config_dir": bob_config_dir,
        "alice_user": f"alice-{tag}",
        "bob_user": f"bob-{tag}",
        "alice_password": jwt_password,
        "bob_password": jwt_password,
        "tenant_id": "",
        "idp_id": "",
        "role_binding_id": "",
        "project_id": "",
        "membership_id": "",
        **_PLACEHOLDER_OIDC,
    }

    try:
        yield resources
    finally:
        logger.info("teardown tenant %s", tenant_name)
        leftover_private: list[str] = []
        for service, key, label in (
            (f"{PRIVATE_API}.ProjectMemberships/Delete", "membership_id", "ProjectMembership"),
            (f"{PRIVATE_API}.Projects/Delete", "project_id", "Project"),
            (f"{PRIVATE_API}.RoleBindings/Delete", "role_binding_id", "RoleBinding"),
            (f"{PRIVATE_API}.IdentityProviders/Delete", "idp_id", "IdentityProvider"),
        ):
            resource_id = resources.get(key, "")
            if not resource_id:
                continue
            _delete_private(private_grpc, service=service, resource_id=resource_id, label=label)
            get_service = service.rsplit("/", 1)[0] + "/Get"
            if not _wait_private_absent(
                private_grpc,
                service=get_service,
                resource_id=resource_id,
                label=label,
                retries=24 if key == "project_id" else 12,
                delay=5 if key == "project_id" else 2,
            ):
                leftover_private.append(f"{label} {resource_id}")
        try:
            admin_token = get_admin_token(keycloak_url=keycloak_url, username="admin", password=keycloak_admin_password)
            for username in (resources["alice_user"], resources["bob_user"]):
                try:
                    delete_user_by_username(keycloak_url=keycloak_url, admin_token=admin_token, username=username)
                except Exception as exc:
                    logger.warning("Keycloak user %s teardown failed: %s", username, _safe_exception(exc))
        except Exception as exc:
            logger.warning("Keycloak user teardown skipped: %s", _safe_exception(exc))
        if resources.get("tenant_id"):
            _delete_private(
                private_grpc,
                service=f"{PRIVATE_API}.Tenants/Delete",
                resource_id=resources["tenant_id"],
                label="Tenant",
            )
            try:
                wait_for_tenant_deletion(k8s=k8s_hub_client, name=tenant_name)
            except TimeoutError:
                logger.warning("Tenant CR %s still present after deletion", tenant_name)
            try:
                poll_until(
                    fn=lambda: _namespace_absent(tenant_name),
                    until=lambda gone: gone is True,
                    retries=36,
                    delay=5,
                    description=f"namespace {tenant_name} gone",
                )
            except TimeoutError:
                logger.warning("Namespace %s still present after tenant deletion", tenant_name)
            try:
                poll_until(
                    fn=lambda: _organization_absent(
                        keycloak_url=keycloak_url,
                        admin_token=get_admin_token(
                            keycloak_url=keycloak_url, username="admin", password=keycloak_admin_password
                        ),
                        org_name=tenant_name,
                    ),
                    until=lambda gone: gone is True,
                    retries=60,
                    delay=5,
                    description=f"Keycloak organization {tenant_name} gone",
                )
            except Exception as exc:
                logger.warning(
                    "Keycloak organization %s still present after tenant deletion: %s",
                    tenant_name,
                    _safe_exception(exc),
                )

        shutil.rmtree(alice_config_dir, ignore_errors=True)
        shutil.rmtree(bob_config_dir, ignore_errors=True)
        if leftover_private:
            pytest.fail("Private-API dependents still present after teardown: " + ", ".join(leftover_private))
