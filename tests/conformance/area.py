"""Load area declarations for the conformance suite."""

from __future__ import annotations

from pathlib import Path


def load_areas(path: Path) -> dict[str, object]:
    """Return the area declarations found under ``path``."""
    if not path.exists():
        return {}
    return {}
