from __future__ import annotations

import os

import pytest

from tests.e2e.core.runner import env


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
