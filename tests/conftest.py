from __future__ import annotations

import os

import pytest

from tests.fulfillment_cli import FulfillmentCLI
from tests.grpc_client import GRPCClient
from tests.k8s_client import K8sClient
from tests.runner import env, run


@pytest.fixture(scope="session")
def namespace() -> str:
    return env("OSAC_NAMESPACE", "osac-devel")


@pytest.fixture(scope="session")
def cluster_domain() -> str:
    return run("kubectl", "get", "ingress.config.openshift.io", "cluster", "-o", "jsonpath={.spec.domain}")


@pytest.fixture(scope="session")
def fulfillment_address(namespace: str, cluster_domain: str) -> str:
    return env("OSAC_FULFILLMENT_ADDRESS", f"fulfillment-api-{namespace}.{cluster_domain}:443")


@pytest.fixture(scope="session")
def service_account() -> str:
    return env("OSAC_SERVICE_ACCOUNT", "admin")


@pytest.fixture(scope="session")
def grpc(fulfillment_address: str, namespace: str, service_account: str) -> GRPCClient:
    token: str = run(
        "oc", "create", "token", service_account, "-n", namespace, "--duration", "1h", "--as", "system:admin"
    )
    return GRPCClient(address=fulfillment_address, token=token)


@pytest.fixture(scope="session")
def k8s_hub_client(namespace: str) -> K8sClient:
    return K8sClient(namespace=namespace)


@pytest.fixture(scope="session")
def k8s_virt_client(namespace: str) -> K8sClient:
    vm_kubeconfig: str = os.environ["OSAC_VM_KUBECONFIG"]
    return K8sClient(namespace=namespace, kubeconfig=vm_kubeconfig)


@pytest.fixture(scope="session")
def cli(namespace: str, fulfillment_address: str, service_account: str) -> FulfillmentCLI:
    return FulfillmentCLI(
        binary=env("FULFILLMENT_CLI_PATH", "fulfillment-cli"),
        address=f"https://{fulfillment_address.rsplit(':', 1)[0]}",
        token_script=f"oc create token -n {namespace} {service_account} --as system:admin",
        namespace=namespace,
    )


@pytest.fixture(scope="session")
def vm_template() -> str:
    return env("OSAC_VM_TEMPLATE", "osac.templates.ocp_virt_vm")
