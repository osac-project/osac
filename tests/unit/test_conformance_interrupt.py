"""Contract tests for graceful conformance interrupt state."""

from __future__ import annotations

import pytest

from tests.conformance.interrupt import Interrupted, InterruptGate


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
