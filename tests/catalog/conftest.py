from __future__ import annotations

import pytest

from tests.core.runner import env


@pytest.fixture(scope="session")
def cluster_template() -> str:
    return env("OSAC_CLUSTER_TEMPLATE", "osac.templates.ocp_4_17_small")
