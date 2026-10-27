from __future__ import annotations

import contextlib
import os
import subprocess
import textwrap
import time
from collections.abc import Iterator
from pathlib import Path

import pytest

from tests.e2e.core.caas_versions import ensure_caas_disk_image_version
from tests.e2e.core.grpc_client import PRIVATE_API, GRPCClient
from tests.e2e.core.helpers import unique_name, wait_for_grpc_subnet_ready, wait_for_tenant_condition
from tests.e2e.core.k8s_client import K8sClient
from tests.e2e.core.keycloak import get_jwt
from tests.e2e.core.keycloak_admin import (
    add_user_to_organization,
    add_user_to_organization_group,
    ensure_organization_group,
    get_admin_token,
    get_user_id,
    wait_for_organization,
)
from tests.e2e.core.metering import MeteringCollector
from tests.e2e.core.osac_cli import OsacCLI
from tests.e2e.core.runner import env, poll_until, run, run_unchecked


@pytest.fixture(scope="session")
def default_storage_tier() -> str:
    """Reference installer-provided storage tier.

    Defaults to "local" tier created by osac-installer when lvms.enabled=true.
    Available to all suites that create ComputeInstances (VMaaS and references).
    """
    return env("OSAC_STORAGE_TIER", "local")


def _requires_serial_xdist(args: list[str]) -> bool:
    """True when CLI targets a suite that must run sequentially.

    CaaS, BMaaS serial/full, and enablement suites require ``-n 0``.
    Broader invocations like ``pytest tests/`` are not detected.
    """
    normalized = [str(a).replace("\\", "/").rstrip("/") for a in args]
    if not normalized:
        return False
    return any(
        a.endswith("e2e/bmaas/serial")
        or "/e2e/bmaas/serial/" in (a + "/")
        or a.endswith("tests/e2e/bmaas")
        or a.endswith("/e2e/bmaas")
        or a == "e2e/bmaas"
        or a.endswith("e2e/caas")
        or "/e2e/caas/" in (a + "/")
        or a.endswith("e2e/networking")
        or "/e2e/networking/" in (a + "/")
        or a.endswith("e2e/enablement")
        or "/e2e/enablement/" in (a + "/")
        for a in normalized
    )


@pytest.hookimpl(tryfirst=True)
def pytest_cmdline_main(config: pytest.Config) -> None:
    """Force serial xdist before xdist's cmdline hook creates worker specs.

    pyproject ``addopts -n 4`` is already parsed; setting ``numprocesses`` in
    ``pytest_configure`` is too late because xdist's ``tryfirst`` cmdline hook
    has already populated ``config.option.tx``. Must return None (cmdline_main
    is firstresult).
    """
    if not _requires_serial_xdist(list(config.args or [])):
        return None
    config.option.numprocesses = 0
    if hasattr(config.option, "dist"):
        config.option.dist = "no"
    if hasattr(config.option, "tx"):
        config.option.tx = []
    return None


def pytest_configure(config: pytest.Config) -> None:
    """Give each xdist worker its own log_file so records stay chronologically ordered.

    A shared log_file is interleaved across workers as they report at their own
    pace; gather-osac-logs.sh merges the per-worker files back into one sorted
    e2e.log artifact.
    """
    config.addinivalue_line("markers", "metering: test verifies metering events via the test adapter HTTP API")
    config.addinivalue_line("markers", "caas_cluster_create_focus: temporarily isolate the primary CaaS PR E2E")
    config.addinivalue_line("markers", "requires_caas: test requires the CaaS service to be enabled")
    config.addinivalue_line("markers", "requires_bmaas: test requires the BMaaS service to be enabled")
    config.addinivalue_line("markers", "requires_vmaas: test requires the VMaaS service to be enabled")
    config.addinivalue_line("markers", "requires_netris: test requires the Netris EVPN fabric")
    config.addinivalue_line("markers", "requires_netris_vmaas: test requires Netris VMaaS networking")
    config.addinivalue_line("markers", "requires_netris_bmaas: test requires Netris BMaaS networking")
    config.addinivalue_line(
        "markers", "requires_stretched_networking: test requires BM and VM connectivity on a shared subnet"
    )
    config.addinivalue_line("markers", "reference_common: reference test shared by all service profiles")
    config.addinivalue_line("markers", "reference_networking: shared networking reference test")
    config.addinivalue_line("markers", "sanity: fast, low-risk smoke test suitable for every PR")
    config.addinivalue_line("markers", "regression: broader/slower coverage, run on a schedule or on demand")
    config.addinivalue_line(
        "markers", "serial: must run alone, not in parallel with other tests (e.g. exhausts a shared resource)"
    )
    worker_id = os.environ.get("PYTEST_XDIST_WORKER")
    if worker_id is not None:
        log_dir = Path(config.getini("log_file")).parent
        log_dir.mkdir(parents=True, exist_ok=True)
        config.option.log_file = str(log_dir / f"e2e_{worker_id}.log")


@pytest.fixture(scope="session")
def namespace() -> str:
    return env("OSAC_NAMESPACE", "osac-devel")


def _apply_manifest(manifest: str) -> None:
    subprocess.run(["oc", "apply", "-f", "-"], input=manifest, text=True, capture_output=True, check=True, timeout=120)


@pytest.fixture(scope="session", autouse=True)
def cluster_tenants(namespace: str, k8s_hub_client: K8sClient) -> None:
    """Apply the install namespace's Tenant and the shared Tenant before tests.

    The E2E workflows apply these two CRs between install and test; doing it
    here keeps a bare pytest run against a deployed install self-sufficient.
    Applying is idempotent, so parallel workers and repeated runs converge on
    the same objects.
    """
    for name in sorted({namespace, "shared"}):
        _apply_manifest(
            "apiVersion: osac.openshift.io/v1alpha1\n"
            "kind: Tenant\n"
            "metadata:\n"
            f"  name: {name}\n"
            f"  namespace: {namespace}\n"
            "spec: {}\n"
        )
    # Per-tenant StorageClasses come from an AAP job; wait for the test
    # tenant's classes so provisioning jobs do not queue behind storage jobs.
    # When the condition never appears the environment has no storage
    # configured and the wait is skipped.
    condition = ""
    for _ in range(6):
        condition = k8s_hub_client.get_tenant_condition_status(
            name=namespace, condition_type="ClusterStorageReady", checked=False
        )
        if condition:
            break
        time.sleep(5)
    if condition and condition != "True":
        wait_for_tenant_condition(k8s=k8s_hub_client, name=namespace, condition_type="ClusterStorageReady")


@pytest.fixture(scope="session")
def cluster_domain() -> str:
    return run("kubectl", "get", "ingress.config.openshift.io", "cluster", "-o", "jsonpath={.spec.domain}")


@pytest.fixture(scope="session")
def fulfillment_address(namespace: str, cluster_domain: str) -> str:
    return env("OSAC_FULFILLMENT_ADDRESS", f"fulfillment-api-{namespace}.{cluster_domain}:443")


@pytest.fixture(scope="session")
def fulfillment_private_address(namespace: str, cluster_domain: str) -> str:
    return env("OSAC_FULFILLMENT_PRIVATE_ADDRESS", f"fulfillment-internal-api-{namespace}.{cluster_domain}:443")


@pytest.fixture(scope="session")
def service_account() -> str:
    return env("OSAC_SERVICE_ACCOUNT", "admin")


@pytest.fixture(scope="session")
def keycloak_realm() -> str:
    return env("OSAC_KEYCLOAK_REALM", "osac")


@pytest.fixture(scope="session")
def keycloak_client_id() -> str:
    return env("OSAC_KEYCLOAK_CLIENT_ID", "osac-cli")


@pytest.fixture(scope="session")
def jwt_username() -> str:
    return env("OSAC_JWT_USERNAME", "tenant1_admin")


@pytest.fixture(scope="session")
def jwt_tenant(jwt_username: str) -> str:
    """The fulfillment tenant the JWT user belongs to.

    The mapping mirrors ensure_jwt_users: every fixture user of tenant1 maps
    to the tenant1 tenant, and tenant2's users to tenant2.
    """
    return f"tenant{jwt_username.removeprefix('tenant').split('_')[0]}"


@pytest.fixture(scope="session")
def grpc(
    fulfillment_address: str,
    keycloak_url: str,
    keycloak_realm: str,
    keycloak_client_id: str,
    jwt_username: str,
    jwt_password: str,
) -> GRPCClient:
    return GRPCClient(
        address=fulfillment_address,
        token_factory=lambda: get_jwt(
            keycloak_url=keycloak_url,
            realm=keycloak_realm,
            client_id=keycloak_client_id,
            username=jwt_username,
            password=jwt_password,
        ),
    )


@pytest.fixture(scope="session")
def private_grpc(fulfillment_private_address: str, namespace: str, service_account: str) -> GRPCClient:
    token: str = run(
        "oc", "create", "token", service_account, "-n", namespace, "--duration", "4h", "--as", "system:admin"
    )
    return GRPCClient(address=fulfillment_private_address, token=token)


@pytest.fixture(scope="session")
def caas_disk_image_version(private_grpc: GRPCClient) -> str:
    """Explicit backed version for positive bare-metal CaaS scenarios."""
    return ensure_caas_disk_image_version(private_grpc)


@pytest.fixture(scope="session", autouse=True)
def ensure_tenants(ensure_k8s_only_network_class: None, private_grpc: GRPCClient) -> None:
    for name in ("tenant1", "tenant2"):
        private_grpc.ensure_tenant(name=name)


@pytest.fixture(scope="session", autouse=True)
def _wait_for_default_subnets_ready(
    ensure_jwt_users: None, setup_organization_memberships: None, grpc: GRPCClient
) -> None:
    """Wait for tenant-default subnets to reach READY in the fulfillment database.

    Tenant creation triggers the DefaultNetworkingProvisioner which creates a
    default VirtualNetwork, Subnet, and SecurityGroup in SUBNET_STATE_PENDING.
    The osac-operator marks the K8s CRs Ready, then the subnet feedback
    controller syncs that state back to PostgreSQL.  Tests that implicitly
    reference these subnets (e.g. BareMetalInstance creation inherits the
    tenant's default subnet) hit FailedPrecondition if the DB update hasn't
    landed yet.

    Depends on ``ensure_jwt_users`` (which itself depends on ``ensure_tenants``)
    so that the ``grpc`` client's first call does not trigger JIT user
    provisioning before ``ensure_jwt_users`` creates the RoleBinding.

    Depends on ``setup_organization_memberships`` so that the Keycloak
    organization membership is in place before the first JWT is obtained;
    without it the token may lack the tenant claim and
    ``list_subnet_ids()`` returns an empty list (``WHERE tenant = $1``
    receives an empty string).
    """
    # Timeout must exceed 2x the operator's statusPollInterval (30s) to
    # accommodate two sequential polling cycles (VirtualNetwork -> Subnet).
    subnet_ids: list[str] = poll_until(
        fn=lambda: grpc.list_subnet_ids(),
        until=lambda ids: len(ids) > 0,
        retries=60,
        delay=2,
        description="at least one subnet to appear in gRPC",
        retry_on_error=True,
    )
    for subnet_id in subnet_ids:
        wait_for_grpc_subnet_ready(grpc=grpc, subnet_id=subnet_id)


@pytest.fixture(scope="session", autouse=True)
def ensure_jwt_users(ensure_tenants: None, private_grpc: GRPCClient) -> None:
    """Pre-create users that JWT fixtures authenticate as, so JIT provisioning
    doesn't race on concurrent first requests from xdist workers."""
    for username, tenant in [
        ("tenant1_admin", "tenant1"),
        ("tenant1_user", "tenant1"),
        ("tenant2_user", "tenant2"),
        ("tenant2_admin", "tenant2"),
    ]:
        with contextlib.suppress(subprocess.CalledProcessError):
            private_grpc.call(
                service=f"{PRIVATE_API}.Users/Create",
                data={
                    "object": {
                        "metadata": {"name": username.replace("_", "-"), "tenant": tenant},
                        "spec": {"username": username, "enabled": True},
                    }
                },
            )


@pytest.fixture(scope="session")
def keycloak_admin_password() -> str:
    return env("OSAC_KEYCLOAK_ADMIN_PASSWORD", "admin")


@pytest.fixture(scope="session", autouse=True)
def setup_organization_memberships(ensure_tenants: None, keycloak_url: str, keycloak_admin_password: str) -> None:
    """
    Add test users to their corresponding Keycloak organizations.
    This runs after ensure_tenants creates the Tenant resources, which the
    tenant controller syncs to Keycloak as organizations.
    """
    # Get admin token for Keycloak admin API
    admin_token = get_admin_token(keycloak_url=keycloak_url, username="admin", password=keycloak_admin_password)

    # Map of organization name -> list of usernames
    org_users = {"tenant1": ["tenant1_user", "tenant1_admin"], "tenant2": ["tenant2_user", "tenant2_admin"]}

    for org_name, usernames in org_users.items():
        admin_token = get_admin_token(keycloak_url=keycloak_url, username="admin", password=keycloak_admin_password)
        # Wait for the organization to be synced to Keycloak by the tenant controller
        org_id = wait_for_organization(keycloak_url=keycloak_url, admin_token=admin_token, org_name=org_name)
        admin_token = get_admin_token(keycloak_url=keycloak_url, username="admin", password=keycloak_admin_password)

        # Add each user to the organization
        for username in usernames:
            user_id = get_user_id(keycloak_url=keycloak_url, admin_token=admin_token, username=username)
            add_user_to_organization(
                keycloak_url=keycloak_url,
                admin_token=admin_token,
                org_id=org_id,
                user_id=user_id,
                username=username,
                org_name=org_name,
            )

        # Create /members group in the organization and add all users to it
        # This is required for the organization scope to include the organization in the JWT token
        group_id = ensure_organization_group(
            keycloak_url=keycloak_url, admin_token=admin_token, org_id=org_id, org_name=org_name
        )

        for username in usernames:
            user_id = get_user_id(keycloak_url=keycloak_url, admin_token=admin_token, username=username)
            add_user_to_organization_group(
                keycloak_url=keycloak_url,
                admin_token=admin_token,
                org_id=org_id,
                group_id=group_id,
                user_id=user_id,
                username=username,
                org_name=org_name,
            )


@pytest.fixture(scope="session")
def k8s_hub_client(namespace: str) -> K8sClient:
    return K8sClient(namespace=namespace)


_K8S_ONLY_NETWORK_MANAGER_CONFIGMAP = textwrap.dedent("""\
    apiVersion: v1
    kind: ConfigMap
    metadata:
      name: osac-network-k8s-manager-k8s-only
      namespace: {namespace}
      labels:
        osac.openshift.io/network-k8s-manager: "true"
    data:
      name: k8s_only
      description: "Composite k8s-only manager (CUDN + NetworkPolicy + MetalLB), no separate physical fabric"
      capabilities: "ipv4"
""")


@pytest.fixture(scope="session", autouse=True)
def ensure_k8s_only_network_class(private_grpc: GRPCClient, k8s_hub_client: K8sClient, namespace: str) -> None:
    """No-op unless OSAC_NETWORK_CLASS_K8S_ONLY is set. When set, registers the
    k8s_only k8s-manager ConfigMap and points the environment's singleton
    NetworkClass at it (k8s_manager: k8s_only, fabric_manager cleared)."""
    if not env("OSAC_NETWORK_CLASS_K8S_ONLY", ""):
        return

    k8s_hub_client.apply(manifest=_K8S_ONLY_NETWORK_MANAGER_CONFIGMAP.format(namespace=namespace))

    network_classes = private_grpc.list_network_classes()
    if not network_classes:
        raise RuntimeError(
            "OSAC_NETWORK_CLASS_K8S_ONLY is set but no NetworkClass exists — "
            "expected the platform default to already be published by the publish-templates job"
        )
    private_grpc.update_network_class(
        network_class_id=network_classes[0]["id"], fabric_manager="", k8s_manager="k8s_only"
    )


@pytest.fixture(scope="session")
def cli(
    namespace: str, fulfillment_address: str, keycloak_url: str, jwt_username: str, jwt_password: str
) -> Iterator[OsacCLI]:
    instance = OsacCLI(
        binary=env("OSAC_CLI_PATH", "osac"),
        address=f"https://{fulfillment_address.rsplit(':', 1)[0]}",
        token_script=_make_jwt_token_script(keycloak_url, jwt_username, jwt_password),
        namespace=namespace,
    )
    yield instance
    instance.close()


@pytest.fixture
def pull_secret_name(cli: OsacCLI, pull_secret_path: str) -> Iterator[str]:
    """Create a tenant-scoped pull Secret for a CaaS cluster, then remove it."""
    name = unique_name("e2e-pull-secret")
    cli.create_secret(name=name, from_files={".dockerconfigjson": pull_secret_path}, secret_type="pull-secret")
    try:
        yield name
    finally:
        cli.delete_secret(name=name)


@pytest.fixture(scope="session")
def private_cli(namespace: str, fulfillment_private_address: str, service_account: str) -> Iterator[OsacCLI]:
    instance = OsacCLI(
        binary=env("OSAC_CLI_PATH", "osac"),
        address=f"https://{fulfillment_private_address.rsplit(':', 1)[0]}",
        token_script=f"oc create token -n {namespace} {service_account} --as system:admin",
        namespace=namespace,
        private=True,
    )
    yield instance
    instance.close()


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
        f" -d username={username} -d password={password} -d 'scope=openid organization'"
        " | python3 -c \"import sys,json;print(json.load(sys.stdin)['access_token'])\""
    )


@pytest.fixture(scope="session")
def jwt_cli_user(namespace: str, fulfillment_address: str, keycloak_url: str, jwt_password: str) -> Iterator[OsacCLI]:
    instance = OsacCLI(
        binary=env("OSAC_CLI_PATH", "osac"),
        address=f"https://{fulfillment_address.rsplit(':', 1)[0]}",
        token_script=_make_jwt_token_script(keycloak_url, "tenant1_user", jwt_password),
        namespace=namespace,
    )
    yield instance
    instance.close()


@pytest.fixture(scope="session")
def jwt_cli_admin(namespace: str, fulfillment_address: str, keycloak_url: str, jwt_password: str) -> Iterator[OsacCLI]:
    instance = OsacCLI(
        binary=env("OSAC_CLI_PATH", "osac"),
        address=f"https://{fulfillment_address.rsplit(':', 1)[0]}",
        token_script=_make_jwt_token_script(keycloak_url, "tenant1_admin", jwt_password),
        namespace=namespace,
    )
    yield instance
    instance.close()


@pytest.fixture(scope="session")
def jwt_grpc_tenant1_admin(fulfillment_address: str, keycloak_url: str, jwt_password: str) -> GRPCClient:
    return GRPCClient(
        address=fulfillment_address,
        token_factory=lambda: get_jwt(
            keycloak_url=keycloak_url,
            realm="osac",
            client_id="osac-cli",
            username="tenant1_admin",
            password=jwt_password,
        ),
    )


@pytest.fixture(scope="session")
def jwt_grpc_tenant1(fulfillment_address: str, keycloak_url: str, jwt_password: str) -> GRPCClient:
    return GRPCClient(
        address=fulfillment_address,
        token_factory=lambda: get_jwt(
            keycloak_url=keycloak_url,
            realm="osac",
            client_id="osac-cli",
            username="tenant1_user",
            password=jwt_password,
        ),
    )


@pytest.fixture(scope="session")
def jwt_grpc_tenant2(fulfillment_address: str, keycloak_url: str, jwt_password: str) -> GRPCClient:
    return GRPCClient(
        address=fulfillment_address,
        token_factory=lambda: get_jwt(
            keycloak_url=keycloak_url,
            realm="osac",
            client_id="osac-cli",
            username="tenant2_user",
            password=jwt_password,
        ),
    )


# --- Cross-cutting concern: Metering ---


def _expose_metering_adapter_route(namespace: str) -> str:
    """Expose the echo adapter Service and return its HTTP route URL."""
    service, rc = run_unchecked(
        "oc",
        "get",
        "svc",
        "-n",
        namespace,
        "-l",
        "app.kubernetes.io/component=echo-adapter",
        "-o",
        "jsonpath={.items[0].metadata.name}",
    )
    if rc != 0 or not service:
        raise RuntimeError(f"no echo-adapter Service found in namespace {namespace}")
    # Exposing an existing route fails; the route itself is what matters.
    run_unchecked("oc", "expose", "svc", service, "-n", namespace, "--name=metering-test-adapter")
    host, rc = run_unchecked(
        "oc", "get", "route", "metering-test-adapter", "-n", namespace, "-o", "jsonpath={.spec.host}"
    )
    if rc != 0 or not host:
        raise RuntimeError(f"failed to expose the echo adapter route in namespace {namespace}")
    return f"http://{host}"


def pytest_collection_finish(session: pytest.Session) -> None:
    # Collect-only does not execute metering fixtures or contact the adapter.
    if session.config.getoption("collectonly"):
        return
    metering_tests = [item for item in session.items if item.get_closest_marker("metering")]
    if metering_tests and not os.environ.get("METERING_ADAPTER_URL"):
        try:
            _expose_metering_adapter_route(env("OSAC_NAMESPACE", "osac-devel"))
        except RuntimeError as exc:
            pytest.fail(
                f"METERING_ADAPTER_URL is not set and the echo adapter route could not be exposed "
                f"for {len(metering_tests)} metering test(s): {exc}. "
                "Set the env var, enable metering.echoAdapter, or run with -m 'not metering'.",
                pytrace=False,
            )


@pytest.fixture(scope="session")
def metering_adapter_url(namespace: str) -> str:
    """The metering echo adapter's URL.

    METERING_ADAPTER_URL wins when set (CI exports it after creating the
    route); otherwise the echo adapter Service is exposed here, so a local
    run needs no preparation beyond the install.
    """
    if url := os.environ.get("METERING_ADAPTER_URL"):
        return url
    return _expose_metering_adapter_route(namespace)


@pytest.fixture
def metering(metering_adapter_url: str, jwt_tenant: str) -> Iterator[MeteringCollector]:
    """Composable metering verifier. Inject into any test to verify
    that lifecycle events appear via the test adapter HTTP API.

    Records a start timestamp on setup, verifies all expectations
    (including CloudEvent structure validation) on teardown. Every
    heartbeat must name the JWT user's tenant: the echo adapter
    aggregates every tenant's topics, so only the value catches an
    event published to the wrong tenant.
    """
    collector = MeteringCollector(base_url=metering_adapter_url)
    collector.with_tenant(jwt_tenant)
    collector.start()
    yield collector
    try:
        collector.verify()
    finally:
        collector.stop()
