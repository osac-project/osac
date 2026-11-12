"""Tests for the Metal3 root-disk fallback."""

import sys
from pathlib import Path
from typing import Any

sys.path.insert(0, str(Path(__file__).parents[2] / "collections/ansible_collections/osac/templates/plugins/filter"))

from root_disk import root_device_hints, smallest_root_disk


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


def test_hints_prefer_serial() -> None:
    disk = _disk("/dev/vda", 32) | {"serial": "SER-42", "wwn": "0x5000c50015ea71ad"}
    data = [{"spec": {"hardware": {"storage": [disk]}}}]
    assert root_device_hints(data, {}, 4) == {"serial": "SER-42"}


def test_hints_use_wwn_without_serial() -> None:
    disk = _disk("/dev/vda", 32) | {"wwn": "0x5000c50015ea71ad"}
    data = [{"spec": {"hardware": {"storage": [disk]}}}]
    assert root_device_hints(data, {}, 4) == {"wwn": "0x5000c50015ea71ad"}


def test_hints_fall_back_to_device_name() -> None:
    data = [{"spec": {"hardware": {"storage": [_disk("/dev/vda", 32)]}}}]
    assert root_device_hints(data, {}, 4) == {"deviceName": "/dev/vda"}


def test_hints_ignore_blank_serial_and_wwn() -> None:
    disk = _disk("/dev/vdb", 32) | {"serial": "  ", "wwn": ""}
    data = [{"spec": {"hardware": {"storage": [disk]}}}]
    assert root_device_hints(data, {}, 4) == {"deviceName": "/dev/vdb"}


def test_hints_empty_without_eligible_disk() -> None:
    data = [{"spec": {"hardware": {"storage": [_disk("/dev/vda", 2)]}}}]
    assert root_device_hints(data, {}, 4) == {}
    assert root_device_hints([], {}, 4) == {}


def test_hints_describe_the_selected_disk() -> None:
    small = _disk("/dev/vdb", 32)
    large = _disk("/dev/vda", 161) | {"serial": "SER-LARGE"}
    data = [{"spec": {"hardware": {"storage": [large, small]}}}]
    assert root_device_hints(data, {}, 4) == {"deviceName": "/dev/vdb"}
