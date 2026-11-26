"""Route a test VM through an existing MetalLB pool without modifying the pool."""

from __future__ import annotations

import ipaddress
import json
import os
import re
import subprocess
from collections.abc import Iterator
from contextlib import contextmanager
from pathlib import Path

import pytest

from tests.e2e.core.grpc_client import PUBLIC_API, GRPCClient
from tests.e2e.core.helpers import unique_name
from tests.e2e.core.k8s_client import K8sClient
from tests.e2e.core.runner import poll_until
from tests.e2e.core.wait_kinds import WaitResourceKind
from tests.e2e.vmaas.regression.ssh_key_registry.helpers import (
    _definite_create_rejection,
    _rpc_status,
    _verified_kubernetes_environment,
)


def _labeled_crs(k8s: K8sClient, resource: str, kind: str, resource_id: str) -> list[dict[str, object]]:
    output, _ = k8s._get(
        "get", resource, "-n", k8s.namespace,
        "-l", f"osac.openshift.io/{kind}-uuid={resource_id}", "-o", "json",
    )
    return json.loads(output).get("items", [])


class RouteCleanupError(RuntimeError):
    """An owned external route could not be safely removed."""


@contextmanager
def external_ssh_service(k8s: K8sClient, cr: dict[str, object]) -> Iterator[str]:
    """Expose one test-owned VM with a test-owned Service from an existing pool."""
    __tracebackhide__ = True
    _verified_kubernetes_environment(k8s)
    discovery = subprocess.run(
        [*k8s.base_args(), "api-resources", "--api-group=metallb.io", "-o", "name"],
        capture_output=True,
        text=True,
        check=True,
        timeout=60,
    ).stdout.splitlines()
    if "ipaddresspools.metallb.io" not in discovery:
        pytest.skip("External SSH requires an existing MetalLB address pool")
    pools = k8s.list_json_all_namespaces(resource="ipaddresspools.metallb.io").get("items", [])
    if not pools:
        pytest.skip("External SSH requires an existing MetalLB address pool")
    reference = cr["status"]["virtualMachineReference"]
    namespace = reference["namespace"]
    vm_name = reference.get("name") or cr["metadata"]["name"]
    name = unique_name("e2e-ssh-route")
    owner = unique_name("e2e-ssh-owner")
    manifest = {
        "apiVersion": "v1",
        "kind": "Service",
        "metadata": {"name": name, "namespace": namespace, "labels": {"osac-e2e-owner": owner}},
        "spec": {
            "type": "LoadBalancer",
            "selector": {"vm.kubevirt.io/name": vm_name},
            "ports": [{"name": "ssh", "protocol": "TCP", "port": 22, "targetPort": 22}],
        },
    }
    uid: str | None = None
    uncertain = False

    def owned_services() -> list[dict[str, object]]:
        output, _ = k8s._get("get", "service", name, "-n", namespace, "--ignore-not-found", "-o", "json")
        if not output.strip():
            return []
        item = json.loads(output)
        if item.get("metadata", {}).get("labels", {}).get("osac-e2e-owner") != owner:
            return []
        return [item]

    try:
        uncertain = True
        result = subprocess.run(
            [*k8s.base_args(), "create", "-f", "-", "-o", "json"],
            input=json.dumps(manifest),
            capture_output=True,
            text=True,
            check=False,
            timeout=60,
        )
        if result.returncode:
            detail = result.stderr.lower()
            if re.search(r"error from server \((?:alreadyexists|forbidden|unauthorized|invalid|badrequest)\)", detail):
                uncertain = False
            raise RuntimeError("Could not create the test-owned external SSH Service")
        created = json.loads(result.stdout)
        uid = created["metadata"]["uid"]
        if created["metadata"].get("labels", {}).get("osac-e2e-owner") != owner:
            raise AssertionError("External SSH Service ownership did not match")
        uncertain = False

        def assigned_address() -> str:
            matches = owned_services()
            if len(matches) != 1 or matches[0].get("metadata", {}).get("uid") != uid:
                raise RuntimeError("Test-owned external SSH Service is missing or was replaced")
            ingress = matches[0].get("status", {}).get("loadBalancer", {}).get("ingress", [])
            if not ingress or not ingress[0].get("ip"):
                return ""
            address = str(ipaddress.ip_address(ingress[0]["ip"]))
            slice_output, _ = k8s._get(
                "get", "endpointslices", "-n", namespace,
                "-l", f"kubernetes.io/service-name={name}", "-o", "json",
            )
            slices = json.loads(slice_output).get("items", [])
            ready = any(
                item.get("metadata", {}).get("labels", {}).get("kubernetes.io/service-name") == name
                and any(
                    endpoint.get("addresses") and endpoint.get("conditions", {}).get("ready") is not False
                    for endpoint in item.get("endpoints", [])
                )
                for item in slices
            )
            return address if ready else ""

        address = poll_until(
            fn=assigned_address,
            until=bool,
            retries=120,
            delay=5,
            description="test-owned external SSH Service address and endpoint",
            resource_kind=WaitResourceKind.SERVICE,
            resource_id=name,
        )
        yield address
    finally:
        try:
            if uncertain:
                matches = poll_until(
                    fn=owned_services,
                    until=bool,
                    retries=30,
                    delay=2,
                    description=f"recover uncertain Service {name}",
                    resource_kind=WaitResourceKind.SERVICE,
                    resource_id=name,
                )
                if len(matches) != 1:
                    raise RuntimeError("External SSH Service ownership is ambiguous")
                uid = matches[0]["metadata"]["uid"]
            if uid:
                body = {"apiVersion": "v1", "kind": "DeleteOptions", "preconditions": {"uid": uid}}
                result = subprocess.run(
                    [*k8s.base_args(), "delete", f"--raw=/api/v1/namespaces/{namespace}/services/{name}", "-f", "-"],
                    input=json.dumps(body),
                    capture_output=True,
                    text=True,
                    check=False,
                    timeout=60,
                )
                if result.returncode and "notfound" not in result.stderr.lower():
                    raise RuntimeError("Could not safely delete the test-owned external SSH Service")
                poll_until(
                    fn=lambda: not owned_services(),
                    until=bool,
                    retries=60,
                    delay=2,
                    description=f"test-owned Service {name} removal",
                    resource_kind=WaitResourceKind.SERVICE,
                    resource_id=name,
                )
        except BaseException as exc:
            raise RouteCleanupError(
                f"External SSH Service cleanup failed for {namespace}/{name}, uid={uid}; preserve the VM"
            ) from exc


def assert_external_guest_key_access(
    cr: dict[str, object],
    address: str,
    correct_key: Path,
    unrelated_key: Path,
    work_dir: Path,
    environment: dict[str, str],
) -> None:
    """Check direct SSH using the host key learned through the authenticated API tunnel."""
    __tracebackhide__ = True
    reference = cr["status"]["virtualMachineReference"]
    guest_name = reference.get("name") or cr["metadata"]["name"]
    known_hosts = work_dir / f"known_hosts_{guest_name}"
    if not known_hosts.is_file():
        raise RuntimeError("A host key must be established through the verified API tunnel first")
    user = os.environ.get("OSAC_VM_SSH_USER", "fedora")
    base = [
        "ssh",
        "-F",
        "/dev/null",
        "-o",
        f"HostKeyAlias=osac-e2e-{guest_name}",
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
        "StrictHostKeyChecking=yes",
        "-o",
        "PreferredAuthentications=publickey",
        "-o",
        "ConnectTimeout=15",
    ]

    def authenticate() -> bool:
        try:
            result = subprocess.run(
                [*base, "-i", str(correct_key), f"{user}@{address}", "printf OSAC_SSH_OK"],
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
        description="external guest SSH authentication",
        resource_kind=WaitResourceKind.VIRTUAL_MACHINE_INSTANCE,
        resource_id=guest_name,
    )
    try:
        wrong = subprocess.run(
            [*base, "-i", str(unrelated_key), f"{user}@{address}", "true"],
            capture_output=True,
            text=True,
            env=environment,
            timeout=35,
            check=False,
        )
    except subprocess.TimeoutExpired:
        raise RuntimeError("External unrelated-key check timed out; result is inconclusive") from None
    if wrong.returncode == 0 or not re.search(r"permission denied \(publickey(?:[,)]|$)", wrong.stderr.lower()):
        raise AssertionError("External unrelated key did not receive an SSH authentication denial")


@contextmanager
def external_ssh_ip(grpc: GRPCClient, private_grpc: GRPCClient, k8s: K8sClient, instance_id: str) -> Iterator[str]:
    """Attach a test-owned ExternalIP from an existing pool and remove both resources."""
    __tracebackhide__ = True
    _verified_kubernetes_environment(k8s)
    instance = private_grpc.call(service="osac.private.v1.ComputeInstances/Get", data={"id": instance_id})["object"]
    project = instance.get("metadata", {}).get("project") or ""
    hub = instance.get("status", {}).get("hub") or ""
    if not hub:
        raise RuntimeError("Cannot identify the VM's hub for external SSH")
    available = []
    for pool_id in private_grpc.list_external_ip_pool_ids():
        pool = private_grpc.get_external_ip_pool(pool_id=pool_id)["object"]
        status = pool.get("status", {})
        if (
            status.get("state") == "EXTERNAL_IP_POOL_STATE_READY"
            and status.get("hub") == hub
            and int(status.get("available", 0)) > 0
        ):
            available.append(pool_id)
    if not available:
        pytest.skip("External SSH requires a ready ExternalIPPool with capacity on the VM's hub")
    ip_name, attachment_name = unique_name("e2e-ssh-external-ip"), unique_name("e2e-ssh-external-attachment")
    owner = unique_name("e2e-ssh-owner")
    ip_id: str | None = None
    attachment_id: str | None = None
    uncertain_ip = False
    uncertain_attachment = False

    def owned(kind: str, name: str) -> list[str]:
        result = grpc.call(
            service=f"{PUBLIC_API}.{kind}/List", data={"filter": f"this.metadata.name == {json.dumps(name)}"}
        )
        return [
            item["id"]
            for item in result.get("items", [])
            if item.get("metadata", {}).get("name") == name
            and (item.get("metadata", {}).get("project") or "") == project
            and item.get("metadata", {}).get("labels", {}).get("e2e-owner") == owner
        ]

    def definite_rejection(exc: subprocess.CalledProcessError) -> bool:
        detail = f"{exc.stdout or ''} {exc.stderr or ''}".lower()
        return _definite_create_rejection(detail)

    def absent(kind: str, resource_id: str) -> bool:
        try:
            grpc.call(service=f"{PUBLIC_API}.{kind}/Get", data={"id": resource_id})
        except subprocess.CalledProcessError as exc:
            detail = f"{exc.stdout or ''} {exc.stderr or ''}".lower()
            if _rpc_status(detail) == "notfound":
                return True
            raise
        return False

    try:
        uncertain_ip = True
        try:
            response = grpc.call(
                service=f"{PUBLIC_API}.ExternalIPs/Create",
                data={
                    "object": {
                        "metadata": {"name": ip_name, "project": project, "labels": {"e2e-owner": owner}},
                        "spec": {"pool": {"id": available[0]}},
                    }
                },
            )
        except subprocess.CalledProcessError as exc:
            if definite_rejection(exc):
                uncertain_ip = False
            raise
        ip_id = response["object"]["id"]
        uncertain_ip = False

        def allocated() -> str:
            status = grpc.get_external_ip(external_ip_id=ip_id)["object"].get("status", {})
            if status.get("state") != "EXTERNAL_IP_STATE_ALLOCATED" or not status.get("address"):
                return ""
            return str(ipaddress.ip_address(status["address"]))

        address = poll_until(
            fn=allocated,
            until=bool,
            retries=120,
            delay=5,
            description="test-owned ExternalIP allocation",
            resource_kind=WaitResourceKind.EXTERNAL_IP,
            resource_id=ip_id,
        )
        uncertain_attachment = True
        try:
            response = grpc.call(
                service=f"{PUBLIC_API}.ExternalIPAttachments/Create",
                data={
                    "object": {
                        "metadata": {"name": attachment_name, "project": project, "labels": {"e2e-owner": owner}},
                        "spec": {"external_ip": {"id": ip_id}, "compute_instance": {"id": instance_id}},
                    }
                },
            )
        except subprocess.CalledProcessError as exc:
            if definite_rejection(exc):
                uncertain_attachment = False
            raise
        attachment_id = response["object"]["id"]
        uncertain_attachment = False
        poll_until(
            fn=lambda: (
                grpc.get_external_ip_attachment(attachment_id=attachment_id)["object"]
                .get("status", {})
                .get("state", "")
            ),
            until=lambda state: state == "EXTERNAL_IP_ATTACHMENT_STATE_READY",
            retries=120,
            delay=5,
            description="test-owned ExternalIP attachment",
            resource_kind=WaitResourceKind.EXTERNAL_IP_ATTACHMENT,
            resource_id=attachment_id,
        )
        yield address
    finally:
        try:
            if uncertain_attachment:
                recovered = poll_until(
                    fn=lambda: owned("ExternalIPAttachments", attachment_name),
                    until=bool,
                    retries=30,
                    delay=2,
                    description="recover uncertain test attachment",
                    resource_kind=WaitResourceKind.EXTERNAL_IP_ATTACHMENT,
                    resource_id=attachment_name,
                )
                if len(recovered) != 1:
                    raise RuntimeError("ExternalIP attachment ownership is ambiguous")
                attachment_id = recovered[0]
            if attachment_id:
                try:
                    grpc.delete_external_ip_attachment(attachment_id=attachment_id)
                except subprocess.CalledProcessError as exc:
                    if _rpc_status(f"{exc.stdout or ''} {exc.stderr or ''}") != "notfound":
                        raise
                poll_until(
                    fn=lambda: absent("ExternalIPAttachments", attachment_id),
                    until=bool,
                    retries=120,
                    delay=5,
                    description="test-owned ExternalIP attachment removal",
                    resource_kind=WaitResourceKind.EXTERNAL_IP_ATTACHMENT,
                    resource_id=attachment_id,
                )
                poll_until(
                    fn=lambda: all(
                        item.get("metadata", {}).get("labels", {}).get("osac.openshift.io/externalipattachment-uuid")
                        != attachment_id
                        for item in _labeled_crs(k8s, "externalipattachments", "externalipattachment", attachment_id)
                    ),
                    until=bool,
                    retries=120,
                    delay=5,
                    description="test-owned attachment CR removal",
                    resource_kind=WaitResourceKind.EXTERNAL_IP_ATTACHMENT,
                    resource_id=attachment_id,
                )
            if uncertain_ip:
                recovered = poll_until(
                    fn=lambda: owned("ExternalIPs", ip_name),
                    until=bool,
                    retries=30,
                    delay=2,
                    description="recover uncertain test ExternalIP",
                    resource_kind=WaitResourceKind.EXTERNAL_IP,
                    resource_id=ip_name,
                )
                if len(recovered) != 1:
                    raise RuntimeError("ExternalIP ownership is ambiguous")
                ip_id = recovered[0]
            if ip_id:
                try:
                    grpc.delete_external_ip(external_ip_id=ip_id)
                except subprocess.CalledProcessError as exc:
                    if _rpc_status(f"{exc.stdout or ''} {exc.stderr or ''}") != "notfound":
                        raise
                poll_until(
                    fn=lambda: absent("ExternalIPs", ip_id),
                    until=bool,
                    retries=120,
                    delay=5,
                    description="test-owned ExternalIP removal",
                    resource_kind=WaitResourceKind.EXTERNAL_IP,
                    resource_id=ip_id,
                )
                poll_until(
                    fn=lambda: all(
                        item.get("metadata", {}).get("labels", {}).get("osac.openshift.io/externalip-uuid") != ip_id
                        for item in _labeled_crs(k8s, "externalips", "externalip", ip_id)
                    ),
                    until=bool,
                    retries=120,
                    delay=5,
                    description="test-owned ExternalIP CR removal",
                    resource_kind=WaitResourceKind.EXTERNAL_IP,
                    resource_id=ip_id,
                )
        except BaseException as exc:
            raise RouteCleanupError(
                f"ExternalIP route cleanup failed for ip={ip_id or ip_name}, "
                f"attachment={attachment_id or attachment_name}; preserve the VM"
            ) from exc
