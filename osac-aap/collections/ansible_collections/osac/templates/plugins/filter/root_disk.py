"""Choose a root disk from Metal3 inspection data."""

import enum
from collections.abc import Callable


# Root-device hint keys in preference order: a stable hardware identity
# (serial, wwn) survives virtio probe-order changes that reshuffle device
# names, so it is preferred whenever the inspected disk reports one.
HINT_KEYS = ("serial", "wwn", "deviceName")


class RootDiskPolicy(enum.Enum):
    """How the root disk is selected from the inspected storage."""

    # The smallest eligible disk; configured with min_size (GiB).
    SMALLEST = "smallest"


# Default minimum root disk size (GiB), mirroring the role's
# bm_host_root_disk_min_gib default.
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


def root_device_hints(
    hardware_data: list[dict], bmh: dict, policy: str = "smallest", **options
) -> dict:
    """Build rootDeviceHints for the disk chosen by the policy.

    The policy selects the disk and defaults to RootDiskPolicy.SMALLEST,
    the smallest eligible disk; it is configured with min_size, the minimum
    size in GiB, which defaults to DEFAULT_MIN_SIZE_GIB. The hint then uses
    the first non-empty identity the inspected disk reports, in order:
    serial, wwn, deviceName. An empty dict means no eligible disk was found
    and Metal3 keeps its default behavior.
    """
    try:
        selected_policy = RootDiskPolicy(policy)
    except ValueError:
        raise ValueError(f"unsupported root disk policy: {policy}") from None
    disk = _select_disk(selected_policy, _disks(hardware_data, bmh), options)
    if not disk:
        return {}
    for key in HINT_KEYS[:-1]:
        value = disk.get(key)
        if isinstance(value, str) and value.strip():
            return {key: value.strip()}
    return {"deviceName": disk["name"]}


class FilterModule:
    def filters(self) -> dict[str, Callable[..., dict]]:
        return {"root_device_hints": root_device_hints}
