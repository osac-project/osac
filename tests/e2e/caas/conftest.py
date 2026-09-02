from __future__ import annotations

import os
import shlex
from collections.abc import Iterator

import pytest

from tests.e2e.core.osac_cli import OsacCLI
from tests.e2e.core.runner import env


# TODO(OSAC-1060): Remove this override once the osac-operator storage controller
# handles CaaS cluster deletion independently of tenant-level storage provisioning.
# With JWT auth the cluster lands in tenant1, which triggers cluster storage provisioning
# via AAP. When that job fails (no storage backends configured), the cluster-storage
# finalizer blocks ClusterOrder deletion indefinitely. Using SA auth places the cluster
# in the shared tenant, which the storage controller skips.
@pytest.fixture(scope="session")
def cli(namespace: str, fulfillment_address: str, service_account: str) -> Iterator[OsacCLI]:
    """Session-scoped osac CLI authenticated with a service-account token."""
    instance = OsacCLI(
        binary=env("OSAC_CLI_PATH", "osac"),
        address=f"https://{fulfillment_address.rsplit(':', 1)[0]}",
        token_script=f"oc create token -n {shlex.quote(namespace)} {shlex.quote(service_account)} --as system:admin",
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
