from __future__ import annotations

import os
import uuid
from pathlib import Path

import pytest

from tests.e2e.bmaas.regression.networking import bmi_ssh
from tests.e2e.core.grpc_client import GRPCClient
from tests.e2e.core.runner import env


@pytest.fixture(scope="session")
def external_ip_pool_name() -> str:
    return env("OSAC_EXTERNAL_IP_POOL", "tenant-external-pool")


@pytest.fixture(scope="session")
def external_ip_pool_cidr() -> str:
    return env("OSAC_EXTERNAL_IP_POOL_CIDR", "198.51.100.24/29")


@pytest.fixture(scope="session")
def auto_eip_catalog_item_name() -> str:
    return env("OSAC_BMI_AUTO_EIP_CATALOG_ITEM", "ci-bm-auto-eip")


@pytest.fixture(scope="session")
def mgmt_cluster_ip() -> str:
    return env("OSAC_MGMT_CLUSTER_IP", "192.168.40.2")


@pytest.fixture(scope="session")
def net_test_run_id() -> str:
    return uuid.uuid4().hex[:8]


@pytest.fixture(scope="session")
def bmi_template() -> str:
    return env("OSAC_BMI_TEMPLATE", "bm-host-provisioning")


@pytest.fixture(scope="session")
def bmh_namespace() -> str:
    return env("OSAC_BMH_NAMESPACE", "host-inventory")


@pytest.fixture(scope="session")
def bmh_ssh_hosts() -> dict[str, str]:
    return bmi_ssh.parse_ssh_hosts(env("OSAC_BMH_SSH_HOSTS"))


@pytest.fixture(scope="session")
def catalog_item_name() -> str:
    return env("OSAC_BMI_CATALOG_ITEM", "ci-bm-default")


@pytest.fixture(scope="session")
def bmi_instance_type(private_grpc: GRPCClient) -> str:
    """Send spec.instance_type only when that BareMetalInstanceType exists.

    Labs often export OSAC_BMI_INSTANCE_TYPE=default even when no types are cataloged.
    """
    requested = (
        os.environ.get("OSAC_BMI_INSTANCE_TYPE", "").strip() or os.environ.get("OSAC_BM_HOST_TYPE", "").strip()
    )
    if not requested:
        return ""
    items = private_grpc.call(service="osac.private.v1.BareMetalInstanceTypes/List").get("items") or []
    names = {str((item.get("metadata") or {}).get("name") or "") for item in items}
    return requested if requested in names else ""


@pytest.fixture(scope="session")
def bmi_user_data() -> str:
    path = os.environ.get("OSAC_BMI_USER_DATA_FILE", "").strip()
    if path:
        return Path(path).read_text()
    inline = os.environ.get("OSAC_BMI_USER_DATA", "").strip()
    if inline:
        return inline
    return "#cloud-config\nmanage_etc_hosts: false\n"


@pytest.fixture(scope="session")
def net_ssh_public_key() -> str:
    """Pubkey injected into the BMI must match OSAC_BMI_SSH_IDENTITY used by guest_ssh.

    OSAC_BMI_SSH_PUBLIC_KEY may be a file path or the key material (ssh-ed25519 …).
    """
    explicit = os.environ.get("OSAC_BMI_SSH_PUBLIC_KEY", "").strip()
    if explicit:
        if explicit.startswith(("ssh-", "ecdsa-", "sk-")):
            return explicit
        return Path(explicit).read_text().strip()
    identity = os.environ.get("OSAC_BMI_SSH_IDENTITY", "/root/.ssh/id_rsa")
    pub = Path(identity + ".pub")
    if pub.is_file():
        return pub.read_text().strip()
    return Path(env("OSAC_BMI_SSH_PUBLIC_KEY", "/root/.ssh/id_rsa.pub")).read_text().strip()
