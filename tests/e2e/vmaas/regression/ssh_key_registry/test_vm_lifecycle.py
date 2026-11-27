"""VM SSH-key reference, compatibility and update regressions."""

from __future__ import annotations

import base64
import copy
import json
import logging
import os
import re
import subprocess
import sys
from datetime import UTC, datetime, timedelta
from pathlib import Path
from urllib.parse import urlencode
from uuid import uuid4

import pytest

from tests.e2e.core.grpc_client import PRIVATE_API, PUBLIC_API, GRPCClient
from tests.e2e.core.helpers import unique_name, wait_for_cr, wait_for_provision, wait_for_running
from tests.e2e.core.k8s_client import K8sClient
from tests.e2e.core.osac_cli import OsacCLI
from tests.e2e.core.runner import CommandFailed, poll_until, run
from tests.e2e.core.wait_kinds import WaitResourceKind
from tests.e2e.vmaas.conftest import DEFAULT_DISK_IMAGE_SOURCE_REF
from tests.e2e.vmaas.regression.ssh_key_registry.helpers import (
    _assert_existing_instance_id_rejected,
    _assert_guest_key_access,
    _assert_rejected_instance_cr_absent,
    _assert_tenant_user,
    _assert_vm_key_injected,
    _cleanup_instances,
    _cleanup_secrets,
    _create_owned_instance,
    _create_owned_secret,
    _create_project_secret,
    _definite_create_rejection,
    _delete_owned_membership,
    _hygiene_needles,
    _hygiene_scan,
    _instances_named,
    _optional_guest_ssh_environment,
    _owned_project_ids_named,
    _project_ids_named,
    _project_secrets,
    _recover_owned_membership,
    _require_tenant1_fixture_profile,
    _rpc_status,
    _secrets,
    _verified_kubernetes_environment,
    _wait_for_secret_absent,
)
from tests.e2e.vmaas.regression.test_native_csi_driver_install import _controller_api_proxy, _controller_request

pytestmark = pytest.mark.regression
LOG = logging.getLogger(__name__)


def test_unusable_ssh_key_references_are_rejected(
    cli: OsacCLI,
    grpc: GRPCClient,
    k8s_hub_client: K8sClient,
    vm_template: str,
    default_network_attachment: dict[str, object],
    ssh_key_dir: Path,
) -> None:
    """TC-09b: missing, deleted and wrong-type keys cannot create a VM."""
    key_path = ssh_key_dir / "id_ed25519"
    run("ssh-keygen", "-t", "ed25519", "-f", str(key_path), "-N", "", "-C", "osac-e2e")
    public_path = key_path.with_suffix(".pub")
    valid_name = unique_name("e2e-ssh-ref-valid")
    deleted_name = unique_name("e2e-ssh-ref-deleted")
    wrong_type_name = unique_name("e2e-ssh-ref-opaque")
    missing_name = unique_name("e2e-ssh-ref-missing")
    secret_names = [valid_name, deleted_name, wrong_type_name, missing_name]
    vm_names = [unique_name(f"e2e-ssh-ref-{suffix}") for suffix in ("control", "missing", "deleted", "opaque")]
    if any(_secrets(cli, name) for name in secret_names) or any(_instances_named(cli, name) for name in vm_names):
        raise AssertionError("Test resource name already exists")

    owned: dict[str, str] = {}
    uncertain: set[str] = set()
    secret_owned: dict[str, str] = {}
    secret_uncertain: set[str] = set()
    try:
        _create_owned_secret(
            cli,
            secret_owned,
            secret_uncertain,
            name=valid_name,
            secret_type="ssh-public-key",
            field="public_key",
            path=public_path,
        )
        control_id = _create_owned_instance(
            cli,
            owned,
            uncertain,
            template=vm_template,
            name=vm_names[0],
            network_attachments=[default_network_attachment],
            ssh_key=valid_name,
        )
        wait_for_cr(k8s=k8s_hub_client, uuid=control_id)
        control = grpc.get_compute_instance(ci_id=control_id)["object"]
        _assert_existing_instance_id_rejected(grpc, cli, control, owned, uncertain)
        deleted_id = _create_owned_secret(
            cli,
            secret_owned,
            secret_uncertain,
            name=deleted_name,
            secret_type="ssh-public-key",
            field="public_key",
            path=public_path,
        )
        cli.delete_secret(name=deleted_id)
        _wait_for_secret_absent(cli, deleted_name)
        secret_owned.pop(deleted_id)
        _create_owned_secret(
            cli,
            secret_owned,
            secret_uncertain,
            name=wrong_type_name,
            secret_type="opaque",
            field="public_key",
            path=public_path,
        )

        cases = (
            ("missing", missing_name, vm_names[1], "ssh key secret was not found or is not visible"),
            ("deleted", deleted_name, vm_names[2], "ssh key secret was not found or is not visible"),
            ("wrong-type", wrong_type_name, vm_names[3], "expected secret_type_ssh_public_key"),
        )
        for case, key_name, vm_name, expected in cases:
            with pytest.raises(subprocess.CalledProcessError) as error:
                _create_owned_instance(
                    cli,
                    owned,
                    uncertain,
                    template=vm_template,
                    name=vm_name,
                    network_attachments=[default_network_attachment],
                    ssh_key=key_name,
                )
            detail = f"{error.value.stdout or ''} {error.value.stderr or ''}".lower()
            expected_code = "invalidargument" if case == "wrong-type" else "notfound"
            match = re.search(r"rpc error:\s*code\s*=\s*(\w+)\b", detail)
            status = match.group(1) if match else ""
            if expected not in detail or status != expected_code:
                if status not in {
                    "alreadyexists",
                    "notfound",
                    "invalidargument",
                    "permissiondenied",
                    "unauthenticated",
                }:
                    uncertain.add(vm_name)
                raise AssertionError(f"{case} reference was rejected for the wrong reason")
            if _instances_named(cli, vm_name):
                uncertain.add(vm_name)
                raise AssertionError(f"{case} reference left a ComputeInstance in the public API")

            rejected_id = str(uuid4())
            api_name = unique_name("e2e-ssh-rejected-id")
            spec = copy.deepcopy(control["spec"])
            spec["sshKey"] = {"name": key_name}
            observation = f"{api_name} (id={rejected_id})"
            uncertain.add(observation)
            try:
                response = grpc.call(
                    service=f"{PUBLIC_API}.ComputeInstances/Create",
                    data={"object": {"id": rejected_id, "metadata": {"name": api_name}, "spec": spec}},
                )
            except subprocess.CalledProcessError as exc:
                detail = f"{exc.stdout or ''} {exc.stderr or ''}".lower()
                status = _rpc_status(detail)
                if expected not in detail or status != expected_code:
                    if status not in {
                        "alreadyexists",
                        "notfound",
                        "invalidargument",
                        "permissiondenied",
                        "unauthenticated",
                    }:
                        uncertain.add(api_name)
                    raise AssertionError(f"{case} explicit-ID request was rejected for the wrong reason") from exc
            except Exception:
                uncertain.add(api_name)
                raise
            else:
                accepted_id = response.get("object", {}).get("id")
                if accepted_id:
                    owned[api_name] = accepted_id
                    uncertain.remove(observation)
                else:
                    uncertain.add(api_name)
                raise AssertionError(f"{case} explicit-ID request unexpectedly created a VM")
            observation = f"{api_name} (id={rejected_id})"
            uncertain.add(observation)
            if _instances_named(cli, api_name):
                raise AssertionError(f"{case} explicit-ID request left an API object")
            _assert_rejected_instance_cr_absent(k8s_hub_client, rejected_id)
            uncertain.remove(observation)
    finally:
        _cleanup_instances(cli, grpc, k8s_hub_client, owned, uncertain)
        _cleanup_secrets(cli, secret_owned, secret_uncertain)


def test_ssh_key_reference_cannot_be_changed(
    cli: OsacCLI,
    grpc: GRPCClient,
    k8s_hub_client: K8sClient,
    vm_template: str,
    default_network_attachment: dict[str, object],
    ssh_key_dir: Path,
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    """TC-10: structured selection and immutable SSH-key references."""
    key_path = ssh_key_dir / "id_ed25519"
    run("ssh-keygen", "-t", "ed25519", "-f", str(key_path), "-N", "", "-C", "osac-e2e")
    public_path = key_path.with_suffix(".pub")
    names = [unique_name("e2e-ssh-original"), unique_name("e2e-ssh-other")]
    vm_name = unique_name("e2e-ssh-immutable")
    missing_project = unique_name("e2e-ssh-missing-project")
    invalid_selectors = (
        ("name=", "client", "name is empty"),
        (f"project={missing_project}", "client", "name is required"),
        (f"name={names[0]},name={names[0]}", "client", "name appears more than once"),
        (f"name={names[0]},project=a,project=b", "client", "project appears more than once"),
        (f"name={names[0]},region=x", "client", 'unknown key "region"'),
        (f"name={names[0]},", "client", "empty key/value fragment"),
        (f"{names[0]},{names[1]}", "client", "expected a bare name or name=<name>"),
        (f"name{names[0]}", "server", "ssh key secret was not found or is not visible"),
        (f"name={names[0]},project={missing_project}", "server", "ssh key secret was not found or is not visible"),
    )
    invalid_vm_names = [unique_name("e2e-ssh-bad-selector") for _ in invalid_selectors]
    vm_names = [vm_name, *invalid_vm_names]
    if any(_secrets(cli, name) for name in names) or any(_instances_named(cli, name) for name in vm_names):
        raise AssertionError("Test resource name already exists")
    owned: dict[str, str] = {}
    uncertain: set[str] = set()
    secret_owned: dict[str, str] = {}
    secret_uncertain: set[str] = set()
    try:
        for name in names:
            _create_owned_secret(
                cli,
                secret_owned,
                secret_uncertain,
                name=name,
                secret_type="ssh-public-key",
                field="public_key",
                path=public_path,
            )
        original, replacement = (_secrets(cli, name)[0] for name in names)
        instance_id = _create_owned_instance(
            cli,
            owned,
            uncertain,
            template=vm_template,
            name=vm_name,
            network_attachments=[default_network_attachment],
            ssh_key=f"name={names[0]},project=",
        )
        control = grpc.get_compute_instance(ci_id=instance_id)["object"]
        _assert_existing_instance_id_rejected(grpc, cli, control, owned, uncertain)
        ref = control["spec"]["sshKey"]
        assert ref["id"] == original["id"] and ref["name"] == names[0] and not ref.get("project")
        cr_name = wait_for_cr(k8s=k8s_hub_client, uuid=instance_id)
        wait_for_provision(k8s=k8s_hub_client, name=cr_name)

        for (selector, side, expected), invalid_vm_name in zip(invalid_selectors, invalid_vm_names, strict=True):
            with pytest.raises(subprocess.CalledProcessError) as error:
                _create_owned_instance(
                    cli,
                    owned,
                    uncertain,
                    template=vm_template,
                    name=invalid_vm_name,
                    network_attachments=[default_network_attachment],
                    ssh_key=selector,
                )
            detail = f"{error.value.stdout or ''} {error.value.stderr or ''}".lower()
            if expected not in detail:
                raise AssertionError(f"Selector was rejected for an unexpected reason ({side})")
            if side == "client" and ("invalid --ssh-key value" not in detail or "rpc error" in detail):
                raise AssertionError("Malformed selector was not rejected by the CLI")
            if side == "server" and _rpc_status(detail) != "notfound":
                raise AssertionError("Unavailable key selector was not rejected by the API")
            if _instances_named(cli, invalid_vm_name):
                uncertain.add(invalid_vm_name)
                raise AssertionError("Malformed selector left a ComputeInstance in the public API")
            if side == "server":
                rejected_id = str(uuid4())
                api_name = unique_name("e2e-ssh-selector-id")
                if _instances_named(cli, api_name):
                    raise AssertionError("Test VM name already exists")
                spec = copy.deepcopy(control["spec"])
                spec["sshKey"] = (
                    {"name": names[0], "project": missing_project} if "=" in selector else {"name": selector}
                )
                observation = f"{api_name} (id={rejected_id})"
                uncertain.add(observation)
                try:
                    response = grpc.call(
                        service=f"{PUBLIC_API}.ComputeInstances/Create",
                        data={"object": {"id": rejected_id, "metadata": {"name": api_name}, "spec": spec}},
                    )
                except subprocess.CalledProcessError as exc:
                    api_detail = f"{exc.stdout or ''} {exc.stderr or ''}".lower()
                    status = _rpc_status(api_detail)
                    if status != "notfound" or expected not in api_detail:
                        if _definite_create_rejection(api_detail):
                            uncertain.remove(observation)
                        raise AssertionError("Selector API request failed for an unexpected reason") from exc
                else:
                    accepted_id = response.get("object", {}).get("id")
                    if accepted_id:
                        owned[api_name] = accepted_id
                        uncertain.remove(observation)
                    raise AssertionError("Unavailable selector unexpectedly created a VM")
                if _instances_named(cli, api_name):
                    raise AssertionError("Rejected selector request left an API object")
                _assert_rejected_instance_cr_absent(k8s_hub_client, rejected_id)
                uncertain.remove(observation)

        editor = ssh_key_dir / "editor.py"
        editor.write_text(
            f"#!{sys.executable}\n"
            "import json, os, sys\n"
            "from pathlib import Path\n"
            "p = Path(sys.argv[1]); data = json.loads(p.read_text())\n"
            "if os.environ['OSAC_E2E_SSH_EDIT'] == 'clear':\n"
            "    data['spec'].pop('ssh_key', None)\n"
            "else:\n"
            "    data['spec']['ssh_key'] = {'id': os.environ['OSAC_E2E_SSH_ID'], "
            "'name': os.environ['OSAC_E2E_SSH_NAME']}\n"
            "p.write_text(json.dumps(data))\n",
            encoding="utf-8",
        )
        editor.chmod(0o700)
        monkeypatch.setenv("EDITOR", str(editor))
        monkeypatch.setenv("OSAC_E2E_SSH_ID", replacement["id"])
        monkeypatch.setenv("OSAC_E2E_SSH_NAME", names[1])
        for operation in ("repoint", "clear"):
            monkeypatch.setenv("OSAC_E2E_SSH_EDIT", operation)
            with pytest.raises(subprocess.CalledProcessError) as error:
                cli.edit_compute_instance(uuid=instance_id)
            if "ssh_key is immutable" not in (error.value.stderr or "").lower():
                raise AssertionError(f"{operation} was rejected for a reason other than immutability")
            assert grpc.get_compute_instance(ci_id=instance_id)["object"]["spec"]["sshKey"] == ref

        try:
            cli.delete_secret(name=original["id"])
        except subprocess.CalledProcessError as exc:
            detail = f"{exc.stdout or ''} {exc.stderr or ''}".lower()
            if not any(term in detail for term in ("failedprecondition", "in use", "in-use")):
                raise AssertionError("In-use Secret deletion failed for an unexpected reason") from exc
            LOG.info("In-use Secret deletion was rejected")
        else:
            _wait_for_secret_absent(cli, names[0])
            secret_owned.pop(original["id"])
            LOG.info("In-use Secret deletion was allowed")
        assert grpc.get_compute_instance(ci_id=instance_id)["object"]["spec"]["sshKey"] == ref
    finally:
        _cleanup_instances(cli, grpc, k8s_hub_client, owned, uncertain)
        _cleanup_secrets(cli, secret_owned, secret_uncertain)


def test_shared_ssh_key_reference_is_rejected(
    cli: OsacCLI,
    jwt_cli_user: OsacCLI,
    grpc: GRPCClient,
    jwt_grpc_tenant1: GRPCClient,
    k8s_hub_client: K8sClient,
    request: pytest.FixtureRequest,
    vm_template: str,
    default_network_attachment: dict[str, object],
    ssh_key_dir: Path,
) -> None:
    """TC-07c (partial): reject shared key references with an accepted tenant-key control."""
    environment, guest_skip_reason = _optional_guest_ssh_environment(request)
    _assert_tenant_user(jwt_cli_user)
    _require_tenant1_fixture_profile(cli)
    key_path = ssh_key_dir / "id_ed25519"
    run("ssh-keygen", "-t", "ed25519", "-f", str(key_path), "-N", "", "-C", "osac-e2e")
    unrelated_key = ssh_key_dir / "unrelated"
    if environment is not None:
        run("ssh-keygen", "-t", "ed25519", "-f", str(unrelated_key), "-N", "", "-C", "osac-e2e")
    key_name = unique_name("e2e-ssh-shared-key")
    catalog_name = unique_name("e2e-ssh-shared-catalog")
    rejected_name = unique_name("e2e-ssh-shared-rejected")
    control_name = unique_name("e2e-ssh-shared-control")
    if _secrets(jwt_cli_user, key_name) or any(
        _instances_named(jwt_cli_user, name) for name in (rejected_name, control_name)
    ):
        raise AssertionError("Test key or VM name already exists")

    def catalog_items() -> list[dict[str, object]]:
        return grpc.call(
            service=f"{PUBLIC_API}.ComputeInstanceCatalogItems/List",
            data={"filter": f"this.metadata.name == {json.dumps(catalog_name)}"},
        ).get("items", [])

    if catalog_items():
        raise AssertionError("Test catalog name already exists")
    keys: dict[str, str] = {}
    key_uncertain: set[str] = set()
    vms: dict[str, str] = {}
    vm_uncertain: set[str] = set()
    catalog_id: str | None = None
    uncertain_catalog = False
    try:
        key_id = _create_owned_secret(
            jwt_cli_user,
            keys,
            key_uncertain,
            name=key_name,
            secret_type="ssh-public-key",
            field="public_key",
            path=key_path.with_suffix(".pub"),
        )
        fields = {
            "boot_disk": {
                "storage_tier": {"editable": {"default_value": {"name": cli.default_storage_tier}}},
                "size_gib": {"editable": {}},
            },
            "network_attachments": {"editable": {}},
            "disk_image": {"editable": {}},
            "instance_type": {"editable": {}},
            "run_strategy": {"editable": {}},
            "ssh_key": {"editable": {}},
        }
        uncertain_catalog = True
        try:
            catalog_id = grpc.create_compute_instance_catalog_item(
                name=catalog_name, template=vm_template, fields=fields
            )
        except subprocess.CalledProcessError as exc:
            detail = f"{exc.stdout or ''} {exc.stderr or ''}".lower()
            if _definite_create_rejection(detail):
                uncertain_catalog = False
            raise
        uncertain_catalog = False
        attachment = {
            "subnet": {"id": default_network_attachment["subnet"]},
            "security_groups": [{"id": group} for group in default_network_attachment["security_groups"]],
        }
        spec = {
            "catalog_item": {"id": catalog_id},
            "instance_type": {"name": cli.default_instance_type},
            "disk_image": {"name": cli.default_disk_image},
            "boot_disk": {"size_gib": 20, "storage_tier": {"name": cli.default_storage_tier}},
            "network_attachments": [attachment],
            "run_strategy": "Always",
            "ssh_key": {"name": key_name, "shared": True},
        }
        rejected_id = str(uuid4())
        observation = f"{rejected_name} (id={rejected_id})"
        vm_uncertain.add(observation)
        try:
            response = jwt_grpc_tenant1.call(
                service=f"{PUBLIC_API}.ComputeInstances/Create",
                data={"object": {"id": rejected_id, "metadata": {"name": rejected_name}, "spec": spec}},
            )
        except subprocess.CalledProcessError as exc:
            detail = f"{exc.stdout or ''} {exc.stderr or ''}".lower()
            status = _rpc_status(detail)
            if status != "invalidargument" or "ssh key reference must be tenant-scoped" not in detail:
                if status not in {
                    "alreadyexists",
                    "invalidargument",
                    "notfound",
                    "permissiondenied",
                    "unauthenticated",
                }:
                    vm_uncertain.add(rejected_name)
                raise AssertionError("Shared key reference was rejected for an unexpected reason") from exc
        except Exception:
            vm_uncertain.add(rejected_name)
            raise
        else:
            instance_id = response.get("object", {}).get("id")
            if instance_id:
                vms[rejected_name] = instance_id
                vm_uncertain.remove(observation)
            else:
                vm_uncertain.add(rejected_name)
            raise AssertionError("A shared SSH-key reference was unexpectedly accepted")
        observation = f"{rejected_name} (id={rejected_id})"
        vm_uncertain.add(observation)
        if _instances_named(jwt_cli_user, rejected_name):
            raise AssertionError("Rejected shared-key VM persisted")

        spec["ssh_key"] = {"name": key_name, "shared": False}
        vm_uncertain.add(control_name)
        try:
            response = jwt_grpc_tenant1.call(
                service=f"{PUBLIC_API}.ComputeInstances/Create",
                data={"object": {"metadata": {"name": control_name}, "spec": spec}},
            )
        except subprocess.CalledProcessError as exc:
            detail = f"{exc.stdout or ''} {exc.stderr or ''}".lower()
            if _definite_create_rejection(detail):
                vm_uncertain.remove(control_name)
            raise
        except Exception:
            vm_uncertain.add(control_name)
            raise
        control_id = response.get("object", {}).get("id")
        if not control_id:
            vm_uncertain.add(control_name)
            raise AssertionError("Control VM creation returned no ID")
        vms[control_name] = control_id
        vm_uncertain.remove(control_name)
        control = jwt_grpc_tenant1.get_compute_instance(ci_id=control_id)["object"]
        _assert_existing_instance_id_rejected(jwt_grpc_tenant1, jwt_cli_user, control, vms, vm_uncertain)
        _assert_rejected_instance_cr_absent(k8s_hub_client, rejected_id)
        vm_uncertain.remove(observation)
        reference = control["spec"]["sshKey"]
        if reference.get("id") != key_id or reference.get("name") != key_name or reference.get("shared"):
            raise AssertionError("Control VM did not select the tenant-scoped key")
        cr_name = wait_for_cr(k8s=k8s_hub_client, uuid=control_id)
        cr = k8s_hub_client.get_json(resource="computeinstance", name=cr_name)
        if cr.get("spec", {}).get("sshKey") != key_path.with_suffix(".pub").read_text(encoding="utf-8"):
            raise AssertionError("Control VM did not receive the selected public key")
        if environment is None:
            pytest.skip(f"API and CR checks passed; guest check skipped: {guest_skip_reason}")
        wait_for_provision(k8s=k8s_hub_client, name=cr_name)
        wait_for_running(k8s=k8s_hub_client, name=cr_name)
        cr = k8s_hub_client.get_json(resource="computeinstance", name=cr_name)
        _assert_guest_key_access(cr, key_path, unrelated_key, ssh_key_dir, environment)
    finally:
        _cleanup_instances(jwt_cli_user, jwt_grpc_tenant1, k8s_hub_client, vms, vm_uncertain)
        errors: list[Exception] = []
        if uncertain_catalog:
            errors.append(RuntimeError(f"Catalog creation outcome uncertain for {catalog_name}"))
        if catalog_id:
            try:
                grpc.delete_compute_instance_catalog_item(catalog_item_id=catalog_id)
                poll_until(
                    fn=lambda: all(item.get("id") != catalog_id for item in catalog_items()),
                    until=bool,
                    retries=30,
                    delay=2,
                    description=f"Catalog {catalog_name} removal",
                    resource_kind=WaitResourceKind.COMPUTE_INSTANCE_CATALOG_ITEM,
                    resource_id=catalog_id,
                )
            except Exception as exc:
                errors.append(exc)
        try:
            _cleanup_secrets(jwt_cli_user, keys, key_uncertain)
        except Exception as exc:
            errors.append(exc)
        if errors:
            raise ExceptionGroup("Could not clean all shared-reference test resources", errors)


def test_linux_vm_without_registered_key(
    cli: OsacCLI,
    jwt_cli_user: OsacCLI,
    grpc: GRPCClient,
    jwt_grpc_tenant1: GRPCClient,
    k8s_hub_client: K8sClient,
    request: pytest.FixtureRequest,
    vm_template: str,
    default_network_attachment: dict[str, object],
) -> None:
    """TC-11 (partial): keyless Linux VM has no registered-key injection resources."""
    if not os.environ.get("OSAC_VM_KUBECONFIG"):
        pytest.skip("VM-cluster inspection requires OSAC_VM_KUBECONFIG")
    k8s_virt_client = request.getfixturevalue("k8s_virt_client")
    _verified_kubernetes_environment(k8s_virt_client)
    _assert_tenant_user(jwt_cli_user)
    _require_tenant1_fixture_profile(cli)
    name = unique_name("e2e-ssh-keyless")
    if _instances_named(jwt_cli_user, name):
        raise AssertionError("Test VM name already exists")
    owned: dict[str, str] = {}
    uncertain: set[str] = set()
    try:
        instance_id = _create_owned_instance(
            jwt_cli_user,
            owned,
            uncertain,
            template=vm_template,
            name=name,
            network_attachments=[default_network_attachment],
            ssh_key=None,
            instance_type=cli.default_instance_type,
            disk_image=cli.default_disk_image,
            boot_disk_storage_tier=cli.default_storage_tier,
        )
        instance = jwt_grpc_tenant1.get_compute_instance(ci_id=instance_id)["object"]
        assert not instance.get("spec", {}).get("sshKey")
        cr_name = wait_for_cr(k8s=k8s_hub_client, uuid=instance_id)
        wait_for_provision(k8s=k8s_hub_client, name=cr_name)
        wait_for_running(k8s=k8s_hub_client, name=cr_name)
        cr = k8s_hub_client.get_json(resource="computeinstance", name=cr_name)
        assert not cr.get("spec", {}).get("sshKey")
        vm_ref = cr["status"]["virtualMachineReference"]
        namespace = vm_ref["namespace"]
        vm_name = vm_ref.get("name") or cr_name
        vm = k8s_virt_client.get_json(resource="virtualmachine", name=vm_name, namespace=namespace)
        assert not vm.get("spec", {}).get("template", {}).get("spec", {}).get("accessCredentials")
        injection_name = f"{cr_name}-ssh-public-key"
        output, _ = k8s_virt_client._get(
            "get", "secret", injection_name, "-n", namespace, "--ignore-not-found", "-o", "name"
        )
        assert not output.strip(), "Keyless VM unexpectedly has an SSH-key injection Secret"
    finally:
        _cleanup_instances(jwt_cli_user, jwt_grpc_tenant1, k8s_hub_client, owned, uncertain)


def test_windows_image_rejects_ssh_key(
    cli: OsacCLI,
    jwt_cli_user: OsacCLI,
    grpc: GRPCClient,
    jwt_grpc_tenant1: GRPCClient,
    k8s_hub_client: K8sClient,
    vm_template: str,
    default_network_attachment: dict[str, object],
    ssh_key_dir: Path,
) -> None:
    """TC-11 (partial): a Windows catalog image rejects registered SSH keys."""
    _assert_tenant_user(jwt_cli_user)
    _require_tenant1_fixture_profile(cli)
    key_path = ssh_key_dir / "id_ed25519"
    run("ssh-keygen", "-t", "ed25519", "-f", str(key_path), "-N", "", "-C", "osac-e2e")
    key_name = unique_name("e2e-ssh-win-key")
    image_name = unique_name("e2e-ssh-win-image")
    catalog_name = unique_name("e2e-ssh-win-catalog")
    control_name = unique_name("e2e-ssh-win-control")
    rejected_name = unique_name("e2e-ssh-win-rejected")
    if _secrets(jwt_cli_user, key_name) or any(
        _instances_named(jwt_cli_user, name) for name in (control_name, rejected_name)
    ):
        raise AssertionError("Test resource name already exists")
    if grpc.list_disk_image_ids(filter_expr=f"this.metadata.name == {json.dumps(image_name)}"):
        raise AssertionError("Test DiskImage name already exists")
    if grpc.call(
        service=f"{PUBLIC_API}.ComputeInstanceCatalogItems/List",
        data={"filter": f"this.metadata.name == {json.dumps(catalog_name)}"},
    ).get("items"):
        raise AssertionError("Test catalog name already exists")

    vm_owned: dict[str, str] = {}
    vm_uncertain: set[str] = set()
    secret_owned: dict[str, str] = {}
    secret_uncertain: set[str] = set()
    image_id: str | None = None
    catalog_id: str | None = None
    uncertain_fixture: str | None = None
    try:
        _create_owned_secret(
            jwt_cli_user,
            secret_owned,
            secret_uncertain,
            name=key_name,
            secret_type="ssh-public-key",
            field="public_key",
            path=key_path.with_suffix(".pub"),
        )
        uncertain_fixture = f"DiskImage {image_name}"
        image_id = grpc.create_disk_image(
            name=image_name, source_ref=DEFAULT_DISK_IMAGE_SOURCE_REF, guest_os_family="GUEST_OS_FAMILY_WINDOWS"
        )
        uncertain_fixture = None
        image = grpc.get_disk_image(disk_image_id=image_id)["object"]
        assert image["spec"]["guestOsFamily"] == "GUEST_OS_FAMILY_WINDOWS"
        fields = {
            "boot_disk": {
                "storage_tier": {"editable": {"default_value": {"name": cli.default_storage_tier}}},
                "size_gib": {"editable": {}},
            },
            "network_attachments": {"editable": {}},
            "disk_image": {"locked": {"name": image_name}},
            "instance_type": {"editable": {}},
            "run_strategy": {"editable": {}},
        }
        uncertain_fixture = f"catalog {catalog_name}"
        catalog_id = grpc.create_compute_instance_catalog_item(name=catalog_name, template=vm_template, fields=fields)
        uncertain_fixture = None
        catalog = grpc.get_compute_instance_catalog_item(catalog_item_id=catalog_id)["object"]
        assert catalog["fields"]["diskImage"]["locked"]["id"] == image_id
        control_id = _create_owned_instance(
            jwt_cli_user,
            vm_owned,
            vm_uncertain,
            template=vm_template,
            name=control_name,
            network_attachments=[default_network_attachment],
            ssh_key=key_name,
            instance_type=cli.default_instance_type,
            disk_image=cli.default_disk_image,
            boot_disk_storage_tier=cli.default_storage_tier,
        )
        wait_for_cr(k8s=k8s_hub_client, uuid=control_id)
        control = jwt_grpc_tenant1.get_compute_instance(ci_id=control_id)["object"]
        _assert_existing_instance_id_rejected(jwt_grpc_tenant1, jwt_cli_user, control, vm_owned, vm_uncertain)
        attachment = {
            "subnet": {"id": default_network_attachment["subnet"]},
            "security_groups": [{"id": group} for group in default_network_attachment["security_groups"]],
        }
        rejected_id = str(uuid4())
        observation = f"{rejected_name} (id={rejected_id})"
        vm_uncertain.add(observation)
        try:
            response = jwt_grpc_tenant1.call(
                service=f"{PUBLIC_API}.ComputeInstances/Create",
                data={
                    "object": {
                        "id": rejected_id,
                        "metadata": {"name": rejected_name},
                        "spec": {
                            "catalog_item": {"id": catalog_id},
                            "instance_type": {"name": cli.default_instance_type},
                            "boot_disk": {"size_gib": 20, "storage_tier": {"name": cli.default_storage_tier}},
                            "network_attachments": [attachment],
                            "run_strategy": "Always",
                            "ssh_key": {"name": key_name},
                        },
                    }
                },
            )
        except subprocess.CalledProcessError as exc:
            detail = f"{exc.stdout or ''} {exc.stderr or ''}".lower()
            status = _rpc_status(detail)
            if status != "invalidargument" or "ssh key injection is not supported for windows instances" not in detail:
                if status not in {
                    "alreadyexists",
                    "invalidargument",
                    "notfound",
                    "permissiondenied",
                    "unauthenticated",
                }:
                    vm_uncertain.add(rejected_name)
                raise AssertionError("Windows SSH-key request was rejected for an unexpected reason") from exc
        except Exception:
            vm_uncertain.add(rejected_name)
            raise
        else:
            vm_id = response.get("object", {}).get("id")
            if vm_id:
                vm_owned[rejected_name] = vm_id
                vm_uncertain.remove(observation)
            else:
                vm_uncertain.add(rejected_name)
            raise AssertionError("Windows image accepted an SSH-key reference")
        observation = f"{rejected_name} (id={rejected_id})"
        vm_uncertain.add(observation)
        if _instances_named(jwt_cli_user, rejected_name):
            raise AssertionError("Rejected Windows VM persisted")
        _assert_rejected_instance_cr_absent(k8s_hub_client, rejected_id)
        vm_uncertain.remove(observation)
    finally:
        _cleanup_instances(jwt_cli_user, jwt_grpc_tenant1, k8s_hub_client, vm_owned, vm_uncertain)
        errors: list[Exception] = []
        image_safe = not (uncertain_fixture and uncertain_fixture.startswith("catalog "))
        if uncertain_fixture:
            errors.append(RuntimeError(f"Creation outcome uncertain for {uncertain_fixture}; inspect ownership"))
        if catalog_id:
            try:
                grpc.delete_compute_instance_catalog_item(catalog_item_id=catalog_id)
                poll_until(
                    fn=lambda: all(
                        item.get("id") != catalog_id
                        for item in grpc.call(
                            service=f"{PUBLIC_API}.ComputeInstanceCatalogItems/List",
                            data={"filter": f"this.metadata.name == {json.dumps(catalog_name)}"},
                        ).get("items", [])
                    ),
                    until=bool,
                    retries=30,
                    delay=2,
                    description=f"Catalog {catalog_name} removal",
                    resource_kind=WaitResourceKind.COMPUTE_INSTANCE_CATALOG_ITEM,
                    resource_id=catalog_id,
                )
            except Exception as exc:
                image_safe = False
                errors.append(exc)
        if image_id and image_safe:
            try:
                grpc.delete_disk_image(disk_image_id=image_id)
                poll_until(
                    fn=lambda: (
                        image_id
                        not in grpc.list_disk_image_ids(filter_expr=f"this.metadata.name == {json.dumps(image_name)}")
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
        try:
            _cleanup_secrets(jwt_cli_user, secret_owned, secret_uncertain)
        except Exception as exc:
            errors.append(exc)
        if errors:
            raise ExceptionGroup("Could not clean up all Windows test fixtures", errors)


def test_key_update_observation(
    cli: OsacCLI,
    grpc: GRPCClient,
    k8s_hub_client: K8sClient,
    request: pytest.FixtureRequest,
    vm_template: str,
    default_network_attachment: dict[str, object],
    ssh_key_dir: Path,
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    """TC-13 (partial): record updates and verify old/new VM CR key projections."""
    environment, guest_skip_reason = _optional_guest_ssh_environment(request)
    paths = [ssh_key_dir / f"key{index}" for index in range(3)]
    for path in paths:
        run("ssh-keygen", "-t", "ed25519", "-f", str(path), "-N", "", "-C", "osac-e2e")
    public_paths = [path.with_suffix(".pub") for path in paths]
    keys = [path.read_text(encoding="utf-8") for path in public_paths]
    name = unique_name("e2e-ssh-update")
    vm_names = [unique_name("e2e-ssh-update-vm") for _ in range(2)]
    if _secrets(cli, name) or any(_instances_named(cli, vm_name) for vm_name in vm_names):
        raise AssertionError("Test resource name already exists")
    key_owned: dict[str, str] = {}
    key_uncertain: set[str] = set()
    vm_owned: dict[str, str] = {}
    vm_uncertain: set[str] = set()
    try:
        key_id = _create_owned_secret(
            cli,
            key_owned,
            key_uncertain,
            name=name,
            secret_type="ssh-public-key",
            field="public_key",
            path=public_paths[0],
        )
        editor = ssh_key_dir / "editor.py"
        editor.write_text(
            f"#!{sys.executable}\n"
            "import base64, json, os, sys\n"
            "from pathlib import Path\n"
            "p = Path(sys.argv[1]); data = json.loads(p.read_text())\n"
            "data['data']['public_key'] = base64.b64encode("
            "Path(os.environ['OSAC_E2E_NEW_SSH_KEY']).read_bytes()).decode()\n"
            "p.write_text(json.dumps(data))\n",
            encoding="utf-8",
        )
        editor.chmod(0o700)
        monkeypatch.setenv("EDITOR", str(editor))

        def update(previous: str, index: int) -> str:
            monkeypatch.setenv("OSAC_E2E_NEW_SSH_KEY", str(public_paths[index]))
            try:
                cli._run("edit", "--output", "json", "secret", key_id)
            except subprocess.CalledProcessError as exc:
                detail = f"{exc.stdout or ''} {exc.stderr or ''}".lower()
                status = _rpc_status(detail)
                unavailable = status == "unimplemented"
                policy_rejection = (
                    status in {"invalidargument", "failedprecondition"}
                    and any(term in detail for term in ("ssh", "secret", "data"))
                    and any(
                        term in detail
                        for term in (
                            "immutable",
                            "not supported",
                            "unsupported",
                            "cannot update",
                            "cannot be updated",
                            "read-only",
                        )
                    )
                )
                if not unavailable and not policy_rejection:
                    raise AssertionError("Secret update failed for an unexpected reason") from exc
                current = previous
                LOG.info("SSH-key update operation unavailable" if unavailable else "SSH-key update request rejected")
            else:
                unavailable = False
                current = keys[index]
                LOG.info("SSH-key update request accepted")
            found = _secrets(cli, key_id)
            if len(found) != 1 or found[0].get("id") != key_id:
                raise AssertionError("SSH-key update changed the Secret identity")
            stored = base64.b64decode(found[0].get("data", {}).get("public_key", ""), validate=True).decode()
            if stored != current:
                raise AssertionError("Secret data does not match the recorded update outcome")
            if unavailable:
                pytest.skip("Secret update operation is unavailable; update behavior cannot be observed")
            return current

        first_key = update(keys[0], 1)
        first_id = _create_owned_instance(
            cli,
            vm_owned,
            vm_uncertain,
            template=vm_template,
            name=vm_names[0],
            network_attachments=[default_network_attachment],
            ssh_key=name,
        )
        first_cr_name = wait_for_cr(k8s=k8s_hub_client, uuid=first_id)
        wait_for_provision(k8s=k8s_hub_client, name=first_cr_name)
        first_cr = k8s_hub_client.get_json(resource="computeinstance", name=first_cr_name)
        if first_cr["spec"]["sshKey"] != first_key:
            raise AssertionError("First VM did not receive the current registered key")
        if environment is not None:
            wait_for_running(k8s=k8s_hub_client, name=first_cr_name)
            first_cr = k8s_hub_client.get_json(resource="computeinstance", name=first_cr_name)
            first_index = keys.index(first_key)
            _assert_guest_key_access(
                first_cr, paths[first_index], paths[(first_index + 1) % 3], ssh_key_dir, environment
            )
        second_key = update(first_key, 2)
        current_cr = k8s_hub_client.get_json(resource="computeinstance", name=first_cr_name)
        if current_cr["spec"]["sshKey"] != first_key:
            raise AssertionError("Existing VM key changed after the Secret update")
        second_id = _create_owned_instance(
            cli,
            vm_owned,
            vm_uncertain,
            template=vm_template,
            name=vm_names[1],
            network_attachments=[default_network_attachment],
            ssh_key=name,
        )
        second_cr_name = wait_for_cr(k8s=k8s_hub_client, uuid=second_id)
        wait_for_provision(k8s=k8s_hub_client, name=second_cr_name)
        second_cr = k8s_hub_client.get_json(resource="computeinstance", name=second_cr_name)
        if second_cr["spec"]["sshKey"] != second_key:
            raise AssertionError("New VM did not receive the current registered key")
        if k8s_hub_client.get_json(resource="computeinstance", name=first_cr_name)["spec"]["sshKey"] != first_key:
            raise AssertionError("Existing VM key changed after the new VM was created")
        if environment is None:
            pytest.skip(f"API and CR checks passed; guest check skipped: {guest_skip_reason}")
        wait_for_running(k8s=k8s_hub_client, name=second_cr_name)
        first_cr = k8s_hub_client.get_json(resource="computeinstance", name=first_cr_name)
        second_cr = k8s_hub_client.get_json(resource="computeinstance", name=second_cr_name)
        first_index, second_index = keys.index(first_key), keys.index(second_key)
        first_other = second_index if first_index != second_index else (first_index + 1) % 3
        second_other = first_index if first_index != second_index else (second_index + 1) % 3
        _assert_guest_key_access(first_cr, paths[first_index], paths[first_other], ssh_key_dir, environment)
        _assert_guest_key_access(second_cr, paths[second_index], paths[second_other], ssh_key_dir, environment)
    finally:
        _cleanup_instances(cli, grpc, k8s_hub_client, vm_owned, vm_uncertain)
        _cleanup_secrets(cli, key_owned, key_uncertain)


def test_catalog_ssh_key_policy_observation(
    cli: OsacCLI,
    jwt_cli_user: OsacCLI,
    grpc: GRPCClient,
    jwt_grpc_tenant1: GRPCClient,
    jwt_grpc_tenant1_admin: GRPCClient,
    private_grpc: GRPCClient,
    k8s_hub_client: K8sClient,
    request: pytest.FixtureRequest,
    vm_template: str,
    ssh_key_dir: Path,
) -> None:
    """TC-06 (partial): observe default, override, locked and absent key policies."""
    environment, guest_skip_reason = _optional_guest_ssh_environment(request)
    _assert_tenant_user(jwt_cli_user)
    _require_tenant1_fixture_profile(cli)
    paths = [ssh_key_dir / "default", ssh_key_dir / "override"]
    for path in paths:
        run("ssh-keygen", "-t", "ed25519", "-f", str(path), "-N", "", "-C", "osac-e2e")
    key_names = [unique_name("e2e-ssh-cat-default"), unique_name("e2e-ssh-cat-override")]
    catalog_names = [unique_name(f"e2e-ssh-cat-{kind}") for kind in ("editable", "locked", "none")]
    vm_names = [unique_name("e2e-ssh-cat-vm") for _ in range(4)]
    project = unique_name("e2e-ssh-cat-project")
    owner = unique_name("e2e-ssh-cat-owner")
    image_name = unique_name("e2e-ssh-cat-image")
    if _project_ids_named(jwt_grpc_tenant1_admin, project):
        raise AssertionError("Test project name already exists")
    for name in catalog_names:
        existing = jwt_grpc_tenant1_admin.call(
            service=f"{PUBLIC_API}.ComputeInstanceCatalogItems/List",
            data={"filter": f"this.metadata.name == {json.dumps(name)}"},
        )
        if existing.get("items"):
            raise AssertionError("Test catalog name already exists")
    key_owned: dict[str, str] = {}
    key_uncertain: set[str] = set()
    vm_owned: dict[str, str] = {}
    vm_uncertain: set[str] = set()
    catalogs: dict[str, str] = {}
    uncertain_catalog: str | None = None
    uncertain_project = False
    project_id: str | None = None
    membership_id: str | None = None
    membership_name = unique_name("e2e-ssh-cat-manager")
    uncertain_membership = False
    image_id: str | None = None
    uncertain_image = False
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
        uncertain_image = True
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
        uncertain_image = False
        for name, path in zip(key_names, paths, strict=True):
            _create_project_secret(
                jwt_cli_user, key_owned, key_uncertain, project=project, name=name, public_path=path.with_suffix(".pub")
            )
        key_ids = list(key_owned)
        fields = {
            "boot_disk": {
                "storage_tier": {"editable": {"default_value": {"name": cli.default_storage_tier}}},
                "size_gib": {"editable": {}},
            },
            "network_attachments": {"editable": {}},
            "disk_image": {"editable": {}},
            "instance_type": {"editable": {}},
            "run_strategy": {"editable": {}},
        }
        policies = (
            {"editable": {"default_value": {"name": key_names[0], "project": project}}},
            {"locked": {"name": key_names[0], "project": project}},
            None,
        )
        for catalog_name, policy in zip(catalog_names, policies, strict=True):
            catalog_fields = dict(fields)
            if policy:
                catalog_fields["ssh_key"] = policy
            uncertain_catalog = catalog_name
            try:
                catalog_id = jwt_grpc_tenant1_admin.create_compute_instance_catalog_item(
                    name=catalog_name, template=vm_template, fields=catalog_fields
                )
            except subprocess.CalledProcessError as exc:
                detail = f"{exc.stdout or ''} {exc.stderr or ''}".lower()
                if _definite_create_rejection(detail):
                    uncertain_catalog = None
                raise
            catalogs[catalog_id] = catalog_name
            uncertain_catalog = None
        catalog_ids = list(catalogs)
        editable = jwt_grpc_tenant1_admin.get_compute_instance_catalog_item(catalog_item_id=catalog_ids[0])["object"]
        locked = jwt_grpc_tenant1_admin.get_compute_instance_catalog_item(catalog_item_id=catalog_ids[1])["object"]
        editable_ref = editable["fields"]["sshKey"]["editable"]["defaultValue"]
        locked_ref = locked["fields"]["sshKey"]["locked"]
        for ref in (editable_ref, locked_ref):
            if ref.get("id") != key_ids[0] or ref.get("name") != key_names[0] or ref.get("project") != project:
                raise AssertionError("Catalog policy did not resolve the intended project key")
        cases = (
            (catalog_ids[0], None, key_ids[0], False),
            (catalog_ids[0], key_names[1], key_ids[1], False),
            (catalog_ids[1], key_names[1], None, True),
            (catalog_ids[2], None, None, False),
        )
        for vm_name, (catalog_id, selector, expected_id, rejected) in zip(vm_names, cases, strict=True):
            requested_id = str(uuid4())
            spec = {
                "catalog_item": {"id": catalog_id},
                "instance_type": {"name": cli.default_instance_type},
                "disk_image": {"name": image_name},
                "boot_disk": {"size_gib": 20, "storage_tier": {"name": cli.default_storage_tier}},
                "run_strategy": "Always",
            }
            if selector:
                spec["ssh_key"] = {"name": selector, "project": project}
            observation = f"{vm_name} (id={requested_id})"
            vm_uncertain.add(observation)
            try:
                response = jwt_grpc_tenant1.call(
                    service=f"{PUBLIC_API}.ComputeInstances/Create",
                    data={
                        "object": {"id": requested_id, "metadata": {"name": vm_name, "project": project}, "spec": spec}
                    },
                )
            except subprocess.CalledProcessError as exc:
                detail = f"{exc.stdout or ''} {exc.stderr or ''}".lower()
                status = _rpc_status(detail)
                if not rejected or status != "invalidargument" or "ssh_key: field is not editable" not in detail:
                    if status not in {
                        "alreadyexists",
                        "invalidargument",
                        "notfound",
                        "permissiondenied",
                        "unauthenticated",
                    }:
                        vm_uncertain.add(vm_name)
                    raise AssertionError("Catalog request failed for an unexpected reason") from exc
                observation = f"{vm_name} (id={requested_id})"
                vm_uncertain.add(observation)
                if _instances_named(jwt_cli_user, vm_name, project=project):
                    raise AssertionError("Rejected catalog override persisted a VM") from exc
                _assert_rejected_instance_cr_absent(k8s_hub_client, requested_id)
                vm_uncertain.remove(observation)
                current = jwt_grpc_tenant1_admin.get_compute_instance_catalog_item(catalog_item_id=catalog_ids[1])[
                    "object"
                ]
                if current["fields"]["sshKey"]["locked"] != locked_ref:
                    raise AssertionError("Rejected override changed the locked catalog key") from exc
                continue
            except Exception:
                vm_uncertain.add(vm_name)
                raise
            instance_id = response.get("object", {}).get("id")
            if not instance_id:
                vm_uncertain.add(vm_name)
                raise AssertionError("Catalog VM create returned no ID")
            vm_owned[vm_name] = instance_id
            vm_uncertain.remove(observation)
            if instance_id != requested_id:
                raise AssertionError("Catalog VM did not retain its supplied test ID")
            if rejected:
                raise AssertionError("Locked catalog accepted a key override")
            instance = jwt_grpc_tenant1.get_compute_instance(ci_id=instance_id)["object"]
            reference = instance.get("spec", {}).get("sshKey") or {}
            if expected_id is None:
                if reference:
                    raise AssertionError("Keyless catalog unexpectedly selected a key")
            elif reference.get("id") != expected_id or reference.get("project") != project:
                raise AssertionError("Catalog selected an unexpected key")
            cr_name = wait_for_cr(k8s=k8s_hub_client, uuid=instance_id)
            cr = k8s_hub_client.get_json(resource="computeinstance", name=cr_name)
            expected_key = (
                paths[key_ids.index(expected_id)].with_suffix(".pub").read_text(encoding="utf-8") if expected_id else ""
            )
            if (cr.get("spec", {}).get("sshKey") or "") != expected_key:
                raise AssertionError("Catalog key was not projected correctly to the VM CR")
            if environment is not None and expected_id is not None:
                wait_for_provision(k8s=k8s_hub_client, name=cr_name)
                wait_for_running(k8s=k8s_hub_client, name=cr_name)
                cr = k8s_hub_client.get_json(resource="computeinstance", name=cr_name)
                key_index = key_ids.index(expected_id)
                _assert_guest_key_access(cr, paths[key_index], paths[1 - key_index], ssh_key_dir, environment)
            _cleanup_instances(jwt_cli_user, jwt_grpc_tenant1, k8s_hub_client, vm_owned, vm_uncertain, project=project)
            vm_owned.clear()
        if environment is None:
            pytest.skip(f"API and CR checks passed; guest check skipped: {guest_skip_reason}")
    except subprocess.CalledProcessError as exc:
        detail = f"{exc.stdout or ''} {exc.stderr or ''}".lower()
        if uncertain_image and _definite_create_rejection(detail):
            uncertain_image = False
        raise
    finally:
        _cleanup_instances(jwt_cli_user, jwt_grpc_tenant1, k8s_hub_client, vm_owned, vm_uncertain, project=project)
        errors: list[Exception] = []
        if uncertain_catalog:
            errors.append(RuntimeError(f"Catalog creation outcome uncertain for {uncertain_catalog}"))
        for catalog_id, catalog_name in catalogs.items():
            try:
                jwt_grpc_tenant1_admin.delete_compute_instance_catalog_item(catalog_item_id=catalog_id)
                poll_until(
                    fn=lambda catalog_id=catalog_id, catalog_name=catalog_name: all(
                        item.get("id") != catalog_id
                        for item in jwt_grpc_tenant1_admin.call(
                            service=f"{PUBLIC_API}.ComputeInstanceCatalogItems/List",
                            data={"filter": f"this.metadata.name == {json.dumps(catalog_name)}"},
                        ).get("items", [])
                    ),
                    until=bool,
                    retries=30,
                    delay=2,
                    description=f"Catalog {catalog_name} removal",
                    resource_kind=WaitResourceKind.COMPUTE_INSTANCE_CATALOG_ITEM,
                    resource_id=catalog_id,
                )
            except Exception as exc:
                errors.append(exc)
        if errors:
            raise ExceptionGroup("Could not clean up catalogs; preserving registered keys", errors)
        for secret_id in key_owned:
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
        if key_uncertain:
            errors.append(RuntimeError(f"Secret creation outcome uncertain for {', '.join(sorted(key_uncertain))}"))
        if uncertain_image:
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
            raise ExceptionGroup("Could not clean catalog fixtures; preserving project", errors)
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


def test_deployed_logs_hide_ssh_key_material(
    cli: OsacCLI,
    jwt_cli_user: OsacCLI,
    jwt_grpc_tenant1: GRPCClient,
    k8s_hub_client: K8sClient,
    request: pytest.FixtureRequest,
    vm_template: str,
    default_network_attachment: dict[str, object],
    ssh_key_dir: Path,
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    """NFR-SSH-01: scan CLI, service and provision-job output for test key material."""
    if not os.environ.get("OSAC_VM_KUBECONFIG"):
        pytest.skip("VM-cluster inspection requires OSAC_VM_KUBECONFIG")
    k8s_virt_client = request.getfixturevalue("k8s_virt_client")
    _verified_kubernetes_environment(k8s_virt_client)
    __tracebackhide__ = True
    _verified_kubernetes_environment(k8s_hub_client)
    _assert_tenant_user(jwt_cli_user)
    _require_tenant1_fixture_profile(cli)
    started = (datetime.now(UTC) - timedelta(minutes=1)).strftime("%Y-%m-%dT%H:%M:%SZ")
    key_path = ssh_key_dir / "id_ed25519"
    run("ssh-keygen", "-t", "ed25519", "-f", str(key_path), "-N", "", "-C", "osac-e2e")
    marker = unique_name("e2e-ssh-log-marker").encode()
    payload = b"Synthetic private-key-like test text\n" + marker + b"\n"
    payload_path = ssh_key_dir / "invalid.pub"
    payload_path.write_bytes(payload)
    needles = _hygiene_needles(key_path.read_bytes(), payload, marker)
    canary = b"\n".join(value for values in needles.values() for value in values)
    if _hygiene_scan(canary, needles) != set(needles):
        raise AssertionError("The hygiene scanner did not identify every synthetic canary category")
    key_name = unique_name("e2e-ssh-log-key")
    invalid_name = unique_name("e2e-ssh-log-invalid")
    vm_name = unique_name("e2e-ssh-log-vm")
    rejected_name = unique_name("e2e-ssh-log-rejected")
    missing_name = unique_name("e2e-ssh-log-missing")
    if any(_secrets(jwt_cli_user, name) for name in (key_name, invalid_name)) or any(
        _instances_named(jwt_cli_user, name) for name in (vm_name, rejected_name)
    ):
        raise AssertionError("Test resource name already exists")
    key_owned: dict[str, str] = {}
    key_uncertain: set[str] = set()
    vm_owned: dict[str, str] = {}
    vm_uncertain: set[str] = set()
    captures: list[tuple[str, str]] = []

    def capture_cli(*args: str, timeout: int = 300) -> str:
        __tracebackhide__ = True
        command = (jwt_cli_user.binary, "--config", jwt_cli_user.config_dir, *args)
        result = subprocess.run(command, capture_output=True, text=True, timeout=timeout, check=False)
        captures.append(("CLI " + " ".join(args[:2]), result.stdout + "\n" + result.stderr))
        if result.returncode:
            raise CommandFailed(result.returncode, command, output=result.stdout, stderr=result.stderr)
        return result.stdout.strip()

    original_run = jwt_cli_user._run
    monkeypatch.setattr(jwt_cli_user, "_run", capture_cli)
    try:
        try:
            key_id = _create_owned_secret(
                jwt_cli_user,
                key_owned,
                key_uncertain,
                name=key_name,
                secret_type="ssh-public-key",
                field="public_key",
                path=key_path.with_suffix(".pub"),
            )
            for args in (
                ("get", "secret", key_id, "-o", "json"),
                ("get", "secret", key_id, "-o", "yaml"),
                ("get", "secrets", "-o", "json"),
                ("get", "secrets"),
            ):
                jwt_cli_user._run(*args)
        except Exception:
            raise AssertionError("Could not create or inspect the hygiene test key; output withheld") from None
        try:
            _create_owned_secret(
                jwt_cli_user,
                key_owned,
                key_uncertain,
                name=invalid_name,
                secret_type="ssh-public-key",
                field="public_key",
                path=payload_path,
            )
        except subprocess.CalledProcessError as exc:
            if "invalidargument" not in f"{exc.stdout or ''} {exc.stderr or ''}".lower():
                raise AssertionError("Synthetic invalid payload was rejected for an unexpected reason") from None
        else:
            raise AssertionError("Synthetic invalid payload was unexpectedly accepted")
        if _secrets(jwt_cli_user, invalid_name):
            key_uncertain.add(invalid_name)
            raise AssertionError("Rejected invalid payload persisted a Secret")
        try:
            _create_owned_instance(
                jwt_cli_user,
                vm_owned,
                vm_uncertain,
                template=vm_template,
                name=rejected_name,
                network_attachments=[default_network_attachment],
                ssh_key=missing_name,
                instance_type=cli.default_instance_type,
                disk_image=cli.default_disk_image,
                boot_disk_storage_tier=cli.default_storage_tier,
            )
        except subprocess.CalledProcessError as exc:
            if "notfound" not in f"{exc.stdout or ''} {exc.stderr or ''}".lower():
                raise AssertionError("Missing SSH key was rejected for an unexpected reason") from None
        else:
            raise AssertionError("VM with a missing SSH key was unexpectedly accepted")
        if _instances_named(jwt_cli_user, rejected_name):
            vm_uncertain.add(rejected_name)
            raise AssertionError("Rejected missing-key VM persisted")
        instance_id = _create_owned_instance(
            jwt_cli_user,
            vm_owned,
            vm_uncertain,
            template=vm_template,
            name=vm_name,
            network_attachments=[default_network_attachment],
            ssh_key=key_name,
            instance_type=cli.default_instance_type,
            disk_image=cli.default_disk_image,
            boot_disk_storage_tier=cli.default_storage_tier,
        )
        cr_name = wait_for_cr(k8s=k8s_hub_client, uuid=instance_id)
        wait_for_provision(k8s=k8s_hub_client, name=cr_name)
        wait_for_running(k8s=k8s_hub_client, name=cr_name)
        jwt_cli_user._run("get", "computeinstance", instance_id, "-o", "json")
        cr = k8s_hub_client.get_json(resource="computeinstance", name=cr_name)
        _assert_vm_key_injected(cr, k8s_virt_client, key_path.with_suffix(".pub").read_text(encoding="utf-8"))
        vm_namespace = cr["status"]["virtualMachineReference"]["namespace"]

        def finished_provision_jobs() -> list[str]:
            latest = k8s_hub_client.get_json(resource="computeinstance", name=cr_name)
            jobs = [job for job in latest.get("status", {}).get("jobs", []) if job.get("task") == "provision"]
            terminal = {"Succeeded", "Failed", "Canceled", "Cancelled"}
            if any(job.get("attempt") != 1 for job in jobs):
                raise RuntimeError(
                    "Provision attempt count is missing or greater than one; the AAP log scan is incomplete"
                )
            if not jobs or any(not job.get("jobID") or job.get("state") not in terminal for job in jobs):
                return []
            if not any(job.get("state") == "Succeeded" for job in jobs):
                raise AssertionError("No successful AAP provision job was recorded")
            return [str(job["jobID"]) for job in jobs]

        job_ids = poll_until(
            fn=finished_provision_jobs,
            until=bool,
            retries=120,
            delay=5,
            description=f"completed AAP provision jobs for {cr_name}",
            resource_kind=WaitResourceKind.COMPUTE_INSTANCE,
            resource_id=instance_id,
        )

        for label, content in captures:
            found = _hygiene_scan(content.encode(), needles)
            if found:
                raise AssertionError(f"Sensitive test material appeared in {label}: {', '.join(sorted(found))}")
        core_sources = {"fulfillment-grpc-server": False, "osac-operator": False}
        unreadable = 0
        seen: set[tuple[str | None, str]] = set()
        for client, namespace in (
            (k8s_hub_client, k8s_hub_client.namespace),
            (k8s_hub_client, "osac-infra"),
            (k8s_virt_client, vm_namespace),
        ):
            identity = (client.kubeconfig, namespace)
            if identity in seen:
                continue
            seen.add(identity)
            try:
                pods = client.list_json(resource="pods", namespace=namespace).get("items", [])
            except Exception:
                if client is k8s_hub_client and namespace == k8s_hub_client.namespace:
                    raise RuntimeError("Could not list core OSAC pod logs; output withheld") from None
                unreadable += 1
                continue
            for pod in pods:
                pod_name = pod.get("metadata", {}).get("name", "")
                if not pod_name:
                    continue
                result = subprocess.run(
                    [
                        *client.base_args(),
                        "logs",
                        "-n",
                        namespace,
                        pod_name,
                        "--all-containers=true",
                        f"--since-time={started}",
                    ],
                    capture_output=True,
                    timeout=300,
                    check=False,
                )
                rc = result.returncode
                is_core = client is k8s_hub_client and namespace == k8s_hub_client.namespace
                required_pod = is_core and any(pod_name.startswith(name + "-") for name in core_sources)
                if rc:
                    if required_pod:
                        raise RuntimeError("A required core-service pod log was unavailable")
                    unreadable += 1
                    continue
                found = _hygiene_scan(result.stdout + b"\n" + result.stderr, needles)
                if found:
                    raise AssertionError(f"Sensitive test material appeared in a pod log: {', '.join(sorted(found))}")
                for required in core_sources:
                    if is_core and pod_name.startswith(required + "-"):
                        core_sources[required] = True
        if not all(core_sources.values()):
            raise RuntimeError("Required fulfillment or operator pod logs were unavailable")
        try:
            encoded_token = k8s_hub_client.get_json(resource="secret", name="osac-aap-api-token")["data"]["token"]
            token = base64.b64decode(encoded_token, validate=True).decode()
            with _controller_api_proxy(k8s_hub_client) as controller_url:
                found_jobs: set[str] = set()
                for page in range(1, 101):
                    query = urlencode({"created__gt": started, "page_size": 200, "order_by": "id", "page": page})
                    result = _controller_request(controller_url=controller_url, token=token, path=f"jobs/?{query}")
                    jobs = result.get("results")
                    if not isinstance(jobs, list):
                        raise RuntimeError("AAP job list had an unexpected shape")
                    previous_count = len(found_jobs)
                    for job in jobs:
                        job_id = str(int(job["id"]))
                        if job_id in found_jobs:
                            continue
                        found_jobs.add(job_id)
                        poll_until(
                            fn=lambda job_id=job_id: _controller_request(
                                controller_url=controller_url, token=token, path=f"jobs/{job_id}/"
                            ).get("status", ""),
                            until=lambda state: state in {"successful", "failed", "error", "canceled"},
                            retries=60,
                            delay=5,
                            description="AAP job completion before hygiene scan",
                            resource_kind=WaitResourceKind.AAP_JOB,
                            resource_id=job_id,
                        )
                        content = _controller_request(
                            controller_url=controller_url, token=token, path=f"jobs/{job_id}/stdout/?format=json"
                        ).get("content")
                        if not isinstance(content, str) or (job_id in job_ids and not content):
                            raise RuntimeError("Required AAP provision output was empty or invalid")
                        found = _hygiene_scan(content.encode(), needles)
                        if found:
                            raise AssertionError(
                                f"Sensitive test material appeared in AAP job output: {', '.join(sorted(found))}"
                            )
                    if not result.get("next"):
                        break
                    if len(found_jobs) == previous_count:
                        raise RuntimeError("AAP job pagination made no progress")
                else:
                    raise RuntimeError("AAP job scan exceeded the pagination limit")
                if not set(job_ids).issubset(found_jobs):
                    raise RuntimeError("The test VM's AAP provision job was missing from the log scan")
        except AssertionError:
            raise
        except Exception:
            raise RuntimeError("Could not read required AAP provision output; details withheld") from None
        LOG.info("SSH-key hygiene scan completed; %d optional pod sources unavailable", unreadable)
    except (subprocess.CalledProcessError, subprocess.TimeoutExpired):
        raise RuntimeError("SSH-key hygiene operation failed; output withheld") from None
    finally:
        monkeypatch.setattr(jwt_cli_user, "_run", original_run)
        try:
            _cleanup_instances(jwt_cli_user, jwt_grpc_tenant1, k8s_hub_client, vm_owned, vm_uncertain)
            _cleanup_secrets(jwt_cli_user, key_owned, key_uncertain)
        except Exception:
            raise RuntimeError(
                f"Could not clean hygiene test resources {vm_name}, {key_name}, {invalid_name}; output withheld"
            ) from None
