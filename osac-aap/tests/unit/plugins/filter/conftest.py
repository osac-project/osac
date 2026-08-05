from pathlib import Path

import pytest


@pytest.fixture
def roles_dir() -> Path:
    return (
        Path(__file__).resolve().parents[4]
        / "collections"
        / "ansible_collections"
        / "osac"
        / "templates"
        / "roles"
    )
