"""Fixtures local to the SSH-key registry regressions."""

from collections.abc import Iterator
from pathlib import Path
from tempfile import TemporaryDirectory

import pytest


@pytest.fixture
def ssh_key_dir() -> Iterator[Path]:
    """Remove the temporary private keys even when a test fails."""
    with TemporaryDirectory(prefix="osac-e2e-ssh-") as directory:
        yield Path(directory)
