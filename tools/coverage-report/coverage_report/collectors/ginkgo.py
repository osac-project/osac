"""Collect Go unit-coverage evidence from coverprofiles.

The collector reads Go coverprofiles produced by ``go test -coverprofile`` or
``ginkgo --coverprofile`` and turns them into one
:class:`~coverage_report.model.EvidenceRecord` per (feature, Go package)
mapped in ``features.yaml``. It never computes a percentage for evidence it did
not see: a mapped package that is absent from the profile becomes a
``gap_unknown`` record and blocks the feature metric.

Coverprofile format (one block per line after a ``mode:`` header)::

    mode: set|count|atomic
    github.com/org/repo/pkg/file.go:10.2,12.15 3 1

``ginkgo -r`` merges profiles and can repeat a block. Repeated blocks are
deduplicated by their source position and the highest count wins, so the result
does not depend on line order or on how many profiles contributed.

Provenance: a coverprofile carries no revision. An optional sidecar
``<profile>.meta.json`` may carry ``{"revision": ..., "command": ...,
"generated_at": ...}``. A revision equal to the reported commit makes the
evidence ``current``; a different one makes it ``stale``; a missing revision is
``unknown`` and is reported as a diagnostic rather than silently trusted.
"""

import re
from dataclasses import dataclass, field
from typing import Dict, Iterable, List, Mapping, Optional, Sequence, Set, Tuple

from ..manifest import relative_path
from ..sidecar import read_sidecar
from ..model import (
    CollectorBatch,
    Diagnostic,
    EvidenceClass,
    EvidenceRecord,
    Freshness,
    Metric,
    Tier,
    make_evidence_id,
    revision_freshness,
)
from ..scope import FeatureEntry
from ..unit_metrics import (
    ALREADY_REPORTED_REASONS,
    COVERAGE_KIND,
    MISSING_KIND,
    MetricOutcome,
    compute_unit_metric,
    unit_scope_revision,
    percent_of,
)
from . import CollectorContext

#: Collector name used in evidence ids and diagnostics. Registration is owned by
#: the CLI wiring, not by this module.
COLLECTOR_NAME = "ginkgo-unit"

META_SUFFIX = ".meta.json"
VALID_MODES = frozenset({"set", "count", "atomic"})

_OPERATION_PARSE = "parse_coverprofile"
_OPERATION_COLLECT = "collect"
_OPERATION_METRICS = "compute_metrics"

#: A coverprofile block: ``file.go:L.C,L.C numStmts count``.
_BLOCK_RE = re.compile(
    r"^(?P<file>.+):(?P<start_line>\d+)\.(?P<start_col>\d+),"
    r"(?P<end_line>\d+)\.(?P<end_col>\d+) "
    r"(?P<statements>\d+) (?P<count>\d+)$"
)

#: Diagnostic severities for reason codes produced by the metric functions.
_METRIC_SEVERITIES = {
    "UNIT_METRIC_SCOPE_INCOMPLETE": "warning",
    "UNIT_EVIDENCE_STALE": "warning",
    "UNIT_EVIDENCE_PARTIAL_STALE": "warning",
    "UNIT_METRIC_NO_STATEMENTS": "warning",
}


@dataclass(frozen=True)
class ParseIssue:
    """A recoverable coverprofile problem, before ``run_id`` is attached."""

    code: str
    message: str
    severity: str = "warning"


@dataclass(frozen=True)
class _Block:
    """One coverprofile block, identified by its source span."""

    file: str
    start_line: int
    start_col: int
    end_line: int
    end_col: int
    statements: int
    count: int

    @property
    def key(self) -> Tuple[str, int, int, int, int]:
        return (
            self.file,
            self.start_line,
            self.start_col,
            self.end_line,
            self.end_col,
        )

    @property
    def package(self) -> str:
        return self.file.rsplit("/", 1)[0] if "/" in self.file else ""

    def merged_with(self, other: "_Block") -> "_Block":
        """Return the order-independent merge of two identical blocks."""
        return _Block(
            file=self.file,
            start_line=self.start_line,
            start_col=self.start_col,
            end_line=self.end_line,
            end_col=self.end_col,
            statements=max(self.statements, other.statements),
            count=max(self.count, other.count),
        )


@dataclass(frozen=True)
class PackageCoverage:
    """Aggregated coverage for one Go package."""

    package: str
    statements: int
    covered: int
    blocks: int

    @property
    def percent(self) -> float:
        return percent_of(self.covered, self.statements)

    def to_details(self) -> Dict[str, object]:
        return {
            "statements": self.statements,
            "covered": self.covered,
            "percent": self.percent,
            "blocks": self.blocks,
        }


@dataclass(frozen=True)
class ProfileCoverage:
    """One parsed coverprofile, including its provenance."""

    path: str  # repo-relative, used as the evidence source
    mode: Optional[str]
    revision: Optional[str]
    command: Optional[str]
    generated_at: Optional[str]
    freshness: Freshness
    packages: Mapping[str, PackageCoverage]
    issues: Tuple[ParseIssue, ...] = ()
    blocks: Mapping[Tuple[str, int, int, int, int], _Block] = field(
        default_factory=dict
    )


@dataclass
class _PackageAccumulator:
    """Mutable per-package block accumulator used while merging profiles."""

    blocks: Dict[Tuple[str, int, int, int, int], _Block] = field(
        default_factory=dict
    )
    sources: Set[str] = field(default_factory=set)
    freshnesses: Set[Freshness] = field(default_factory=set)
    revisions: Set[str] = field(default_factory=set)

    def add(self, block: _Block) -> None:
        existing = self.blocks.get(block.key)
        self.blocks[block.key] = (
            block if existing is None else existing.merged_with(block)
        )

    def coverage(self, package: str) -> PackageCoverage:
        statements = 0
        covered = 0
        for block in self.blocks.values():
            statements += block.statements
            if block.count > 0:
                covered += block.statements
        return PackageCoverage(
            package=package,
            statements=statements,
            covered=covered,
            blocks=len(self.blocks),
        )


@dataclass(frozen=True)
class _MergedPackage:
    coverage: PackageCoverage
    source: str
    revision: Optional[str]
    freshness: Freshness


def parse_coverprofile(
    text: str, *, source: str
) -> Tuple[Optional[str], Mapping[str, PackageCoverage], Tuple[ParseIssue, ...]]:
    """Parse one coverprofile.

    Malformed lines are reported and skipped; valid lines are still counted.
    The returned package map is independent of line order because duplicate
    blocks are merged by source position with the maximum count.

    Returns:
        ``(mode, packages, issues)`` where ``mode`` is the declared mode (or
        ``None`` when no header was found).
    """
    mode, blocks, _packages, issues = _parse_profile_text(text, source=source)
    return mode, _aggregate_packages(blocks.values()), issues


def _parse_profile_text(
    text: str, *, source: str
) -> Tuple[
    Optional[str],
    Dict[Tuple[str, int, int, int, int], _Block],
    Mapping[str, PackageCoverage],
    Tuple[ParseIssue, ...],
]:
    """Return ``(mode, blocks, packages, issues)`` for one profile's text."""
    mode: Optional[str] = None
    header_seen = False
    blocks: Dict[Tuple[str, int, int, int, int], _Block] = {}
    issues: List[ParseIssue] = []

    for line_number, raw_line in enumerate(text.splitlines(), start=1):
        line = raw_line.strip()
        if not line:
            continue

        if not header_seen:
            header_seen = True
            if line.startswith("mode:"):
                mode = line.split(":", 1)[1].strip()
                if mode not in VALID_MODES:
                    issues.append(
                        ParseIssue(
                            code="UNIT_COVERPROFILE_MODE_UNKNOWN",
                            message=(
                                "{0}:{1}: unknown coverprofile mode {2!r}; "
                                "expected set, count, or atomic".format(
                                    source, line_number, mode
                                )
                            ),
                        )
                    )
                continue
            issues.append(
                ParseIssue(
                    code="UNIT_COVERPROFILE_MODE_MISSING",
                    message=(
                        "{0}:{1}: coverprofile has no 'mode:' header; "
                        "parsing the remaining lines anyway".format(
                            source, line_number
                        )
                    ),
                )
            )

        match = _BLOCK_RE.match(line)
        if match is None:
            snippet = line if len(line) <= 120 else line[:117] + "..."
            issues.append(
                ParseIssue(
                    code="UNIT_COVERPROFILE_LINE_MALFORMED",
                    message=(
                        "{0}:{1}: malformed coverprofile line skipped: {2!r}".format(
                            source, line_number, snippet
                        )
                    ),
                )
            )
            continue

        block = _Block(
            file=match.group("file"),
            start_line=int(match.group("start_line")),
            start_col=int(match.group("start_col")),
            end_line=int(match.group("end_line")),
            end_col=int(match.group("end_col")),
            statements=int(match.group("statements")),
            count=int(match.group("count")),
        )

        if not block.package:
            issues.append(
                ParseIssue(
                    code="UNIT_COVERPROFILE_PACKAGE_UNKNOWN",
                    message=(
                        "{0}:{1}: block file {2!r} has no package path; "
                        "block skipped".format(source, line_number, block.file)
                    ),
                )
            )
            continue

        existing = blocks.get(block.key)
        blocks[block.key] = (
            block if existing is None else existing.merged_with(block)
        )

    if not header_seen or not blocks:
        issues.append(
            ParseIssue(
                code="UNIT_COVERPROFILE_EMPTY",
                message="{0}: coverprofile contains no coverage blocks".format(
                    source
                ),
            )
        )

    return (
        mode,
        blocks,
        _aggregate_packages(blocks.values()),
        tuple(issues),
    )


def _aggregate_packages(blocks: Iterable[_Block]) -> Dict[str, PackageCoverage]:
    accumulators: Dict[str, List[_Block]] = {}
    for block in blocks:
        accumulators.setdefault(block.package, []).append(block)

    result: Dict[str, PackageCoverage] = {}
    for package in sorted(accumulators):
        statements = 0
        covered = 0
        package_blocks = accumulators[package]
        for block in package_blocks:
            statements += block.statements
            if block.count > 0:
                covered += block.statements
        result[package] = PackageCoverage(
            package=package,
            statements=statements,
            covered=covered,
            blocks=len(package_blocks),
        )
    return result


def _read_sidecar(
    profile_path: str, *, source: str
) -> Tuple[Optional[Mapping[str, object]], Tuple[ParseIssue, ...]]:
    """Read ``<profile>.meta.json`` with the shared sidecar reader."""
    data, error = read_sidecar(profile_path)
    if error:
        return None, (
            ParseIssue(
                code="UNIT_META_MALFORMED",
                message="{0}{1}: {2}".format(source, META_SUFFIX, error),
            ),
        )
    return data, ()


def _sidecar_string(
    data: Optional[Mapping[str, object]], key: str
) -> Optional[str]:
    if not data:
        return None
    value = data.get(key)
    return value if isinstance(value, str) and value else None


def _read_profile(
    ctx: CollectorContext, path: str
) -> ProfileCoverage:
    """Read and parse one coverprofile, attaching provenance."""
    source = relative_path(ctx.repo_root, path)
    issues: List[ParseIssue] = []

    try:
        with open(path, "r", encoding="utf-8") as handle:
            text = handle.read()
    except OSError as error:
        return ProfileCoverage(
            path=source,
            mode=None,
            revision=None,
            command=None,
            generated_at=None,
            freshness=Freshness.UNKNOWN,
            packages={},
            issues=(
                ParseIssue(
                    code="UNIT_COVERPROFILE_UNREADABLE",
                    message="{0}: could not read coverprofile: {1}".format(
                        source, error
                    ),
                    severity="error",
                ),
            ),
        )

    mode, blocks, packages, parse_issues = _parse_profile_text(
        text, source=source
    )
    issues.extend(parse_issues)

    sidecar, sidecar_issues = _read_sidecar(path, source=source)
    issues.extend(sidecar_issues)

    revision = _sidecar_string(sidecar, "revision")
    return ProfileCoverage(
        path=source,
        mode=mode,
        revision=revision,
        command=_sidecar_string(sidecar, "command"),
        generated_at=_sidecar_string(sidecar, "generated_at"),
        freshness=revision_freshness(revision, ctx.sha),
        packages=packages,
        issues=tuple(issues),
        blocks=blocks,
    )


def _merge_freshness(values: Iterable[Freshness]) -> Freshness:
    present = set(values)
    if Freshness.STALE in present:
        return Freshness.STALE
    if Freshness.UNKNOWN in present:
        return Freshness.UNKNOWN
    return Freshness.CURRENT


def _merge_revision(values: Iterable[Optional[str]]) -> Optional[str]:
    distinct = {value for value in values if value}
    return distinct.pop() if len(distinct) == 1 else None


def _merge_blocks(
    profiles: Sequence[ProfileCoverage],
) -> Dict[str, _MergedPackage]:
    """Build the merged per-package view used for evidence records.

    Blocks are deduplicated by source position across profiles, so running
    ``ginkgo -r`` twice over overlapping package sets does not inflate the
    statement count.
    """
    accumulators: Dict[str, _PackageAccumulator] = {}

    for profile in profiles:
        for block in profile.blocks.values():
            accumulator = accumulators.setdefault(
                block.package, _PackageAccumulator()
            )
            accumulator.add(block)
            accumulator.sources.add(profile.path)
            accumulator.freshnesses.add(profile.freshness)
            if profile.revision:
                accumulator.revisions.add(profile.revision)

    merged: Dict[str, _MergedPackage] = {}
    for package in sorted(accumulators):
        accumulator = accumulators[package]
        merged[package] = _MergedPackage(
            coverage=accumulator.coverage(package),
            source=",".join(sorted(accumulator.sources)),
            revision=_merge_revision(accumulator.revisions),
            freshness=_merge_freshness(accumulator.freshnesses),
        )
    return merged


def _package_matches(pattern: str, package: str) -> bool:
    """Match an exact import path or a ``/...`` subtree prefix."""
    if pattern.endswith("/..."):
        base = pattern[:-4]
        return package == base or package.startswith(base + "/")
    return package == pattern


def _prefix_base(pattern: str) -> Optional[str]:
    return pattern[:-4] if pattern.endswith("/...") else None


def _patterns_overlap(first: str, second: str) -> bool:
    """Return True when two scope patterns can select the same package.

    Two *exact* import paths may be shared by features explicitly (that is a
    human declaration, not an inference). Any ``/...`` prefix that swallows
    another feature's pattern makes the feature boundary ambiguous.
    """
    first_base = _prefix_base(first)
    second_base = _prefix_base(second)
    if first_base is not None and _package_matches(first, second):
        return True
    if second_base is not None and _package_matches(second, first):
        return True
    return False


def _ambiguous_pairs(
    features: Sequence[FeatureEntry],
) -> List[Tuple[FeatureEntry, str, FeatureEntry, str]]:
    pairs: List[Tuple[FeatureEntry, str, FeatureEntry, str]] = []
    ordered = sorted(features, key=lambda feature: feature.key)
    for index, first in enumerate(ordered):
        for second in ordered[index + 1 :]:
            for first_package in first.unit_packages:
                for second_package in second.unit_packages:
                    if _patterns_overlap(
                        first_package.package, second_package.package
                    ):
                        pairs.append(
                            (
                                first,
                                first_package.package,
                                second,
                                second_package.package,
                            )
                        )
    return pairs


def _diagnostic(
    ctx: CollectorContext,
    *,
    operation: str,
    code: str,
    message: str,
    severity: str,
    recovery: str = "",
) -> Diagnostic:
    return Diagnostic(
        run_id=ctx.run_id,
        collector=COLLECTOR_NAME,
        operation=operation,
        code=code,
        message=message,
        severity=severity,
        recovery=recovery,
    )


def _issues_to_diagnostics(
    ctx: CollectorContext, profile: ProfileCoverage
) -> List[Diagnostic]:
    diagnostics = []
    for issue in profile.issues:
        diagnostics.append(
            _diagnostic(
                ctx,
                operation=_OPERATION_PARSE,
                code=issue.code,
                message=issue.message,
                severity=issue.severity,
                recovery=(
                    "Fix the coverprofile or regenerate it with "
                    "'ginkgo --cover --coverprofile=<file>'."
                    if issue.severity == "error"
                    else "Inspect the profile; valid lines were still counted."
                ),
            )
        )
    return diagnostics


def _measured_record(
    feature: FeatureEntry,
    package: _MergedPackage,
) -> EvidenceRecord:
    return EvidenceRecord(
        id=make_evidence_id(
            COLLECTOR_NAME, COVERAGE_KIND, package.coverage.package, package.source
        ),
        evidence_class=EvidenceClass.MEASURED,
        tier=Tier.UNIT,
        feature=feature.key,
        subject=package.coverage.package,
        source=package.source,
        collector=COLLECTOR_NAME,
        kind=COVERAGE_KIND,
        revision=package.revision,
        freshness=package.freshness,
        details=package.coverage.to_details(),
    )


def _missing_record(
    feature: FeatureEntry,
    pattern: str,
    rationale: str,
    *,
    source: str,
    revision: Optional[str],
    freshness: Freshness,
) -> EvidenceRecord:
    return EvidenceRecord(
        id=make_evidence_id(COLLECTOR_NAME, MISSING_KIND, pattern, source),
        evidence_class=EvidenceClass.GAP_UNKNOWN,
        tier=Tier.UNIT,
        feature=feature.key,
        subject=pattern,
        source=source,
        collector=COLLECTOR_NAME,
        kind=MISSING_KIND,
        revision=revision,
        freshness=freshness,
        details={
            "pattern": pattern,
            "rationale": rationale,
            "profiles": sorted(source.split(",")),
        },
    )


def _feature_records(
    ctx: CollectorContext,
    feature: FeatureEntry,
    merged: Mapping[str, _MergedPackage],
    *,
    all_sources: str,
    overall_revision: Optional[str],
    overall_freshness: Freshness,
) -> Tuple[List[EvidenceRecord], List[Diagnostic]]:
    records: List[EvidenceRecord] = []
    diagnostics: List[Diagnostic] = []

    ordered_packages = sorted(merged)
    for unit_package in feature.unit_packages:
        pattern = unit_package.package
        matched = [
            merged[package]
            for package in ordered_packages
            if _package_matches(pattern, package)
        ]
        if not matched:
            records.append(
                _missing_record(
                    feature,
                    pattern,
                    unit_package.rationale,
                    source=all_sources,
                    revision=overall_revision,
                    freshness=overall_freshness,
                )
            )
            diagnostics.append(
                _diagnostic(
                    ctx,
                    operation=_OPERATION_COLLECT,
                    code="UNIT_PACKAGE_MISSING",
                    message=(
                        "feature {0} maps package {1!r} but the coverprofile "
                        "does not contain it; the gap is unknown, not zero".format(
                            feature.key, pattern
                        )
                    ),
                    severity="warning",
                    recovery=(
                        "Run the tests for that package with coverage, or fix the "
                        "unit_packages entry if the import path changed."
                    ),
                )
            )
            continue

        for package in matched:
            records.append(_measured_record(feature, package))

    return records, diagnostics


def _metric_diagnostic(
    ctx: CollectorContext, outcome: MetricOutcome
) -> Optional[Diagnostic]:
    code = outcome.reason_code
    if not code or code in ALREADY_REPORTED_REASONS:
        return None
    return _diagnostic(
        ctx,
        operation=_OPERATION_METRICS,
        code=code,
        message=outcome.reason_message or code,
        severity=_METRIC_SEVERITIES.get(code, "warning"),
        recovery=(
            "Supply the missing evidence, or narrow the feature's unit_packages "
            "to packages the coverprofile actually measures."
        ),
    )


def _empty_batch(
    ctx: CollectorContext,
    features: Sequence[FeatureEntry],
    *,
    diagnostics: Sequence[Diagnostic] = (),
) -> CollectorBatch:
    """Return the batch for "no usable coverprofile": no records, no zeros."""
    metrics: List[Metric] = []
    all_diagnostics = list(diagnostics)
    for feature in features:
        outcome = compute_unit_metric(
            feature.key,
            (),
            metric_version=ctx.config.metric_versions["unit"],
            expected_packages=len(feature.unit_packages),
            inventory_revision=unit_scope_revision(
                package.package for package in feature.unit_packages
            ),
        )
        metrics.append(outcome.metric)
        all_diagnostics.append(
            _diagnostic(
                ctx,
                operation=_OPERATION_COLLECT,
                code="UNIT_EVIDENCE_UNAVAILABLE",
                message=(
                    "no coverprofile was found for feature {0}; unit coverage is "
                    "unavailable, not zero".format(feature.key)
                ),
                severity="warning",
                recovery=(
                    "Run 'ginkgo run -r --cover --coverprofile=<file>' (or "
                    "'go test -coverprofile=<file>') and pass the profile with "
                    "--coverprofile, or configure artifacts.coverprofiles."
                ),
            )
        )
    return CollectorBatch(
        collector=COLLECTOR_NAME,
        records=(),
        metrics=tuple(sorted(metrics, key=lambda metric: metric.sort_key())),
        diagnostics=tuple(
            sorted(all_diagnostics, key=lambda diagnostic: diagnostic.sort_key())
        ),
    )


class GinkgoUnitCollector:
    """Collect unit coverage evidence from Go coverprofiles."""

    name = COLLECTOR_NAME

    def collect(self, ctx: CollectorContext) -> CollectorBatch:
        features = tuple(sorted(ctx.features, key=lambda feature: feature.key))
        diagnostics: List[Diagnostic] = []

        paths = ctx.resolved_inputs.get("coverprofile", ())
        if not paths:
            return _empty_batch(ctx, features)

        profiles: List[ProfileCoverage] = []
        for path in paths:
            profile = _read_profile(ctx, path)
            profiles.append(profile)
            diagnostics.extend(_issues_to_diagnostics(ctx, profile))

        usable = [profile for profile in profiles if profile.packages]
        if not usable:
            return _empty_batch(ctx, features, diagnostics=diagnostics)

        for profile in usable:
            if profile.revision is None:
                diagnostics.append(
                    _diagnostic(
                        ctx,
                        operation=_OPERATION_PARSE,
                        code="UNIT_REVISION_UNKNOWN",
                        message=(
                            "{0}: coverprofile has no revision metadata; its "
                            "freshness is unknown".format(profile.path)
                        ),
                        severity="warning",
                        recovery=(
                            "Write a sidecar {0}{1} containing "
                            '{{"revision": "<commit sha>"}}.'.format(
                                profile.path, META_SUFFIX
                            )
                        ),
                    )
                )

        merged = _merge_blocks(usable)
        all_sources = ",".join(sorted(profile.path for profile in usable))
        overall_revision = _merge_revision(
            profile.revision for profile in usable
        )
        overall_freshness = _merge_freshness(
            profile.freshness for profile in usable
        )

        for (
            first,
            first_package,
            second,
            second_package,
        ) in _ambiguous_pairs(features):
            diagnostics.append(
                _diagnostic(
                    ctx,
                    operation=_OPERATION_COLLECT,
                    code="UNIT_PACKAGE_AMBIGUOUS",
                    message=(
                        "features {0} ({1!r}) and {2} ({3!r}) overlap; a measured "
                        "package can satisfy both scopes".format(
                            first.key,
                            first_package,
                            second.key,
                            second_package,
                        )
                    ),
                    severity="error",
                    recovery=(
                        "Replace each '/...' prefix with explicit import paths, "
                        "or remove the overlapping entry."
                    ),
                )
            )

        records: List[EvidenceRecord] = []
        for feature in features:
            feature_records, feature_diagnostics = _feature_records(
                ctx,
                feature,
                merged,
                all_sources=all_sources,
                overall_revision=overall_revision,
                overall_freshness=overall_freshness,
            )
            records.extend(feature_records)
            diagnostics.extend(feature_diagnostics)

        metrics: List[Metric] = []
        for feature in features:
            feature_records = [
                record for record in records if record.feature == feature.key
            ]
            outcome = compute_unit_metric(
                feature.key,
                feature_records,
                metric_version=ctx.config.metric_versions["unit"],
                expected_packages=len(feature.unit_packages),
                inventory_revision=unit_scope_revision(
                    package.package for package in feature.unit_packages
                ),
            )
            metrics.append(outcome.metric)
            metric_diagnostic = _metric_diagnostic(ctx, outcome)
            if metric_diagnostic is not None:
                diagnostics.append(metric_diagnostic)

        return CollectorBatch(
            collector=COLLECTOR_NAME,
            records=tuple(sorted(records, key=lambda record: record.sort_key())),
            metrics=tuple(sorted(metrics, key=lambda metric: metric.sort_key())),
            diagnostics=tuple(
                sorted(diagnostics, key=lambda diagnostic: diagnostic.sort_key())
            ),
        )


#: Convenience instance for the CLI wiring.
COLLECTOR = GinkgoUnitCollector()


def collect(ctx: CollectorContext) -> CollectorBatch:
    """Module-level entry point mirroring the collector protocol."""
    return COLLECTOR.collect(ctx)
