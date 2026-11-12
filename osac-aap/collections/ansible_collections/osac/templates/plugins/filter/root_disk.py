"""Choose a root disk from Metal3 inspection data."""

import enum
from collections.abc import Callable


# Root-device hint keys in preference order, read from the inspection
# fields of the same names (HardwareData status.hardware.storage): a stable
# hardware identity (serialNumber, wwn) survives virtio probe-order changes
# that reshuffle device names, so it is preferred whenever the disk reports
# one. deviceName is the fallback, not part of this list.
HINT_KEYS = ("serialNumber", "wwn")


class RootDiskPolicy(enum.Enum):
    """How the root disk is selected from the inspected storage."""

    # The smallest eligible disk; configured with min_size (GiB).
    SMALLEST = "smallest"


# Default minimum root disk size (GiB) for the smallest-disk policy.
# TODO(OSAC-4576): Replace this minimum when the required image size is agreed.
DEFAULT_MIN_SIZE_GIB = 4


def _disks(hardware_data: list[dict], bmh: dict) -> list[dict]:
    inspected = hardware_data[0]["spec"].get("hardware") if hardware_data else None
    status = bmh.get("status", {}).get("hardware") or {}
    return (inspected or {}).get("storage") or status.get("storage") or []


def _select_disk(policy: RootDiskPolicy, disks: list[dict], options: dict) -> dict | None:
    if policy is RootDiskPolicy.SMALLEST:
        minimum_bytes = int(options.get("min_size", DEFAULT_MIN_SIZE_GIB)) * (1 << 30)
        eligible = [
            disk
            for disk in disks
            if disk.get("name") and disk.get("sizeBytes") and int(disk["sizeBytes"]) >= minimum_bytes
        ]
        if not eligible:
            return None
        return min(eligible, key=lambda disk: (int(disk["sizeBytes"]), disk["name"]))
    raise ValueError(f"unsupported root disk policy: {policy}")


def _device_name(disk: dict) -> str:
    """The most stable device name the inspection reports.

    Virtio disks expose no serial or wwn, so the deviceName fallback is the
    only option there, and the probe-order name (/dev/vda) is the one that
    reshuffles between inspection and deploy. Metal3 accepts by-path links
    as deviceName and they pin the PCI slot instead, so prefer one whenever
    the inspection reports it, in name or alternateNames.
    """
    name = disk.get("name") or ""
    if name.startswith("/dev/disk/by-path/"):
        return name
    for alternate in disk.get("alternateNames") or []:
        if isinstance(alternate, str) and alternate.startswith("/dev/disk/by-path/"):
            return alternate
    return name


def root_device_hints(
    hardware_data: list[dict], bmh: dict, policy: str = "smallest", **options
) -> dict:
    """Build rootDeviceHints for the disk chosen by the policy.

    The policy selects the disk and defaults to RootDiskPolicy.SMALLEST,
    the smallest eligible disk; it is configured with min_size, the minimum
    size in GiB, which defaults to DEFAULT_MIN_SIZE_GIB. The hint then uses
    the first non-empty identity the inspected disk reports, in order:
    serialNumber, wwn, and the device name as the fallback. An empty dict
    means no eligible disk was found and Metal3 keeps its default behavior.
    """
    try:
        selected_policy = RootDiskPolicy(policy)
    except ValueError:
        raise ValueError(f"unsupported root disk policy: {policy}") from None
    disk = _select_disk(selected_policy, _disks(hardware_data, bmh), options)
    if not disk:
        return {}
    for key in HINT_KEYS:
        value = disk.get(key)
        if isinstance(value, str) and value.strip():
            return {key: value.strip()}
    return {"deviceName": _device_name(disk)}


class FilterModule:
    def filters(self) -> dict[str, Callable[..., dict]]:
        return {"root_device_hints": root_device_hints}
