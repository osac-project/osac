"""Fixtures and selection rules shared by the Netris VMaaS networking tests."""

from __future__ import annotations

import os
from pathlib import Path

import pytest

from tests.e2e.vmaas.conftest import default_disk_image, default_instance_type, test_run_id  # noqa: F401

EXTERNAL_PATH_FLAG = "OSAC_NETRIS_VM_EXTERNAL_PATH_ENABLED"
EXTERNAL_PATH_PREREQUISITE = (
    f"Set {EXTERNAL_PATH_FLAG}=true after the Netris VMaaS ExternalIP/EgressIP route and return policy is configured; "
    "it gates only the external_ip and references directories, not the NAT and ExternalIP tests of the main suite"
)

# These two directories are not BMaaS parity: they stay behind the flag and run after the main suite.
_EXTERNAL_PATH_SUITES = (Path(__file__).parent / "external_ip", Path(__file__).parent / "references")

# Not gated (it sends no traffic), but it runs after the main suite so that a failure cannot hide it under -x.
_AFTER_MAIN_SUITE = (Path(__file__).parent / "test_external_ip_billing.py",)


def external_path_enabled() -> bool:
    return os.environ.get(EXTERNAL_PATH_FLAG, "").strip().lower() == "true"


def pytest_collection_modifyitems(config: pytest.Config, items: list[pytest.Item]) -> None:
    """Run the main suite, then the billing test, then skip the ExternalIP and IP-reference suites unless enabled.

    pytest collects directories before sibling files, and the lab runs with -x: a failure in the
    external directories would otherwise hide the whole main suite. Items outside this directory
    keep their positions.
    """
    suite_dir = Path(__file__).parent
    positions = [i for i, item in enumerate(items) if item.path.is_relative_to(suite_dir)]

    def rank(item: pytest.Item) -> int:
        if any(item.path.is_relative_to(suite) for suite in _EXTERNAL_PATH_SUITES):
            return 2
        return 1 if item.path in _AFTER_MAIN_SUITE else 0

    ordered = sorted((items[i] for i in positions), key=rank)
    for position, item in zip(positions, ordered, strict=True):
        items[position] = item
    if external_path_enabled():
        return
    skip = pytest.mark.skip(reason=EXTERNAL_PATH_PREREQUISITE)
    for item in items:
        if any(item.path.is_relative_to(suite) for suite in _EXTERNAL_PATH_SUITES):
            item.add_marker(skip)
