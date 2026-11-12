"""Tests for the Metal3 root-disk fallback."""

import sys
from pathlib import Path
from typing import Any

import pytest

sys.path.insert(0, str(Path(__file__).parents[2] / "collections/ansible_collections/osac/templates/plugins/filter"))

from root_disk import DEFAULT_MIN_SIZE_GIB, root_device_hints


def _disk(name: str, gib: int) -> dict[str, Any]:
    return {"name": name, "sizeBytes": gib * (1 << 30)}


def test_selects_smallest_inspected_disk() -> None:
    data = [{"spec": {"hardware": {"storage": [_disk("/dev/vda", 161), _disk("/dev/vdb", 32)]}}}]
    bmh = {"status": {"hardware": {"storage": [_disk("/dev/sda", 8)]}}}
    hints = root_device_hints(data, bmh, "smallest", min_size=4)
    assert hints == {"deviceName": "/dev/vdb"}


def test_uses_bmh_status_when_hardwaredata_is_absent() -> None:
    bmh = {"status": {"hardware": {"storage": [_disk("/dev/vda", 161)]}}}
    hints = root_device_hints([], bmh, "smallest", min_size=4)
    assert hints == {"deviceName": "/dev/vda"}


def test_smallest_is_the_default_policy() -> None:
    data = [{"spec": {"hardware": {"storage": [_disk("/dev/vda", 161), _disk("/dev/vdb", 32)]}}}]
    assert root_device_hints(data, {}, min_size=4) == {"deviceName": "/dev/vdb"}


def test_min_size_defaults_to_the_role_minimum() -> None:
    data = [{"spec": {"hardware": {"storage": [_disk("/dev/vda", DEFAULT_MIN_SIZE_GIB - 1)]}}}]
    assert root_device_hints(data, {}) == {}
    data = [{"spec": {"hardware": {"storage": [_disk("/dev/vda", DEFAULT_MIN_SIZE_GIB)]}}}]
    assert root_device_hints(data, {}) == {"deviceName": "/dev/vda"}


def test_rejects_an_unknown_policy() -> None:
    data = [{"spec": {"hardware": {"storage": [_disk("/dev/vda", 32)]}}}]
    with pytest.raises(ValueError, match="unsupported root disk policy"):
        root_device_hints(data, {}, "largest", min_size=4)


def test_keeps_metal3_default_without_eligible_disk() -> None:
    data = [{"spec": {"hardware": {"storage": [_disk("/dev/vda", 2)]}}}]
    assert root_device_hints(data, {}, "smallest", min_size=4) == {}
    assert root_device_hints([], {}, "smallest", min_size=4) == {}


def test_hints_prefer_serial_number() -> None:
    disk = _disk("/dev/vda", 32) | {"serialNumber": "SER-42", "wwn": "0x5000c50015ea71ad"}
    data = [{"spec": {"hardware": {"storage": [disk]}}}]
    assert root_device_hints(data, {}, "smallest", min_size=4) == {"serialNumber": "SER-42"}


def test_hints_read_virtio_inspection_serial_number() -> None:
    # Shape of a libvirt fixture disk: serial set in the domain XML, exposed
    # by inspection as serialNumber (virtio-blk truncates it to 20 bytes) and
    # a by-path primary name.
    disk = {
        "name": "/dev/disk/by-path/virtio-pci-0000:03:00.0",
        "alternateNames": ["/dev/vda", "/dev/disk/by-path/virtio-pci-0000:03:00.0"],
        "rotational": True,
        "serialNumber": "virtual-bmh-1c320d51",
        "sizeBytes": 161061273600,
        "type": "HDD",
        "vendor": "0x1af4",
    }
    data = [{"spec": {"hardware": {"storage": [disk]}}}]
    assert root_device_hints(data, {}, "smallest") == {"serialNumber": "virtual-bmh-1c320d51"}


def test_hints_fall_back_to_the_by_path_device_name() -> None:
    disk = _disk("/dev/vda", 32) | {
        "alternateNames": ["/dev/vda", "/dev/disk/by-path/pci-0000:03:00.0"]
    }
    data = [{"spec": {"hardware": {"storage": [disk]}}}]
    assert root_device_hints(data, {}, "smallest", min_size=4) == {
        "deviceName": "/dev/disk/by-path/pci-0000:03:00.0"
    }


def test_hints_use_wwn_without_serial_number() -> None:
    disk = _disk("/dev/vda", 32) | {"wwn": "0x5000c50015ea71ad"}
    data = [{"spec": {"hardware": {"storage": [disk]}}}]
    assert root_device_hints(data, {}, "smallest", min_size=4) == {"wwn": "0x5000c50015ea71ad"}


def test_hints_fall_back_to_device_name() -> None:
    data = [{"spec": {"hardware": {"storage": [_disk("/dev/vda", 32)]}}}]
    assert root_device_hints(data, {}, "smallest", min_size=4) == {"deviceName": "/dev/vda"}


def test_hints_ignore_blank_serial_number_and_wwn() -> None:
    disk = _disk("/dev/vdb", 32) | {"serialNumber": "  ", "wwn": ""}
    data = [{"spec": {"hardware": {"storage": [disk]}}}]
    assert root_device_hints(data, {}, "smallest", min_size=4) == {"deviceName": "/dev/vdb"}


def test_hints_empty_without_eligible_disk() -> None:
    data = [{"spec": {"hardware": {"storage": [_disk("/dev/vda", 2)]}}}]
    assert root_device_hints(data, {}, "smallest", min_size=4) == {}
    assert root_device_hints([], {}, "smallest", min_size=4) == {}


def test_hints_describe_the_selected_disk() -> None:
    small = _disk("/dev/vdb", 32)
    large = _disk("/dev/vda", 161) | {"serialNumber": "SER-LARGE"}
    data = [{"spec": {"hardware": {"storage": [large, small]}}}]
    assert root_device_hints(data, {}, "smallest", min_size=4) == {"deviceName": "/dev/vdb"}
