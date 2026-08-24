from __future__ import annotations

import uuid
from pathlib import Path

import pytest

from tests.core.grpc_client import GRPCClient
from tests.core.runner import env


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
def catalog_item_name() -> str:
    return env("OSAC_BMI_CATALOG_ITEM", "ci-bm-default")


@pytest.fixture(scope="session")
def net_ssh_public_key() -> str:
    key_path = Path(env("OSAC_BMI_SSH_PUBLIC_KEY", "/root/.ssh/id_rsa.pub"))
    return key_path.read_text().strip()
