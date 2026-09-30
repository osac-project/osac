"""Fixtures for CaaS cluster networking regression tests.

Uses JWT auth (tenant1_admin) for both the CLI and gRPC client so that
networking resources and clusters land in the same tenant scope.  This
replaces the SA-auth workaround now that OSAC-4340 is resolved.

The ``caas_networking`` fixture follows the VMaaS ``default_networking``
pattern: create VN -> Subnet -> SG, wait Ready, yield, cleanup in reverse.
"""

from __future__ import annotations

import os
from collections.abc import Iterator

import pytest

from tests.e2e.core.grpc_client import GRPCClient
from tests.e2e.core.helpers import (
    unique_name,
    wait_for_security_group_cr,
    wait_for_security_group_deletion,
    wait_for_security_group_ready,
    wait_for_subnet_cr,
    wait_for_subnet_deletion,
    wait_for_subnet_ready,
    wait_for_virtual_network_cr,
    wait_for_virtual_network_deletion,
    wait_for_virtual_network_ready,
)
from tests.e2e.core.k8s_client import K8sClient
from tests.e2e.core.osac_cli import OsacCLI
from tests.e2e.core.runner import env


# ---------------------------------------------------------------------------
# Override the CaaS-level SA-auth CLI with a JWT-authenticated CLI.
# The root conftest ``cli`` fixture already uses JWT; importing it here
# shadows the CaaS conftest override so tests under this directory use
# tenant-scoped JWT auth by default.
# ---------------------------------------------------------------------------
@pytest.fixture(scope="session")
def cli(
    namespace: str, fulfillment_address: str, keycloak_url: str, jwt_username: str, jwt_password: str
) -> Iterator[OsacCLI]:
    """JWT-authenticated CLI for CaaS networking tests (tenant-scoped)."""

    def _make_jwt_token_script(kc_url: str, username: str, password: str) -> str:
        return (
            f"curl -sk -X POST {kc_url}/realms/osac/protocol/openid-connect/token"
            f" -d grant_type=password -d client_id=osac-cli"
            f" -d username={username} -d password={password} -d 'scope=openid organization'"
            " | python3 -c \"import sys,json;print(json.load(sys.stdin)['access_token'])\""
        )

    instance = OsacCLI(
        binary=env("OSAC_CLI_PATH", "osac"),
        address=f"https://{fulfillment_address.rsplit(':', 1)[0]}",
        token_script=_make_jwt_token_script(keycloak_url, jwt_username, jwt_password),
        namespace=namespace,
    )
    yield instance
    instance.close()


# ---------------------------------------------------------------------------
# CaaS fixtures (template, pull secret, SSH key)
# ---------------------------------------------------------------------------
@pytest.fixture(scope="session")
def cluster_template() -> str:
    """CaaS cluster template name, overridable via OSAC_CLUSTER_TEMPLATE."""
    return env("OSAC_CLUSTER_TEMPLATE", "ocp-ci-small")


@pytest.fixture(scope="session")
def pull_secret_path() -> str:
    """Filesystem path to the OCP pull secret (OSAC_PULL_SECRET_PATH)."""
    return env("OSAC_PULL_SECRET_PATH")


@pytest.fixture(scope="session")
def ssh_public_key_path() -> str:
    """Filesystem path to the SSH public key (OSAC_SSH_PUBLIC_KEY_PATH)."""
    return env("OSAC_SSH_PUBLIC_KEY_PATH", os.path.expanduser("~/.ssh/id_rsa.pub"))


# ---------------------------------------------------------------------------
# Managed networking: VirtualNetwork -> Subnet -> SecurityGroup
# ---------------------------------------------------------------------------
@pytest.fixture(scope="session")
def caas_networking(grpc: GRPCClient, k8s_hub_client: K8sClient) -> Iterator[dict[str, str]]:
    """Create managed networking resources for CaaS cluster tests.

    Follows the VMaaS ``default_networking`` pattern exactly:
    create VN -> Subnet -> SG, wait each Ready, yield names, cleanup in
    reverse order (SG -> Subnet -> VN).

    Returns a dict with keys:
        vn_name, vn_id, vn_cr,
        subnet_name, subnet_id, subnet_cr,
        sg_name, sg_id, sg_cr
    """
    prefix = unique_name("caas-net")
    vn_name = f"{prefix}-vn"
    subnet_name = f"{prefix}-sub"
    sg_name = f"{prefix}-sg"

    vn_id: str | None = None
    vn_cr: str | None = None
    subnet_id: str | None = None
    subnet_cr: str | None = None
    sg_id: str | None = None
    sg_cr: str | None = None

    try:
        # VirtualNetwork
        print(f"\nCreating VirtualNetwork: {vn_name}")
        vn_id = grpc.create_virtual_network(name=vn_name, ipv4_cidr="10.210.0.0/16")
        vn_cr = wait_for_virtual_network_cr(k8s=k8s_hub_client, uuid=vn_id)
        wait_for_virtual_network_ready(k8s=k8s_hub_client, name=vn_cr)
        print(f"VirtualNetwork {vn_cr} is Ready")

        # Subnet
        print(f"Creating Subnet: {subnet_name}")
        subnet_id = grpc.create_subnet(name=subnet_name, virtual_network=vn_id, ipv4_cidr="10.210.1.0/24")
        subnet_cr = wait_for_subnet_cr(k8s=k8s_hub_client, uuid=subnet_id)
        wait_for_subnet_ready(k8s=k8s_hub_client, name=subnet_cr)
        print(f"Subnet {subnet_cr} is Ready")

        # SecurityGroup
        print(f"Creating SecurityGroup: {sg_name}")
        sg_id = grpc.create_security_group(name=sg_name, virtual_network=vn_id)
        sg_cr = wait_for_security_group_cr(k8s=k8s_hub_client, uuid=sg_id)
        wait_for_security_group_ready(k8s=k8s_hub_client, name=sg_cr)
        print(f"SecurityGroup {sg_cr} is Ready")

        yield {
            "vn_name": vn_name,
            "vn_id": vn_id,
            "vn_cr": vn_cr,
            "subnet_name": subnet_name,
            "subnet_id": subnet_id,
            "subnet_cr": subnet_cr,
            "sg_name": sg_name,
            "sg_id": sg_id,
            "sg_cr": sg_cr,
        }
    finally:
        # Cleanup in reverse order: SG -> Subnet -> VN
        if sg_id and sg_cr:
            try:
                print(f"Deleting SecurityGroup {sg_id}...")
                grpc.delete_security_group(sg_id=sg_id)
                wait_for_security_group_deletion(k8s=k8s_hub_client, name=sg_cr)
                print(f"SecurityGroup {sg_id} deleted")
            except Exception as e:
                print(f"WARNING: Failed to delete SecurityGroup {sg_id}: {e}")
        if subnet_id and subnet_cr:
            try:
                print(f"Deleting Subnet {subnet_id}...")
                grpc.delete_subnet(subnet_id=subnet_id)
                wait_for_subnet_deletion(k8s=k8s_hub_client, name=subnet_cr)
                print(f"Subnet {subnet_id} deleted")
            except Exception as e:
                print(f"WARNING: Failed to delete Subnet {subnet_id}: {e}")
        if vn_id and vn_cr:
            try:
                print(f"Deleting VirtualNetwork {vn_id}...")
                grpc.delete_virtual_network(vn_id=vn_id)
                wait_for_virtual_network_deletion(k8s=k8s_hub_client, name=vn_cr)
                print(f"VirtualNetwork {vn_id} deleted")
            except Exception as e:
                print(f"WARNING: Failed to delete VirtualNetwork {vn_id}: {e}")
