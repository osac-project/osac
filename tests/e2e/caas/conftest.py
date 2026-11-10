from __future__ import annotations

import os
import shlex
from collections.abc import Iterator
from pathlib import Path

import pytest

from tests.e2e.core.helpers import registered_ssh_key_secret
from tests.e2e.core.osac_cli import OsacCLI
from tests.e2e.core.runner import env


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
