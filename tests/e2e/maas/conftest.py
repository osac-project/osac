from __future__ import annotations

import pytest


@pytest.fixture(scope="session", autouse=True)
def setup_organization_memberships() -> None:
    """MaaS import uses the private API and does not need tenant JWT setup."""

