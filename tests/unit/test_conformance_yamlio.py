"""Contract tests for the strict YAML loader shared by every reader."""

from __future__ import annotations

import pytest

from tests.conformance import yamlio
from tests.conformance.errors import ConfigError


def test_duplicate_mapping_keys_are_rejected() -> None:
    with pytest.raises(ConfigError, match="duplicate key"):
        yamlio.loads("profiles: [vm]\nprofiles: [netris]\n", "conformance.yaml")


def test_unhashable_mapping_keys_are_rejected() -> None:
    with pytest.raises(ConfigError, match="unhashable key"):
        yamlio.loads("? {a: b}\n: c\n", "conformance.yaml")
