"""Tests for the Metal3 root-disk fallback."""

import sys
from pathlib import Path
from typing import Any

sys.path.insert(0, str(Path(__file__).parents[2] / "collections/ansible_collections/osac/templates/plugins/filter"))

from root_disk import smallest_root_disk


def _disk(name: str, gib: int) -> dict[str, Any]:
    return {"name": name, "sizeBytes": gib * (1 << 30)}


def test_selects_smallest_inspected_disk() -> None:
    data = [{"spec": {"hardware": {"storage": [_disk("/dev/vda", 161), _disk("/dev/vdb", 32)]}}}]
    bmh = {"status": {"hardware": {"storage": [_disk("/dev/sda", 8)]}}}
    assert smallest_root_disk(data, bmh, 4) == "/dev/vdb"


def test_uses_bmh_status_when_hardwaredata_is_absent() -> None:
    bmh = {"status": {"hardware": {"storage": [_disk("/dev/vda", 161)]}}}
    assert smallest_root_disk([], bmh, 4) == "/dev/vda"


def test_keeps_metal3_default_without_eligible_disk() -> None:
    data = [{"spec": {"hardware": {"storage": [_disk("/dev/vda", 2)]}}}]
    assert smallest_root_disk(data, {}, 4) == ""
    assert smallest_root_disk([], {}, 4) == ""
