"""Guest SSH authentication over an authenticated Kubernetes API tunnel."""

from __future__ import annotations

import os
from pathlib import Path

import pytest

from tests.e2e.core.grpc_client import GRPCClient
from tests.e2e.core.helpers import unique_name, wait_for_cr, wait_for_provision, wait_for_running
from tests.e2e.core.k8s_client import K8sClient
from tests.e2e.core.osac_cli import OsacCLI
from tests.e2e.core.runner import run
from tests.e2e.vmaas.regression.ssh_key_registry.external_ssh import (
    RouteCleanupError,
    assert_external_guest_key_access,
    external_ssh_ip,
    external_ssh_service,
)
from tests.e2e.vmaas.regression.ssh_key_registry.helpers import (
    _assert_guest_key_access,
    _assert_vm_key_injected,
    _cleanup_instances,
    _cleanup_secrets,
    _create_owned_instance,
    _create_owned_secret,
    _guest_ssh_environment,
    _instances_named,
    _secrets,
    _verified_kubernetes_environment,
)

pytestmark = pytest.mark.regression


def test_registered_key_authenticates_to_guest(
    cli: OsacCLI,
    grpc: GRPCClient,
    private_grpc: GRPCClient,
    networking_provisioning_enabled: bool,
    k8s_hub_client: K8sClient,
    request: pytest.FixtureRequest,
    vm_template: str,
    default_network_attachment: dict[str, object],
    ssh_key_dir: Path,
) -> None:
    """TC-12 (partial): guest authentication through a Kubernetes API tunnel."""
    if not os.environ.get("OSAC_VM_KUBECONFIG"):
        pytest.skip("VM injection inspection requires OSAC_VM_KUBECONFIG")
    k8s_virt_client = request.getfixturevalue("k8s_virt_client")
    _verified_kubernetes_environment(k8s_virt_client)
    try:
        environment = _guest_ssh_environment(k8s_virt_client)
        guest_skip_reason = ""
    except pytest.skip.Exception as exc:
        environment = None
        guest_skip_reason = str(exc)
    key_paths = [ssh_key_dir / "correct", ssh_key_dir / "unrelated"]
    for path in key_paths:
        run("ssh-keygen", "-t", "ed25519", "-f", str(path), "-N", "", "-C", "osac-e2e")
    secret_name = unique_name("e2e-ssh-guest-key")
    vm_name = unique_name("e2e-ssh-guest")
    if _secrets(cli, secret_name) or _instances_named(cli, vm_name):
        raise AssertionError("Test resource name already exists")

    owned: dict[str, str] = {}
    uncertain: set[str] = set()
    secret_owned: dict[str, str] = {}
    secret_uncertain: set[str] = set()
    route_cleanup_failed = False
    try:
        _create_owned_secret(
            cli,
            secret_owned,
            secret_uncertain,
            name=secret_name,
            secret_type="ssh-public-key",
            field="public_key",
            path=key_paths[0].with_suffix(".pub"),
        )
        instance_id = _create_owned_instance(
            cli,
            owned,
            uncertain,
            template=vm_template,
            name=vm_name,
            network_attachments=[default_network_attachment],
            ssh_key=secret_name,
        )
        cr_name = wait_for_cr(k8s=k8s_hub_client, uuid=instance_id)
        wait_for_provision(k8s=k8s_hub_client, name=cr_name)
        wait_for_running(k8s=k8s_hub_client, name=cr_name)
        cr = k8s_hub_client.get_json(resource="computeinstance", name=cr_name)
        expected_key = key_paths[0].with_suffix(".pub").read_text(encoding="utf-8")
        _assert_vm_key_injected(cr, k8s_virt_client, expected_key)
        if environment is None:
            pytest.skip(f"Injection checks passed; guest check skipped: {guest_skip_reason}")
        _assert_guest_key_access(cr, key_paths[0], key_paths[1], ssh_key_dir, environment)
        if networking_provisioning_enabled:
            groups = default_network_attachment.get("security_groups", [])
            if not any(
                rule.get("protocol") == "PROTOCOL_ALL" and rule.get("ipv4Cidr") == "0.0.0.0/0"
                for group_id in groups
                for rule in grpc.get_security_group(sg_id=group_id)["object"].get("spec", {}).get("ingress", [])
            ):
                raise RuntimeError("The test VM's security group does not allow the external SSH probe")
            route = external_ssh_ip(grpc, private_grpc, k8s_hub_client, instance_id)
        else:
            route = external_ssh_service(k8s_virt_client, cr)
        try:
            with route as address:
                assert_external_guest_key_access(cr, address, key_paths[0], key_paths[1], ssh_key_dir, environment)
        except RouteCleanupError as exc:
            route_cleanup_failed = True
            secret_ids = ", ".join(sorted(secret_owned))
            raise RouteCleanupError(
                f"{exc}; preserved ComputeInstance {instance_id} and SSH Secret IDs {secret_ids}"
            ) from exc
    finally:
        if not route_cleanup_failed:
            _cleanup_instances(cli, grpc, k8s_hub_client, owned, uncertain)
            _cleanup_secrets(cli, secret_owned, secret_uncertain)
