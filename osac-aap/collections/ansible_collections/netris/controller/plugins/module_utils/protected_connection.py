"""Validation helpers for installation-owned Netris connection settings."""

from __future__ import annotations

import json
from pathlib import Path
from typing import Any, Mapping


_CONTRACT = json.loads(
    (Path(__file__).with_name("netris_protected_inputs.json")).read_text()
)
_POLICY_VERSION = _CONTRACT["policy_version"]
_MAX_DEPTH = 32
_MAX_NODES = 10_000


class ProtectedInputError(ValueError):
    """A launch payload contains a protected Netris setting or is malformed."""

    def __init__(self, message: str, *, path: str, key: str | None = None) -> None:
        self.path = path
        self.key = key
        self.policy_version = _POLICY_VERSION
        super().__init__(f"{message} at {path} (policy {_POLICY_VERSION})")


def _normalize_key(value: str) -> str:
    return "".join(
        character.lower()
        for character in value
        if character.isascii() and character not in "_-."
    )


_PROTECTED_NAMES = {
    _normalize_key(alias)
    for item in _CONTRACT["protected_inputs"]
    for alias in (*item["aliases"], item["canonical_environment"])
}


def _path_is_protected(path: tuple[str, ...]) -> bool:
    return any(
        _normalize_key("".join(path[index:])) in _PROTECTED_NAMES
        for index in range(len(path))
    )


def _path_text(path: tuple[str, ...]) -> str:
    result = "$"
    for component in path:
        result += f"[{component!r}]"
    return result


def _walk(value: Any, path: tuple[str, ...], depth: int, state: dict[str, int]) -> None:
    if depth > _MAX_DEPTH:
        raise ProtectedInputError(
            "extra_vars nesting exceeds the supported limit", path=_path_text(path)
        )
    state["nodes"] += 1
    if state["nodes"] > _MAX_NODES:
        raise ProtectedInputError(
            "extra_vars contains too many values", path=_path_text(path)
        )

    if isinstance(value, Mapping):
        for key, child in value.items():
            if not isinstance(key, str):
                raise ProtectedInputError(
                    "extra_vars keys must be strings", path=_path_text(path)
                )
            child_path = (*path, key)
            if _path_is_protected(child_path):
                raise ProtectedInputError(
                    f"protected Netris setting {key!r} is not allowed",
                    path=_path_text(child_path),
                    key=key,
                )
            _walk(child, child_path, depth + 1, state)
    elif isinstance(value, (list, tuple)):
        for child in value:
            _walk(child, path, depth + 1, state)


def validate_extra_vars(value: Mapping[str, Any]) -> None:
    """Reject protected Netris keys without changing the launch payload."""

    if not isinstance(value, Mapping):
        raise ProtectedInputError("extra_vars must be a mapping", path="$")
    _walk(value, (), 0, {"nodes": 0})


def strip_protected_env(mapping: Mapping[str, Any]) -> dict[str, Any]:
    """Return environment data with protected keys removed."""

    if not isinstance(mapping, Mapping):
        raise ProtectedInputError("environment data must be a mapping", path="$")
    return {
        key: value
        for key, value in mapping.items()
        if not isinstance(key, str) or not _path_is_protected((key,))
    }
