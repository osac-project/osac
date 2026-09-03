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

# Placeholder OIDC config for IdentityProviders/Create. IT tests reach READY with
# these URLs; a live mock IdP is not part of a clean hub install.
_PLACEHOLDER_OIDC = {
    "issuer": "https://oidc.example.com",
    "authorization_url": "https://oidc.example.com/authorize",
    "token_url": "https://oidc.example.com/token",
    "client_id": "e2e-onboarding",
    "client_secret": "e2e-onboarding-secret",
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
            logger.warning("%s %s teardown delete failed: %s", label, resource_id, combined.strip())


def _namespace_absent(name: str) -> bool:
    _, rc = run_unchecked("kubectl", "--as", "system:admin", "get", "ns", name)
    return rc != 0


def _organization_absent(*, keycloak_url: str, admin_token: str, org_name: str) -> bool:
    query = urlencode({"exact": "true", "search": org_name})
    status, body = keycloak_admin_request(
        keycloak_url=keycloak_url, admin_token=admin_token, method="GET", path=f"/organizations?{query}"
    )
    if status != 200:
        logger.warning("Keycloak org query for %s failed: status=%s body=%s", org_name, status, body.decode())
        return False
    try:
        orgs = json.loads(body)
    except ValueError:
        logger.warning("Keycloak org query for %s returned non-JSON: %s", org_name, body.decode())
        return False
    return isinstance(orgs, list) and len(orgs) == 0


@pytest.fixture
def onboarding_resources(
    private_grpc: GRPCClient,
    k8s_hub_client: K8sClient,
    keycloak_url: str,
    keycloak_admin_password: str,
    fulfillment_address: str,
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
    password = env("OSAC_JWT_PASSWORD", "foobar")
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
        "alice_password": password,
        "bob_password": password,
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
        if resources.get("membership_id"):
            _delete_private(
                private_grpc,
                service=f"{PRIVATE_API}.ProjectMemberships/Delete",
                resource_id=resources["membership_id"],
                label="ProjectMembership",
            )
        if resources.get("project_id"):
            _delete_private(
                private_grpc,
                service=f"{PRIVATE_API}.Projects/Delete",
                resource_id=resources["project_id"],
                label="Project",
            )
        if resources.get("role_binding_id"):
            _delete_private(
                private_grpc,
                service=f"{PRIVATE_API}.RoleBindings/Delete",
                resource_id=resources["role_binding_id"],
                label="RoleBinding",
            )
        if resources.get("idp_id"):
            _delete_private(
                private_grpc,
                service=f"{PRIVATE_API}.IdentityProviders/Delete",
                resource_id=resources["idp_id"],
                label="IdentityProvider",
            )
        try:
            admin_token = get_admin_token(keycloak_url=keycloak_url, username="admin", password=keycloak_admin_password)
            for username in (resources["alice_user"], resources["bob_user"]):
                try:
                    delete_user_by_username(keycloak_url=keycloak_url, admin_token=admin_token, username=username)
                except Exception:
                    logger.warning("Keycloak user %s teardown failed", username, exc_info=True)
        except Exception:
            logger.warning("Keycloak user teardown skipped", exc_info=True)
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
            except Exception:
                logger.warning(
                    "Keycloak organization %s still present after tenant deletion", tenant_name, exc_info=True
                )

        shutil.rmtree(alice_config_dir, ignore_errors=True)
        shutil.rmtree(bob_config_dir, ignore_errors=True)
