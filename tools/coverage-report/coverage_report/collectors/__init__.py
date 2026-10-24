"""Collector protocol and registry.

A collector reads one kind of evidence source and returns a single immutable
:class:`~coverage_report.model.CollectorBatch`. W0 ships an empty registry;
later waves add collectors here and register them at import time.

Contract for every collector:

* Never mutate shared state; return a new batch.
* Set ``EvidenceRecord.collector`` to the collector's ``name``.
* Produce ``EvidenceRecord`` objects, not bare numbers. Metrics are computed by
  the engine from evidence and behaviors, so the evidence trail stays auditable.
* Record a :class:`~coverage_report.model.Diagnostic` instead of raising for a
  recoverable source problem (missing file, unreadable artifact). Raise only for
  a programming error.
* Do not read the network. Do not call Jira.
"""

from typing import Dict, List, Optional, Protocol, Tuple, runtime_checkable

from ..config import EffectiveConfig
from ..model import CollectorBatch
from ..scope import FeatureEntry
from .artifacts import discover_artifacts


class CollectorContext:
    """Everything a collector needs, assembled once per run.

    Args:
        repo_root: Absolute repository root.
        sha: Commit under report.
        features: Resolved features in key order.
        config: Effective configuration.
        run_id: Deterministic run identifier.
        explicit_inputs: Paths passed with ``--coverprofile``/``--junit``,
            keyed by kind. Empty values mean the collector should fall back to
            configured globs.
        resolved_inputs: Artifact paths resolved once for the run. When omitted
            they are resolved from ``explicit_inputs`` and the configured
            globs, so direct collector use keeps working.
    """

    def __init__(
        self,
        *,
        repo_root: str,
        sha: str,
        features: Tuple[FeatureEntry, ...],
        config: EffectiveConfig,
        run_id: str,
        explicit_inputs: Optional[Dict[str, Tuple[str, ...]]] = None,
        resolved_inputs: Optional[Dict[str, Tuple[str, ...]]] = None,
    ) -> None:
        self.repo_root = repo_root
        self.sha = sha
        self.features = features
        self.config = config
        self.run_id = run_id
        self.explicit_inputs: Dict[str, Tuple[str, ...]] = {
            key: tuple(value)
            for key, value in (explicit_inputs or {}).items()
        }
        if resolved_inputs is None:
            self.resolved_inputs: Dict[str, Tuple[str, ...]] = {
                "coverprofile": discover_artifacts(
                    self.explicit_inputs.get("coverprofile", ()),
                    self.config.coverprofiles,
                ),
                "junit": discover_artifacts(
                    self.explicit_inputs.get("junit", ()),
                    self.config.junit,
                ),
            }
        else:
            self.resolved_inputs = {
                kind: tuple(paths) for kind, paths in resolved_inputs.items()
            }


@runtime_checkable
class Collector(Protocol):
    """Structural type every collector satisfies."""

    name: str

    def collect(self, ctx: CollectorContext) -> CollectorBatch:
        """Return one batch of observations for this run."""
        ...


_REGISTRY: List[Collector] = []


def register(collector: Collector) -> Collector:
    """Register a collector instance and return it (usable as a decorator)."""
    if not getattr(collector, "name", None):
        raise ValueError("collector must define a non-empty name")
    for existing in _REGISTRY:
        if existing.name == collector.name:
            raise ValueError(
                "collector name {0!r} is already registered".format(collector.name)
            )
    _REGISTRY.append(collector)
    return collector


def registered() -> Tuple[Collector, ...]:
    """Return the registered collectors in registration order."""
    return tuple(_REGISTRY)


def clear() -> None:
    """Remove every registered collector. Test helper."""
    _REGISTRY.clear()
