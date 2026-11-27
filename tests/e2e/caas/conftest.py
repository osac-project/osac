from __future__ import annotations

import os
import shlex
from collections.abc import Iterator
from pathlib import Path

import pytest

from tests.e2e.core import failure_snapshot
from tests.e2e.core.helpers import registered_ssh_key_secret
from tests.e2e.core.k8s_client import K8sClient, service_account_token_args
from tests.e2e.core.osac_cli import OsacCLI
from tests.e2e.core.runner import env


@pytest.fixture(scope="session")
def k8s_target_client(k8s_hub_client: K8sClient) -> K8sClient:
    """Kubernetes client for the cluster that owns the CaaS outer resources.

    OSAC_TARGET_KUBECONFIG identifies it explicitly. In a vcluster run the hub is
    the vcluster, so Agents, InfraEnvs, HostedClusters, NodePools, the
    HostedControlPlane and its kubeconfig Secret live on the outer cluster while
    OSAC_HUB_KUBECONFIG (or KUBECONFIG) is the vcluster one. Unset keeps every
    read on the hub, which is how a single-cluster deployment runs.
    """
    kubeconfig = env("OSAC_TARGET_KUBECONFIG", "")
    if not kubeconfig.strip():
        return k8s_hub_client
    client = K8sClient(namespace=k8s_hub_client.namespace, kubeconfig=kubeconfig.strip())
    # Fail on broken initial outer-cluster access before entering retrying polls.
    # kube-system is cluster-scoped and always present, unlike the install
    # namespace, whose name belongs to the hub.
    client.get_json(resource="namespace", name="kube-system")
    return client


@pytest.fixture(scope="module", autouse=True)
def caas_failure_snapshot_sources(
    k8s_hub_client: K8sClient, k8s_target_client: K8sClient, namespace: str
) -> Iterator[None]:
    """Keep CaaS management and guest reads scoped to CaaS runs."""
    failure_snapshot.SOURCES.register(
        "caas", failure_snapshot.caas_source(k8s_hub_client, namespace, k8s_target_client)
    )
    failure_snapshot.SOURCES.register(
        "caas-guest", failure_snapshot.caas_guest_source(k8s_hub_client, namespace, k8s_target_client)
    )
    yield
    failure_snapshot.SOURCES.unregister("caas")
    failure_snapshot.SOURCES.unregister("caas-guest")


# Default to the tenant-admin JWT so Clusters and their workers are tenant-owned.
# The optional service-account path must also use a tenant-scoped identity:
# administrator identities default to shared, where Cluster creation is rejected.
# Switching authentication is not a workaround for tenant storage teardown failures.
@pytest.fixture(scope="session")
def cli(namespace: str, fulfillment_address: str, service_account: str, jwt_cli_admin: OsacCLI) -> Iterator[OsacCLI]:
    if env("OSAC_CAAS_USE_JWT", "true").lower() == "true":
        yield jwt_cli_admin
        return
    instance = OsacCLI(
        binary=env("OSAC_CLI_PATH", "osac"),
        address=f"https://{fulfillment_address.rsplit(':', 1)[0]}",
        token_script=shlex.join(
            ["oc", "create", "token", *service_account_token_args(namespace=namespace, service_account=service_account)]
        ),
        namespace=namespace,
    )
    yield instance
    instance.close()


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
    """Filesystem path to the SSH public key, default ~/.ssh/id_rsa.pub (OSAC_SSH_PUBLIC_KEY_PATH)."""
    return env("OSAC_SSH_PUBLIC_KEY_PATH", os.path.expanduser("~/.ssh/id_rsa.pub"))


@pytest.fixture(scope="session")
def ssh_key_secret(cli: OsacCLI, ssh_public_key_path: str, test_run_id: str) -> Iterator[str]:
    """Register the configured SSH public key as a tenant Secret for CaaS journeys.

    The Cluster API only accepts a registered SSH-key Secret reference, so the
    suite registers the key once per session with the same CLI identity that
    creates the clusters, and cleans it up afterward.
    """
    public_key = Path(ssh_public_key_path).read_text().strip()
    with registered_ssh_key_secret(cli, public_key, f"e2e-caas-key-{test_run_id}") as name:
        yield name


@pytest.fixture(scope="session")
def test_run_id() -> str:
    from uuid import uuid4

    return str(uuid4())[:8]
