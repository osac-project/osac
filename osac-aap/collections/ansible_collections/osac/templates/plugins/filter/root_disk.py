"""Choose a root disk from Metal3 inspection data."""

from collections.abc import Callable


# Root-device hint keys in preference order: a stable hardware identity
# (serial, wwn) survives virtio probe-order changes that reshuffle device
# names, so it is preferred whenever the inspected disk reports one.
HINT_KEYS = ("serial", "wwn", "deviceName")


def _disks(hardware_data: list[dict], bmh: dict) -> list[dict]:
    inspected = hardware_data[0]["spec"].get("hardware") if hardware_data else None
    status = bmh.get("status", {}).get("hardware") or {}
    return (inspected or {}).get("storage") or status.get("storage") or []


def _eligible_disks(hardware_data: list[dict], bmh: dict, minimum_gib: int) -> list[dict]:
    minimum_bytes = int(minimum_gib) * (1 << 30)
    return [
        disk
        for disk in _disks(hardware_data, bmh)
        if disk.get("name") and disk.get("sizeBytes") and int(disk["sizeBytes"]) >= minimum_bytes
    ]


def _root_disk(hardware_data: list[dict], bmh: dict, minimum_gib: int) -> dict | None:
    eligible = _eligible_disks(hardware_data, bmh, minimum_gib)
    if not eligible:
        return None
    return min(eligible, key=lambda disk: (int(disk["sizeBytes"]), disk["name"]))


def smallest_root_disk(hardware_data: list[dict], bmh: dict, minimum_gib: int) -> str:
    disk = _root_disk(hardware_data, bmh, minimum_gib)
    return disk["name"] if disk else ""


def root_device_hints(hardware_data: list[dict], bmh: dict, minimum_gib: int) -> dict:
    """Build rootDeviceHints for the selected disk.

    The hint uses the first non-empty identity the inspected disk reports,
    in order: serial, wwn, deviceName. An empty dict means no eligible disk
    was found and Metal3 keeps its default behavior.
    """
    disk = _root_disk(hardware_data, bmh, minimum_gib)
    if not disk:
        return {}
    for key in HINT_KEYS[:-1]:
        value = disk.get(key)
        if isinstance(value, str) and value.strip():
            return {key: value.strip()}
    return {"deviceName": disk["name"]}


class FilterModule:
    def filters(self) -> dict[str, Callable[..., dict | str]]:
        return {
            "smallest_root_disk": smallest_root_disk,
            "root_device_hints": root_device_hints,
        }
