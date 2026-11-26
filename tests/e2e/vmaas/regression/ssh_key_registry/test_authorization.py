"""Project and tenant SSH-key authorization regressions."""

from __future__ import annotations

import base64
import copy
import json
import re
import subprocess
from pathlib import Path
from uuid import uuid4

import pytest

from tests.e2e.core.grpc_client import PRIVATE_API, PUBLIC_API, GRPCClient
from tests.e2e.core.helpers import unique_name, wait_for_cr, wait_for_provision, wait_for_running
from tests.e2e.core.k8s_client import K8sClient
from tests.e2e.core.osac_cli import OsacCLI
from tests.e2e.core.runner import poll_until, run
from tests.e2e.core.wait_kinds import WaitResourceKind
from tests.e2e.vmaas.conftest import DEFAULT_DISK_IMAGE_SOURCE_REF
from tests.e2e.vmaas.regression.ssh_key_registry.helpers import (
    _assert_existing_instance_id_rejected,
    _assert_guest_key_access,
    _assert_rejected_instance_cr_absent,
    _assert_tenant_user,
    _cleanup_instances,
    _cleanup_secrets,
    _create_owned_instance,
    _create_owned_secret,
    _create_project_secret,
    _definite_create_rejection,
    _delete_owned_membership,
    _instances_named,
    _optional_guest_ssh_environment,
    _owned_project_ids_named,
    _project_ids_named,
    _project_secrets,
    _recover_owned_membership,
    _require_tenant1_fixture_profile,
    _rpc_status,
    _secrets,
)

pytestmark = pytest.mark.regression


def test_same_key_name_in_different_projects(
    cli: OsacCLI,
    jwt_cli_user: OsacCLI,
    jwt_grpc_tenant1: GRPCClient,
    jwt_grpc_tenant1_admin: GRPCClient,
    private_grpc: GRPCClient,
    k8s_hub_client: K8sClient,
    request: pytest.FixtureRequest,
    vm_template: str,
    default_network_attachment: dict[str, object],
    ssh_key_dir: Path,
) -> None:
    """TC-05 (partial): distinguish same-name keys when VMs select across projects."""
    environment, guest_skip_reason = _optional_guest_ssh_environment(request)
    _assert_tenant_user(jwt_cli_user)
    _require_tenant1_fixture_profile(cli)
    project_names = [unique_name("e2e-ssh-project-a"), unique_name("e2e-ssh-project-b")]
    key_name = unique_name("e2e-ssh-project-key")
    owner = unique_name("e2e-ssh-owner")
    project_ids: list[str] = []
    uncertain_project: str | None = None
    membership_ids: list[str] = []
    uncertain_membership: tuple[str, str] | None = None
    secret_owned: dict[str, str] = {}
    secret_uncertain: set[str] = set()
    public_keys: list[str] = []
    image_name = unique_name("e2e-ssh-project-image")
    vm_names = [unique_name("e2e-ssh-cross-project"), unique_name("e2e-ssh-default-project")]
    project_vms: dict[str, str] = {}
    default_vms: dict[str, str] = {}
    project_uncertain: set[str] = set()
    default_uncertain: set[str] = set()
    image_id: str | None = None
    image_uncertain = False
    for suffix in ("a", "b"):
        key_path = ssh_key_dir / f"id_ed25519_{suffix}"
        run("ssh-keygen", "-t", "ed25519", "-f", str(key_path), "-N", "", "-C", "osac-e2e")
        public_keys.append(key_path.with_suffix(".pub").read_text(encoding="utf-8"))
    if any(_project_ids_named(jwt_grpc_tenant1_admin, name) for name in project_names):
        raise AssertionError("Test project name already exists")
    try:
        for project in project_names:
            uncertain_project = project
            try:
                response = jwt_grpc_tenant1_admin.call(
                    service=f"{PUBLIC_API}.Projects/Create",
                    data={"object": {"metadata": {"name": project, "labels": {"e2e-owner": owner}}}},
                )
            except subprocess.CalledProcessError as exc:
                if _definite_create_rejection(f"{exc.stdout or ''} {exc.stderr or ''}"):
                    uncertain_project = None
                raise
            project_ids.append(response["object"]["id"])
            uncertain_project = None
            membership_name = unique_name("e2e-ssh-manager")
            uncertain_membership = (membership_name, project)
            try:
                response = private_grpc.call(
                    service=f"{PRIVATE_API}.ProjectMemberships/Create",
                    data={
                        "object": {
                            "metadata": {
                                "name": membership_name,
                                "tenant": "tenant1",
                                "project": project,
                                "labels": {"e2e-owner": owner},
                            },
                            "spec": {"role": "PROJECT_MEMBERSHIP_ROLE_MANAGER", "users": [{"name": "tenant1-user"}]},
                        }
                    },
                )
            except subprocess.CalledProcessError as exc:
                detail = f"{exc.stdout or ''} {exc.stderr or ''}".lower()
                if _definite_create_rejection(detail):
                    uncertain_membership = None
                raise
            membership_id = response["object"]["id"]
            membership_ids.append(membership_id)
            uncertain_membership = None
            poll_until(
                fn=lambda membership_id=membership_id: (
                    private_grpc.call(service=f"{PRIVATE_API}.ProjectMemberships/Get", data={"id": membership_id})[
                        "object"
                    ]
                    .get("status", {})
                    .get("state", "")
                ),
                until=lambda state: state == "PROJECT_MEMBERSHIP_STATE_READY",
                retries=60,
                delay=2,
                description=f"Project membership {membership_name} ready",
                resource_kind=WaitResourceKind.PROJECT_MEMBERSHIP,
                resource_id=membership_id,
            )
        jwt_cli_user.relogin()
        if jwt_grpc_tenant1._token_factory is None:
            raise AssertionError("A refreshable tenant1 token is required")
        jwt_grpc_tenant1 = GRPCClient(address=jwt_grpc_tenant1.address, token_factory=jwt_grpc_tenant1._token_factory)
        for index, project in enumerate(project_names):
            if _project_secrets(jwt_cli_user, project, key_name):
                raise AssertionError("Test Secret name already exists in project")
            _create_project_secret(
                jwt_cli_user,
                secret_owned,
                secret_uncertain,
                project=project,
                name=key_name,
                public_path=ssh_key_dir / f"id_ed25519_{chr(97 + index)}.pub",
            )
        keys = []
        for project, public_key in zip(project_names, public_keys, strict=True):
            found = _project_secrets(jwt_cli_user, project, key_name)
            if len(found) != 1:
                raise AssertionError("Expected exactly one Secret in each project")
            secret = found[0]
            if secret.get("metadata", {}).get("project") != project:
                raise AssertionError("Secret has an incorrect project")
            if base64.b64decode(secret.get("data", {}).get("public_key", ""), validate=True).decode() != public_key:
                raise AssertionError("Project Secret has the wrong public key")
            keys.append(secret)
        assert keys[0]["id"] != keys[1]["id"]
        with pytest.raises(subprocess.CalledProcessError) as error:
            _create_project_secret(
                jwt_cli_user,
                secret_owned,
                secret_uncertain,
                project=project_names[0],
                name=key_name,
                public_path=ssh_key_dir / "id_ed25519_b.pub",
            )
        if "alreadyexists" not in f"{error.value.stdout or ''} {error.value.stderr or ''}".lower():
            raise AssertionError("Duplicate key was rejected for a reason other than a name conflict")
        after = _project_secrets(jwt_cli_user, project_names[0], key_name)
        if len(after) != 1 or after[0]["id"] != keys[0]["id"] or after[0].get("data") != keys[0].get("data"):
            raise AssertionError("Duplicate registration changed the original Secret")
        if _instances_named(jwt_cli_user, vm_names[1]):
            raise AssertionError("Test VM name already exists")
        image_uncertain = True
        response = jwt_grpc_tenant1.call(
            service=f"{PUBLIC_API}.DiskImages/Create",
            data={
                "object": {
                    "metadata": {"name": image_name, "project": project_names[1]},
                    "spec": {
                        "source_type": "SOURCE_TYPE_REGISTRY",
                        "source_ref": DEFAULT_DISK_IMAGE_SOURCE_REF,
                        "guest_os_family": "GUEST_OS_FAMILY_LINUX",
                        "architecture": ["ARCHITECTURE_AMD64"],
                    },
                }
            },
        )
        image_id = response["object"]["id"]
        image_uncertain = False
        for vm_name, project, vm_owned, vm_uncertain in (
            (vm_names[0], project_names[1], project_vms, project_uncertain),
            (vm_names[1], "", default_vms, default_uncertain),
        ):
            instance_id = _create_owned_instance(
                jwt_cli_user,
                vm_owned,
                vm_uncertain,
                template=vm_template,
                name=vm_name,
                network_attachments=[] if project else [default_network_attachment],
                ssh_key=f"name={key_name},project={project_names[0]}",
                instance_type=cli.default_instance_type,
                disk_image=image_name if project else cli.default_disk_image,
                boot_disk_storage_tier=cli.default_storage_tier,
                project=project or None,
            )
            instance = jwt_grpc_tenant1.get_compute_instance(ci_id=instance_id)["object"]
            reference = instance["spec"]["sshKey"]
            if (
                (instance["metadata"].get("project") or "") != project
                or reference.get("id") != keys[0]["id"]
                or reference.get("name") != key_name
                or reference.get("project") != project_names[0]
            ):
                raise AssertionError("VM selected the wrong same-name project key")
            cr_name = wait_for_cr(k8s=k8s_hub_client, uuid=instance_id)
            cr = k8s_hub_client.get_json(resource="computeinstance", name=cr_name)
            if cr["spec"]["sshKey"] != public_keys[0]:
                raise AssertionError("VM projected the wrong same-name project key")
            if environment is not None:
                wait_for_provision(k8s=k8s_hub_client, name=cr_name)
                wait_for_running(k8s=k8s_hub_client, name=cr_name)
                cr = k8s_hub_client.get_json(resource="computeinstance", name=cr_name)
                _assert_guest_key_access(
                    cr, ssh_key_dir / "id_ed25519_a", ssh_key_dir / "id_ed25519_b", ssh_key_dir, environment
                )
            _cleanup_instances(jwt_cli_user, jwt_grpc_tenant1, k8s_hub_client, vm_owned, vm_uncertain, project=project)
            vm_owned.clear()
        if environment is None:
            pytest.skip(f"API and CR checks passed; guest check skipped: {guest_skip_reason}")
    except subprocess.CalledProcessError as exc:
        detail = f"{exc.stdout or ''} {exc.stderr or ''}".lower()
        if image_uncertain and _definite_create_rejection(detail):
            image_uncertain = False
        raise
    finally:
        _cleanup_instances(jwt_cli_user, jwt_grpc_tenant1, k8s_hub_client, default_vms, default_uncertain)
        _cleanup_instances(
            jwt_cli_user, jwt_grpc_tenant1, k8s_hub_client, project_vms, project_uncertain, project=project_names[1]
        )
        errors: list[Exception] = []
        if image_uncertain:
            errors.append(RuntimeError(f"DiskImage creation outcome uncertain for {image_name}"))
        if image_id:
            try:
                jwt_grpc_tenant1.delete_disk_image(disk_image_id=image_id)
                poll_until(
                    fn=lambda: (
                        image_id
                        not in jwt_grpc_tenant1.list_disk_image_ids(
                            filter_expr=f"this.metadata.name == {json.dumps(image_name)}"
                        )
                    ),
                    until=bool,
                    retries=30,
                    delay=2,
                    description=f"DiskImage {image_name} removal",
                    resource_kind=WaitResourceKind.DISK_IMAGE,
                    resource_id=image_id,
                )
            except Exception as exc:
                errors.append(exc)
        for secret_id, project in secret_owned.items():
            try:
                if _project_secrets(jwt_cli_user, project, secret_id):
                    jwt_cli_user._run("--project", project, "delete", "secret", secret_id)
                poll_until(
                    fn=lambda project=project, secret_id=secret_id: (
                        len(_project_secrets(jwt_cli_user, project, secret_id)) == 0
                    ),
                    until=bool,
                    retries=30,
                    delay=2,
                    description=f"Secret {secret_id} removal in {project}",
                    resource_kind=WaitResourceKind.SECRET,
                    resource_id=secret_id,
                )
            except Exception as exc:
                errors.append(exc)
        if secret_uncertain:
            errors.append(
                RuntimeError(f"Secret creation outcome is uncertain for {', '.join(sorted(secret_uncertain))}")
            )
        if uncertain_membership:
            try:
                membership_ids.append(_recover_owned_membership(private_grpc, *uncertain_membership, owner))
            except Exception as exc:
                errors.append(exc)
        if errors:
            raise ExceptionGroup("Could not clean up project Secrets; preserving projects and memberships", errors)
        for membership_id in reversed(membership_ids):
            try:
                _delete_owned_membership(private_grpc, membership_id)
            except Exception as exc:
                errors.append(exc)
        if errors:
            raise ExceptionGroup("Could not clean up all project memberships; preserving projects", errors)
        if uncertain_project is not None:
            try:
                recovered = poll_until(
                    fn=lambda: _owned_project_ids_named(jwt_grpc_tenant1_admin, uncertain_project, owner),
                    until=bool,
                    retries=30,
                    delay=2,
                    description=f"Recover uncertain project {uncertain_project}",
                    resource_kind=WaitResourceKind.PROJECT,
                    resource_id=uncertain_project,
                )
                project_ids.extend(recovered)
            except Exception as exc:
                errors.append(exc)
        for project_id in reversed(project_ids):
            try:
                project_name = jwt_grpc_tenant1_admin.get_project(project_id=project_id)["object"]["metadata"]["name"]
                jwt_grpc_tenant1_admin.delete_project(project_id=project_id)
                poll_until(
                    fn=lambda project_id=project_id, project_name=project_name: (
                        project_id not in _project_ids_named(jwt_grpc_tenant1_admin, project_name)
                    ),
                    until=bool,
                    retries=30,
                    delay=2,
                    description=f"Project {project_id} removal",
                    resource_kind=WaitResourceKind.PROJECT,
                    resource_id=project_id,
                )
            except Exception as exc:
                errors.append(exc)
        if errors:
            raise ExceptionGroup("Could not clean up all test projects", errors)


def test_project_key_access_controls(
    cli: OsacCLI,
    jwt_cli_user: OsacCLI,
    jwt_cli_admin: OsacCLI,
    jwt_grpc_tenant1: GRPCClient,
    jwt_grpc_tenant1_admin: GRPCClient,
    private_grpc: GRPCClient,
    k8s_hub_client: K8sClient,
    request: pytest.FixtureRequest,
    vm_template: str,
    ssh_key_dir: Path,
) -> None:
    """TC-07a, TC-08 (partial): verify project-key access before and after revocation."""
    environment, guest_skip_reason = _optional_guest_ssh_environment(request)
    _assert_tenant_user(jwt_cli_user)
    _require_tenant1_fixture_profile(cli)
    key_path = ssh_key_dir / "id_ed25519"
    run("ssh-keygen", "-t", "ed25519", "-f", str(key_path), "-N", "", "-C", "osac-e2e")
    unrelated_key = ssh_key_dir / "unrelated"
    if environment is not None:
        run("ssh-keygen", "-t", "ed25519", "-f", str(unrelated_key), "-N", "", "-C", "osac-e2e")
    visible_project = unique_name("e2e-ssh-visible")
    hidden_project = unique_name("e2e-ssh-hidden")
    destination_project = unique_name("e2e-ssh-destination")
    image_name = unique_name("e2e-ssh-destination-image")
    owner = unique_name("e2e-ssh-owner")
    key_name = unique_name("e2e-ssh-project-key")
    missing_key = unique_name("e2e-ssh-project-missing")
    control_name = unique_name("e2e-ssh-project-control")
    projects = (visible_project, hidden_project, destination_project)
    if any(_project_ids_named(jwt_grpc_tenant1_admin, project) for project in projects):
        raise AssertionError("Test project name already exists")
    project_ids: list[str] = []
    uncertain_project: str | None = None
    membership_id: str | None = None
    memberships: dict[str, str] = {}
    uncertain_membership: tuple[str, str] | None = None
    image_id: str | None = None
    uncertain_image = False
    keys: dict[str, str] = {}
    key_uncertain: set[str] = set()
    vm_owned: dict[str, str] = {}
    vm_uncertain: set[str] = set()
    try:
        for project in projects:
            uncertain_project = project
            try:
                result = jwt_grpc_tenant1_admin.call(
                    service=f"{PUBLIC_API}.Projects/Create",
                    data={"object": {"metadata": {"name": project, "labels": {"e2e-owner": owner}}}},
                )
            except subprocess.CalledProcessError as exc:
                if _definite_create_rejection(f"{exc.stdout or ''} {exc.stderr or ''}"):
                    uncertain_project = None
                raise
            project_ids.append(result["object"]["id"])
            uncertain_project = None
        for membership_project, role in (
            (visible_project, "PROJECT_MEMBERSHIP_ROLE_VIEWER"),
            (destination_project, "PROJECT_MEMBERSHIP_ROLE_MANAGER"),
        ):
            membership_name = unique_name("e2e-ssh-membership")
            uncertain_membership = (membership_name, membership_project)
            try:
                result = private_grpc.call(
                    service=f"{PRIVATE_API}.ProjectMemberships/Create",
                    data={
                        "object": {
                            "metadata": {
                                "name": membership_name,
                                "tenant": "tenant1",
                                "project": membership_project,
                                "labels": {"e2e-owner": owner},
                            },
                            "spec": {"role": role, "users": [{"name": "tenant1-user"}]},
                        }
                    },
                )
            except subprocess.CalledProcessError as exc:
                detail = f"{exc.stdout or ''} {exc.stderr or ''}".lower()
                if _definite_create_rejection(detail):
                    uncertain_membership = None
                raise
            member_id = result["object"]["id"]
            memberships[membership_project] = member_id
            uncertain_membership = None
            poll_until(
                fn=lambda member_id=member_id: (
                    private_grpc.call(service=f"{PRIVATE_API}.ProjectMemberships/Get", data={"id": member_id})["object"]
                    .get("status", {})
                    .get("state", "")
                ),
                until=lambda state: state == "PROJECT_MEMBERSHIP_STATE_READY",
                retries=60,
                delay=2,
                description=f"Project membership {membership_name} ready",
                resource_kind=WaitResourceKind.PROJECT_MEMBERSHIP,
                resource_id=member_id,
            )
        membership_id = memberships[visible_project]
        membership_name = visible_project
        jwt_cli_user.relogin()
        if jwt_grpc_tenant1._token_factory is None:
            raise AssertionError("A refreshable tenant1 token is required")
        jwt_grpc_tenant1 = GRPCClient(address=jwt_grpc_tenant1.address, token_factory=jwt_grpc_tenant1._token_factory)
        _create_project_secret(
            jwt_cli_admin,
            keys,
            key_uncertain,
            project=visible_project,
            name=key_name,
            public_path=key_path.with_suffix(".pub"),
        )
        _create_project_secret(
            jwt_cli_admin,
            keys,
            key_uncertain,
            project=hidden_project,
            name=key_name,
            public_path=key_path.with_suffix(".pub"),
        )
        visible = _project_secrets(jwt_cli_user, visible_project, key_name)[0]
        if _instances_named(jwt_cli_user, control_name, project=destination_project):
            raise AssertionError("Test VM name already exists")
        uncertain_image = True
        try:
            response = jwt_grpc_tenant1.call(
                service=f"{PUBLIC_API}.DiskImages/Create",
                data={
                    "object": {
                        "metadata": {"name": image_name, "project": destination_project},
                        "spec": {
                            "source_type": "SOURCE_TYPE_REGISTRY",
                            "source_ref": DEFAULT_DISK_IMAGE_SOURCE_REF,
                            "guest_os_family": "GUEST_OS_FAMILY_LINUX",
                            "architecture": ["ARCHITECTURE_AMD64"],
                        },
                    }
                },
            )
        except subprocess.CalledProcessError as exc:
            detail = f"{exc.stdout or ''} {exc.stderr or ''}".lower()
            if _definite_create_rejection(detail):
                uncertain_image = False
            raise
        image_id = response["object"]["id"]
        uncertain_image = False
        control_id = _create_owned_instance(
            jwt_cli_user,
            vm_owned,
            vm_uncertain,
            template=vm_template,
            name=control_name,
            network_attachments=[],
            ssh_key=f"name={key_name},project={visible_project}",
            instance_type=cli.default_instance_type,
            disk_image=image_name,
            boot_disk_storage_tier=cli.default_storage_tier,
            project=destination_project,
        )
        control = jwt_grpc_tenant1.get_compute_instance(ci_id=control_id)["object"]
        _assert_existing_instance_id_rejected(
            jwt_grpc_tenant1, jwt_cli_user, control, vm_owned, vm_uncertain, project=destination_project
        )
        assert control["metadata"]["project"] == destination_project
        assert control["spec"]["sshKey"]["id"] == visible["id"]
        assert control["spec"]["sshKey"]["project"] == visible_project
        control_cr = wait_for_cr(k8s=k8s_hub_client, uuid=control_id)

        def assert_denied(denied_project: str) -> None:
            errors: list[str] = []
            for name in (missing_key, key_name):
                vm_name = unique_name("e2e-ssh-project-denied")
                if _instances_named(jwt_cli_user, vm_name, project=destination_project):
                    raise AssertionError("Test VM name already exists")
                rejected_id = str(uuid4())
                spec = copy.deepcopy(control["spec"])
                spec["sshKey"] = {"name": name, "project": denied_project}
                observation = f"{vm_name} (id={rejected_id})"
                vm_uncertain.add(observation)
                try:
                    result = jwt_grpc_tenant1.call(
                        service=f"{PUBLIC_API}.ComputeInstances/Create",
                        data={
                            "object": {
                                "id": rejected_id,
                                "metadata": {"name": vm_name, "project": destination_project},
                                "spec": spec,
                            }
                        },
                    )
                except subprocess.CalledProcessError as exc:
                    detail = f"{exc.stdout or ''} {exc.stderr or ''}".lower()
                    expected = "ssh key secret was not found or is not visible in the selected project."
                    pattern = r"\s*error:\s*code:\s*notfound\s*message:\s*" + re.escape(expected) + r"\s*"
                    if not re.fullmatch(pattern, detail):
                        status = _rpc_status(detail)
                        if status not in {
                            "alreadyexists",
                            "invalidargument",
                            "notfound",
                            "permissiondenied",
                            "unauthenticated",
                        }:
                            vm_uncertain.add(vm_name)
                        raise AssertionError("Project key was rejected for an unexpected reason") from exc
                    errors.append("\n".join(line.strip() for line in detail.splitlines() if line.strip()))
                except Exception:
                    vm_uncertain.add(vm_name)
                    raise
                else:
                    instance_id = result.get("object", {}).get("id")
                    if instance_id:
                        vm_owned[vm_name] = instance_id
                        vm_uncertain.remove(observation)
                    else:
                        vm_uncertain.add(vm_name)
                    raise AssertionError("VM with an inaccessible project key was accepted")
                observation = f"{vm_name} (id={rejected_id})"
                vm_uncertain.add(observation)
                if _instances_named(jwt_cli_user, vm_name, project=destination_project):
                    raise AssertionError("Rejected project-key request persisted a VM")
                _assert_rejected_instance_cr_absent(k8s_hub_client, rejected_id)
                vm_uncertain.remove(observation)
            assert errors[0] == errors[1]

        assert_denied(hidden_project)
        wait_for_provision(k8s=k8s_hub_client, name=control_cr)
        wait_for_running(k8s=k8s_hub_client, name=control_cr)
        original_cr = k8s_hub_client.get_json(resource="computeinstance", name=control_cr)
        original_key = original_cr["spec"].get("sshKey")
        if original_key != key_path.with_suffix(".pub").read_text(encoding="utf-8"):
            raise AssertionError("Existing VM did not receive the selected registered key")
        if environment is not None:
            _assert_guest_key_access(original_cr, key_path, unrelated_key, ssh_key_dir, environment)

        # TC-07a: retain key-project access while removing destination-project access.
        _delete_owned_membership(private_grpc, memberships[destination_project])
        memberships.pop(destination_project)

        def destination_access_revoked() -> bool:
            jwt_cli_user.relogin()
            try:
                return not any(
                    item.get("id") == control_id
                    for item in _instances_named(jwt_cli_user, control_name, project=destination_project)
                )
            except subprocess.CalledProcessError as exc:
                detail = f"{exc.stdout or ''} {exc.stderr or ''}".lower()
                if _rpc_status(detail) in {"permissiondenied", "notfound"}:
                    return True
                raise

        poll_until(
            fn=destination_access_revoked,
            until=bool,
            retries=30,
            delay=2,
            description=f"destination-project access revocation for {destination_project}",
            resource_kind=WaitResourceKind.PROJECT,
            resource_id=destination_project,
        )
        jwt_grpc_tenant1 = GRPCClient(address=jwt_grpc_tenant1.address, token_factory=jwt_grpc_tenant1._token_factory)
        visible_control = _project_secrets(jwt_cli_user, visible_project, key_name)
        if len(visible_control) != 1 or visible_control[0].get("id") != visible["id"]:
            raise AssertionError("Key-project access was lost during destination-project revocation")
        denied_name = unique_name("e2e-ssh-destination-denied")
        denied_id = str(uuid4())
        if _instances_named(jwt_cli_admin, denied_name, project=destination_project):
            raise AssertionError("Test VM name already exists")
        observation = f"{denied_name} (id={denied_id})"
        vm_uncertain.add(observation)
        try:
            result = jwt_grpc_tenant1.call(
                service=f"{PUBLIC_API}.ComputeInstances/Create",
                data={
                    "object": {
                        "id": denied_id,
                        "metadata": {"name": denied_name, "project": destination_project},
                        "spec": copy.deepcopy(control["spec"]),
                    }
                },
            )
        except subprocess.CalledProcessError as exc:
            detail = f"{exc.stdout or ''} {exc.stderr or ''}".lower()
            status = _rpc_status(detail)
            if status not in {"permissiondenied", "notfound", "invalidargument", "failedprecondition"}:
                if status in {"alreadyexists", "invalidargument", "permissiondenied", "notfound", "unauthenticated"}:
                    vm_uncertain.remove(observation)
                raise AssertionError("Destination-project request failed for an unexpected reason") from exc
            key_body = key_path.with_suffix(".pub").read_text(encoding="utf-8").split()[1].lower()
            if visible["id"].lower() in detail or key_body in detail:
                raise AssertionError("Destination-project rejection disclosed the key") from None
        except Exception:
            vm_uncertain.add(denied_name)
            raise
        else:
            instance_id = result.get("object", {}).get("id")
            if instance_id:
                vm_owned[denied_name] = instance_id
                vm_uncertain.remove(observation)
            raise AssertionError("VM creation in an inaccessible destination project was accepted")
        if _instances_named(jwt_cli_admin, denied_name, project=destination_project):
            raise AssertionError("Rejected destination-project request persisted a VM")
        _assert_rejected_instance_cr_absent(k8s_hub_client, denied_id)
        vm_uncertain.remove(observation)

        # Restore test-owned destination access before exercising TC-08.
        restore_name = unique_name("e2e-ssh-membership")
        uncertain_membership = (restore_name, destination_project)
        try:
            result = private_grpc.call(
                service=f"{PRIVATE_API}.ProjectMemberships/Create",
                data={
                    "object": {
                        "metadata": {
                            "name": restore_name,
                            "tenant": "tenant1",
                            "project": destination_project,
                            "labels": {"e2e-owner": owner},
                        },
                        "spec": {"role": "PROJECT_MEMBERSHIP_ROLE_MANAGER", "users": [{"name": "tenant1-user"}]},
                    }
                },
            )
        except subprocess.CalledProcessError as exc:
            detail = f"{exc.stdout or ''} {exc.stderr or ''}".lower()
            if _definite_create_rejection(detail):
                uncertain_membership = None
            raise
        restored_id = result["object"]["id"]
        memberships[destination_project] = restored_id
        uncertain_membership = None
        poll_until(
            fn=lambda: (
                private_grpc.call(service=f"{PRIVATE_API}.ProjectMemberships/Get", data={"id": restored_id})["object"]
                .get("status", {})
                .get("state", "")
            ),
            until=lambda state: state == "PROJECT_MEMBERSHIP_STATE_READY",
            retries=60,
            delay=2,
            description="restored destination-project membership",
            resource_kind=WaitResourceKind.PROJECT_MEMBERSHIP,
            resource_id=restored_id,
        )
        jwt_cli_user.relogin()
        jwt_grpc_tenant1 = GRPCClient(address=jwt_grpc_tenant1.address, token_factory=jwt_grpc_tenant1._token_factory)
        private_grpc.call(service=f"{PRIVATE_API}.ProjectMemberships/Delete", data={"id": membership_id})

        def membership_removed() -> bool:
            try:
                private_grpc.call(service=f"{PRIVATE_API}.ProjectMemberships/Get", data={"id": membership_id})
            except subprocess.CalledProcessError as exc:
                detail = f"{exc.stdout or ''} {exc.stderr or ''}".lower()
                if _rpc_status(detail) == "notfound":
                    return True
                raise
            return False

        poll_until(
            fn=membership_removed,
            until=bool,
            retries=30,
            delay=2,
            description=f"Project membership {membership_name} removal",
            resource_kind=WaitResourceKind.PROJECT_MEMBERSHIP,
            resource_id=membership_id,
        )
        membership_id = None
        memberships.pop(visible_project)

        def access_revoked() -> bool:
            jwt_cli_user.relogin()
            try:
                return not _project_secrets(jwt_cli_user, visible_project, key_name)
            except subprocess.CalledProcessError as exc:
                detail = f"{exc.stdout or ''} {exc.stderr or ''}".lower()
                if _rpc_status(detail) in {"permissiondenied", "notfound"}:
                    return True
                raise

        poll_until(
            fn=access_revoked,
            until=bool,
            retries=30,
            delay=2,
            description=f"Project access revocation for {visible_project}",
            resource_kind=WaitResourceKind.PROJECT,
            resource_id=visible_project,
        )
        visible_after = _project_secrets(jwt_cli_admin, visible_project, visible["id"])
        if len(visible_after) != 1 or visible_after[0].get("id") != visible["id"]:
            raise AssertionError("The project key is missing after access revocation")
        if visible_after[0].get("data") != visible.get("data"):
            raise AssertionError("The project key changed after access revocation")
        jwt_grpc_tenant1 = GRPCClient(address=jwt_grpc_tenant1.address, token_factory=jwt_grpc_tenant1._token_factory)
        assert_denied(visible_project)
        current_cr = k8s_hub_client.get_json(resource="computeinstance", name=control_cr)
        if current_cr["spec"].get("sshKey") != original_key:
            raise AssertionError("Existing VM key changed after project access was revoked")
        wait_for_running(k8s=k8s_hub_client, name=control_cr)
        if environment is None:
            pytest.skip(f"API and CR checks passed; guest check skipped: {guest_skip_reason}")
        _assert_guest_key_access(current_cr, key_path, unrelated_key, ssh_key_dir, environment)
    finally:
        _cleanup_instances(
            jwt_cli_admin, jwt_grpc_tenant1_admin, k8s_hub_client, vm_owned, vm_uncertain, project=destination_project
        )
        errors: list[Exception] = []
        for secret_id, project in keys.items():
            key_cli = jwt_cli_admin
            try:
                if _project_secrets(key_cli, project, secret_id):
                    key_cli._run("--project", project, "delete", "secret", secret_id)
                poll_until(
                    fn=lambda key_cli=key_cli, project=project, secret_id=secret_id: (
                        not _project_secrets(key_cli, project, secret_id)
                    ),
                    until=bool,
                    retries=30,
                    delay=2,
                    description=f"Secret {secret_id} removal",
                    resource_kind=WaitResourceKind.SECRET,
                    resource_id=secret_id,
                )
            except Exception as exc:
                errors.append(exc)
        if key_uncertain:
            errors.append(RuntimeError(f"Secret creation outcome uncertain for {', '.join(sorted(key_uncertain))}"))
        if uncertain_image:
            errors.append(RuntimeError(f"DiskImage creation outcome uncertain for {image_name}"))
        if image_id:
            try:
                jwt_grpc_tenant1_admin.delete_disk_image(disk_image_id=image_id)
                poll_until(
                    fn=lambda: (
                        image_id
                        not in jwt_grpc_tenant1_admin.list_disk_image_ids(
                            filter_expr=f"this.metadata.name == {json.dumps(image_name)}"
                        )
                    ),
                    until=bool,
                    retries=30,
                    delay=2,
                    description=f"DiskImage {image_name} removal",
                    resource_kind=WaitResourceKind.DISK_IMAGE,
                    resource_id=image_id,
                )
            except Exception as exc:
                errors.append(exc)
        if uncertain_membership:
            try:
                name, scope = uncertain_membership
                memberships[scope] = _recover_owned_membership(private_grpc, name, scope, owner)
            except Exception as exc:
                errors.append(exc)
        if errors:
            raise ExceptionGroup("Could not remove test resources; preserving projects and memberships", errors)
        for member_id in memberships.values():
            try:
                _delete_owned_membership(private_grpc, member_id)
            except Exception as exc:
                errors.append(exc)
        if uncertain_project:
            try:
                recovered = poll_until(
                    fn=lambda: _owned_project_ids_named(jwt_grpc_tenant1_admin, uncertain_project, owner),
                    until=bool,
                    retries=30,
                    delay=2,
                    description=f"Recover project {uncertain_project}",
                    resource_kind=WaitResourceKind.PROJECT,
                    resource_id=uncertain_project,
                )
                project_ids.extend(recovered)
            except Exception as exc:
                errors.append(exc)
        if errors:
            raise ExceptionGroup("Could not remove test membership or recover project", errors)
        for project_id in reversed(project_ids):
            try:
                project_name = jwt_grpc_tenant1_admin.get_project(project_id=project_id)["object"]["metadata"]["name"]
                jwt_grpc_tenant1_admin.delete_project(project_id=project_id)
                poll_until(
                    fn=lambda project_id=project_id, project_name=project_name: (
                        project_id not in _project_ids_named(jwt_grpc_tenant1_admin, project_name)
                    ),
                    until=bool,
                    retries=30,
                    delay=2,
                    description=f"Project {project_name} removal",
                    resource_kind=WaitResourceKind.PROJECT,
                    resource_id=project_id,
                )
            except Exception as exc:
                errors.append(exc)
        if errors:
            raise ExceptionGroup("Could not remove all test projects", errors)


def test_cross_tenant_keys_are_not_disclosed(
    cli: OsacCLI,
    jwt_cli_user: OsacCLI,
    jwt_cli_tenant2_admin: OsacCLI,
    jwt_grpc_tenant1: GRPCClient,
    k8s_hub_client: K8sClient,
    request: pytest.FixtureRequest,
    vm_template: str,
    default_network_attachment: dict[str, object],
    ssh_key_dir: Path,
) -> None:
    """TC-07b (partial): hidden tenant keys are indistinguishable from missing keys."""
    environment, guest_skip_reason = _optional_guest_ssh_environment(request)
    _assert_tenant_user(jwt_cli_user)
    _require_tenant1_fixture_profile(cli)
    own_path = ssh_key_dir / "own"
    other_path = ssh_key_dir / "other"
    for path in (own_path, other_path):
        run("ssh-keygen", "-t", "ed25519", "-f", str(path), "-N", "", "-C", "osac-e2e")
    shared_name = unique_name("e2e-ssh-same-name")
    hidden_name = unique_name("e2e-ssh-other-name")
    nonexistent_name = unique_name("e2e-ssh-missing-name")
    control_name = unique_name("e2e-ssh-tenant-control")
    case_names = [unique_name("e2e-ssh-tenant-denied") for _ in range(4)]
    if (
        _secrets(jwt_cli_user, shared_name)
        or any(_secrets(jwt_cli_tenant2_admin, name) for name in (shared_name, hidden_name, nonexistent_name))
        or any(_instances_named(jwt_cli_user, name) for name in (control_name, *case_names))
    ):
        raise AssertionError("Test resource name already exists")
    own_keys: dict[str, str] = {}
    other_keys: dict[str, str] = {}
    own_uncertain: set[str] = set()
    other_uncertain: set[str] = set()
    vm_owned: dict[str, str] = {}
    vm_uncertain: set[str] = set()
    try:
        own_id = _create_owned_secret(
            jwt_cli_user,
            own_keys,
            own_uncertain,
            name=shared_name,
            secret_type="ssh-public-key",
            field="public_key",
            path=own_path.with_suffix(".pub"),
        )
        hidden_id = _create_owned_secret(
            jwt_cli_tenant2_admin,
            other_keys,
            other_uncertain,
            name=shared_name,
            secret_type="ssh-public-key",
            field="public_key",
            path=other_path.with_suffix(".pub"),
        )
        _create_owned_secret(
            jwt_cli_tenant2_admin,
            other_keys,
            other_uncertain,
            name=hidden_name,
            secret_type="ssh-public-key",
            field="public_key",
            path=other_path.with_suffix(".pub"),
        )
        assert hidden_id != own_id
        control_id = _create_owned_instance(
            jwt_cli_user,
            vm_owned,
            vm_uncertain,
            template=vm_template,
            name=control_name,
            network_attachments=[default_network_attachment],
            ssh_key=shared_name,
            instance_type=cli.default_instance_type,
            disk_image=cli.default_disk_image,
            boot_disk_storage_tier=cli.default_storage_tier,
        )
        control = jwt_grpc_tenant1.get_compute_instance(ci_id=control_id)["object"]
        _assert_existing_instance_id_rejected(jwt_grpc_tenant1, jwt_cli_user, control, vm_owned, vm_uncertain)
        assert control["spec"]["sshKey"]["id"] == own_id
        assert control["spec"]["sshKey"]["id"] != hidden_id
        cr_name = wait_for_cr(k8s=k8s_hub_client, uuid=control_id)
        cr = k8s_hub_client.get_json(resource="computeinstance", name=cr_name)
        assert cr["spec"]["sshKey"] == own_path.with_suffix(".pub").read_text(encoding="utf-8")

        selectors = ({"id": str(uuid4())}, {"id": hidden_id}, {"name": nonexistent_name}, {"name": hidden_name})
        errors: list[str] = []
        for selector, vm_name in zip(selectors, case_names, strict=True):
            rejected_id = str(uuid4())
            spec = copy.deepcopy(control["spec"])
            spec["sshKey"] = selector
            observation = f"{vm_name} (id={rejected_id})"
            vm_uncertain.add(observation)
            try:
                result = jwt_grpc_tenant1.call(
                    service=f"{PUBLIC_API}.ComputeInstances/Create",
                    data={"object": {"id": rejected_id, "metadata": {"name": vm_name}, "spec": spec}},
                )
            except subprocess.CalledProcessError as exc:
                detail = f"{exc.stdout or ''} {exc.stderr or ''}".lower()
                expected = "ssh key secret was not found or is not visible in the selected project."
                pattern = r"\s*error:\s*code:\s*notfound\s*message:\s*" + re.escape(expected) + r"\s*"
                if not re.fullmatch(pattern, detail):
                    status = _rpc_status(detail)
                    if status not in {
                        "alreadyexists",
                        "invalidargument",
                        "notfound",
                        "permissiondenied",
                        "unauthenticated",
                    }:
                        vm_uncertain.add(vm_name)
                    raise AssertionError("Hidden or nonexistent key was rejected for an unexpected reason") from exc
                errors.append("\n".join(line.strip() for line in detail.splitlines() if line.strip()))
            except Exception:
                vm_uncertain.add(vm_name)
                raise
            else:
                instance_id = result.get("object", {}).get("id")
                if instance_id:
                    vm_owned[vm_name] = instance_id
                    vm_uncertain.remove(observation)
                else:
                    vm_uncertain.add(vm_name)
                raise AssertionError("A VM with an unavailable cross-tenant key was accepted")
            observation = f"{vm_name} (id={rejected_id})"
            vm_uncertain.add(observation)
            if _instances_named(jwt_cli_user, vm_name):
                raise AssertionError("Rejected VM request left an API object")
            _assert_rejected_instance_cr_absent(k8s_hub_client, rejected_id)
            vm_uncertain.remove(observation)
        assert errors[0] == errors[1] and errors[2] == errors[3]
        if environment is None:
            pytest.skip(f"API and CR checks passed; guest check skipped: {guest_skip_reason}")
        wait_for_provision(k8s=k8s_hub_client, name=cr_name)
        wait_for_running(k8s=k8s_hub_client, name=cr_name)
        cr = k8s_hub_client.get_json(resource="computeinstance", name=cr_name)
        _assert_guest_key_access(cr, own_path, other_path, ssh_key_dir, environment)
    finally:
        _cleanup_instances(jwt_cli_user, jwt_grpc_tenant1, k8s_hub_client, vm_owned, vm_uncertain)
        cleanup_errors: list[Exception] = []
        for key_cli, owned, uncertain in (
            (jwt_cli_user, own_keys, own_uncertain),
            (jwt_cli_tenant2_admin, other_keys, other_uncertain),
        ):
            try:
                _cleanup_secrets(key_cli, owned, uncertain)
            except Exception as exc:
                cleanup_errors.append(exc)
        if cleanup_errors:
            raise ExceptionGroup("Could not clean up all tenant test Secrets", cleanup_errors)


@pytest.mark.parametrize("key_scope", ["default", "project"], ids=["TC-03", "TC-04"])
def test_named_project_vm_selects_registered_key(
    cli: OsacCLI,
    jwt_cli_user: OsacCLI,
    jwt_grpc_tenant1: GRPCClient,
    jwt_grpc_tenant1_admin: GRPCClient,
    private_grpc: GRPCClient,
    k8s_hub_client: K8sClient,
    request: pytest.FixtureRequest,
    vm_template: str,
    ssh_key_dir: Path,
    key_scope: str,
) -> None:
    """TC-03, TC-04: named-project VM selects and authenticates with the registered key."""
    environment, guest_skip_reason = _optional_guest_ssh_environment(request)
    _assert_tenant_user(jwt_cli_user)
    _require_tenant1_fixture_profile(cli)
    key_path = ssh_key_dir / "id_ed25519"
    run("ssh-keygen", "-t", "ed25519", "-f", str(key_path), "-N", "", "-C", "osac-e2e")
    unrelated_key = ssh_key_dir / "unrelated"
    if environment is not None:
        run("ssh-keygen", "-t", "ed25519", "-f", str(unrelated_key), "-N", "", "-C", "osac-e2e")
    project = unique_name("e2e-ssh-vm-project")
    owner = unique_name("e2e-ssh-owner")
    key_name = unique_name("e2e-ssh-project-key")
    vm_name = unique_name("e2e-ssh-project-vm")
    image_name = unique_name("e2e-ssh-project-image")
    if _project_ids_named(jwt_grpc_tenant1_admin, project) or _secrets(jwt_cli_user, key_name):
        raise AssertionError("Test resource name already exists")
    project_id: str | None = None
    uncertain_project = False
    membership_id: str | None = None
    membership_name = unique_name("e2e-ssh-manager")
    uncertain_membership = False
    image_id: str | None = None
    uncertain_resource: str | None = None
    default_keys: dict[str, str] = {}
    project_keys: dict[str, str] = {}
    key_uncertain: set[str] = set()
    vm_owned: dict[str, str] = {}
    vm_uncertain: set[str] = set()
    try:
        uncertain_project = True
        try:
            response = jwt_grpc_tenant1_admin.call(
                service=f"{PUBLIC_API}.Projects/Create",
                data={"object": {"metadata": {"name": project, "labels": {"e2e-owner": owner}}}},
            )
        except subprocess.CalledProcessError as exc:
            if _definite_create_rejection(f"{exc.stdout or ''} {exc.stderr or ''}"):
                uncertain_project = False
            raise
        project_id = response["object"]["id"]
        uncertain_project = False
        uncertain_membership = True
        try:
            response = private_grpc.call(
                service=f"{PRIVATE_API}.ProjectMemberships/Create",
                data={
                    "object": {
                        "metadata": {
                            "name": membership_name,
                            "tenant": "tenant1",
                            "project": project,
                            "labels": {"e2e-owner": owner},
                        },
                        "spec": {"role": "PROJECT_MEMBERSHIP_ROLE_MANAGER", "users": [{"name": "tenant1-user"}]},
                    }
                },
            )
        except subprocess.CalledProcessError as exc:
            detail = f"{exc.stdout or ''} {exc.stderr or ''}".lower()
            if _definite_create_rejection(detail):
                uncertain_membership = False
            raise
        membership_id = response["object"]["id"]
        uncertain_membership = False
        poll_until(
            fn=lambda: (
                private_grpc.call(service=f"{PRIVATE_API}.ProjectMemberships/Get", data={"id": membership_id})["object"]
                .get("status", {})
                .get("state", "")
            ),
            until=lambda state: state == "PROJECT_MEMBERSHIP_STATE_READY",
            retries=60,
            delay=2,
            description=f"Project membership {membership_name} ready",
            resource_kind=WaitResourceKind.PROJECT_MEMBERSHIP,
            resource_id=membership_id,
        )
        jwt_cli_user.relogin()
        if jwt_grpc_tenant1._token_factory is None:
            raise AssertionError("A refreshable tenant1 token is required")
        jwt_grpc_tenant1 = GRPCClient(address=jwt_grpc_tenant1.address, token_factory=jwt_grpc_tenant1._token_factory)
        if key_scope == "default":
            key_id = _create_owned_secret(
                jwt_cli_user,
                default_keys,
                key_uncertain,
                name=key_name,
                secret_type="ssh-public-key",
                field="public_key",
                path=key_path.with_suffix(".pub"),
            )
            selector = key_name
        else:
            _create_project_secret(
                jwt_cli_user,
                project_keys,
                key_uncertain,
                project=project,
                name=key_name,
                public_path=key_path.with_suffix(".pub"),
            )
            key_id = next(iter(project_keys))
            selector = f"name={key_name},project={project}"

        uncertain_resource = f"DiskImage {image_name}"
        response = jwt_grpc_tenant1.call(
            service=f"{PUBLIC_API}.DiskImages/Create",
            data={
                "object": {
                    "metadata": {"name": image_name, "project": project},
                    "spec": {
                        "source_type": "SOURCE_TYPE_REGISTRY",
                        "source_ref": DEFAULT_DISK_IMAGE_SOURCE_REF,
                        "guest_os_family": "GUEST_OS_FAMILY_LINUX",
                        "architecture": ["ARCHITECTURE_AMD64"],
                    },
                }
            },
        )
        image_id = response["object"]["id"]
        uncertain_resource = None
        instance_id = _create_owned_instance(
            jwt_cli_user,
            vm_owned,
            vm_uncertain,
            template=vm_template,
            name=vm_name,
            network_attachments=[],
            ssh_key=selector,
            instance_type=cli.default_instance_type,
            disk_image=image_name,
            boot_disk_storage_tier=cli.default_storage_tier,
            project=project,
        )
        instance = jwt_grpc_tenant1.get_compute_instance(ci_id=instance_id)["object"]
        reference = instance["spec"]["sshKey"]
        if (
            instance["metadata"].get("project") != project
            or reference.get("id") != key_id
            or reference.get("name") != key_name
        ):
            raise AssertionError("Named-project VM selected an unexpected SSH-key reference")
        if (reference.get("project") or "") != (project if key_scope == "project" else ""):
            raise AssertionError("Named-project VM selected the wrong key project")
        cr_name = wait_for_cr(k8s=k8s_hub_client, uuid=instance_id)
        wait_for_provision(k8s=k8s_hub_client, name=cr_name)
        wait_for_running(k8s=k8s_hub_client, name=cr_name)
        cr = k8s_hub_client.get_json(resource="computeinstance", name=cr_name)
        if cr["spec"]["sshKey"] != key_path.with_suffix(".pub").read_text(encoding="utf-8"):
            raise AssertionError("Named-project VM received the wrong SSH public key")
        if environment is None:
            pytest.skip(f"API and CR checks passed; guest check skipped: {guest_skip_reason}")
        _assert_guest_key_access(cr, key_path, unrelated_key, ssh_key_dir, environment)
    except subprocess.CalledProcessError as exc:
        detail = f"{exc.stdout or ''} {exc.stderr or ''}".lower()
        if uncertain_resource and _definite_create_rejection(detail):
            uncertain_resource = None
        raise
    finally:
        _cleanup_instances(jwt_cli_user, jwt_grpc_tenant1, k8s_hub_client, vm_owned, vm_uncertain, project=project)
        errors: list[Exception] = []
        try:
            _cleanup_secrets(jwt_cli_user, default_keys, key_uncertain if key_scope == "default" else set())
        except Exception as exc:
            errors.append(exc)
        for secret_id in project_keys:
            try:
                if _project_secrets(jwt_cli_user, project, secret_id):
                    jwt_cli_user._run("--project", project, "delete", "secret", secret_id)
                poll_until(
                    fn=lambda secret_id=secret_id: not _project_secrets(jwt_cli_user, project, secret_id),
                    until=bool,
                    retries=30,
                    delay=2,
                    description=f"Secret {secret_id} removal",
                    resource_kind=WaitResourceKind.SECRET,
                    resource_id=secret_id,
                )
            except Exception as exc:
                errors.append(exc)
        if key_scope == "project" and key_uncertain:
            errors.append(RuntimeError(f"Secret creation outcome uncertain for {', '.join(sorted(key_uncertain))}"))
        if image_id:
            try:
                jwt_grpc_tenant1.delete_disk_image(disk_image_id=image_id)
                poll_until(
                    fn=lambda: (
                        image_id
                        not in jwt_grpc_tenant1.list_disk_image_ids(
                            filter_expr=f"this.metadata.name == {json.dumps(image_name)}"
                        )
                    ),
                    until=bool,
                    retries=30,
                    delay=2,
                    description=f"DiskImage {image_name} removal",
                    resource_kind=WaitResourceKind.DISK_IMAGE,
                    resource_id=image_id,
                )
            except Exception as exc:
                errors.append(exc)
        if uncertain_resource:
            errors.append(RuntimeError(f"Creation outcome uncertain for {uncertain_resource}"))
        if uncertain_project:
            try:
                recovered = poll_until(
                    fn=lambda: _owned_project_ids_named(jwt_grpc_tenant1_admin, project, owner),
                    until=bool,
                    retries=30,
                    delay=2,
                    description=f"Recover project {project}",
                    resource_kind=WaitResourceKind.PROJECT,
                    resource_id=project,
                )
                project_id = recovered[0]
            except Exception as exc:
                errors.append(exc)
        if uncertain_membership:
            try:
                membership_id = _recover_owned_membership(private_grpc, membership_name, project, owner)
            except Exception as exc:
                errors.append(exc)
        if errors:
            raise ExceptionGroup("Could not clean all named-project resources; preserving project", errors)
        if membership_id:
            _delete_owned_membership(private_grpc, membership_id)
        if project_id:
            jwt_grpc_tenant1_admin.delete_project(project_id=project_id)
            poll_until(
                fn=lambda: project_id not in _project_ids_named(jwt_grpc_tenant1_admin, project),
                until=bool,
                retries=30,
                delay=2,
                description=f"Project {project} removal",
                resource_kind=WaitResourceKind.PROJECT,
                resource_id=project_id,
            )
