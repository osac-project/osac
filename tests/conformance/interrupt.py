"""Graceful interrupt state for the conformance runner."""

from __future__ import annotations

import contextlib
import signal
from collections.abc import Iterator
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
        self._deferring = False
        self._pending: list[int] = []

    def trigger(self, signum: int = signal.SIGINT) -> None:
        if self._deferring:
            # Queue every signal: two of them are the escalation path, so
            # keeping only the last one would swallow the second.
            self._pending.append(signum)
            return
        self._activate(signum)

    def _activate(self, signum: int) -> None:
        if self.first:
            self.second = True
            child = self.child
            if child is not None and child.poll() is None:
                child.kill()
            raise KeyboardInterrupt
        self.first = True
        child = self.child
        if child is None:
            # Nothing is running, so no test outcome can be lost.
            raise Interrupted
        if child.poll() is None:
            # Let the running pytest process finish the current test and stop.
            # pytest stops gracefully on SIGINT and still writes the results of
            # completed tests; SIGTERM would kill it without writing anything.
            child.send_signal(signal.SIGINT)
        # While the child stays attached, the runner reads the outcomes pytest
        # wrote and raises ``Interrupted`` with them. Raising from this handler
        # instead would escape that read-back and discard completed outcomes.

    @contextlib.contextmanager
    def hold(self) -> Iterator[None]:
        """Defer signal handling until a child process has been attached.

        A signal can arrive between ``Popen`` returning and the runner storing
        the handle. While this context is active the handler records the signal
        instead of escaping, so after registration the pending signal reaches
        the child rather than abandoning a live process.

        Signals are replayed in the order they arrived, from this context's
        ``finally``. A first signal only forwards ``SIGINT`` to the child; a
        second one raises ``KeyboardInterrupt``. The caller must therefore
        already have its cleanup in place around the ``with`` block, so the
        child is stopped and reaped on that path too.
        """
        self._deferring = True
        try:
            yield
        finally:
            self._deferring = False
            pending, self._pending = self._pending, []
            for signum in pending:
                self._activate(signum)

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
