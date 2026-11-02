"""Fixtures of the Netris VMaaS networking tests."""

from __future__ import annotations

import pytest

from tests.e2e.core.metering import MeteringCollector
from tests.e2e.vmaas.conftest import default_disk_image, default_instance_type, test_run_id  # noqa: F401


@pytest.fixture
def billing_metering(request: pytest.FixtureRequest) -> MeteringCollector:
    """The metering collector, or a skip when this profile has no echo adapter.

    The tests carry no ``metering`` marker on purpose: that marker makes the whole session fail at
    collection when no adapter is reachable (tests/e2e/conftest.py), which would take every other test of
    the run down for a profile that only lacks this one dependency.
    """
    try:
        request.getfixturevalue("metering_adapter_url")
    except (RuntimeError, OSError) as exc:
        pytest.skip(f"no metering echo adapter: set METERING_ADAPTER_URL or enable metering.echoAdapter ({exc})")
    return request.getfixturevalue("metering")
