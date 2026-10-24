"""Deterministic hybrid coverage reporter for OSAC.

The package computes per-feature, per-tier coverage evidence. Collectors feed
it structured observations; this package owns the model, validation, metric
computation, and serialization. No network access, no Jira calls: a Jira key is
only a lookup key into version-controlled configuration.
"""

TOOL_VERSION = "0.1.0"

__all__ = ["TOOL_VERSION"]
