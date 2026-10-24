"""Built-in collector registration.

Importing this module registers the collectors this tool ships with, in a fixed
order. The order is only a readability aid: :func:`merge_batches` sorts every
merged collection, so a different registration order cannot change the report.

Tests that need an empty registry call :func:`coverage_report.collectors.clear`;
tests that want the shipped set call :func:`register_builtins` again.
"""

from typing import Tuple

from . import Collector, register, registered
from .behaviors import BehaviorInventoryCollector
from .ginkgo import GinkgoUnitCollector
from .junit import JunitCollector
from .mappings import TestMappingCollector

#: One shared instance per collector. Registration order is deterministic.
BUILTIN_COLLECTORS: Tuple[Collector, ...] = (
    BehaviorInventoryCollector(),
    TestMappingCollector(),
    JunitCollector(),
    GinkgoUnitCollector(),
)


def register_builtins() -> Tuple[Collector, ...]:
    """Register every built-in collector that is not registered yet."""
    existing = {collector.name for collector in registered()}
    for collector in BUILTIN_COLLECTORS:
        if collector.name in existing:
            continue
        register(collector)
        existing.add(collector.name)
    return registered()


register_builtins()
