"""Graceful interrupt state for the conformance runner."""

from __future__ import annotations

import signal
from types import FrameType
from typing import TYPE_CHECKING, Protocol

if TYPE_CHECKING:
    from tests.conformance.model import TestOutcome


class _Signalable(Protocol):
    """The part of a child process the interrupt gate drives."""

    def poll(self) -> int | None: ...

    def send_signal(self, signum: int) -> None: ...

    def kill(self) -> None: ...


class Interrupted(Exception):
    """The first signal asked the runner to stop starting new tests.

    Carries the outcomes that were already recorded so the runner can report
    them instead of discarding the work of completed tests.
    """

    def __init__(self, outcomes: tuple[TestOutcome, ...] = ()) -> None:
        self.outcomes = outcomes
        super().__init__("interrupted")


class InterruptGate:
    def __init__(self) -> None:
        self.first = False
        self.second = False
        self.child: _Signalable | None = None
        self._old: dict[int, signal.Handlers] = {}

    def trigger(self, signum: int = signal.SIGINT) -> None:
        if self.first:
            self.second = True
            child = self.child
            if child is not None and child.poll() is None:
                child.kill()
            raise KeyboardInterrupt
        self.first = True
        child = self.child
        if child is not None and child.poll() is None:
            # Let the running pytest process finish the current test and stop.
            # pytest stops gracefully on SIGINT and still writes the results of
            # completed tests; SIGTERM would kill it without writing anything.
            child.send_signal(signal.SIGINT)
            return
        raise Interrupted

    def install(self) -> None:
        for signum in (signal.SIGINT, signal.SIGTERM):
            self._old[signum] = signal.getsignal(signum)
            signal.signal(signum, self._handle)

    def restore(self) -> None:
        for signum, handler in self._old.items():
            signal.signal(signum, handler)
        self._old.clear()

    def _handle(self, signum: int, _frame: FrameType | None) -> None:
        self.trigger(signum)
