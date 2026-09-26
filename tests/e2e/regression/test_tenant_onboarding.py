from __future__ import annotations

import json
import logging
import os
import re
import subprocess
import textwrap
from pathlib import Path
from typing import Any

import pytest

from tests.e2e.core.grpc_client import PRIVATE_API, GRPCClient
from tests.e2e.core.keycloak_admin import (
    get_admin_token,
    keycloak_admin_request,
    provision_organization_password_user,
    wait_for_organization,
)
from tests.e2e.core.osac_cli import OsacCLI
from tests.e2e.core.runner import poll_until, run_unchecked

logger = logging.getLogger(__name__)

pytestmark = pytest.mark.regression

_CREATED_ID = re.compile(r"identifier '([^']+)'")
_TENANT_NAME = re.compile(r"^test-onboard-[0-9a-f]{8}$")
_BREAK_GLASS_PASSWORD = re.compile(r"Password:\s+(\S+)")
_SECRET_VALUE = re.compile(r"(?i)((?:password|token|secret|authorization|bearer)[:\s=]+)\S+")


def _redact_secrets(text: str) -> str:
    """Strip credential values from CLI/API text used in assertions and logs."""
    return _SECRET_VALUE.sub(r"\1[REDACTED]", text)


def _parse_created_id(stdout: str) -> str:
    """Return the resource id from an `osac create` success line."""
    match = _CREATED_ID.search(stdout)
    assert match is not None, "Failed to parse identifier from CLI create output"
    return match.group(1)


def _status(resp: dict[str, Any]) -> dict[str, Any]:
    """Return the `status` object from a private-API Get/List envelope."""
    obj = resp.get("object")
    if not isinstance(obj, dict):
        return {}
    status = obj.get("status")
    return status if isinstance(status, dict) else {}


def _status_value(resp: dict[str, Any], snake: str) -> str:
    """Read a status field, accepting snake_case or camelCase proto JSON keys."""
    status = _status(resp)
    camel = snake.split("_")
    camel_name = camel[0] + "".join(part.title() for part in camel[1:])
    value = status.get(snake, status.get(camel_name, ""))
    return str(value) if value is not None else ""


def _cli(resources: dict[str, str], identity: str, *args: str) -> str:
    """Run an osac command as Alice or Bob and require a zero exit code."""
    combined, rc = _cli_unchecked(resources, identity, *args)
    assert rc == 0, f"osac {' '.join(args)} failed rc={rc}: {_redact_secrets(combined)}"
    return combined


def _cli_unchecked(resources: dict[str, str], identity: str, *args: str) -> tuple[str, int]:
    """Run an osac command as Alice or Bob without requiring success."""
    return run_unchecked(resources["cli_binary"], "--config", resources[f"{identity}_config_dir"], *args)


def _collect_strings(value: object) -> list[str]:
    """Return string leaves from a JSON-like Get response for secret comparison."""
    if isinstance(value, str):
        return [value]
    if isinstance(value, dict):
        found: list[str] = []
        for key, item in value.items():
            if isinstance(key, str):
                found.append(key)
            found.extend(_collect_strings(item))
        return found
    if isinstance(value, list):
        found = []
        for item in value:
            found.extend(_collect_strings(item))
        return found
    return []


def _secret_matches_any(secret: str, candidates: list[str]) -> bool:
    """Return True if the secret appears in any collected string field."""
    return any(secret in candidate for candidate in candidates)


def _password_login(resources: dict[str, str], identity: str, user: str, password: str) -> None:
    """Log in on the public fulfillment address with the password grant.

    The password is written to a 0600 file and passed with ``--password-file`` so
    it never appears in subprocess argv if login fails.
    """
    password_path = Path(resources[f"{identity}_config_dir"]) / f".{identity}-login-password"
    try:
        fd = os.open(password_path, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600)
        with os.fdopen(fd, "w", encoding="utf-8") as handle:
            handle.write(password)
        combined, rc = run_unchecked(
            resources["cli_binary"],
            "--config",
            resources[f"{identity}_config_dir"],
            "login",
            "--address",
            resources["public_address"],
            "--insecure",
            "--flow",
            "password",
            "--user",
            user,
            "--password-file",
            str(password_path),
        )
        assert rc == 0, f"osac login failed rc={rc}: {_redact_secrets(combined)}"
    except subprocess.CalledProcessError as exc:
        raise RuntimeError(f"osac login failed rc={exc.returncode}") from None
    finally:
        password_path.unlink(missing_ok=True)


def _whoami_roles(text: str) -> list[str]:
    """Parse the Roles: line from `osac whoami` output."""
    for line in text.splitlines():
        if line.startswith("Roles:"):
            return [role.strip() for role in line.split(":", 1)[1].split(",") if role.strip()]
    return []


def _whoami_tenant(text: str) -> str:
    """Parse the Tenant: line from `osac whoami` output."""
    for line in text.splitlines():
        if line.startswith("Tenant:"):
            return line.split(":", 1)[1].strip()
    return ""


def _assert_whoami(text: str, *, user: str, tenant_name: str, tenant_admin: bool) -> None:
    """Require a parseable session before checking tenant-admin presence or absence."""
    token_unreadable = "unable to read token" in text.lower()
    assert not token_unreadable, "whoami could not read the session token"
    logged_in = f"Logged in as: {user}" in text
    assert logged_in, f"whoami did not report logged in as {user}"
    saw_tenant = _whoami_tenant(text) == tenant_name
    assert saw_tenant, f"whoami tenant mismatch, expected {tenant_name}"
    roles = _whoami_roles(text)
    if tenant_admin:
        assert "tenant-admin" in roles, "whoami missing tenant-admin role"
    else:
        assert "tenant-admin" not in roles, "whoami unexpectedly has tenant-admin role"


def _write_manifest(directory: str, filename: str, content: str) -> str:
    """Write a YAML/JSON manifest into an isolated CLI config directory."""
    path = Path(directory) / filename
    path.write_text(content)
    return str(path)


def _is_transient_grpc(exc: subprocess.CalledProcessError) -> bool:
    """Return True when a grpcurl failure looks like a transient transport error."""
    combined = (exc.stderr or "") + (exc.stdout or "")
    return "Unavailable" in combined or "connection refused" in combined.lower()


def _wait_private_status(
    grpc: GRPCClient, *, service: str, resource_id: str, field: str, expected: str, description: str
) -> dict[str, Any]:
    """Poll private Get until a status field matches, retrying Unavailable."""

    def _current() -> dict[str, Any]:
        """Fetch the resource, treating transport errors as not-yet-ready."""
        try:
            return grpc.call(service=service, data={"id": resource_id})
        except subprocess.CalledProcessError as exc:
            if _is_transient_grpc(exc):
                return {}
            raise

    return poll_until(
        fn=_current,
        until=lambda resp: _status_value(resp, field) == expected,
        retries=24,
        delay=5,
        description=description,
    )


def _user_listed(grpc: GRPCClient, *, username: str, tenant_name: str) -> bool:
    """Return True when Users/List includes this username on the story tenant."""
    filt = f"this.spec.username == {json.dumps(username)}"
    try:
        resp = grpc.call(service=f"{PRIVATE_API}.Users/List", data={"filter": filt})
    except subprocess.CalledProcessError as exc:
        if _is_transient_grpc(exc):
            return False
        raise
    items = resp.get("items") or []
    for item in items:
        if not isinstance(item, dict):
            continue
        spec = item.get("spec") if isinstance(item.get("spec"), dict) else {}
        metadata = item.get("metadata") if isinstance(item.get("metadata"), dict) else {}
        if spec.get("username") == username and metadata.get("tenant") == tenant_name:
            return True
    return False


def _namespace_json(name: str) -> dict[str, Any]:
    """Return the Kubernetes Namespace object as JSON."""
    raw, rc = run_unchecked("kubectl", "--as", "system:admin", "get", "ns", name, "-o", "json")
    assert rc == 0, f"namespace {name} not found: {raw}"
    data = json.loads(raw)
    assert isinstance(data, dict)
    return data


def _private_absent(grpc: GRPCClient, *, service: str, resource_id: str) -> bool:
    """Return True when private Get reports NotFound."""
    combined, rc = grpc.call_unchecked(service=service, data={"id": resource_id})
    return rc != 0 and "NotFound" in combined


@pytest.mark.iam
def test_tenant_onboarding_demo1_milestone_02(
    onboarding_resources: dict[str, str],
    private_cli: OsacCLI,
    private_grpc: GRPCClient,
    keycloak_url: str,
    keycloak_admin_password: str,
) -> None:
    """Demo 1 tenant onboarding: create, IdP users, RBAC, project, cascade delete."""
    resources = onboarding_resources
    tenant_name = resources["tenant_name"]
    project_name = resources["project_name"]
    alice = resources["alice_user"]
    bob = resources["bob_user"]

    assert _TENANT_NAME.fullmatch(tenant_name), tenant_name

    # Flow 1: CPA creates tenant on the private API; break-glass password is only on create stdout.
    tenant_yaml = _write_manifest(
        private_cli.config_dir,
        "tenant.yaml",
        textwrap.dedent(
            f"""\
            "@type": type.googleapis.com/osac.private.v1.Tenant
            metadata:
              name: {tenant_name}
            """
        ),
    )
    create_out = private_cli._run("create", "-f", tenant_yaml)
    tenant_id = _parse_created_id(create_out)
    resources["tenant_id"] = tenant_id
    password_match = _BREAK_GLASS_PASSWORD.search(create_out)
    assert password_match, "create stdout missing break-glass password"
    break_glass_password = password_match.group(1)
    has_break_glass_user = "break-glass" in create_out.lower()
    assert has_break_glass_user, "create stdout missing break-glass user"

    _wait_private_status(
        private_grpc,
        service=f"{PRIVATE_API}.Tenants/Get",
        resource_id=tenant_id,
        field="state",
        expected="TENANT_STATE_SYNCED",
        description=f"tenant {tenant_name} SYNCED",
    )

    # Flow 2: Get tenant has break_glass_user_id and must not echo the create password.
    get_tenant = private_grpc.call(service=f"{PRIVATE_API}.Tenants/Get", data={"id": tenant_id})
    bg_user_id = _status_value(get_tenant, "break_glass_user_id")
    assert bg_user_id, "break_glass_user_id missing after SYNCED"
    get_leaked = _secret_matches_any(break_glass_password, _collect_strings(get_tenant))
    assert not get_leaked, "Tenants/Get echoed break-glass password"
    get_cli_raw = private_cli._run("get", "tenant", tenant_id, "-o", "json")
    try:
        get_cli = json.loads(get_cli_raw)
    except json.JSONDecodeError:
        pytest.fail("CLI get tenant did not return JSON")
    yaml_leaked = _secret_matches_any(break_glass_password, _collect_strings(get_cli))
    assert not yaml_leaked, "CLI get tenant echoed break-glass password"

    # Flow 3: Keycloak org enabled; tenant namespace Active with tenant-ref label.
    admin_token = get_admin_token(keycloak_url=keycloak_url, username="admin", password=keycloak_admin_password)
    org_id = wait_for_organization(
        keycloak_url=keycloak_url, admin_token=admin_token, org_name=tenant_name, timeout_seconds=120
    )
    org_status, org_body = keycloak_admin_request(
        keycloak_url=keycloak_url, admin_token=admin_token, method="GET", path=f"/organizations/{org_id}"
    )
    assert org_status == 200, f"Keycloak organization GET status={org_status}"
    org = json.loads(org_body)
    org_enabled = org.get("enabled") is True
    assert org_enabled, "Keycloak organization is not enabled"

    def _ns_active() -> str:
        """Return the tenant namespace phase, or empty if it is not ready yet."""
        raw, rc = run_unchecked("kubectl", "--as", "system:admin", "get", "ns", tenant_name, "-o", "json")
        if rc != 0:
            return ""
        try:
            data = json.loads(raw)
        except ValueError:
            return ""
        status = data.get("status") if isinstance(data, dict) else {}
        if not isinstance(status, dict):
            return ""
        return str(status.get("phase", ""))

    poll_until(
        fn=_ns_active,
        until=lambda phase: phase == "Active",
        retries=24,
        delay=5,
        description=f"namespace {tenant_name}",
    )
    ns = _namespace_json(tenant_name)
    labels = ns.get("metadata", {}).get("labels", {}) if isinstance(ns.get("metadata"), dict) else {}
    assert isinstance(labels, dict)
    has_tenant_ref = labels.get("osac.openshift.io/tenant-ref") == tenant_name
    assert has_tenant_ref, "namespace missing osac.openshift.io/tenant-ref label"

    # Flow 4: OIDC IdP reaches READY without an issuer-patch script.
    idp_create = private_grpc.call(
        service=f"{PRIVATE_API}.IdentityProviders/Create",
        data={
            "object": {
                "metadata": {"name": resources["idp_name"], "tenant": tenant_name},
                "spec": {
                    "title": "Demo 1 OIDC",
                    "enabled": True,
                    "oidc": {
                        "authorization_url": resources["authorization_url"],
                        "token_url": resources["token_url"],
                        "client_id": resources["client_id"],
                        "issuer": resources["issuer"],
                    },
                },
            }
        },
    )
    resources["idp_id"] = str(idp_create["object"]["id"])
    _wait_private_status(
        private_grpc,
        service=f"{PRIVATE_API}.IdentityProviders/Get",
        resource_id=resources["idp_id"],
        field="phase",
        expected="IDENTITY_PROVIDER_PHASE_READY",
        description=f"identity provider {resources['idp_name']} READY",
    )

    # Clean hub: installer Keycloak only has tenant1/tenant2 users. Create Alice/Bob
    # in the new org with passwords so osac login --flow password works. This is the
    # same Keycloak-admin pattern as setup_organization_memberships, not a pre-baked
    # mock OIDC. OSAC User CRs still JIT on the first public API call (OSAC-3068).
    admin_token = get_admin_token(keycloak_url=keycloak_url, username="admin", password=keycloak_admin_password)
    for username, password in ((alice, resources["alice_password"]), (bob, resources["bob_password"])):
        provision_organization_password_user(
            keycloak_url=keycloak_url,
            admin_token=admin_token,
            org_id=org_id,
            org_name=tenant_name,
            username=username,
            password=password,
        )

    # Flow 5: Alice then Bob password-login; OSAC-3068 get projects before get users.
    _password_login(resources, "alice", alice, resources["alice_password"])
    _cli(resources, "alice", "get", "projects")
    _assert_whoami(_cli(resources, "alice", "whoami"), user=alice, tenant_name=tenant_name, tenant_admin=False)

    poll_until(
        fn=lambda: _user_listed(private_grpc, username=alice, tenant_name=tenant_name),
        until=lambda found: found is True,
        retries=24,
        delay=5,
        description=f"OSAC user {alice}",
    )

    _password_login(resources, "bob", bob, resources["bob_password"])
    _cli(resources, "bob", "get", "projects")
    _assert_whoami(_cli(resources, "bob", "whoami"), user=bob, tenant_name=tenant_name, tenant_admin=False)

    poll_until(
        fn=lambda: _user_listed(private_grpc, username=bob, tenant_name=tenant_name),
        until=lambda found: found is True,
        retries=24,
        delay=5,
        description=f"OSAC user {bob}",
    )

    # Flow 6: CPA binds tenant-admin to Alice.
    rb_create = private_grpc.call(
        service=f"{PRIVATE_API}.RoleBindings/Create",
        data={
            "object": {
                "metadata": {"name": resources["role_binding_name"], "tenant": tenant_name},
                "spec": {"role": {"name": "tenant-admin"}, "users": [{"name": alice}]},
            }
        },
    )
    resources["role_binding_id"] = str(rb_create["object"]["id"])
    _wait_private_status(
        private_grpc,
        service=f"{PRIVATE_API}.RoleBindings/Get",
        resource_id=resources["role_binding_id"],
        field="state",
        expected="ROLE_BINDING_STATE_READY",
        description=f"role binding {resources['role_binding_name']} READY",
    )

    # Flow 7: Alice re-logins so whoami picks up tenant-admin from a fresh JWT.
    _password_login(resources, "alice", alice, resources["alice_password"])
    _assert_whoami(_cli(resources, "alice", "whoami"), user=alice, tenant_name=tenant_name, tenant_admin=True)

    # Flow 8: Alice creates a named project → ACTIVE.
    project_yaml = _write_manifest(
        resources["alice_config_dir"],
        "project.yaml",
        textwrap.dedent(
            f"""\
            "@type": type.googleapis.com/osac.public.v1.Project
            metadata:
              name: {project_name}
            """
        ),
    )
    project_out = _cli(resources, "alice", "create", "-f", project_yaml)
    resources["project_id"] = _parse_created_id(project_out)
    _wait_private_status(
        private_grpc,
        service=f"{PRIVATE_API}.Projects/Get",
        resource_id=resources["project_id"],
        field="state",
        expected="PROJECT_STATE_ACTIVE",
        description=f"project {project_name} ACTIVE",
    )

    # Flow 9: Alice grants Bob VIEWER on that project. metadata.project is set on
    # the object so create does not depend on JWT project context. Alice's CLI
    # session already holds the token, so the password is not passed on argv.
    membership_yaml = _write_manifest(
        resources["alice_config_dir"],
        "membership.yaml",
        textwrap.dedent(
            f"""\
            "@type": type.googleapis.com/osac.public.v1.ProjectMembership
            metadata:
              name: {resources["membership_name"]}
              project: {project_name}
            spec:
              role: PROJECT_MEMBERSHIP_ROLE_VIEWER
              users:
                - name: {bob}
            """
        ),
    )
    membership_out = _cli(resources, "alice", "create", "-f", membership_yaml)
    resources["membership_id"] = _parse_created_id(membership_out)
    membership = _wait_private_status(
        private_grpc,
        service=f"{PRIVATE_API}.ProjectMemberships/Get",
        resource_id=resources["membership_id"],
        field="state",
        expected="PROJECT_MEMBERSHIP_STATE_READY",
        description=f"project membership {resources['membership_name']} READY",
    )
    spec = membership.get("object", {}).get("spec", {})
    members = spec.get("users", []) if isinstance(spec, dict) else []
    member_names = {user.get("name") for user in members if isinstance(user, dict)}
    bob_is_member = bob in member_names
    assert bob_is_member, f"project membership missing user {bob}"
    viewer = spec.get("role") == "PROJECT_MEMBERSHIP_ROLE_VIEWER"
    assert viewer, "project membership role is not VIEWER"

    # Flow 10: Bob whoami still has no tenant-admin.
    _password_login(resources, "bob", bob, resources["bob_password"])
    _assert_whoami(_cli(resources, "bob", "whoami"), user=bob, tenant_name=tenant_name, tenant_admin=False)

    # Flow 11: Bob can list the project but cannot delete it.
    bob_projects = _cli(resources, "bob", "get", "projects", "-o", "json")
    bob_items = json.loads(bob_projects)
    if isinstance(bob_items, dict):
        listed = bob_items.get("items") or [bob_items]
    elif isinstance(bob_items, list):
        listed = bob_items
    else:
        listed = []
    listed_project = any(
        isinstance(item, dict)
        and ((item.get("metadata") or {}).get("name") == project_name or item.get("id") == resources["project_id"])
        for item in listed
    )
    assert listed_project, f"Bob cannot list project {project_name}"
    denied_out, denied_rc = _cli_unchecked(resources, "bob", "delete", "project", resources["project_id"])
    assert denied_rc != 0, f"Bob should be denied project delete, rc={denied_rc}"
    denied = "denied" in denied_out.lower() or "permission" in denied_out.lower()
    assert denied, "Bob project delete did not report permission denied"
    still_active = private_grpc.call(service=f"{PRIVATE_API}.Projects/Get", data={"id": resources["project_id"]})
    project_still_active = _status_value(still_active, "state") == "PROJECT_STATE_ACTIVE"
    assert project_still_active, "project left ACTIVE after Bob's denied delete"

    # Flow 12: Alice deletes the named project while Bob's VIEWER membership
    # still exists. OSAC-3069 cascade must remove memberships and the project.
    project_id = resources["project_id"]
    membership_id = resources.get("membership_id", "")
    delete_out, delete_rc = _cli_unchecked(resources, "alice", "delete", "project", project_id)
    assert delete_rc == 0, (
        f"Alice delete project with membership present failed rc={delete_rc}: {_redact_secrets(delete_out)}"
    )

    try:
        poll_until(
            fn=lambda: _private_absent(private_grpc, service=f"{PRIVATE_API}.Projects/Get", resource_id=project_id),
            until=lambda gone: gone is True,
            retries=24,
            delay=5,
            description=f"project {project_name} gone after delete with membership",
        )
    except TimeoutError as exc:
        leftover = private_grpc.call_unchecked(service=f"{PRIVATE_API}.Projects/Get", data={"id": project_id})
        pytest.fail(
            "Project remained after delete with ProjectMembership present (OSAC-3069 cascade). "
            f"last={leftover!r} timeout={exc}"
        )
    resources["project_id"] = ""

    if membership_id:
        try:
            poll_until(
                fn=lambda: _private_absent(
                    private_grpc, service=f"{PRIVATE_API}.ProjectMemberships/Get", resource_id=membership_id
                ),
                until=lambda gone: gone is True,
                retries=12,
                delay=2,
                description=f"project membership {membership_id} gone after project cascade",
            )
        except TimeoutError as exc:
            leftover_m = private_grpc.call_unchecked(
                service=f"{PRIVATE_API}.ProjectMemberships/Get", data={"id": membership_id}
            )
            pytest.fail(
                f"ProjectMembership {membership_id} remained after project delete cascade. "
                f"last={leftover_m!r} timeout={exc}"
            )
        resources["membership_id"] = ""

    logger.info("Demo 1 onboarding complete for tenant %s (id %s)", tenant_name, tenant_id)
