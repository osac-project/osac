from __future__ import annotations

import os
import subprocess
import time
from collections.abc import Callable
from typing import TypeVar

T = TypeVar("T")


def run(*args: str, timeout: int = 300) -> str:
    result = subprocess.run(args, capture_output=True, text=True, timeout=timeout, check=True)
    return result.stdout.strip()


def run_unchecked(*args: str, timeout: int = 300) -> tuple[str, int]:
    result = subprocess.run(args, capture_output=True, text=True, timeout=timeout, check=False)
    combined = (result.stdout.strip() + "\n" + result.stderr.strip()).strip()
    return combined, result.returncode


def poll_until(
    *, fn: Callable[[], T], until: Callable[[T], bool], retries: int = 60, delay: int = 5, description: str
) -> T:
    value: T | None = None
    for _ in range(retries):
        value = fn()
        if until(value):
            return value
        time.sleep(delay)
    raise TimeoutError(f"{description} — timeout after {retries * delay}s, last value: {value!r}")


def env(name: str, default: str | None = None) -> str:
    value = os.environ.get(name, default)
    if value is None:
        msg = f"Required environment variable {name} is not set"
        raise RuntimeError(msg)
    return value
