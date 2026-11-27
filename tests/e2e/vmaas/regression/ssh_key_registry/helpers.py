"""Shared ownership, cleanup and API helpers for SSH-key regression tests."""

from __future__ import annotations

import base64
import json
import os
import re
import shlex
import shutil
import subprocess
from pathlib import Path
from typing import Any

import pytest

from tests.e2e.core.grpc_client import PRIVATE_API, PUBLIC_API, GRPCClient
from tests.e2e.core.helpers import unique_name, wait_for_deletion
from tests.e2e.core.k8s_client import K8sClient
from tests.e2e.core.osac_cli import OsacCLI
from tests.e2e.core.runner import CommandFailed, poll_until, run_unchecked
from tests.e2e.core.wait_kinds import WaitResourceKind

_UUID_RE = re.compile(r"\b[0-9a-fA-F]{8}(?:-[0-9a-fA-F]{4}){3}-[0-9a-fA-F]{12}\b")


def _rpc_status(detail: str) -> str:
    """Read the gRPC status field, never a status-like word in a message."""
    match = re.search(r"(?:^|\n)\s*code:\s*(\w+)\b|rpc error:\s*code\s*=\s*(\w+)\b", detail.lower())
    return (match.group(1) or match.group(2)) if match else ""


def _definite_create_rejection(detail: str) -> bool:
    """A known server rejection or a local CLI parse error cannot create a resource."""
    status = _rpc_status(detail)
    if status in {"alreadyexists", "invalidargument", "notfound", "permissiondenied", "unauthenticated"}:
        return True
    return not status and "invalid --ssh-key value" in detail.lower() and "rpc error" not in detail.lower()


def _secrets(cli: OsacCLI, name: str | None = None) -> list[dict[str, Any]]:
    """Read Secrets without including their data in failure messages."""
    __tracebackhide__ = True
    args = ["get", "secrets"]
    if name is not None:
        args.append(name)
    args.extend(["-o", "json"])
    payload = json.loads(cli._run(*args))
    if isinstance(payload, dict):
        return [payload]
    if isinstance(payload, list) and all(isinstance(item, dict) for item in payload):
        return payload
    raise AssertionError("Unexpected Secret response shape")


def _wait_for_secret_absent(cli: OsacCLI, name: str) -> None:
    poll_until(
        fn=lambda: len(_secrets(cli, name)) == 0,
        until=bool,
        retries=30,
        delay=2,
        description=f"Secret {name} removal",
        resource_kind=WaitResourceKind.SECRET,
        resource_id=name,
    )


def _created_secret_id(output: str) -> str:
    match = _UUID_RE.search(output)
    if match is None:
        raise AssertionError("Secret create did not report an ID")
    return match.group()


def _create_owned_secret(
    cli: OsacCLI,
    owned: dict[str, str],
    uncertain: set[str],
    *,
    name: str,
    secret_type: str,
    field: str,
    path: Path,
    captured: list[str] | None = None,
) -> str:
    __tracebackhide__ = True
    already_uncertain = name in uncertain
    uncertain.add(name)
    try:
        args = ("create", "secret", "--name", name, "--type", secret_type, "--from-file", f"{field}={path}")
        if captured is None:
            output = cli._run(*args)
        else:
            command = (cli.binary, "--config", cli.config_dir, *args)
            output, rc = run_unchecked(*command, include_stderr=True)
            if rc:
                raise CommandFailed(rc, command, output=output)
            captured.append(output)
        secret_id = _created_secret_id(output)
    except subprocess.CalledProcessError as exc:
        detail = f"{exc.stdout or ''} {exc.stderr or ''}".lower()
        if _definite_create_rejection(detail) and not already_uncertain:
            uncertain.discard(name)
        raise
    owned[secret_id] = name
    if not already_uncertain:
        uncertain.discard(name)
    return secret_id


def _cleanup_secrets(cli: OsacCLI, owned: dict[str, str], uncertain: set[str]) -> None:
    errors: list[Exception] = []
    for secret_id, name in owned.items():
        try:
            found = _secrets(cli, secret_id)
            if any(item.get("id") == secret_id for item in found):
                cli.delete_secret(name=secret_id)
                poll_until(
                    fn=lambda name=name, secret_id=secret_id: all(
                        item.get("id") != secret_id for item in _secrets(cli, secret_id)
                    ),
                    until=bool,
                    retries=30,
                    delay=2,
                    description=f"Secret {name} removal",
                    resource_kind=WaitResourceKind.SECRET,
                    resource_id=secret_id,
                )
        except Exception as exc:
            errors.append(exc)
    if uncertain:
        errors.append(
            RuntimeError(f"Secret creation outcome is uncertain for {', '.join(sorted(uncertain))}; inspect ownership")
        )
    if errors:
        raise ExceptionGroup("Could not clean up all test Secrets", errors)


def _assert_key_secret(secret: dict[str, Any], *, name: str, secret_id: str, public_key: str) -> None:
    __tracebackhide__ = True
    metadata = secret.get("metadata") or {}
    if metadata.get("name") != name or metadata.get("project", ""):
        raise AssertionError("Secret name or default-project scope was incorrect")
    if secret.get("id") != secret_id or secret.get("type") not in ("SECRET_TYPE_SSH_PUBLIC_KEY", "ssh-public-key"):
        raise AssertionError("Secret ID or type was incorrect")
    data = secret.get("data") or {}
    if "PRIVATE KEY" in json.dumps(secret).upper():
        raise AssertionError("Secret response included private-key material")
    try:
        for value in data.values():
            if b"PRIVATE KEY" in base64.b64decode(value, validate=True).upper():
                raise AssertionError("Secret data included private-key material")
        decoded = base64.b64decode(data.get("public_key", ""), validate=True).decode()
    except (ValueError, UnicodeError) as exc:
        raise AssertionError("Secret public key was not valid encoded data") from exc
    if decoded != public_key:
        raise AssertionError("Secret did not retain the registered public key")


def _assert_tenant_user(cli: OsacCLI) -> None:
    identity = cli._run("whoami")
    roles = next(
        (line.partition(":")[2].strip().split(",") for line in identity.splitlines() if line.startswith("Roles:")), []
    )
    if "Logged in as: tenant1_user" not in identity or "Tenant: tenant1" not in identity:
        raise AssertionError("Test requires the tenant1 user in tenant1")
    if "tenant-admin" in {role.strip() for role in roles}:
        raise AssertionError("Test requires a non-admin tenant user")


def _require_tenant1_fixture_profile(cli: OsacCLI) -> None:
    identity = cli._run("whoami")
    tenant = next((line.partition(":")[2].strip() for line in identity.splitlines() if line.startswith("Tenant:")), "")
    if not tenant:
        raise AssertionError("Cannot determine the tenant for the shared VM fixtures")
    if tenant != "tenant1":
        pytest.skip("This case needs VM fixtures created in tenant1 for the tenant1 user")


def _hygiene_needles(private_key: bytes, payload: bytes, marker: bytes) -> dict[str, list[bytes]]:
    __tracebackhide__ = True
    lines = private_key.splitlines()
    bodies = [line for line in lines[1:-1] if line]
    if not lines or not bodies:
        raise AssertionError("Generated private key has an unexpected format")
    return {
        "private-key header": [lines[0], base64.b64encode(lines[0])],
        "private-key body": bodies,
        "encoded private key": [base64.b64encode(private_key)],
        "submitted marker": [marker],
        "encoded submitted payload": [base64.b64encode(payload)],
    }


def _hygiene_scan(data: bytes, needles: dict[str, list[bytes]]) -> set[str]:
    __tracebackhide__ = True
    return {label for label, values in needles.items() if any(value in data for value in values)}


def _instances_named(cli: OsacCLI, name: str, *, project: str = "") -> list[dict[str, Any]]:
    scope = ["--project", project] if project else []
    payload = json.loads(
        cli._run(
            *scope,
            "get",
            "computeinstances",
            "--filter",
            f"this.metadata.name == {json.dumps(name)} && this.metadata.project == {json.dumps(project)}",
            "-o",
            "json",
        )
    )
    items = payload if isinstance(payload, list) else [payload]
    return [
        item
        for item in items
        if item.get("metadata", {}).get("name") == name and (item.get("metadata", {}).get("project") or "") == project
    ]


def _instance_cr_name(k8s: K8sClient, instance_id: str) -> str:
    output, _ = k8s._get(
        "get",
        "computeinstances.osac.openshift.io",
        "-n",
        k8s.namespace,
        "-l",
        f"osac.openshift.io/computeinstance-uuid={instance_id}",
        "-o",
        "json",
    )
    objects = json.loads(output).get("items", [])
    matches = [
        item.get("metadata", {}).get("name", "")
        for item in objects
        if item.get("metadata", {}).get("labels", {}).get("osac.openshift.io/computeinstance-uuid") == instance_id
    ]
    if len(matches) > 1:
        raise AssertionError("Multiple ComputeInstance CRs have the same instance ID")
    return matches[0] if matches else ""


def _assert_existing_instance_id_rejected(
    grpc: GRPCClient,
    cli: OsacCLI,
    instance: dict[str, Any],
    owned: dict[str, str],
    uncertain: set[str],
    *,
    project: str = "",
) -> None:
    """Control that a supplied VM ID is honored, without creating another successful VM."""
    name = unique_name("e2e-ssh-id-control")
    if _instances_named(cli, name, project=project):
        raise AssertionError("Test VM name already exists")
    already_uncertain = name in uncertain
    uncertain.add(name)
    try:
        response = grpc.call(
            service=f"{PUBLIC_API}.ComputeInstances/Create",
            data={
                "object": {
                    "id": instance["id"],
                    "metadata": {"name": name, "project": project},
                    "spec": instance["spec"],
                }
            },
        )
    except subprocess.CalledProcessError as exc:
        detail = f"{exc.stdout or ''} {exc.stderr or ''}".lower()
        status = _rpc_status(detail)
        if _definite_create_rejection(detail) and not already_uncertain:
            uncertain.discard(name)
        if status != "alreadyexists":
            raise AssertionError("Explicit VM ID control failed for an unexpected reason") from exc
    else:
        instance_id = response.get("object", {}).get("id")
        if instance_id and instance_id != instance["id"]:
            owned[name] = instance_id
            if not already_uncertain:
                uncertain.discard(name)
        raise AssertionError("A duplicate explicit ComputeInstance ID was accepted")


def _assert_rejected_instance_cr_absent(k8s: K8sClient, instance_id: str) -> None:
    """Check an explicitly identified rejected request did not produce a CR over multiple observations."""
    observations = 0

    def absent() -> bool:
        nonlocal observations
        if _instance_cr_name(k8s, instance_id):
            raise AssertionError("A rejected ComputeInstance produced a hub CR")
        observations += 1
        return observations >= 3

    poll_until(
        fn=absent,
        until=bool,
        retries=3,
        delay=2,
        description="rejected ComputeInstance CR absence",
        resource_kind=WaitResourceKind.COMPUTE_INSTANCE,
        resource_id=instance_id,
    )


def _create_owned_instance(
    cli: OsacCLI,
    owned: dict[str, str],
    uncertain: set[str],
    *,
    template: str,
    name: str,
    network_attachments: list[dict[str, object]],
    ssh_key: str | None,
    instance_type: str | None = None,
    disk_image: str | None = None,
    boot_disk_storage_tier: str | None = None,
    project: str | None = None,
) -> str:
    already_uncertain = name in uncertain
    uncertain.add(name)
    try:
        instance_id = cli.create_compute_instance(
            template=template,
            name=name,
            network_attachments=network_attachments,
            ssh_key=ssh_key,
            instance_type=instance_type,
            disk_image=disk_image,
            boot_disk_storage_tier=boot_disk_storage_tier,
            project=project,
        )
    except subprocess.CalledProcessError as exc:
        detail = f"{exc.stdout or ''} {exc.stderr or ''}".lower()
        if _definite_create_rejection(detail) and not already_uncertain:
            uncertain.discard(name)
        raise
    owned[name] = instance_id
    if not already_uncertain:
        uncertain.discard(name)
    return instance_id


def _cleanup_instances(
    cli: OsacCLI, grpc: GRPCClient, k8s: K8sClient, owned: dict[str, str], uncertain: set[str], *, project: str = ""
) -> None:
    errors: list[Exception] = []
    for name, instance_id in owned.items():
        try:
            if any(item.get("id") == instance_id for item in _instances_named(cli, name, project=project)):
                cr_name = _instance_cr_name(k8s, instance_id)
                if project:
                    cli._run("--project", project, "delete", "computeinstance", instance_id)
                else:
                    cli.delete_compute_instance(uuid=instance_id)
                if cr_name:
                    wait_for_deletion(k8s=k8s, name=cr_name)
                poll_until(
                    fn=lambda name=name, instance_id=instance_id: all(
                        item.get("id") != instance_id for item in _instances_named(cli, name, project=project)
                    ),
                    until=bool,
                    retries=30,
                    delay=2,
                    description=f"ComputeInstance {name} API removal",
                    resource_kind=WaitResourceKind.COMPUTE_INSTANCE,
                    resource_id=instance_id,
                )
            if _instance_cr_name(k8s, instance_id):
                raise AssertionError(f"ComputeInstance CR for {name} remains after API removal")
        except Exception as exc:
            errors.append(exc)
    if uncertain:
        errors.append(
            RuntimeError(f"VM creation outcome is uncertain for {', '.join(sorted(uncertain))}; inspect ownership")
        )
    if errors:
        raise ExceptionGroup("Could not clean up all test ComputeInstances; preserving their Secrets", errors)


def _project_secrets(cli: OsacCLI, project: str, name: str) -> list[dict[str, Any]]:
    __tracebackhide__ = True
    payload = json.loads(cli._run("--project", project, "get", "secrets", name, "-o", "json"))
    return payload if isinstance(payload, list) else [payload]


def _create_project_secret(
    cli: OsacCLI, owned: dict[str, str], uncertain: set[str], *, project: str, name: str, public_path: Path
) -> None:
    marker = f"{project}/{name}"
    already_uncertain = marker in uncertain
    uncertain.add(marker)
    try:
        output = cli._run(
            "--project",
            project,
            "create",
            "secret",
            "--name",
            name,
            "--type",
            "ssh-public-key",
            "--from-file",
            f"public_key={public_path}",
        )
        secret_id = _created_secret_id(output)
    except subprocess.CalledProcessError as exc:
        detail = f"{exc.stdout or ''} {exc.stderr or ''}".lower()
        if _definite_create_rejection(detail) and not already_uncertain:
            uncertain.discard(marker)
        raise
    owned[secret_id] = project
    if not already_uncertain:
        uncertain.discard(marker)


def _project_ids_named(grpc: GRPCClient, name: str) -> list[str]:
    result = grpc.call(
        service=f"{PUBLIC_API}.Projects/List", data={"filter": f"this.metadata.name == {json.dumps(name)}"}
    )
    return [item["id"] for item in result.get("items", []) if item.get("metadata", {}).get("name") == name]


def _owned_project_ids_named(grpc: GRPCClient, name: str, owner: str) -> list[str]:
    result = grpc.call(
        service=f"{PUBLIC_API}.Projects/List", data={"filter": f"this.metadata.name == {json.dumps(name)}"}
    )
    return [
        item["id"]
        for item in result.get("items", [])
        if item.get("metadata", {}).get("labels", {}).get("e2e-owner") == owner
    ]


def _recover_owned_membership(private_grpc: GRPCClient, name: str, project: str, owner: str) -> str:
    """Recover an ambiguous create only when the exact run-owned membership is visible."""

    def matching() -> list[str]:
        result = private_grpc.call(
            service=f"{PRIVATE_API}.ProjectMemberships/List",
            data={"filter": f"this.metadata.name == {json.dumps(name)}"},
        )
        return [
            item["id"]
            for item in result.get("items", [])
            if item.get("metadata", {}).get("name") == name
            and item.get("metadata", {}).get("tenant") == "tenant1"
            and item.get("metadata", {}).get("project") == project
            and item.get("metadata", {}).get("labels", {}).get("e2e-owner") == owner
        ]

    found = poll_until(
        fn=matching,
        until=bool,
        retries=30,
        delay=2,
        description=f"recover membership {name}",
        resource_kind=WaitResourceKind.PROJECT_MEMBERSHIP,
        resource_id=name,
    )
    if len(found) != 1:
        raise RuntimeError("Test membership ownership is ambiguous")
    return found[0]


def _delete_owned_membership(private_grpc: GRPCClient, membership_id: str) -> None:
    try:
        private_grpc.call(service=f"{PRIVATE_API}.ProjectMemberships/Delete", data={"id": membership_id})
    except subprocess.CalledProcessError as exc:
        if _rpc_status(f"{exc.stdout or ''} {exc.stderr or ''}") != "notfound":
            raise

    def absent() -> bool:
        try:
            private_grpc.call(service=f"{PRIVATE_API}.ProjectMemberships/Get", data={"id": membership_id})
        except subprocess.CalledProcessError as exc:
            if _rpc_status(f"{exc.stdout or ''} {exc.stderr or ''}") == "notfound":
                return True
            raise
        return False

    poll_until(
        fn=absent,
        until=bool,
        retries=30,
        delay=2,
        description=f"membership {membership_id} removal",
        resource_kind=WaitResourceKind.PROJECT_MEMBERSHIP,
        resource_id=membership_id,
    )


def _shared_secrets(private_grpc: GRPCClient, name: str) -> list[dict[str, Any]]:
    __tracebackhide__ = True
    expr = f'this.metadata.tenant == "shared" && this.metadata.name == {json.dumps(name)}'
    result = private_grpc.call(service=f"{PRIVATE_API}.Secrets/List", data={"filter": expr})
    return result.get("items", [])


def _verified_kubernetes_environment(client: K8sClient) -> dict[str, str]:
    """Only use credential-bearing tunnels when the API server is authenticated."""
    environment = os.environ.copy()
    if client.kubeconfig:
        environment["KUBECONFIG"] = client.kubeconfig
    config = json.loads(
        subprocess.run(
            ["kubectl", "config", "view", "-o", "json"], env=environment, capture_output=True, text=True, check=True
        ).stdout
    )
    active = next(context for context in config["contexts"] if context["name"] == config["current-context"])
    cluster = next(item for item in config["clusters"] if item["name"] == active["context"]["cluster"])
    if cluster["cluster"].get("insecure-skip-tls-verify"):
        pytest.skip("This test requires a Kubernetes API connection with TLS certificate verification")
    return environment


def _assert_vm_key_injected(cr: dict[str, Any], k8s_virt_client: K8sClient, expected_key: str) -> None:
    """Verify CR projection, the VM Secret and noCloud credential wiring."""
    __tracebackhide__ = True
    if (cr.get("spec", {}).get("sshKey") or "").strip() != expected_key.strip():
        raise AssertionError("ComputeInstance did not receive the selected SSH key")
    vm_ref = cr["status"]["virtualMachineReference"]
    namespace = vm_ref["namespace"]
    cr_name = cr["metadata"]["name"]
    vm_name = vm_ref.get("name") or cr_name
    secret_name = f"{cr_name}-ssh-public-key"
    secret = k8s_virt_client.get_json(resource="secret", name=secret_name, namespace=namespace)
    encoded_key = secret.get("data", {}).get("ssh-public-key", "")
    if base64.b64decode(encoded_key, validate=True).decode().strip() != expected_key.strip():
        raise AssertionError("VM injection Secret did not contain the selected SSH key")
    vm = k8s_virt_client.get_json(resource="virtualmachine", name=vm_name, namespace=namespace)
    credentials = vm.get("spec", {}).get("template", {}).get("spec", {}).get("accessCredentials", [])
    if not any(
        entry.get("sshPublicKey", {}).get("source", {}).get("secret", {}).get("secretName") == secret_name
        and "noCloud" in entry.get("sshPublicKey", {}).get("propagationMethod", {})
        for entry in credentials
    ):
        raise AssertionError("VM did not reference the SSH injection Secret through noCloud")


def _guest_ssh_environment(k8s_virt_client: K8sClient) -> dict[str, str]:
    """Require a usable SSH client, virtctl and a TLS-verified VM-cluster kubeconfig."""
    if shutil.which("virtctl") is None or shutil.which("ssh") is None:
        pytest.skip("Guest SSH requires both ssh and virtctl")
    return _verified_kubernetes_environment(k8s_virt_client)


def _optional_guest_ssh_environment(request: pytest.FixtureRequest) -> tuple[dict[str, str] | None, str]:
    """Return guest SSH prerequisites or an explicit reason for a later skip."""
    if not os.environ.get("OSAC_VM_KUBECONFIG"):
        return None, "Guest SSH requires OSAC_VM_KUBECONFIG"
    try:
        return _guest_ssh_environment(request.getfixturevalue("k8s_virt_client")), ""
    except pytest.skip.Exception as exc:
        return None, str(exc)


def _assert_guest_key_access(
    cr: dict[str, Any], correct_key: Path, unrelated_key: Path, work_dir: Path, environment: dict[str, str]
) -> None:
    """Authenticate over the verified API tunnel and distinguish denial from transport failure."""
    reference = cr["status"]["virtualMachineReference"]
    namespace = reference["namespace"]
    guest_name = reference.get("name") or cr["metadata"]["name"]
    known_hosts = work_dir / f"known_hosts_{guest_name}"
    alias = f"osac-e2e-{guest_name}"
    target = shlex.quote(f"vm/{guest_name}/{namespace}")
    proxy = f"virtctl port-forward --stdio=true {target} 22"
    user = os.environ.get("OSAC_VM_SSH_USER", "fedora")
    base = [
        "ssh",
        "-F",
        "/dev/null",
        "-o",
        f"ProxyCommand={proxy}",
        "-o",
        f"HostKeyAlias={alias}",
        "-o",
        "IdentityAgent=none",
        "-o",
        "IdentitiesOnly=yes",
        "-o",
        "ControlMaster=no",
        "-o",
        "ControlPath=none",
        "-o",
        "BatchMode=yes",
        "-o",
        "PasswordAuthentication=no",
        "-o",
        "KbdInteractiveAuthentication=no",
        "-o",
        "GSSAPIAuthentication=no",
        "-o",
        "GlobalKnownHostsFile=/dev/null",
        "-o",
        f"UserKnownHostsFile={known_hosts}",
        "-o",
        "ConnectTimeout=15",
    ]

    def bootstrap_host_key() -> bool:
        try:
            subprocess.run(
                [
                    *base,
                    "-o",
                    "StrictHostKeyChecking=accept-new",
                    "-o",
                    "PubkeyAuthentication=no",
                    f"{user}@tunnel",
                    "true",
                ],
                capture_output=True,
                text=True,
                env=environment,
                timeout=35,
                check=False,
            )
        except subprocess.TimeoutExpired:
            return False
        return known_hosts.is_file() and known_hosts.stat().st_size > 0

    poll_until(
        fn=bootstrap_host_key,
        until=bool,
        retries=36,
        delay=10,
        description=f"trusted guest host key for {guest_name}",
        resource_kind=WaitResourceKind.VIRTUAL_MACHINE_INSTANCE,
        resource_id=guest_name,
    )

    def authenticate() -> bool:
        try:
            result = subprocess.run(
                [
                    *base,
                    "-o",
                    "StrictHostKeyChecking=yes",
                    "-o",
                    "PreferredAuthentications=publickey",
                    "-i",
                    str(correct_key),
                    f"{user}@tunnel",
                    "printf OSAC_SSH_OK",
                ],
                capture_output=True,
                text=True,
                env=environment,
                timeout=35,
                check=False,
            )
        except subprocess.TimeoutExpired:
            return False
        return result.returncode == 0 and result.stdout.strip() == "OSAC_SSH_OK"

    poll_until(
        fn=authenticate,
        until=bool,
        retries=36,
        delay=10,
        description=f"guest SSH authentication for {guest_name}",
        resource_kind=WaitResourceKind.VIRTUAL_MACHINE_INSTANCE,
        resource_id=guest_name,
    )
    try:
        wrong = subprocess.run(
            [
                *base,
                "-o",
                "StrictHostKeyChecking=yes",
                "-o",
                "PreferredAuthentications=publickey",
                "-i",
                str(unrelated_key),
                f"{user}@tunnel",
                "true",
            ],
            capture_output=True,
            text=True,
            env=environment,
            timeout=35,
            check=False,
        )
    except subprocess.TimeoutExpired:
        raise RuntimeError("Unrelated-key SSH check timed out; authentication result is inconclusive") from None
    if wrong.returncode == 0 or not re.search(r"permission denied \(publickey(?:[,)]|$)", wrong.stderr.lower()):
        raise AssertionError("Unrelated key did not receive an SSH authentication denial")
