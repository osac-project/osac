"""Generate a per-feature Feature x test-tier coverage report for OSAC.

The report separates measured Go statement coverage from declared and inferred
test-plan mappings. It never turns missing evidence into a zero percentage.
"""

__all__ = ["cli", "inputs", "report"]
