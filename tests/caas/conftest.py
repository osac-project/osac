from __future__ import annotations

import os

import pytest

from tests.core.runner import env


@pytest.fixture(scope="session")
def cluster_template() -> str:
    return env("OSAC_CLUSTER_TEMPLATE", "osac.templates.ocp_4_17_small")


@pytest.fixture(scope="session")
def pull_secret_path() -> str:
    return env("OSAC_PULL_SECRET_PATH")


@pytest.fixture(scope="session")
def ssh_public_key_path() -> str:
    return env("OSAC_SSH_PUBLIC_KEY_PATH", os.path.expanduser("~/.ssh/id_rsa.pub"))
