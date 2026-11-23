"""Contract tests for graceful conformance interrupt state."""

from __future__ import annotations

import signal

import pytest

from tests.conformance.interrupt import Interrupted, InterruptGate


class _Child:
    def __init__(self, *, running: bool = True) -> None:
        self.running = running
        self.signals: list[int] = []
        self.killed = False

    def poll(self) -> int | None:
        return None if self.running else 0

    def send_signal(self, signum: int) -> None:
        self.signals.append(signum)

    def kill(self) -> None:
        self.killed = True
        self.running = False


def test_first_signal_requests_graceful_stop() -> None:
    gate = InterruptGate()

    with pytest.raises(Interrupted):
        gate.trigger()

    assert gate.first is True
    assert gate.second is False


def test_second_signal_forces_an_immediate_exit() -> None:
    gate = InterruptGate()
    with pytest.raises(Interrupted):
        gate.trigger()

    with pytest.raises(KeyboardInterrupt):
        gate.trigger()

    assert gate.second is True


def test_first_signal_is_forwarded_instead_of_terminating_the_child() -> None:
    gate = InterruptGate()
    child = _Child()
    gate.child = child

    gate.trigger()

    assert gate.first is True
    assert child.signals == [signal.SIGINT]
    assert child.killed is False


def test_second_signal_kills_the_child() -> None:
    gate = InterruptGate()
    child = _Child()
    gate.child = child
    gate.trigger()

    with pytest.raises(KeyboardInterrupt):
        gate.trigger()

    assert gate.second is True
    assert child.killed is True


def test_a_finished_child_leaves_the_interrupt_to_the_run_loop() -> None:
    gate = InterruptGate()
    child = _Child(running=False)
    gate.child = child

    gate.trigger()

    assert gate.first is True
    assert child.signals == []
    assert child.killed is False


def test_a_signal_during_registration_is_forwarded_after_attachment() -> None:
    gate = InterruptGate()
    child = _Child()

    with gate.hold():
        gate.child = child
        gate.trigger()

    assert gate.first is True
    assert child.signals == [signal.SIGINT]


def test_two_signals_during_registration_escalate_after_attachment() -> None:
    gate = InterruptGate()
    child = _Child()

    with pytest.raises(KeyboardInterrupt), gate.hold():
        gate.child = child
        gate.trigger()
        gate.trigger()

    assert gate.first is True
    assert gate.second is True
    assert child.signals == [signal.SIGINT]
    assert child.killed is True
