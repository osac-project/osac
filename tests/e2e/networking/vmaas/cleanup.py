from __future__ import annotations

from collections.abc import Callable


def _run_cleanup(cleanup_errors: list[str], resource: str, cleanup: Callable[[], None]) -> None:
    try:
        cleanup()
    except Exception as exc:
        cleanup_errors.append(f"{resource}: {type(exc).__name__}")
