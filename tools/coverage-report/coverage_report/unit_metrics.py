"""Pure metric arithmetic over unit-coverage evidence.

Two metric versions are allowed for the unit tier (see
``config.ALLOWED_METRIC_VERSIONS``):

``legacy-package-average-v1``
    The arithmetic mean of the per-package statement percentages. This is the
    number the legacy dashboard reports. Semi-structured packages (one large
    package that implements most of the feature) dominate it, which is why it
    is kept only for comparison.

``statement-weighted-v2``
    ``sum(covered statements) / sum(total statements)`` over the measured
    packages. This weights a package by its size, so a small fully-covered
    helper cannot hide a large uncovered server package.

Both functions are pure: they take evidence records, return a
:class:`~coverage_report.model.Metric`, and never read a file, the network, or
the clock. Diagnostics are returned as a reason code/message pair and turned
into structured diagnostics by the collector that owns ``run_id``.

Representation notes:

* v2 stores ``numerator=covered statements`` and
  ``denominator=total statements`` verbatim.
* v1 cannot express its mean as covered/total statements, so it stores the sum
  of the per-package percentages expressed in tenths as the numerator and ten
  times the package count as the denominator. ``numerator/denominator`` then
  equals the mean percentage exactly, and the per-package percentages are also
  listed in the formula string.
* A blocked metric (missing package, partial stale evidence) still carries the
  arithmetic over the *present* packages so the shortfall is auditable, but it
  never carries ``value_percent``. No percentage is invented for missing
  evidence.
"""

from dataclasses import dataclass
from decimal import Decimal, ROUND_HALF_UP
from typing import Iterable, Optional, Sequence, Tuple

from .serialize import canonical_digest
from .model import (
    EvidenceClass,
    EvidenceRecord,
    Freshness,
    Metric,
    MetricState,
    Tier,
)

LEGACY_PACKAGE_AVERAGE = "legacy-package-average-v1"
STATEMENT_WEIGHTED = "statement-weighted-v2"
SUPPORTED_VERSIONS: Tuple[str, ...] = (LEGACY_PACKAGE_AVERAGE, STATEMENT_WEIGHTED)

#: Evidence kind the collector uses for a measured Go package.
COVERAGE_KIND = "go-package-coverage"
#: Evidence kind the collector uses for a mapped package absent from the profile.
MISSING_KIND = "go-package-missing"

_PERCENT_QUANTUM = Decimal("0.1")

LEGACY_FORMULA = (
    "mean of per-package statement percentages: sum(package percent) / package "
    "count; numerator is the sum of package percentages in tenths, denominator "
    "is ten times the package count"
)
STATEMENT_WEIGHTED_FORMULA = (
    "statement-weighted coverage: sum(covered statements) / sum(total statements)"
    " * 100"
)

#: Reason codes the collector should not turn into a second diagnostic because
#: the underlying cause already produced one (per package, or per feature).
ALREADY_REPORTED_REASONS = frozenset(
    {"UNIT_PACKAGE_MISSING", "UNIT_EVIDENCE_UNAVAILABLE"}
)


def unit_scope_revision(packages: Iterable[str]) -> str:
    """Return a stable digest of a feature's unit-package scope.

    Unit evidence is compared across commits; the commit itself is the wrong
    revision key because a rebuild at another commit does not change the scope.
    """
    return canonical_digest(sorted(packages))


def percent_of(covered: int, statements: int) -> float:
    """Return ``covered/statements*100`` rounded half-up to one decimal.

    A package with no statements has no reproducible percentage; it reports
    ``0.0`` so a caller that already established that the denominator exists
    elsewhere can keep the arithmetic simple. Callers that must distinguish
    "empty" from "zero" check the statement count first.
    """
    if statements <= 0:
        return 0.0
    value = Decimal(covered) * Decimal(100) / Decimal(statements)
    return float(value.quantize(_PERCENT_QUANTUM, rounding=ROUND_HALF_UP))


def round_percent_value(value: object) -> float:
    """Round an already computed percentage half-up to one decimal place."""
    try:
        decimal_value = Decimal(str(value))
    except Exception:  # noqa: BLE001 - defensive against non-numeric details
        return 0.0
    return float(decimal_value.quantize(_PERCENT_QUANTUM, rounding=ROUND_HALF_UP))


@dataclass(frozen=True)
class MetricOutcome:
    """A metric plus the reason its state is not a plain value.

    ``reason_code`` is empty when no diagnostic is needed. The collector maps a
    non-empty code to a :class:`~coverage_report.model.Diagnostic` carrying the
    run id, unless the code is in :data:`ALREADY_REPORTED_REASONS`.
    """

    metric: Metric
    reason_code: str = ""
    reason_message: str = ""


def _measured(records: Sequence[EvidenceRecord], feature: str) -> Tuple[EvidenceRecord, ...]:
    selected = [
        record
        for record in records
        if record.feature == feature
        and record.evidence_class is EvidenceClass.MEASURED
        and record.kind == COVERAGE_KIND
    ]
    return tuple(sorted(selected, key=lambda record: record.subject))


def _missing(records: Sequence[EvidenceRecord], feature: str) -> Tuple[EvidenceRecord, ...]:
    selected = [
        record
        for record in records
        if record.feature == feature and record.kind == MISSING_KIND
    ]
    return tuple(sorted(selected, key=lambda record: record.subject))


def _statements(record: EvidenceRecord) -> Tuple[int, int]:
    """Return ``(statements, covered)`` from a record's details, defensively."""
    details = record.details or {}
    try:
        statements = int(details.get("statements", 0))
    except (TypeError, ValueError):
        statements = 0
    try:
        covered = int(details.get("covered", 0))
    except (TypeError, ValueError):
        covered = 0
    statements = max(statements, 0)
    covered = max(min(covered, statements), 0)
    return statements, covered


def _percents(records: Sequence[EvidenceRecord]) -> Tuple[Decimal, ...]:
    values = []
    for record in records:
        raw = (record.details or {}).get("percent", 0)
        try:
            values.append(Decimal(str(raw)))
        except Exception:  # noqa: BLE001 - defensive against non-numeric details
            values.append(Decimal(0))
    return tuple(values)


def _legacy_value(
    measured: Sequence[EvidenceRecord],
) -> Tuple[int, int, float]:
    """Return ``(numerator, denominator, percent)`` for the package average."""
    tenths = [
        int((value * 10).to_integral_value(rounding=ROUND_HALF_UP))
        for value in _percents(measured)
    ]
    numerator = sum(tenths)
    denominator = 10 * len(measured)
    percent = float(
        (Decimal(numerator) / Decimal(denominator)).quantize(
            _PERCENT_QUANTUM, rounding=ROUND_HALF_UP
        )
    )
    return numerator, denominator, percent


def _weighted_value(measured: Sequence[EvidenceRecord]) -> Tuple[int, int, float]:
    """Return ``(covered, statements, percent)`` for statement weighting."""
    totals = [_statements(record) for record in measured]
    statements = sum(total for total, _ in totals)
    covered = sum(covered for _, covered in totals)
    return covered, statements, percent_of(covered, statements)


def _base_inputs(records: Sequence[EvidenceRecord]) -> Tuple[str, ...]:
    return tuple(sorted(record.id for record in records))


def _freshness_problem(
    measured: Sequence[EvidenceRecord],
) -> Optional[Tuple[str, str, str]]:
    """Return ``(state, reason_code, message)`` for unusable freshness, if any."""
    if not measured:
        return None
    stale = [record for record in measured if record.freshness is Freshness.STALE]
    if not stale:
        return None
    if len(stale) == len(measured):
        return (
            MetricState.STALE.value,
            "UNIT_EVIDENCE_STALE",
            "every measured package in this feature was produced at another "
            "revision; the unit metric is stale",
        )
    return (
        MetricState.BLOCKED.value,
        "UNIT_EVIDENCE_PARTIAL_STALE",
        "{0} of {1} measured packages are stale; the unit metric is blocked "
        "rather than mixing revisions".format(len(stale), len(measured)),
    )


def compute_unit_metric(
    feature: str,
    records: Sequence[EvidenceRecord],
    *,
    metric_version: str,
    expected_packages: int,
    inventory_revision: Optional[str] = None,
) -> MetricOutcome:
    """Compute one unit-tier metric for one feature.

    Args:
        feature: Feature key the records belong to.
        records: Evidence records produced by the Ginkgo collector for this
            feature (measured package records and gap records).
        metric_version: One of :data:`SUPPORTED_VERSIONS`.
        expected_packages: Number of package entries the feature maps in
            ``features.yaml``. Zero means the feature scope is incomplete.
        inventory_revision: Commit the report describes, copied to the metric.

    Returns:
        A :class:`MetricOutcome`; ``metric.value_percent`` is populated only for
        ``value`` and ``zero`` states.
    """
    if metric_version not in SUPPORTED_VERSIONS:
        raise ValueError(
            "unsupported unit metric version {0!r}".format(metric_version)
        )

    measured = _measured(records, feature)
    missing = _missing(records, feature)
    inputs = _base_inputs(records)

    if metric_version == LEGACY_PACKAGE_AVERAGE:
        base_formula = LEGACY_FORMULA
        numerator, denominator, percent = (
            _legacy_value(measured) if measured else (None, None, None)
        )
    else:
        base_formula = STATEMENT_WEIGHTED_FORMULA
        numerator, denominator, percent = (
            _weighted_value(measured) if measured else (None, None, None)
        )

    formula = base_formula
    if measured:
        contributions = ", ".join(
            "{0}={1}".format(
                record.subject,
                round_percent_value((record.details or {}).get("percent", 0)),
            )
            for record in measured
        )
        formula = "{0}; packages: {1}".format(base_formula, contributions)

    def outcome(
        state: MetricState,
        *,
        reason_code: str = "",
        reason_message: str = "",
        value_percent: Optional[float] = None,
        formula_text: str = formula,
    ) -> MetricOutcome:
        return MetricOutcome(
            metric=Metric(
                feature=feature,
                tier=Tier.UNIT,
                metric_version=metric_version,
                state=state,
                numerator=numerator,
                denominator=denominator,
                value_percent=value_percent,
                reason=reason_code,
                inputs=inputs,
                formula=formula_text,
                inventory_revision=inventory_revision,
            ),
            reason_code=reason_code,
            reason_message=reason_message,
        )

    # 1. No package mapped at all: the scope itself is incomplete.
    if expected_packages <= 0:
        return outcome(
            MetricState.BLOCKED,
            reason_code="UNIT_METRIC_SCOPE_INCOMPLETE",
            reason_message=(
                "feature {0} maps no unit_packages, so there is no scope to "
                "measure".format(feature)
            ),
        )

    # 2. No record at all: the collector found no input. This is not a zero.
    if not measured and not missing:
        return outcome(
            MetricState.UNAVAILABLE,
            reason_code="UNIT_EVIDENCE_UNAVAILABLE",
            reason_message=(
                "no coverprofile evidence was produced for {0}; the unit metric "
                "is unavailable, not zero".format(feature)
            ),
        )

    # 3. A mapped package is absent from the profile: blocked, not zero.
    if missing:
        subjects = ", ".join(record.subject for record in missing)
        return outcome(
            MetricState.BLOCKED,
            reason_code="UNIT_PACKAGE_MISSING",
            reason_message=(
                "mapped package(s) missing from the coverprofile: {0}; no "
                "percentage is invented for them".format(subjects)
            ),
            formula_text="{0}; partial over {1} of {2} mapped package(s)".format(
                formula, len(measured), expected_packages
            ),
        )

    # 4. Freshness: stale evidence is not usable evidence.
    freshness = _freshness_problem(measured)
    if freshness is not None:
        state_value, reason_code, reason_message = freshness
        state = MetricState(state_value)
        return MetricOutcome(
            metric=Metric(
                feature=feature,
                tier=Tier.UNIT,
                metric_version=metric_version,
                state=state,
                numerator=None if state is MetricState.STALE else numerator,
                denominator=None if state is MetricState.STALE else denominator,
                value_percent=None,
                reason=reason_code,
                inputs=inputs,
                formula=formula,
                inventory_revision=inventory_revision,
            ),
            reason_code=reason_code,
            reason_message=reason_message,
        )

    # 5. No statements to divide by: no reproducible denominator.
    total_statements = sum(_statements(record)[0] for record in measured)
    total_covered = sum(_statements(record)[1] for record in measured)
    if total_statements <= 0 or not denominator:
        return outcome(
            MetricState.NA,
            reason_code="UNIT_METRIC_NO_STATEMENTS",
            reason_message=(
                "the measured packages for {0} contain no executable "
                "statements".format(feature)
            ),
        )

    # 6. A measured zero is information; report it as zero, not as a value.
    if total_covered == 0:
        return outcome(MetricState.ZERO, value_percent=0.0)

    return outcome(MetricState.VALUE, value_percent=percent)
