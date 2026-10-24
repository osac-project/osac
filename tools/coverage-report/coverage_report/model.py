"""Core data model and invariants for the coverage reporter.

This module is the contract every collector targets. Later waves add
collectors; they may only produce the types defined here.

Two invariants matter more than anything else:

1. Evidence ids are stable and deterministic. They are a hash of
   ``collector + kind + subject + source`` only, so an unchanged input keeps
   the same id across runs and across batch orderings.
2. Evidence strength never silently increases. The only allowed transition is
   ``inferred_mapping -> declared_mapping`` (a human confirmed an inferred
   mapping). Everything else is rejected by :func:`assert_no_promotion`.
"""

import hashlib
import re
from dataclasses import dataclass, field
from enum import Enum
from typing import Dict, Iterable, List, Mapping, Optional, Sequence, Tuple, Union

from .errors import EvidencePromotionError, ModelError
from .serialize import compact_json

EVIDENCE_ID_PREFIX = "ev-"
EVIDENCE_ID_HEX_LENGTH = 16


class EvidenceClass(str, Enum):
    """How strong the evidence for a claim is, strongest first."""

    MEASURED = "measured"
    OBSERVED_EXECUTION = "observed_execution"
    DECLARED_MAPPING = "declared_mapping"
    INFERRED_MAPPING = "inferred_mapping"
    GAP_UNKNOWN = "gap_unknown"


class Tier(str, Enum):
    """Test tier the evidence was produced at."""

    UNIT = "unit"
    INTEGRATION = "integration"
    E2E = "e2e"


class MetricState(str, Enum):
    """State of a computed metric.

    ``zero`` is deliberately distinct from ``unavailable`` and ``na``: a
    measured zero is information, a missing source is not.
    """

    VALUE = "value"
    ZERO = "zero"
    NA = "na"
    UNAVAILABLE = "unavailable"
    STALE = "stale"
    BLOCKED = "blocked"


class Freshness(str, Enum):
    """Whether evidence still describes the revision under report."""

    CURRENT = "current"
    STALE = "stale"
    UNKNOWN = "unknown"


TIER_ORDER: Tuple[Tier, ...] = (Tier.UNIT, Tier.INTEGRATION, Tier.E2E)

#: The only evidence-class change a later collection pass may make.
ALLOWED_EVIDENCE_TRANSITIONS = frozenset(
    {(EvidenceClass.INFERRED_MAPPING, EvidenceClass.DECLARED_MAPPING)}
)

_BOUNDARY_KINDS = frozenset({"real", "simulated"})

#: Shortest abbreviated commit id accepted as a prefix of the reported commit.
#: Seven characters is git's own minimum for an unambiguous short id.
MIN_REVISION_PREFIX = 7

_HEX_RE = re.compile(r"^[0-9a-f]+$")


def revision_freshness(
    record_revision: Optional[str], run_sha: Optional[str]
) -> Freshness:
    """Classify an artifact's revision against the commit under report.

    A revision is current when it equals the reported commit, or when the
    shorter of the two is an unambiguous prefix (at least
    :data:`MIN_REVISION_PREFIX` hexadecimal characters) of the longer one. Every
    other non-empty pair is stale. A missing value on either side is unknown:
    freshness is never assumed.

    This is the single definition every collector uses, so a coverprofile and a
    JUnit report recorded at the same abbreviated commit cannot disagree.
    """
    if not record_revision or not run_sha:
        return Freshness.UNKNOWN
    candidate = record_revision.strip().lower()
    target = run_sha.strip().lower()
    if not candidate or not target:
        return Freshness.UNKNOWN
    if candidate == target:
        return Freshness.CURRENT
    shorter, longer = sorted((candidate, target), key=len)
    if (
        len(shorter) >= MIN_REVISION_PREFIX
        and _HEX_RE.match(shorter)
        and longer.startswith(shorter)
    ):
        return Freshness.CURRENT
    return Freshness.STALE


def make_evidence_id(collector: str, kind: str, subject: str, source: str) -> str:
    """Return the stable id for one piece of evidence.

    The id intentionally excludes timestamps, revisions, and payload content so
    that the same source re-read at a new revision keeps its identity and can
    be compared as stale rather than treated as new evidence.
    """
    material = "\x1f".join((collector, kind, subject, source))
    digest = hashlib.sha256(material.encode("utf-8")).hexdigest()
    return EVIDENCE_ID_PREFIX + digest[:EVIDENCE_ID_HEX_LENGTH]


def _freeze_strings(values: Optional[Iterable[str]], *, name: str) -> Tuple[str, ...]:
    if values is None:
        return ()
    if isinstance(values, str):
        raise ModelError(
            "{0} must be a sequence of strings, not a bare string".format(name),
            recovery="Pass a list or tuple of strings.",
        )
    frozen = tuple(values)
    for value in frozen:
        if not isinstance(value, str):
            raise ModelError(
                "{0} entries must be strings, got {1!r}".format(name, value),
                recovery="Use string boundary kinds ('real', 'simulated').",
            )
    return frozen


@dataclass(frozen=True)
class Boundary:
    """Which environment a behavior ran in, or is required to run in.

    ``real:`` means real infrastructure/providers; ``simulated:`` means fakes,
    mocks, envtest, or stub providers. Both may be present.
    """

    real: Tuple[str, ...] = ()
    simulated: Tuple[str, ...] = ()

    def __post_init__(self) -> None:
        object.__setattr__(
            self, "real", tuple(sorted(_freeze_strings(self.real, name="boundary.real")))
        )
        object.__setattr__(
            self,
            "simulated",
            tuple(sorted(_freeze_strings(self.simulated, name="boundary.simulated"))),
        )

    def to_dict(self) -> Dict[str, List[str]]:
        return {"real": list(self.real), "simulated": list(self.simulated)}

    @classmethod
    def from_any(
        cls, value: Union["Boundary", Mapping[str, Sequence[str]], None]
    ) -> "Boundary":
        """Coerce ``None``/mapping/instance into a Boundary."""
        if value is None:
            return cls()
        if isinstance(value, cls):
            return value
        if not isinstance(value, Mapping):
            raise ModelError(
                "boundary must be a mapping with 'real' and 'simulated' keys",
                recovery="Use {'real': [...], 'simulated': [...]}.",
            )
        unknown = sorted(set(value) - _BOUNDARY_KINDS)
        if unknown:
            raise ModelError(
                "unknown boundary key(s): {0}".format(", ".join(unknown)),
                recovery="Allowed boundary keys are 'real' and 'simulated'.",
            )
        return cls(
            real=tuple(value.get("real") or ()),
            simulated=tuple(value.get("simulated") or ()),
        )

@dataclass(frozen=True)
class EvidenceRecord:
    """One observation about one subject, produced by one collector.

    ``confidence`` and ``heuristic`` are set only for ``inferred_mapping``
    evidence; the model rejects them on stronger classes so an inference can
    never masquerade as a measurement.
    """

    id: str
    evidence_class: EvidenceClass
    tier: Tier
    feature: str
    subject: str
    source: str
    collector: str
    kind: str
    revision: Optional[str] = None
    timestamp: Optional[str] = None
    freshness: Freshness = Freshness.UNKNOWN
    confidence: Optional[float] = None
    heuristic: Optional[str] = None
    boundary: Boundary = field(default_factory=Boundary)
    details: Dict[str, object] = field(default_factory=dict)

    def __post_init__(self) -> None:
        object.__setattr__(self, "evidence_class", EvidenceClass(self.evidence_class))
        object.__setattr__(self, "tier", Tier(self.tier))
        object.__setattr__(self, "freshness", Freshness(self.freshness))
        object.__setattr__(
            self, "boundary", Boundary.from_any(self.boundary)
        )
        object.__setattr__(self, "details", dict(self.details))
        if not self.id:
            raise ModelError(
                "evidence id must not be empty",
                recovery="Use make_evidence_id(collector, kind, subject, source).",
            )
        if not self.feature:
            raise ModelError("evidence feature must not be empty")
        if not self.subject:
            raise ModelError("evidence subject must not be empty")
        if not self.source:
            raise ModelError("evidence source must not be empty")
        if self.evidence_class is not EvidenceClass.INFERRED_MAPPING:
            if self.confidence is not None:
                raise ModelError(
                    "confidence is only valid for inferred_mapping evidence "
                    "(got {0} for {1})".format(self.evidence_class.value, self.id),
                    recovery="Drop confidence or classify the evidence as inferred_mapping.",
                )
            if self.heuristic is not None:
                raise ModelError(
                    "heuristic is only valid for inferred_mapping evidence "
                    "(got {0} for {1})".format(self.evidence_class.value, self.id),
                    recovery="Drop heuristic or classify the evidence as inferred_mapping.",
                )
        elif self.confidence is not None and not 0.0 <= self.confidence <= 1.0:
            raise ModelError(
                "confidence must be between 0.0 and 1.0, got {0}".format(self.confidence)
            )

    def sort_key(self) -> Tuple[str, ...]:
        return (
            self.collector,
            self.kind,
            self.tier.value,
            self.feature,
            self.subject,
            self.source,
            self.evidence_class.value,
            self.id,
        )

    def to_dict(self) -> Dict[str, object]:
        return {
            "id": self.id,
            "collector": self.collector,
            "kind": self.kind,
            "evidence_class": self.evidence_class.value,
            "tier": self.tier.value,
            "feature": self.feature,
            "subject": self.subject,
            "source": self.source,
            "revision": self.revision,
            "timestamp": self.timestamp,
            "freshness": self.freshness.value,
            "confidence": self.confidence,
            "heuristic": self.heuristic,
            "boundary": self.boundary.to_dict(),
            "details": self.details,
        }


@dataclass(frozen=True)
class Behavior:
    """A testable behavior from a feature test plan.

    ``required_boundary`` lists the boundary kinds that count as adequate
    evidence; an empty tuple means any boundary is acceptable.
    """

    id: str
    title: str
    requirement_anchor: Optional[str] = None
    priority: Optional[str] = None
    automation: Optional[str] = None
    required_tier: Optional[Tier] = None
    #: Where ``required_tier`` came from: the test plan or a mapping-config
    #: ``behavior_tiers`` override. ``None`` means no tier was assigned.
    tier_origin: Optional[str] = None
    #: Whether the tier's source has been reviewed. A tier taken from a
    #: PENDING-REVIEW mapping file applies but is flagged here.
    tier_reviewed: Optional[bool] = None
    #: The tier label exactly as the test plan wrote it, for example
    #: "Contract and component integration / [DEV]".
    tier_label: Optional[str] = None
    #: A secondary tier named after the primary one, for example
    #: "component integration for the RPC" in "Unit / [DEV] plus component
    #: integration for the RPC".
    tier_note: Optional[str] = None
    #: The test plan's raw execution bullet, including commands that do not
    #: name a test path.
    execution: Optional[str] = None
    required_boundary: Tuple[str, ...] = ()
    assessable: bool = True
    exclusion_reason: Optional[str] = None

    def __post_init__(self) -> None:
        if self.required_tier is not None:
            object.__setattr__(self, "required_tier", Tier(self.required_tier))
        if self.tier_origin not in (None, "testplan", "mapping-config"):
            raise ModelError(
                "behavior {0} has unknown tier_origin {1!r}".format(
                    self.id, self.tier_origin
                ),
                recovery="Use 'testplan', 'mapping-config', or null.",
            )
        if self.tier_reviewed is not None and not isinstance(
            self.tier_reviewed, bool
        ):
            raise ModelError(
                "behavior {0} tier_reviewed must be a boolean or null".format(
                    self.id
                )
            )
        object.__setattr__(
            self,
            "required_boundary",
            tuple(
                sorted(
                    _freeze_strings(
                        self.required_boundary, name="behavior.required_boundary"
                    )
                )
            ),
        )
        if not self.id:
            raise ModelError("behavior id must not be empty")
        if not self.title:
            raise ModelError("behavior {0} must have a title".format(self.id))
        unknown = sorted(set(self.required_boundary) - _BOUNDARY_KINDS)
        if unknown:
            raise ModelError(
                "behavior {0} has unknown required_boundary value(s): {1}".format(
                    self.id, ", ".join(unknown)
                ),
                recovery="Allowed values are 'real' and 'simulated'.",
            )
        if not self.assessable and not self.exclusion_reason:
            raise ModelError(
                "behavior {0} is unassessable without an exclusion_reason".format(
                    self.id
                ),
                recovery="Set exclusion_reason so the exclusion is auditable.",
            )

    def to_dict(self) -> Dict[str, object]:
        return {
            "id": self.id,
            "title": self.title,
            "requirement_anchor": self.requirement_anchor,
            "priority": self.priority,
            "automation": self.automation,
            "required_tier": (
                self.required_tier.value if self.required_tier is not None else None
            ),
            "tier_origin": self.tier_origin,
            "tier_reviewed": self.tier_reviewed,
            "tier_label": self.tier_label,
            "tier_note": self.tier_note,
            "execution": self.execution,
            "required_boundary": list(self.required_boundary),
            "assessable": self.assessable,
            "exclusion_reason": self.exclusion_reason,
        }


@dataclass(frozen=True)
class Metric:
    """One coverage metric for one feature at one tier.

    ``value_percent`` is populated only for ``value`` and ``zero`` states.
    ``excluded`` and ``unassessable`` are counts, not lists, so the numerator
    and denominator arithmetic stays auditable.
    """

    feature: str
    tier: Tier
    metric_version: str
    state: MetricState
    numerator: Optional[int] = None
    denominator: Optional[int] = None
    value_percent: Optional[float] = None
    excluded: int = 0
    unassessable: int = 0
    #: Assessable behaviors with no declared tier. They are not part of any
    #: denominator; their presence blocks the tier metrics and is reported here
    #: so reviewers can see how much of the inventory is unassigned.
    tier_unassigned: int = 0
    #: The percentage the metric would report if the tier-unassigned behaviors
    #: were not blocking it. Set only for a blocked behavior metric.
    provisional_percent: Optional[float] = None
    #: Stable code explaining a non-value state, when one applies.
    reason: str = ""
    inputs: Tuple[str, ...] = ()
    formula: str = ""
    inventory_revision: Optional[str] = None

    def __post_init__(self) -> None:
        object.__setattr__(self, "tier", Tier(self.tier))
        object.__setattr__(self, "state", MetricState(self.state))
        if self.tier_unassigned < 0:
            raise ModelError("metric tier_unassigned must not be negative")
        if self.provisional_percent is not None:
            if self.state is not MetricState.BLOCKED:
                raise ModelError(
                    "metric {0}/{1} carries provisional_percent in state {2}; "
                    "only blocked metrics are provisional".format(
                        self.feature, self.tier.value, self.state.value
                    )
                )
            if not 0.0 <= self.provisional_percent <= 100.0:
                raise ModelError(
                    "provisional_percent must be between 0 and 100, got {0}".format(
                        self.provisional_percent
                    )
                )
        object.__setattr__(
            self, "inputs", tuple(sorted(_freeze_strings(self.inputs, name="metric.inputs")))
        )
        if self.state in (MetricState.VALUE, MetricState.ZERO):
            if self.numerator is None or self.denominator is None:
                raise ModelError(
                    "metric {0}/{1} in state {2} needs numerator and denominator".format(
                        self.feature, self.tier.value, self.state.value
                    )
                )
            if self.denominator <= 0:
                raise ModelError(
                    "metric {0}/{1} in state {2} needs a positive denominator".format(
                        self.feature, self.tier.value, self.state.value
                    ),
                    recovery="Use state 'na' when no reproducible denominator exists.",
                )
        else:
            if self.value_percent is not None:
                raise ModelError(
                    "metric {0}/{1} in state {2} must not carry value_percent".format(
                        self.feature, self.tier.value, self.state.value
                    ),
                    recovery="Only 'value' and 'zero' states carry a percentage.",
                )

    def sort_key(self) -> Tuple[str, str]:
        return (self.feature, self.tier.value)

    def to_dict(self) -> Dict[str, object]:
        return {
            "feature": self.feature,
            "tier": self.tier.value,
            "metric_version": self.metric_version,
            "state": self.state.value,
            "numerator": self.numerator,
            "denominator": self.denominator,
            "value_percent": self.value_percent,
            "excluded": self.excluded,
            "unassessable": self.unassessable,
            "tier_unassigned": self.tier_unassigned,
            "provisional_percent": self.provisional_percent,
            "reason": self.reason,
            "inputs": list(self.inputs),
            "formula": self.formula,
            "inventory_revision": self.inventory_revision,
        }


@dataclass(frozen=True)
class Diagnostic:
    """A structured note about a collector or engine operation."""

    run_id: str
    collector: str
    operation: str
    code: str
    message: str
    severity: str
    recovery: str = ""
    duration_ms: int = 0

    def __post_init__(self) -> None:
        if self.severity not in ("error", "warning", "info"):
            raise ModelError(
                "diagnostic severity must be error, warning, or info, got {0!r}".format(
                    self.severity
                )
            )
        if self.duration_ms < 0:
            raise ModelError("diagnostic duration_ms must not be negative")

    def sort_key(self) -> Tuple[str, ...]:
        return (
            self.collector,
            self.operation,
            self.code,
            self.severity,
            self.message,
        )

    def to_dict(self) -> Dict[str, object]:
        return {
            "run_id": self.run_id,
            "collector": self.collector,
            "operation": self.operation,
            "code": self.code,
            "message": self.message,
            "severity": self.severity,
            "recovery": self.recovery,
            "duration_ms": self.duration_ms,
        }


@dataclass(frozen=True)
class InventoryInfo:
    """First-class provenance for one feature's behavior inventory.

    The behavior metrics are only comparable across runs when the test plan they
    were computed from is the same, so the inventory's path, content revision,
    size, and duplicate ids travel with the report instead of hiding inside a
    free-text diagnostic.
    """

    feature: str
    path: Optional[str] = None
    revision: Optional[str] = None
    behavior_count: int = 0
    duplicate_ids: Tuple[str, ...] = ()

    def __post_init__(self) -> None:
        if not self.feature:
            raise ModelError("inventory feature must not be empty")
        if self.behavior_count < 0:
            raise ModelError("inventory behavior_count must not be negative")
        object.__setattr__(
            self,
            "duplicate_ids",
            tuple(
                sorted(
                    _freeze_strings(
                        self.duplicate_ids, name="inventory.duplicate_ids"
                    )
                )
            ),
        )

    def sort_key(self) -> Tuple[str, ...]:
        return (self.feature, self.path or "", self.revision or "")

    def to_dict(self) -> Dict[str, object]:
        return {
            "feature": self.feature,
            "path": self.path,
            "revision": self.revision,
            "behavior_count": self.behavior_count,
            "duplicate_ids": list(self.duplicate_ids),
        }


@dataclass(frozen=True)
class CollectorBatch:
    """Everything one collector produced in one run.

    The batch is immutable: collectors assembled their output locally and hand
    it over once, so nothing downstream can mutate half a result.
    """

    collector: str
    records: Tuple[EvidenceRecord, ...] = ()
    behaviors: Tuple[Behavior, ...] = ()
    metrics: Tuple[Metric, ...] = ()
    diagnostics: Tuple[Diagnostic, ...] = ()
    inventories: Tuple[InventoryInfo, ...] = ()

    def __post_init__(self) -> None:
        if not self.collector:
            raise ModelError("collector name must not be empty")
        object.__setattr__(self, "records", tuple(self.records))
        object.__setattr__(self, "behaviors", tuple(self.behaviors))
        object.__setattr__(self, "metrics", tuple(self.metrics))
        object.__setattr__(self, "diagnostics", tuple(self.diagnostics))
        object.__setattr__(self, "inventories", tuple(self.inventories))


@dataclass(frozen=True)
class MergedBatches:
    """The single merged view every later stage reads."""

    records: Tuple[EvidenceRecord, ...] = ()
    behaviors: Tuple[Behavior, ...] = ()
    metrics: Tuple[Metric, ...] = ()
    diagnostics: Tuple[Diagnostic, ...] = ()
    inventories: Tuple[InventoryInfo, ...] = ()

    def to_dict(self) -> Dict[str, object]:
        return {
            "records": [record.to_dict() for record in self.records],
            "behaviors": [behavior.to_dict() for behavior in self.behaviors],
            "metrics": [metric.to_dict() for metric in self.metrics],
            "diagnostics": [
                diagnostic.to_dict() for diagnostic in self.diagnostics
            ],
            "inventories": [
                inventory.to_dict() for inventory in self.inventories
            ],
        }


def _canonical_record_payload(record: EvidenceRecord) -> str:
    return compact_json(record.to_dict())


def _canonical_metric_payload(metric: Metric) -> str:
    return compact_json(metric.to_dict())


def _dedupe_by_key(
    items: Sequence[object],
    *,
    key,
    payload,
    kind: str,
) -> List[object]:
    """Collapse identical duplicates; reject conflicting ones.

    Ordering-independence is the point: two batches listing the same id with
    different content is a contract violation, not a race to be decided by
    input order.
    """
    chosen: Dict[object, object] = {}
    seen: Dict[object, str] = {}
    for item in items:
        item_key = key(item)
        if item_key not in chosen:
            chosen[item_key] = item
            continue
        # Only a repeated key needs the comparison payload.
        item_payload = payload(item)
        existing_payload = seen.get(item_key)
        if existing_payload is None:
            existing_payload = payload(chosen[item_key])
            seen[item_key] = existing_payload
        if existing_payload != item_payload:
            raise ModelError(
                "conflicting {0} for key {1!r}: two collectors produced different "
                "content for the same identity".format(kind, item_key),
                code="MODEL_CONFLICT",
                recovery="Give each collector output a distinct id/source, or make "
                "the duplicate content identical.",
            )
    return list(chosen.values())


def _merge_records(records: Sequence[EvidenceRecord]) -> List[EvidenceRecord]:
    """Merge records by id, enforcing the evidence-class invariant.

    Two records for the same subject may differ only by the one allowed
    transition, inferred_mapping -> declared_mapping, and the declared record
    wins regardless of input order. Any other class change is a promotion and
    raises; two records of the same class with different content are a model
    conflict.
    """
    grouped: Dict[str, List[EvidenceRecord]] = {}
    for record in records:
        grouped.setdefault(record.id, []).append(record)

    merged: List[EvidenceRecord] = []
    for evidence_id in sorted(grouped):
        group = grouped[evidence_id]
        classes = {record.evidence_class for record in group}

        if classes == {
            EvidenceClass.INFERRED_MAPPING,
            EvidenceClass.DECLARED_MAPPING,
        }:
            chosen = [
                record
                for record in group
                if record.evidence_class is EvidenceClass.DECLARED_MAPPING
            ]
        elif len(classes) > 1:
            previous, current = sorted(classes, key=lambda item: item.value)
            assert_no_promotion(previous, current, evidence_id=evidence_id)
            chosen = group  # unreachable; keeps type checkers happy
        else:
            chosen = group

        merged.extend(
            _dedupe_by_key(
                chosen,
                key=lambda record: record.id,
                payload=_canonical_record_payload,
                kind="evidence record",
            )
        )
    return merged


def merge_batches(batches: Iterable[CollectorBatch]) -> MergedBatches:
    """Merge collector batches exactly once, deterministically.

    Records dedupe by ``id``, behaviors by ``id``, metrics by
    ``(feature, tier)``, diagnostics by their full content. The result is
    sorted by stable keys, so any permutation of the same batches produces
    byte-identical output.
    """
    records: List[EvidenceRecord] = []
    behaviors: List[Behavior] = []
    metrics: List[Metric] = []
    diagnostics: List[Diagnostic] = []
    inventories: List[InventoryInfo] = []

    for batch in batches:
        records.extend(batch.records)
        behaviors.extend(batch.behaviors)
        metrics.extend(batch.metrics)
        diagnostics.extend(batch.diagnostics)
        inventories.extend(batch.inventories)

    merged_records = _merge_records(records)
    merged_behaviors = _dedupe_by_key(
        behaviors,
        key=lambda behavior: behavior.id,
        payload=lambda behavior: compact_json(behavior.to_dict()),
        kind="behavior",
    )
    merged_metrics = _dedupe_by_key(
        metrics,
        key=lambda metric: (metric.feature, metric.tier.value),
        payload=_canonical_metric_payload,
        kind="metric",
    )
    merged_diagnostics = _dedupe_by_key(
        diagnostics,
        key=lambda diagnostic: tuple(
            str(part) for part in diagnostic.to_dict().items()
        ),
        payload=lambda diagnostic: compact_json(diagnostic.to_dict()),
        kind="diagnostic",
    )
    # Two collectors may read the same test plan (the behavior and mapping
    # collectors both do). Identical inventories collapse; genuinely different
    # inventories for one feature are a bug, not an ordering question.
    merged_inventories = _dedupe_by_key(
        inventories,
        key=lambda inventory: inventory.feature,
        payload=lambda inventory: compact_json(inventory.to_dict()),
        kind="inventory",
    )

    return MergedBatches(
        records=tuple(sorted(merged_records, key=lambda record: record.sort_key())),
        behaviors=tuple(
            sorted(merged_behaviors, key=lambda behavior: behavior.id)
        ),
        metrics=tuple(sorted(merged_metrics, key=lambda metric: metric.sort_key())),
        diagnostics=tuple(
            sorted(merged_diagnostics, key=lambda diagnostic: diagnostic.sort_key())
        ),
        inventories=tuple(
            sorted(
                merged_inventories, key=lambda inventory: inventory.sort_key()
            )
        ),
    )


def assert_no_promotion(
    previous: EvidenceClass,
    current: EvidenceClass,
    *,
    evidence_id: str = "",
) -> None:
    """Reject every evidence-class change except inferred -> declared.

    This is called by :func:`merge_batches`; there is no other way for a class
    to change during a run.

    Raises:
        EvidencePromotionError: On any prohibited transition, naming the id and
            both classes.
    """
    previous = EvidenceClass(previous)
    current = EvidenceClass(current)
    if previous is current:
        return
    if (previous, current) in ALLOWED_EVIDENCE_TRANSITIONS:
        return
    raise EvidencePromotionError(
        "evidence {0} changed class {1} -> {2}; only inferred_mapping -> "
        "declared_mapping is allowed".format(
            evidence_id or "<unknown>", previous.value, current.value
        ),
        details={
            "evidence_id": evidence_id,
            "from": previous.value,
            "to": current.value,
        },
        recovery="Keep the original class, or supply a new evidence id from a "
        "collector that genuinely measures the subject.",
    )
