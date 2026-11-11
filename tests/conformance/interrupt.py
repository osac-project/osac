"""Graceful interrupt state for the conformance runner."""

from __future__ import annotations

import signal
from types import FrameType


class Interrupted(Exception):
    """The first signal asked the runner to stop starting new tests."""


class InterruptGate:
    def __init__(self) -> None:
        self.first = False
        self.second = False
        self._old: dict[int, signal.Handlers] = {}

    def trigger(self) -> None:
        if self.first:
            self.second = True
            raise KeyboardInterrupt
        self.first = True
        raise Interrupted

    def install(self) -> None:
        for signum in (signal.SIGINT, signal.SIGTERM):
            self._old[signum] = signal.getsignal(signum)
            signal.signal(signum, self._handle)

    def restore(self) -> None:
        for signum, handler in self._old.items():
            signal.signal(signum, handler)
        self._old.clear()

    def _handle(self, _signum: int, _frame: FrameType | None) -> None:
        self.trigger()
