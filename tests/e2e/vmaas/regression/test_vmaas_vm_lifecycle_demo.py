"""Demo 3 CLI path from NetworkClass through ComputeInstance delete.

Serial-console login stays in test_console.py. This test checks that the
guest address is inside the subnet created here.

On an exclusively reserved cluster, OSAC_E2E_SCRATCH_STORAGE_CLASS enables
temporary CDI scratch preparation. Run with -n 0; the original HCO value is
restored after the lifecycle cleanup. Without the variable, storage is unchanged.
"""

from __future__ import annotations

import ipaddress
import json
import os
import re
import subprocess
import sys
from collections.abc import Callable, Iterator
from datetime import UTC, datetime
from pathlib import Path
from typing import Any
from uuid import uuid4

import pytest

from tests.e2e.core.grpc_client import PRIVATE_API, PUBLIC_API, GRPCClient
from tests.e2e.core.helpers import (
    allocate_worker_subnet,
    unique_name,
    wait_for_cr,
    wait_for_deletion,
    wait_for_grpc_removal,
    wait_for_new_vmi,
    wait_for_restart,
    wait_for_running,
    wait_for_security_group_cr,
    wait_for_security_group_ready,
    wait_for_subnet_cr,
    wait_for_subnet_ready,
    wait_for_virtual_network_cr,
    wait_for_virtual_network_ready,
    wait_for_vmi_ip,
)
from tests.e2e.core.k8s_client import K8sClient
from tests.e2e.core.osac_cli import OsacCLI
from tests.e2e.core.runner import poll_until
from tests.e2e.vmaas.networking_lifecycle_helpers import (
    delete_and_wait_for_security_group,
    delete_and_wait_for_subnet,
    delete_and_wait_for_virtual_network,
)

pytestmark = pytest.mark.regression

_CUDN_MANAGERS = {"k8s_only", "cudn_localnet", "cudn_evpn"}
_SCRATCH_OWNER = "osac.openshift.io/e2e-scratch-owner"
_SCRATCH_OWNER_PATH = "/metadata/annotations/osac.openshift.io~1e2e-scratch-owner"


def _k8s_manager(item: dict[str, Any]) -> str:
    value = item.get("k8s_manager") or item.get("k8sManager") or ""
    return str(value)


def _attachment_refs(instance: dict[str, Any]) -> tuple[str, list[str]]:
    spec = instance.get("spec") or {}
    attachments = spec.get("network_attachments") or spec.get("networkAttachments") or []
    assert len(attachments) == 1, f"expected one network attachment, got {len(attachments)}"
    attachment = attachments[0]
    subnet = attachment.get("subnet") or {}
    groups = attachment.get("security_groups") or attachment.get("securityGroups") or []
    return str(subnet.get("id") or ""), [str(group.get("id") or "") for group in groups]


def _wait_for_api_ready(get_resource: Callable[[], dict[str, Any]], ready_state: str) -> dict[str, Any]:
    # Kubernetes readiness can precede Fulfillment's feedback update. Dependent
    # Create requests validate the API state, so wait for that state as well.
    return poll_until(
        fn=get_resource,
        until=lambda response: response.get("object", {}).get("status", {}).get("state", "") == ready_state,
        retries=60,
        delay=2,
        description=ready_state,
    )


def _cli_objects(cli: OsacCLI, resource: str) -> list[dict[str, Any]]:
    objects = json.loads(cli.get(resource, output="json"))
    return [objects] if isinstance(objects, dict) else objects


def _wait_for_cli_state(cli: OsacCLI, instance_id: str, expected_state: str) -> None:
    poll_until(
        fn=lambda: _cli_objects(cli, "computeinstance"),
        until=lambda items: any(
            item["id"] == instance_id and item.get("status", {}).get("state") == expected_state for item in items
        ),
        retries=60,
        delay=2,
        description=expected_state,
    )


def _rule_signature(rule: dict[str, Any]) -> tuple[str, int, int, str]:
    protocol = rule["protocol"]
    return (
        protocol,
        rule.get("port_from", rule.get("portFrom", 0)) if protocol == "PROTOCOL_TCP" else 0,
        rule.get("port_to", rule.get("portTo", 0)) if protocol == "PROTOCOL_TCP" else 0,
        rule.get("ipv4_cidr") or rule.get("ipv4Cidr") or "",
    )


def _delete_instance_type_if_present(grpc: GRPCClient, name: str) -> None:
    try:
        grpc.delete_instance_type(name=name)
    except subprocess.CalledProcessError as error:
        if not re.match(r"ERROR:\s+Code:\s+NotFound\s+Message:", (error.stderr or "").strip()):
            raise


@pytest.fixture
def edit_lifecycle(
    cli: OsacCLI, tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> Callable[[str, dict[str, str]], None]:
    """Drive the same CLI editor workflow as the demo without interactive input."""
    editor = tmp_path / "edit-vm-lifecycle.py"
    editor.write_text(
        f"#!{sys.executable}\n"
        "import json\n"
        "import os\n"
        "import sys\n"
        "from pathlib import Path\n"
        "\n"
        "path = Path(sys.argv[1])\n"
        "instance = json.loads(path.read_text())\n"
        "instance['spec'].update(json.loads(os.environ['OSAC_E2E_LIFECYCLE_SPEC']))\n"
        "path.write_text(json.dumps(instance))\n",
        encoding="utf-8",
    )
    editor.chmod(0o700)

    def edit(instance_id: str, fields: dict[str, str]) -> None:
        with monkeypatch.context() as context:
            context.setenv("EDITOR", str(editor))
            context.setenv("OSAC_E2E_LIFECYCLE_SPEC", json.dumps(fields))
            cli.edit_compute_instance(uuid=instance_id)

    return edit


class _ScratchRestoreError(RuntimeError):
    """A fixed, credential-free reason for refusing scratch restoration."""


def _safe_command_error(error: BaseException, action: str) -> RuntimeError:
    # Subprocess exceptions embed argv, including gRPC bearer tokens. Keep the
    # action, exception type, and exit/timeout information without raw output.
    detail = type(error).__name__
    if isinstance(error, subprocess.CalledProcessError):
        detail += f" (exit {error.returncode})"
        args = error.cmd if isinstance(error.cmd, (list, tuple)) else []
        if args and isinstance(args[0], str):
            executable = Path(args[0]).name
            if executable in {"grpcurl", "oc", "kubectl", "curl"}:
                detail += f" [{executable}]"
        for arg in args:
            if isinstance(arg, str) and re.fullmatch(
                r"osac\.(?:public|private)\.v1\."
                r"(?:ComputeInstances|ComputeInstanceCatalogItems|InstanceTypes|NetworkClasses|"
                r"VirtualNetworks|Subnets|SecurityGroups)/(?:Create|Get|List|Update|Delete)",
                arg,
            ):
                detail += f" [{arg}]"
                break
        stderr = str(error.stderr or "").lower()
        reasons = [
            label
            for pattern, label in (
                (r"failed to query for service descriptor|failed to resolve symbol", "reflection failure"),
                (r"connection reset", "connection reset"),
                (r"connection refused", "connection refused"),
                (r"code\s*[:=]\s*unavailable\b", "Unavailable"),
                (r"code\s*[:=]\s*unauthenticated\b", "Unauthenticated"),
                (r"code\s*[:=]\s*permissiondenied\b", "PermissionDenied"),
                (r"code\s*[:=]\s*deadlineexceeded\b", "DeadlineExceeded"),
            )
            if re.search(pattern, stderr)
        ]
        detail += " (" + ", ".join(reasons or ["unclassified"]) + ")"
    elif isinstance(error, subprocess.TimeoutExpired):
        detail += f" (timeout {error.timeout}s)"
    elif isinstance(error, _ScratchRestoreError):
        detail += f": {error}"
    return RuntimeError(f"{action}: {detail}")


def _scratch_api(k8s: K8sClient) -> tuple[str, bool]:
    """Select a served HCO version whose schema contains the expected field."""
    crd = k8s.get_json(
        resource="customresourcedefinitions.apiextensions.k8s.io", name="hyperconvergeds.hco.kubevirt.io"
    )
    for version, nested in (("v1", True), ("v1beta1", False)):
        for candidate in crd["spec"]["versions"]:
            if candidate["name"] != version or not candidate.get("served"):
                continue
            fields = (
                candidate.get("schema", {})
                .get("openAPIV3Schema", {})
                .get("properties", {})
                .get("spec", {})
                .get("properties", {})
            )
            if nested:
                fields = fields.get("storage", {}).get("properties", {})
            if fields.get("scratchSpaceStorageClass", {}).get("type") == "string":
                return f"hyperconvergeds.{version}.hco.kubevirt.io", nested
    raise RuntimeError("No supported HyperConverged scratch-storage schema; cluster settings were not changed")


def _patch_scratch_storage(k8s: K8sClient, resource: str, name: str, operations: list[dict[str, Any]]) -> None:
    k8s._get("patch", resource, name, "-n", k8s.namespace, "--type=json", "-p", json.dumps(operations))


def _wait_for_scratch_storage(k8s: K8sClient, expected: str | None) -> None:
    poll_until(
        fn=lambda: (
            k8s.get_json(resource="cdiconfigs.cdi.kubevirt.io", name="config")
            .get("status", {})
            .get("scratchSpaceStorageClass")
        ),
        until=lambda current: current == expected,
        retries=60,
        delay=2,
        description="CDI scratch storage reconciled",
    )


@pytest.fixture
def temporary_scratch_storage(k8s_virt_client: K8sClient, tmp_path: Path) -> Iterator[None]:
    """Opt into cluster-wide preparation only on an exclusively reserved cluster."""
    desired = os.environ.get("OSAC_E2E_SCRATCH_STORAGE_CLASS", "").strip()
    if not desired:
        yield
        return
    if os.environ.get("PYTEST_XDIST_WORKER"):
        raise RuntimeError("Temporary scratch preparation requires serial execution with -n 0")

    # Check the requested class before changing cluster configuration.
    k8s_virt_client.get_json(resource="storageclasses.storage.k8s.io", name=desired)
    resource, nested = _scratch_api(k8s_virt_client)
    scratch_path = "/spec/storage/scratchSpaceStorageClass" if nested else "/spec/scratchSpaceStorageClass"
    raw, _ = k8s_virt_client._get("get", resource, "--all-namespaces", "-o", "json")
    hcos = json.loads(raw)["items"]
    if len(hcos) != 1 or hcos[0]["metadata"].get("deletionTimestamp"):
        raise RuntimeError("Temporary scratch preparation requires exactly one active HyperConverged")
    original = hcos[0]
    metadata = original["metadata"]
    annotations = metadata.get("annotations", {})
    if _SCRATCH_OWNER in annotations:
        raise RuntimeError("Another scratch preparation is active; recover its original setting before running")
    owner = uuid4().hex
    name, uid = metadata["name"], metadata["uid"]
    k8s = K8sClient(
        namespace=metadata["namespace"],
        kubeconfig=k8s_virt_client.kubeconfig,
        as_system_admin=k8s_virt_client.as_system_admin,
    )
    spec = original["spec"]
    storage = spec.get("storage", {}) if nested else spec
    was_present = "scratchSpaceStorageClass" in storage
    previous = storage.get("scratchSpaceStorageClass")
    effective = (
        k8s.get_json(resource="cdiconfigs.cdi.kubevirt.io", name="config")
        .get("status", {})
        .get("scratchSpaceStorageClass")
    )
    # Preserve a non-secret recovery record before the first mutation. A killed
    # process cannot execute teardown; this record retains the original value.
    (tmp_path / "scratch-storage-original.json").write_text(
        json.dumps(
            {
                "namespace": k8s.namespace,
                "resource": resource,
                "scratch_path": scratch_path,
                "storage_parent_present": "storage" in spec if nested else None,
                "name": name,
                "uid": uid,
                "present": was_present,
                "value": previous,
                "effective": effective,
                "temporary": desired,
                "owner": owner,
            }
        ),
        encoding="utf-8",
    )
    changed = previous != desired
    try:
        if changed:
            _patch_scratch_storage(
                k8s,
                resource,
                name,
                [
                    {"op": "test", "path": "/metadata/uid", "value": uid},
                    {"op": "test", "path": "/spec", "value": spec},
                    {
                        "op": "test",
                        "path": "/metadata/annotations" if "annotations" in metadata else "/metadata",
                        "value": annotations if "annotations" in metadata else metadata,
                    },
                    {"op": "add", "path": "/metadata/annotations", "value": {**annotations, _SCRATCH_OWNER: owner}},
                    (
                        {"op": "add", "path": "/spec/storage", "value": {"scratchSpaceStorageClass": desired}}
                        if nested and "storage" not in spec
                        else {"op": "add", "path": scratch_path, "value": desired}
                    ),
                ],
            )
            applied = k8s.get_json(resource=resource, name=name)["spec"]
            applied_storage = applied.get("storage", {}) if nested else applied
            if applied_storage.get("scratchSpaceStorageClass") != desired:
                raise RuntimeError("HyperConverged did not retain the requested scratch-storage setting")
        _wait_for_scratch_storage(k8s, desired)
        yield
    except (subprocess.CalledProcessError, subprocess.TimeoutExpired) as error:
        raise _safe_command_error(error, "prepare scratch storage") from None
    finally:
        if changed:
            primary_error = sys.exception()
            try:
                current = k8s.get_json(resource=resource, name=name)
                if current["metadata"]["uid"] != uid:
                    raise _ScratchRestoreError("HyperConverged was replaced; refusing to restore a different resource")
                current_spec = current["spec"]
                current_storage = current_spec.get("storage", {}) if nested else current_spec
                already_restored = (
                    "scratchSpaceStorageClass" in current_storage
                ) == was_present and current_storage.get("scratchSpaceStorageClass") == previous
                current_owner = current["metadata"].get("annotations", {}).get(_SCRATCH_OWNER)
                if current_owner != owner:
                    if current_owner is not None or not already_restored:
                        raise _ScratchRestoreError(
                            "Scratch setting is not owned by this test; refusing to overwrite it"
                        )
                else:
                    if not already_restored and current_storage.get("scratchSpaceStorageClass") != desired:
                        raise _ScratchRestoreError("Scratch setting changed externally; refusing to overwrite it")
                    restore = (
                        {"op": "replace", "path": scratch_path, "value": previous}
                        if was_present
                        else {"op": "remove", "path": scratch_path}
                    )
                    restore_operations = [] if already_restored else [restore]
                    if (
                        nested
                        and "storage" not in spec
                        and "storage" in current_spec
                        and current_storage in ({}, {"scratchSpaceStorageClass": desired})
                    ):
                        restore_operations = [{"op": "remove", "path": "/spec/storage"}]
                    _patch_scratch_storage(
                        k8s,
                        resource,
                        name,
                        [
                            {"op": "test", "path": "/metadata/uid", "value": uid},
                            {"op": "test", "path": _SCRATCH_OWNER_PATH, "value": owner},
                            {"op": "test", "path": "/spec", "value": current_spec},
                            *restore_operations,
                            {"op": "remove", "path": _SCRATCH_OWNER_PATH},
                        ],
                    )
                _wait_for_scratch_storage(k8s, effective)
            except Exception as error:
                cleanup_error = _safe_command_error(error, "restore scratch storage")
                if primary_error is not None:
                    raise BaseExceptionGroup(
                        "Scratch preparation/restore failed", [primary_error, cleanup_error]
                    ) from None
                raise cleanup_error from None


def test_vmaas_vm_lifecycle_demo(
    cli: OsacCLI,
    private_cli: OsacCLI,
    grpc: GRPCClient,
    private_grpc: GRPCClient,
    k8s_hub_client: K8sClient,
    k8s_virt_client: K8sClient,
    vm_template: str,
    default_storage_tier: str,
    default_disk_image: str,
    edit_lifecycle: Callable[[str, dict[str, str]], None],
    temporary_scratch_storage: None,
) -> None:
    """Order one VM on a tenant network, stop it, start it, and restart it."""
    grpc = grpc.with_read_connection_retries()
    private_grpc = private_grpc.with_read_connection_retries()
    tag = uuid4().hex[:8]
    subnet_net = allocate_worker_subnet()
    it_name = f"e2e-it-{tag}"
    it_cleanup_needed = False
    catalog_id: str | None = None
    vn_id: str | None = None
    vn_cr_name: str | None = None
    subnet_id: str | None = None
    subnet_cr_name: str | None = None
    sg_id: str | None = None
    sg_cr_name: str | None = None
    ci_uuid: str | None = None
    ci_name: str | None = None
    ci_create_name: str | None = None

    try:
        classes = [
            item
            for item in private_grpc.list_network_classes()
            if not (
                item.get("metadata", {}).get("deletion_timestamp") or item.get("metadata", {}).get("deletionTimestamp")
            )
        ]
        assert len(classes) == 1, f"expected one deployment NetworkClass, got {len(classes)}"
        network_class = classes[0]
        assert _k8s_manager(network_class) in _CUDN_MANAGERS, "expected a CUDN-backed VMaaS NetworkClass"

        it_cleanup_needed = True
        private_cli.create_instance_type(name=it_name, vcpus=1, memory_gib=2)
        described = cli.describe_instance_type(name=it_name)
        assert "ACTIVE" in described
        assert re.search(r"vCPUs:\s+1\b", described)
        assert re.search(r"Memory \(GiB\):\s+2\b", described)

        catalog_id = grpc.create_compute_instance_catalog_item(
            name=f"e2e-catalog-{tag}",
            template=vm_template,
            fields={
                "boot_disk": {
                    "storage_tier": {"editable": {"default_value": {"name": default_storage_tier}}},
                    "size_gib": {"editable": {"default_value": 10}},
                },
                "disk_image": {"editable": {"default_value": {"name": default_disk_image}}},
                "instance_type": {"editable": {"default_value": {"name": it_name, "shared": True}}},
                "network_attachments": {"editable": {}},
                "run_strategy": {"editable": {}},
            },
        )
        assert any(item["id"] == catalog_id for item in _cli_objects(cli, "computeinstancecatalogitem"))

        vn_id = grpc.create_virtual_network(name=f"e2e-vnet-{tag}", ipv4_cidr=str(subnet_net))
        vn_cr_name = wait_for_virtual_network_cr(k8s=k8s_hub_client, uuid=vn_id)
        wait_for_virtual_network_ready(k8s=k8s_hub_client, name=vn_cr_name)
        _wait_for_api_ready(lambda: grpc.get_virtual_network(vn_id=vn_id), "VIRTUAL_NETWORK_STATE_READY")
        virtual_network = private_grpc.call(service=f"{PRIVATE_API}.VirtualNetworks/Get", data={"id": vn_id})["object"]
        vn_spec = virtual_network["spec"]
        selected_class = vn_spec.get("network_class") or vn_spec.get("networkClass") or {}
        assert selected_class.get("id") == network_class["id"]
        assert vn_id in grpc.list_virtual_network_ids()

        subnet_id = grpc.create_subnet(name=f"e2e-subnet-{tag}", virtual_network=vn_id, ipv4_cidr=str(subnet_net))
        subnet_cr_name = wait_for_subnet_cr(k8s=k8s_hub_client, uuid=subnet_id)
        assert subnet_id in grpc.list_subnet_ids()
        subnet = grpc.get_subnet(subnet_id=subnet_id)
        assert subnet["object"]["metadata"]["name"] == f"e2e-subnet-{tag}"
        wait_for_subnet_ready(k8s=k8s_hub_client, name=subnet_cr_name)
        subnet = _wait_for_api_ready(lambda: grpc.get_subnet(subnet_id=subnet_id), "SUBNET_STATE_READY")["object"]
        subnet_spec = subnet["spec"]
        subnet_parent = subnet_spec.get("virtual_network") or subnet_spec.get("virtualNetwork") or {}
        assert subnet_parent.get("id") == vn_id
        assert ipaddress.ip_network(subnet_spec.get("ipv4_cidr") or subnet_spec.get("ipv4Cidr") or "") == subnet_net

        sg_name = f"e2e-sg-{tag}"
        sg_id = grpc.create_security_group_with_rules(
            name=sg_name,
            virtual_network=vn_id,
            ingress=[
                {"protocol": "PROTOCOL_TCP", "port_from": 22, "port_to": 22, "ipv4_cidr": "0.0.0.0/0"},
                {"protocol": "PROTOCOL_ICMP", "ipv4_cidr": "0.0.0.0/0"},
            ],
            egress=[{"protocol": "PROTOCOL_ALL", "ipv4_cidr": "0.0.0.0/0"}],
        )
        sg_cr_name = wait_for_security_group_cr(k8s=k8s_hub_client, uuid=sg_id)
        wait_for_security_group_ready(k8s=k8s_hub_client, name=sg_cr_name)
        security_group = _wait_for_api_ready(
            lambda: grpc.get_security_group(sg_id=sg_id), "SECURITY_GROUP_STATE_READY"
        )["object"]
        group_spec = security_group["spec"]
        group_parent = group_spec.get("virtual_network") or group_spec.get("virtualNetwork") or {}
        assert group_parent.get("id") == vn_id
        assert sorted(_rule_signature(rule) for rule in group_spec.get("ingress", [])) == [
            ("PROTOCOL_ICMP", 0, 0, "0.0.0.0/0"),
            ("PROTOCOL_TCP", 22, 22, "0.0.0.0/0"),
        ]
        assert [_rule_signature(rule) for rule in group_spec.get("egress", [])] == [("PROTOCOL_ALL", 0, 0, "0.0.0.0/0")]
        assert sg_id in grpc.list_security_group_ids()

        ci_create_name = unique_name("e2e-vm")
        ci_uuid = cli.create_compute_instance_with_catalog_item(
            catalog_item=catalog_id, name=ci_create_name, subnet=subnet_id, security_groups=[sg_id]
        )
        attached_subnet, attached_groups = _attachment_refs(grpc.get_compute_instance(ci_id=ci_uuid)["object"])
        assert attached_subnet == subnet_id
        assert attached_groups == [sg_id]

        ci_name = wait_for_cr(k8s=k8s_hub_client, uuid=ci_uuid)
        wait_for_running(k8s=k8s_hub_client, name=ci_name)

        instance_spec = grpc.get_compute_instance(ci_id=ci_uuid)["object"]["spec"]
        catalog = instance_spec.get("catalog_item") or instance_spec.get("catalogItem") or {}
        assert catalog.get("id") == catalog_id
        instance_type = instance_spec.get("instance_type") or instance_spec.get("instanceType") or {}
        assert instance_type.get("name") == it_name
        disk_image = instance_spec.get("disk_image") or instance_spec.get("diskImage") or {}
        assert disk_image.get("name") == default_disk_image
        image = grpc.get_disk_image(disk_image_id=disk_image["id"])["object"]
        assert image["metadata"]["name"] == default_disk_image
        image_spec = image["spec"]
        assert image_spec.get("guest_os_family", image_spec.get("guestOsFamily")) == "GUEST_OS_FAMILY_LINUX"
        image_source = image_spec.get("source_ref") or image_spec.get("sourceRef")
        assert image_source, "DiskImage must have a source reference"
        boot_disk = instance_spec.get("boot_disk") or instance_spec.get("bootDisk") or {}
        assert boot_disk.get("size_gib", boot_disk.get("sizeGib")) == 10
        storage_tier = boot_disk.get("storage_tier") or boot_disk.get("storageTier") or {}
        assert storage_tier.get("name") == default_storage_tier

        instance_cr = k8s_hub_client.get_json(resource="computeinstance", name=ci_name)
        assert instance_cr["metadata"]["labels"]["osac.openshift.io/instance-type-name"] == it_name
        cr_spec = instance_cr["spec"]
        assert cr_spec["vcpus"] == 1
        assert cr_spec["memoryGiB"] == 2
        assert cr_spec["guestOSFamily"] == "linux"
        assert cr_spec["image"]["sourceRef"] == image_source
        assert cr_spec["bootDisk"]["sizeGiB"] == 10
        assert cr_spec["bootDisk"]["storageTier"] == default_storage_tier

        vm_ns = k8s_hub_client.get_compute_instance_vm_namespace(name=ci_name)
        guest_ip = wait_for_vmi_ip(k8s=k8s_virt_client, vmi_namespace=vm_ns, compute_instance_name=ci_name)
        assert ipaddress.ip_address(guest_ip) in subnet_net
        _wait_for_cli_state(cli, ci_uuid, "COMPUTE_INSTANCE_STATE_RUNNING")

        edit_lifecycle(ci_uuid, {"run_strategy": "Halted"})
        poll_until(
            fn=lambda: k8s_hub_client.get_compute_instance_phase(name=ci_name, checked=False),
            until=lambda phase: phase == "Stopped",
            retries=60,
            delay=5,
            description=f"{ci_name} stopped",
        )
        poll_until(
            fn=lambda: k8s_virt_client.get_vmi_json(vmi_namespace=vm_ns, compute_instance_name=ci_name),
            until=lambda vmi: vmi == {},
            retries=30,
            delay=5,
            description=f"{ci_name} VMI gone",
        )
        _wait_for_cli_state(cli, ci_uuid, "COMPUTE_INSTANCE_STATE_STOPPED")

        edit_lifecycle(ci_uuid, {"run_strategy": "Always"})
        wait_for_running(k8s=k8s_hub_client, name=ci_name)
        _wait_for_cli_state(cli, ci_uuid, "COMPUTE_INSTANCE_STATE_RUNNING")

        original_vmi_ts = k8s_virt_client.get_vmi_creation_timestamp(vmi_namespace=vm_ns, compute_instance_name=ci_name)
        initial_last_restarted = k8s_hub_client.get_compute_instance_last_restarted_at(name=ci_name)
        restart_ts = datetime.now(tz=UTC).strftime("%Y-%m-%dT%H:%M:%SZ")
        edit_lifecycle(ci_uuid, {"restart_requested_at": restart_ts})
        wait_for_restart(k8s=k8s_hub_client, name=ci_name, initial=initial_last_restarted, restart_ts=restart_ts)
        wait_for_running(k8s=k8s_hub_client, name=ci_name)
        new_vmi_ts = wait_for_new_vmi(
            k8s=k8s_virt_client, vmi_namespace=vm_ns, compute_instance_name=ci_name, initial_timestamp=original_vmi_ts
        )
        assert new_vmi_ts > original_vmi_ts
        _wait_for_cli_state(cli, ci_uuid, "COMPUTE_INSTANCE_STATE_RUNNING")

        removed_ci = ci_uuid
        removed_sg = sg_id
        removed_subnet = subnet_id
        removed_vn = vn_id
        cli.delete_compute_instance(uuid=ci_uuid)
        wait_for_deletion(k8s=k8s_hub_client, name=ci_name)
        wait_for_grpc_removal(grpc=grpc, uuid=ci_uuid)
        ci_uuid = None
        ci_name = None
        ci_create_name = None
        delete_and_wait_for_security_group(grpc, k8s_hub_client, sg_id, sg_cr_name)
        sg_id = None
        delete_and_wait_for_subnet(grpc, k8s_hub_client, subnet_id, subnet_cr_name)
        subnet_id = None
        delete_and_wait_for_virtual_network(grpc, k8s_hub_client, vn_id, vn_cr_name)
        vn_id = None
        assert removed_ci not in grpc.list_compute_instance_ids()
        assert removed_sg not in grpc.list_security_group_ids()
        assert removed_subnet not in grpc.list_subnet_ids()
        assert removed_vn not in grpc.list_virtual_network_ids()
    except (subprocess.CalledProcessError, subprocess.TimeoutExpired) as error:
        raise _safe_command_error(error, "OSAC-6110 lifecycle command failed") from None
    finally:
        primary_error = sys.exception()
        cleanup_errors: list[Exception] = []

        def cleanup(label: str, action: Callable[[], None]) -> None:
            try:
                action()
            except Exception as error:
                cleanup_errors.append(_safe_command_error(error, label))

        if ci_uuid is None and ci_create_name is not None:

            def resolve_ci() -> None:
                nonlocal ci_uuid, ci_name
                try:
                    items = poll_until(
                        fn=lambda: grpc.list_with_filter(
                            service=f"{PUBLIC_API}.ComputeInstances/List",
                            filter_expr=f"this.metadata.name == {json.dumps(ci_create_name)}",
                        ),
                        until=bool,
                        retries=60,
                        delay=2,
                        description="ComputeInstance after incomplete CLI create",
                    )
                except TimeoutError:
                    return
                if len(items) != 1 or items[0].get("metadata", {}).get("name") != ci_create_name:
                    raise RuntimeError("ComputeInstance name lookup was not unique; refusing ambiguous cleanup")
                ci_uuid = items[0]["id"]
                ci_name = wait_for_cr(k8s=k8s_hub_client, uuid=ci_uuid)

            cleanup("find ComputeInstance after incomplete CLI create", resolve_ci)
        if ci_uuid is not None:
            cleanup("delete ComputeInstance", lambda: cli.delete_compute_instance(uuid=ci_uuid))
            if ci_name is not None:
                cleanup(
                    "wait for ComputeInstance CR deletion", lambda: wait_for_deletion(k8s=k8s_hub_client, name=ci_name)
                )
            cleanup("wait for ComputeInstance API deletion", lambda: wait_for_grpc_removal(grpc=grpc, uuid=ci_uuid))
        if sg_id is not None:
            cleanup(
                "delete SecurityGroup",
                lambda: delete_and_wait_for_security_group(grpc, k8s_hub_client, sg_id, sg_cr_name),
            )
        if subnet_id is not None:
            cleanup(
                "delete Subnet", lambda: delete_and_wait_for_subnet(grpc, k8s_hub_client, subnet_id, subnet_cr_name)
            )
        if vn_id is not None:
            cleanup(
                "delete VirtualNetwork",
                lambda: delete_and_wait_for_virtual_network(grpc, k8s_hub_client, vn_id, vn_cr_name),
            )
        if catalog_id is not None:
            cleanup(
                "delete catalog item", lambda: grpc.delete_compute_instance_catalog_item(catalog_item_id=catalog_id)
            )
        if it_cleanup_needed:
            cleanup("delete InstanceType", lambda: _delete_instance_type_if_present(private_grpc, it_name))
        if cleanup_errors:
            errors = [primary_error, *cleanup_errors] if primary_error is not None else cleanup_errors
            raise BaseExceptionGroup("OSAC-6110 lifecycle/cleanup failed", errors) from None
