from __future__ import annotations

import pytest

from tests.core.grpc_client import GRPCClient
from tests.core.k8s_client import K8sClient
from tests.core.keycloak import get_jwt
from tests.core.osac_cli import OsacCLI
from tests.core.runner import env, run


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


@pytest.fixture(scope="session", autouse=True)
def ensure_organizations(grpc: GRPCClient) -> None:
    for name in ("tenant1", "tenant2"):
        grpc.ensure_organization(name=name)


@pytest.fixture(scope="session")
def k8s_hub_client(namespace: str) -> K8sClient:
    return K8sClient(namespace=namespace)


@pytest.fixture(scope="session")
def cli(namespace: str, fulfillment_address: str, service_account: str) -> OsacCLI:
    return OsacCLI(
        binary=env("OSAC_CLI_PATH", "osac"),
        address=f"https://{fulfillment_address.rsplit(':', 1)[0]}",
        token_script=f"oc create token -n {namespace} {service_account} --as system:admin",
        namespace=namespace,
    )


@pytest.fixture(scope="session")
def keycloak_url(cluster_domain: str) -> str:
    return env("OSAC_KEYCLOAK_URL", f"https://keycloak-keycloak.{cluster_domain}")


@pytest.fixture(scope="session")
def jwt_password() -> str:
    return env("OSAC_JWT_PASSWORD", "foobar")


def _make_jwt_token_script(keycloak_url: str, username: str, password: str) -> str:
    return (
        f"curl -sk -X POST {keycloak_url}/realms/osac/protocol/openid-connect/token"
        f" -d grant_type=password -d client_id=osac-cli"
        f" -d username={username} -d password={password} -d scope=openid"
        " | python3 -c \"import sys,json;print(json.load(sys.stdin)['access_token'])\""
    )


@pytest.fixture(scope="session")
def jwt_cli_user(namespace: str, fulfillment_address: str, keycloak_url: str, jwt_password: str) -> OsacCLI:
    return OsacCLI(
        binary=env("OSAC_CLI_PATH", "osac"),
        address=f"https://{fulfillment_address.rsplit(':', 1)[0]}",
        token_script=_make_jwt_token_script(keycloak_url, "my_user", jwt_password),
        namespace=namespace,
    )


@pytest.fixture(scope="session")
def jwt_cli_admin(namespace: str, fulfillment_address: str, keycloak_url: str, jwt_password: str) -> OsacCLI:
    return OsacCLI(
        binary=env("OSAC_CLI_PATH", "osac"),
        address=f"https://{fulfillment_address.rsplit(':', 1)[0]}",
        token_script=_make_jwt_token_script(keycloak_url, "tenant1_admin", jwt_password),
        namespace=namespace,
    )


@pytest.fixture(scope="session")
def jwt_grpc_tenant1(fulfillment_address: str, keycloak_url: str, jwt_password: str) -> GRPCClient:
    token: str = get_jwt(
        keycloak_url=keycloak_url, realm="osac", client_id="osac-cli", username="tenant1_user", password=jwt_password
    )
    return GRPCClient(address=fulfillment_address, token=token)


@pytest.fixture(scope="session")
def jwt_grpc_tenant2(fulfillment_address: str, keycloak_url: str, jwt_password: str) -> GRPCClient:
    token: str = get_jwt(
        keycloak_url=keycloak_url, realm="osac", client_id="osac-cli", username="tenant2_user", password=jwt_password
    )
    return GRPCClient(address=fulfillment_address, token=token)
