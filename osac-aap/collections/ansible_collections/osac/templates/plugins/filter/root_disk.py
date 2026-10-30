"""Choose a root disk from Metal3 inspection data."""

from collections.abc import Callable


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


def smallest_root_disk(hardware_data: list[dict], bmh: dict, minimum_gib: int) -> str:
    eligible = _eligible_disks(hardware_data, bmh, minimum_gib)
    if not eligible:
        return ""
    return min(eligible, key=lambda disk: (int(disk["sizeBytes"]), disk["name"]))["name"]


class FilterModule:
    def filters(self) -> dict[str, Callable[..., str]]:
        return {"smallest_root_disk": smallest_root_disk}
