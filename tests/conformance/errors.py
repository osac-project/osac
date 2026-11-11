"""Conformance entry-point errors and their exit-code meanings."""

from __future__ import annotations

from collections.abc import Sequence


class ConformanceError(Exception):
    """Base class for errors the command line reports without a traceback."""


class CommandLineError(ConformanceError):
    """The command line is unknown or malformed."""


class ConfigError(ConformanceError):
    """The configuration cannot run the selected tests."""

    def __init__(self, checks: Sequence[str]) -> None:
        self.checks = tuple(checks)
        super().__init__("\n".join(self.checks))
