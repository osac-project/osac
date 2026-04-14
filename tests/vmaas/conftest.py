from __future__ import annotations

import os

import pytest

from tests.k8s_client import K8sClient
from tests.runner import env


@pytest.fixture(scope="session")
def k8s_virt_client(namespace: str) -> K8sClient:
    vm_kubeconfig: str = os.environ["OSAC_VM_KUBECONFIG"]
    return K8sClient(namespace=namespace, kubeconfig=vm_kubeconfig)


@pytest.fixture(scope="session")
def vm_template() -> str:
    return env("OSAC_VM_TEMPLATE", "osac.templates.ocp_virt_vm")


@pytest.fixture(scope="session")
def network_class() -> str:
    return env("OSAC_NETWORK_CLASS", "osac.templates.cudn_net")
