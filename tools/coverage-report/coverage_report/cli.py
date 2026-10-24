"""Command-line entry point for the hybrid coverage reporter.

Exit codes:
    0  success
    1  internal error
    2  scope error (unknown or ambiguous feature)
    3  configuration error

The run id is a deterministic hash of the commit, the feature selection, the
effective configuration, and the input file digests. It contains no randomness
and no timestamps, so re-running over the same inputs reproduces it. The only
volatile value in the outputs is ``generated_at`` (pin it with ``--now`` for
byte-identical reports).
"""

import argparse
import json
import os
import sys
from datetime import datetime, timezone
from typing import Dict, List, Optional, Sequence, Tuple

from . import TOOL_VERSION
from .collectors import CollectorContext, builtin, registered  # noqa: F401
from .collectors.artifacts import discover_artifacts
from .collectors.behaviors import resolve_testplan_path
from .mapping_file import mapping_config_path
from .config import CliOverrides, EffectiveConfig, load_config
from .diff import build_diff
from .errors import ConfigError, CoverageReportError, RepoError, ScopeError
from .gaps import (
    behavior_gaps,
    sort_gaps,
    tier_unassigned_gaps,
    unit_gaps,
)
from .manifest import (
    build_manifest,
    collector_status,
    command_line,
    input_digest,
    relative_path,
    sorted_input_digests,
)
from .metrics import (
    BEHAVIOR_METRIC_VERSION,
    BehaviorMetricResult,
    compute_behavior_metric,
)
from .model import (
    TIER_ORDER,
    Behavior,
    CollectorBatch,
    Diagnostic,
    EvidenceRecord,
    InventoryInfo,
    MergedBatches,
    Metric,
    MetricState,
    Tier,
    merge_batches,
)
from .publish import publish
from .render_md import render_markdown
from .report import (
    behavior_details,
    build_feature_report,
    build_summary,
    pending_review_mapping_count,
)
from .scope import (
    FeatureEntry,
    FeatureRegistry,
    ScopeResolution,
    load_registry,
    resolve_scope,
    resolve_sha,
)
from .serialize import VOLATILE_FIELDS, canonical_digest, file_digest
from .unit_metrics import (
    COVERAGE_KIND,
    LEGACY_PACKAGE_AVERAGE,
    MISSING_KIND,
    STATEMENT_WEIGHTED,
    compute_unit_metric,
    unit_scope_revision,
)

REPORT_SCHEMA_VERSION = "coverage-report/v1"
DIAGNOSTICS_SCHEMA_VERSION = "coverage-diagnostics/v1"
INPUT_DIGESTS_SCHEMA_VERSION = "coverage-inputs/v1"

ENGINE_COLLECTOR = "engine"

__all__ = ["main", "render_markdown", "VOLATILE_FIELDS"]


class _ArgumentParser(argparse.ArgumentParser):
    """Argument parser that reports usage failures with a stable code."""

    def error(self, message: str) -> None:  # type: ignore[override]
        raise _UsageError(message)


class _UsageError(CoverageReportError):
    code = "USAGE_ERROR"
    exit_code = 2


def _build_parser() -> argparse.ArgumentParser:
    parser = _ArgumentParser(
        prog="coverage-report",
        description="Compute per-feature, per-tier coverage evidence for OSAC.",
    )
    parser.add_argument(
        "feature",
        nargs="?",
        help="Jira key, feature name, or alias. Omit to report on every feature.",
    )
    parser.add_argument(
        "--repo", default=".", help="Repository root to report on (default: .)."
    )
    parser.add_argument(
        "--sha", default=None, help="Commit sha to report under (default: git HEAD)."
    )
    parser.add_argument(
        "--config", default=None, help="Path to .coverage-skill.yaml to use."
    )
    parser.add_argument(
        "--out", default=None, help="Override the output directory."
    )
    parser.add_argument(
        "--coverprofile",
        action="append",
        default=[],
        metavar="PATH",
        help="Explicit coverprofile input; may be repeated.",
    )
    parser.add_argument(
        "--junit",
        action="append",
        default=[],
        metavar="PATH",
        help="Explicit JUnit XML input; may be repeated.",
    )
    parser.add_argument(
        "--previous",
        default=None,
        metavar="PATH",
        help="Previous report.json to compare against.",
    )
    parser.add_argument(
        "--now",
        default=None,
        metavar="ISO8601",
        help="Pin generated_at for reproducible output.",
    )
    return parser


def normalize_now(value: Optional[str]) -> str:
    """Return ``value`` as canonical UTC ISO-8601, or the current time."""
    if value is None:
        return (
            datetime.now(timezone.utc)
            .replace(microsecond=0)
            .isoformat()
            .replace("+00:00", "Z")
        )
    text = value.strip()
    candidate = text[:-1] + "+00:00" if text.endswith("Z") else text
    try:
        parsed = datetime.fromisoformat(candidate)
    except ValueError as error:
        raise ConfigError(
            "invalid --now value {0!r}: {1}".format(value, error),
            code="NOW_INVALID",
            field="--now",
            recovery="Use an ISO-8601 timestamp such as 2026-10-22T00:00:00Z.",
        )
    if parsed.tzinfo is None:
        parsed = parsed.replace(tzinfo=timezone.utc)
    return (
        parsed.astimezone(timezone.utc)
        .replace(microsecond=0)
        .isoformat()
        .replace("+00:00", "Z")
    )


def _validate_explicit_inputs(
    paths: Sequence[str], *, kind: str
) -> List[str]:
    resolved: List[str] = []
    for path in paths:
        absolute = os.path.abspath(path)
        if not os.path.isfile(absolute):
            raise ConfigError(
                "explicit {0} input not found: {1}".format(kind, path),
                code="INPUT_MISSING",
                field="--{0}".format(kind),
                recovery="Pass an existing file, or omit the flag to use configured globs.",
            )
        resolved.append(absolute)
    return resolved


def _input_copy_plan(inputs: Sequence[Tuple[str, str]]) -> Dict[str, str]:
    """Map output-relative destinations to source files, avoiding collisions."""
    plan: Dict[str, str] = {}
    used: Dict[str, int] = {}
    for kind, source in inputs:
        basename = os.path.basename(source)
        key = "inputs/{0}/{1}".format(kind, basename)
        count = used.get(key, 0) + 1
        used[key] = count
        if count > 1:
            stem, extension = os.path.splitext(basename)
            key = "inputs/{0}/{1}-{2}{3}".format(kind, stem, count, extension)
        plan[key] = source
    return plan


def compute_run_id(
    *,
    sha: str,
    selection: object,
    config: EffectiveConfig,
    input_digests: Sequence[Dict[str, object]],
) -> str:
    """Return the deterministic run id.

    The digest intentionally excludes ``generated_at`` and any absolute output
    path outside the effective config.
    """
    payload = {
        "tool_version": TOOL_VERSION,
        "sha": sha,
        "selection": selection,
        "effective_config": config.to_dict(),
        "config_origins": config.origins,
        "inputs": sorted(
            (
                {
                    "kind": entry["kind"],
                    "path": entry["path"],
                    "sha256": entry["sha256"],
                }
                for entry in input_digests
            ),
            key=lambda entry: (str(entry["kind"]), str(entry["path"])),
        ),
    }
    return "run-" + canonical_digest(payload)[:16]


def _collect(
    *,
    ctx: CollectorContext,
    run_id: str,
    feature_keys: Sequence[str],
) -> Tuple[MergedBatches, List[Dict[str, object]], List[Diagnostic]]:
    """Run every registered collector, isolating failures per collector."""
    batches: List[CollectorBatch] = []
    statuses: List[Dict[str, object]] = []
    diagnostics: List[Diagnostic] = []

    for collector in registered():
        try:
            batch = collector.collect(ctx)
            if not isinstance(batch, CollectorBatch):
                raise TypeError(
                    "collector {0} returned {1}, expected CollectorBatch".format(
                        collector.name, type(batch).__name__
                    )
                )
            if batch.collector != collector.name:
                raise ValueError(
                    "collector {0} returned a batch named {1!r}".format(
                        collector.name, batch.collector
                    )
                )
            out_of_scope = sorted(
                {record.feature for record in batch.records} - set(feature_keys)
            )
            if out_of_scope:
                raise ValueError(
                    "collector {0} produced evidence for out-of-scope feature(s): {1}".format(
                        collector.name, ", ".join(out_of_scope)
                    )
                )
            out_of_scope_inventories = sorted(
                {inventory.feature for inventory in batch.inventories}
                - set(feature_keys)
            )
            if out_of_scope_inventories:
                raise ValueError(
                    "collector {0} produced inventories for out-of-scope "
                    "feature(s): {1}".format(
                        collector.name, ", ".join(out_of_scope_inventories)
                    )
                )
            batches.append(batch)
            statuses.append(
                collector_status(
                    collector.name,
                    "ok",
                    diagnostic_codes=[
                        diagnostic.code for diagnostic in batch.diagnostics
                    ],
                )
            )
        except Exception as error:  # noqa: BLE001 - isolate collector failures
            diagnostics.append(
                Diagnostic(
                    run_id=run_id,
                    collector=collector.name,
                    operation="collect",
                    code="COLLECTOR_FAILED",
                    message=str(error) or type(error).__name__,
                    severity="error",
                    recovery="Fix the collector, or remove it from the registry "
                    "to produce a report without it.",
                )
            )
            statuses.append(
                collector_status(
                    collector.name, "error", diagnostic_codes=["COLLECTOR_FAILED"]
                )
            )

    return merge_batches(batches), statuses, diagnostics


def _unavailable_metric(
    feature: str, tier: Tier, metric_version: str, run_id: str
) -> Tuple[Metric, Diagnostic]:
    metric = Metric(
        feature=feature,
        tier=tier,
        metric_version=metric_version,
        state=MetricState.UNAVAILABLE,
        reason="NO_COLLECTOR_EVIDENCE",
        formula="",
    )
    diagnostic = Diagnostic(
        run_id=run_id,
        collector=ENGINE_COLLECTOR,
        operation="compute_metrics",
        code="NO_COLLECTOR_EVIDENCE",
        message=(
            "no collector produced {0} evidence for {1}; the metric is unavailable, "
            "not zero".format(tier.value, feature)
        ),
        severity="warning",
        recovery="Register a collector that emits {0} evidence for {1} in "
        "coverage_report/collectors/, or pass the relevant artifact with "
        "--coverprofile/--junit.".format(tier.value, feature),
    )
    return metric, diagnostic


def _unit_metric(
    feature: FeatureEntry,
    *,
    collected: Dict[Tuple[str, str], Metric],
    config: EffectiveConfig,
    run_id: str,
) -> Tuple[Metric, List[Diagnostic]]:
    """Return the feature's unit metric, or an explicit unavailable state.

    Unit arithmetic stays in the ginkgo collector; the engine only fills the
    hole when no collector produced one, so a missing coverprofile is never
    reported as zero.
    """
    existing = collected.get((feature.key, Tier.UNIT.value))
    if existing is not None:
        return existing, []
    metric, diagnostic = _unavailable_metric(
        feature.key, Tier.UNIT, config.metric_versions["unit"], run_id
    )
    return metric, [diagnostic]


def _alternative_unit_metric(
    feature: FeatureEntry,
    records: Sequence[EvidenceRecord],
    *,
    config: EffectiveConfig,
) -> List[Metric]:
    """Return the other unit metric version, for side-by-side display.

    The configured version stays the reported metric; this one is derived from
    the same evidence so the legacy number can be shown in parentheses without
    a second collector.
    """
    if not any(
        getattr(record, "kind", None) in (COVERAGE_KIND, MISSING_KIND)
        for record in records
    ):
        return []
    configured = config.metric_versions["unit"]
    alternative = (
        LEGACY_PACKAGE_AVERAGE
        if configured == STATEMENT_WEIGHTED
        else STATEMENT_WEIGHTED
    )
    outcome = compute_unit_metric(
        feature.key,
        records,
        metric_version=alternative,
        expected_packages=len(feature.unit_packages),
        inventory_revision=unit_scope_revision(
            package.package for package in feature.unit_packages
        ),
    )
    return [outcome.metric]


def _behavior_tier_results(
    feature: FeatureEntry,
    *,
    behaviors: Sequence[Behavior],
    records: Sequence[EvidenceRecord],
    inventory: Optional[InventoryInfo],
    config: EffectiveConfig,
    run_id: str,
) -> Tuple[
    Dict[Tuple[str, str], Metric],
    Dict[str, List[Diagnostic]],
    Dict[str, BehaviorMetricResult],
]:
    """Compute integration and e2e behavior metrics, assessments, and gaps."""
    metrics: Dict[Tuple[str, str], Metric] = {}
    diagnostics: Dict[str, List[Diagnostic]] = {}
    tier_detail: Dict[str, BehaviorMetricResult] = {}

    for tier in (Tier.INTEGRATION, Tier.E2E):
        result = compute_behavior_metric(
            feature=feature.key,
            tier=tier,
            behaviors=behaviors,
            records=records,
            inventory=inventory,
            metric_version=config.metric_versions[tier.value],
            require_execution=config.policy.require_execution,
            run_id=run_id,
        )
        metrics[(feature.key, tier.value)] = result.metric
        diagnostics[tier.value] = list(result.diagnostics)
        tier_detail[tier.value] = result
    return metrics, diagnostics, tier_detail


def _previous_report(
    *,
    explicit_path: Optional[str],
    config: EffectiveConfig,
    resolution: ScopeResolution,
    run_id: str,
) -> Tuple[Optional[Dict[str, object]], Optional[str], Optional[str]]:
    """Return ``(report, path, digest)`` for the comparison baseline.

    An explicit ``--previous`` is always honored. Otherwise the report already
    published for this scope is used, unless it describes exactly this run: a
    rerun over identical inputs has nothing to compare, and treating it as a
    baseline would make two identical invocations produce different bytes.
    """
    if explicit_path is not None:
        path = os.path.abspath(explicit_path)
        try:
            with open(path, "r", encoding="utf-8") as handle:
                return _validate_previous(json.load(handle)), path, file_digest(path)
        except (OSError, ValueError) as error:
            raise ConfigError(
                "could not read --previous report {0}: {1}".format(
                    explicit_path, error
                ),
                code="PREVIOUS_REPORT_INVALID",
                field="--previous",
                recovery="Pass a readable report.json from an earlier run.",
            )

    path = os.path.join(config.output_dir, resolution.label, "report.json")
    if not os.path.isfile(path):
        return None, None, None
    try:
        with open(path, "r", encoding="utf-8") as handle:
            previous = json.load(handle)
    except (OSError, ValueError):
        return None, None, None
    if (
        not isinstance(previous, dict)
        or not isinstance(previous.get("metrics"), list)
        or previous.get("run_id") == run_id
    ):
        return None, None, None
    return previous, path, file_digest(path)


def _validate_previous(previous: object) -> Dict[str, object]:
    """Reject a document that is not a report before comparing against it."""
    if not isinstance(previous, dict):
        raise ConfigError(
            "--previous must point at a report.json document",
            code="PREVIOUS_REPORT_INVALID",
            field="--previous",
            recovery="Pass the report.json written by an earlier run.",
        )
    if not isinstance(previous.get("metrics"), list):
        raise ConfigError(
            "--previous report has no metrics list",
            code="PREVIOUS_REPORT_INVALID",
            field="--previous.metrics",
            recovery="Pass the report.json written by an earlier run.",
        )
    return previous


def _publish_report(
    *,
    config: EffectiveConfig,
    resolution: ScopeResolution,
    report: Dict[str, object],
    manifest: Dict[str, object],
    diagnostics: Sequence[Diagnostic],
    input_copies: Dict[str, str],
    input_digests: Sequence[Dict[str, object]],
) -> str:
    name = resolution.label
    return publish(
        config.output_dir,
        name,
        json_artifacts={
            "report.json": report,
            "run-manifest.json": manifest,
            "diagnostics/diagnostics.json": {
                "schema_version": DIAGNOSTICS_SCHEMA_VERSION,
                "run_id": report["run_id"],
                "diagnostics": [
                    diagnostic.to_dict() for diagnostic in diagnostics
                ],
            },
            "inputs/digests.json": {
                "schema_version": INPUT_DIGESTS_SCHEMA_VERSION,
                "run_id": report["run_id"],
                "inputs": list(input_digests),
            },
        },
        text_artifacts={"report.md": render_markdown(report)},
        input_copies=input_copies,
    )


def _print_success(paths: Sequence[Tuple[str, str]]) -> None:
    for label, path in paths:
        print("{0}: {1}".format(label, path))


def _fail(error: CoverageReportError, run_id: str) -> int:
    print(error.render(), file=sys.stderr)
    print("run_id: {0}".format(run_id), file=sys.stderr)
    return error.exit_code


def main(argv: Optional[Sequence[str]] = None) -> int:
    """Run the reporter. Returns the process exit code."""
    raw_argv = list(sys.argv[1:] if argv is None else argv)
    try:
        args = _build_parser().parse_args(raw_argv)
    except _UsageError as error:
        return _fail(error, "unavailable")
    except SystemExit as exit_error:  # --help
        return int(exit_error.code or 0)

    run_id = "unavailable"
    try:
        repo_root = os.path.abspath(args.repo)
        if not os.path.isdir(repo_root):
            raise RepoError(
                "repository root does not exist: {0}".format(args.repo),
                code="REPO_NOT_FOUND",
                recovery="Pass --repo with an existing directory.",
            )

        overrides = CliOverrides(
            output_dir=args.out,
            coverprofiles=tuple(args.coverprofile),
            junit=tuple(args.junit),
            config_path=args.config,
        )
        config = load_config(repo_root, overrides)
        registry: FeatureRegistry = load_registry(config.features_file)

        coverprofiles = _validate_explicit_inputs(
            args.coverprofile, kind="coverprofile"
        )
        junit = _validate_explicit_inputs(args.junit, kind="junit")

        sha, sha_source = resolve_sha(repo_root, args.sha)
        explicit_inputs: List[Tuple[str, str]] = [
            ("coverprofile", path) for path in coverprofiles
        ] + [("junit", path) for path in junit]

        # Resolve configured globs once. The collectors read these lists and
        # the digests hash the same set, so the run id cannot miss an input.
        resolved_inputs: Dict[str, Tuple[str, ...]] = {
            "coverprofile": discover_artifacts(
                coverprofiles, config.coverprofiles
            ),
            "junit": discover_artifacts(junit, config.junit),
        }

        digest_entries: Dict[Tuple[str, str], Dict[str, object]] = {}

        def add_digest(kind: str, path: str) -> None:
            key = (kind, os.path.abspath(path))
            if key in digest_entries:
                return
            digest_entries[key] = input_digest(
                kind=kind,
                repo_root=repo_root,
                path=path,
                sha256=file_digest(path),
                size_bytes=os.path.getsize(path),
            )

        for kind, paths in sorted(resolved_inputs.items()):
            for path in paths:
                add_digest(kind, path)
        add_digest("features", config.features_file)
        config_digest: Optional[str] = None
        if config.config_path is not None:
            config_digest = file_digest(config.config_path)
            add_digest("config", config.config_path)

        try:
            features, matched_by = resolve_scope(registry, args.feature)
        except ScopeError as scope_error:
            run_id = compute_run_id(
                sha=sha,
                selection={"input": args.feature, "resolved": None},
                config=config,
                input_digests=sorted_input_digests(
                    list(digest_entries.values())
                ),
            )
            return _fail(scope_error, run_id)

        # Test plans and mapping files influence the report; hash the ones the
        # collectors will actually read. The coverage-note source reads the
        # integration-testing guide when it exists.
        bootstrap_ctx = CollectorContext(
            repo_root=repo_root,
            sha=sha,
            features=features,
            config=config,
            run_id="unavailable",
            explicit_inputs={
                "coverprofile": tuple(coverprofiles),
                "junit": tuple(junit),
            },
            resolved_inputs=resolved_inputs,
        )
        for feature in features:
            testplan = resolve_testplan_path(bootstrap_ctx, feature)
            if testplan is not None:
                add_digest("testplan", testplan)
            mapping_file = mapping_config_path(bootstrap_ctx, feature.key)
            if mapping_file is not None:
                add_digest("mapping", mapping_file)
        integration_doc = os.path.join(
            repo_root, "docs", "INTEGRATION-TESTING.md"
        )
        if os.path.isfile(integration_doc):
            add_digest("integration-testing-doc", integration_doc)

        digests = sorted_input_digests(list(digest_entries.values()))

        resolution = ScopeResolution(
            requested=args.feature,
            matched_by=matched_by,
            features=features,
            sha=sha,
            sha_source=sha_source,
        )
        run_id = compute_run_id(
            sha=sha,
            selection=[feature.key for feature in features],
            config=config,
            input_digests=digests,
        )
        generated_at = normalize_now(args.now)

        ctx = CollectorContext(
            repo_root=repo_root,
            sha=sha,
            features=features,
            config=config,
            run_id=run_id,
            explicit_inputs={
                "coverprofile": tuple(coverprofiles),
                "junit": tuple(junit),
            },
            resolved_inputs=resolved_inputs,
        )
        merged, collector_statuses, collector_diagnostics = _collect(
            ctx=ctx,
            run_id=run_id,
            feature_keys=[feature.key for feature in features],
        )

        collected_metrics: Dict[Tuple[str, str], Metric] = {}
        for metric in merged.metrics:
            collected_metrics[(metric.feature, metric.tier.value)] = metric
        engine_diagnostics: List[Diagnostic] = []
        # Behavior metrics belong to the engine: they depend on the merged
        # inventory and mappings, not on one collector's partial view.
        for (feature_key, tier_name), metric in sorted(collected_metrics.items()):
            if tier_name == Tier.UNIT.value:
                continue
            engine_diagnostics.append(
                Diagnostic(
                    run_id=run_id,
                    collector=ENGINE_COLLECTOR,
                    operation="compute_metrics",
                    code="COLLECTOR_BEHAVIOR_METRIC_IGNORED",
                    message=(
                        "collector {0} emitted a {1} metric for {2}; the engine "
                        "recomputed it from the merged evidence".format(
                            metric.metric_version, tier_name, feature_key
                        )
                    ),
                    severity="warning",
                    recovery="Remove behavior-tier metrics from the collector; "
                    "only the engine may compute them.",
                )
            )

        feature_keys = {feature.key for feature in features}
        # _collect rejects out-of-scope output; assert the invariant here
        # instead of filtering the merged collections once per feature.
        records_by_feature: Dict[str, List[EvidenceRecord]] = {}
        for record in merged.records:
            assert record.feature in feature_keys, record.feature
            records_by_feature.setdefault(record.feature, []).append(record)
        behaviors_by_feature: Dict[str, List[Behavior]] = {}
        for behavior in merged.behaviors:
            feature_key = behavior.id.split("/", 1)[0]
            assert feature_key in feature_keys, behavior.id
            behaviors_by_feature.setdefault(feature_key, []).append(behavior)
        evidence_in_scope = list(merged.records)
        behaviors_in_scope = list(merged.behaviors)
        inventory_by_feature = {
            inventory.feature: inventory for inventory in merged.inventories
        }

        metrics_by_key: Dict[Tuple[str, str], Metric] = {}
        feature_reports: List[Dict[str, object]] = []
        all_gaps: List[Dict[str, object]] = []

        for feature in features:
            feature_records = records_by_feature.get(feature.key, [])
            feature_behaviors = behaviors_by_feature.get(feature.key, [])
            inventory = inventory_by_feature.get(feature.key)

            unit_metric, unit_diagnostics = _unit_metric(
                feature,
                collected=collected_metrics,
                config=config,
                run_id=run_id,
            )
            engine_diagnostics.extend(unit_diagnostics)
            metrics_by_key[(feature.key, Tier.UNIT.value)] = unit_metric

            alternatives = _alternative_unit_metric(
                feature, feature_records, config=config
            )

            behavior_metrics, tier_diagnostics, tier_results = (
                _behavior_tier_results(
                    feature,
                    behaviors=feature_behaviors,
                    records=feature_records,
                    inventory=inventory,
                    config=config,
                    run_id=run_id,
                )
            )
            metrics_by_key.update(behavior_metrics)
            for tier in (Tier.INTEGRATION, Tier.E2E):
                engine_diagnostics.extend(tier_diagnostics[tier.value])

            assessments = {
                tier.value: {
                    assessment.behavior.id: assessment.to_dict()
                    for assessment in tier_results[tier.value].assessments
                }
                for tier in (Tier.INTEGRATION, Tier.E2E)
            }

            behavior_counts = {
                "unit": 0,
                "integration": 0,
                "e2e": 0,
                "tier_unassigned": 0,
            }
            unassessable_by_tier = dict(behavior_counts)
            for behavior in feature_behaviors:
                key = (
                    behavior.required_tier.value
                    if behavior.required_tier is not None
                    else "tier_unassigned"
                )
                behavior_counts[key] = behavior_counts.get(key, 0) + 1
                if not behavior.assessable:
                    unassessable_by_tier[key] = (
                        unassessable_by_tier.get(key, 0) + 1
                    )

            # Unit-tier behaviors are shown for information only: the unit
            # metric comes from package statement coverage, and creating a
            # second unit metric would double-count the tier.
            unit_result = compute_behavior_metric(
                feature=feature.key,
                tier=Tier.UNIT,
                behaviors=feature_behaviors,
                records=feature_records,
                inventory=inventory,
                metric_version=BEHAVIOR_METRIC_VERSION,
                require_execution=config.policy.require_execution,
                run_id=run_id,
            )
            unit_assessments = {
                assessment.behavior.id: assessment.to_dict()
                for assessment in unit_result.assessments
                if assessment.behavior.required_tier is Tier.UNIT
            }

            mapping_path = mapping_config_path(ctx, feature.key)
            mapping_file = (
                relative_path(ctx.repo_root, mapping_path)
                if mapping_path is not None
                else None
            )

            feature_gaps: List[Dict[str, object]] = []
            for tier in (Tier.INTEGRATION, Tier.E2E):
                feature_gaps.extend(
                    behavior_gaps(
                        feature=feature.key,
                        tier=tier,
                        assessments=tier_results[tier.value].assessments,
                        records=feature_records,
                        testplan=feature.testplan,
                    )
                )
            feature_gaps.extend(
                unit_gaps(
                    feature=feature.key,
                    records=feature_records,
                    low_coverage_percent=config.policy.unit_low_coverage_percent,
                    testplan=feature.testplan,
                )
            )
            # One gap per tier-unassigned behavior, regardless of how many
            # tiers assessed it.
            feature_gaps.extend(
                tier_unassigned_gaps(
                    feature=feature.key,
                    assessments=tier_results[
                        Tier.INTEGRATION.value
                    ].assessments,
                    records=feature_records,
                    testplan=feature.testplan,
                )
            )
            feature_gaps = sort_gaps(feature_gaps)
            all_gaps.extend(feature_gaps)

            breakdown: Dict[str, Dict[str, object]] = {"unit": {}}
            for tier in (Tier.INTEGRATION, Tier.E2E):
                breakdown[tier.value] = tier_results[tier.value].breakdown

            feature_reports.append(
                build_feature_report(
                    feature=feature,
                    metrics={
                        tier.value: metrics_by_key[
                            (feature.key, tier.value)
                        ].to_dict()
                        for tier in TIER_ORDER
                    },
                    alternative_metrics=[
                        metric.to_dict() for metric in alternatives
                    ],
                    inventories=(
                        [inventory.to_dict()] if inventory is not None else []
                    ),
                    denominator_breakdown=breakdown,
                    behavior_details=behavior_details(
                        feature_behaviors, assessments, unit_assessments
                    ),
                    gaps=feature_gaps,
                    tier_unassigned=metrics_by_key[
                        (feature.key, Tier.INTEGRATION.value)
                    ].tier_unassigned,
                    behavior_counts=behavior_counts,
                    unassessable_by_tier=unassessable_by_tier,
                    mapping_file=mapping_file,
                )
            )

        metrics = [
            metrics_by_key[(feature.key, tier.value)]
            for feature in features
            for tier in TIER_ORDER
        ]
        diagnostics = sorted(
            list(merged.diagnostics)
            + list(collector_diagnostics)
            + list(engine_diagnostics),
            key=lambda diagnostic: diagnostic.sort_key(),
        )

        summary = build_summary(
            feature_reports=feature_reports,
            metric_versions={
                tier.value: config.metric_versions[tier.value]
                for tier in TIER_ORDER
            },
            pending_review_mappings=pending_review_mapping_count(
                evidence_in_scope
            ),
            require_execution=config.policy.require_execution,
            unit_low_coverage_percent=config.policy.unit_low_coverage_percent,
        )

        report: Dict[str, object] = {
            "schema_version": REPORT_SCHEMA_VERSION,
            "run_id": run_id,
            "tool_version": TOOL_VERSION,
            "generated_at": generated_at,
            "repo": {"sha": sha, "sha_source": sha_source},
            "scope": resolution.to_dict(),
            "metric_versions": {
                tier: config.metric_versions[tier.value] for tier in TIER_ORDER
            },
            "summary": summary,
            "features": feature_reports,
            "metrics": [metric.to_dict() for metric in metrics],
            "evidence": [record.to_dict() for record in evidence_in_scope],
            "behaviors": [behavior.to_dict() for behavior in behaviors_in_scope],
            "inventories": [
                inventory.to_dict() for inventory in merged.inventories
            ],
            "gaps": sort_gaps(all_gaps),
            "diagnostics": [diagnostic.to_dict() for diagnostic in diagnostics],
        }

        previous_report, previous_path, previous_digest = _previous_report(
            explicit_path=args.previous,
            config=config,
            resolution=resolution,
            run_id=run_id,
        )
        report["comparison"] = build_diff(previous_report, report)

        manifest = build_manifest(
            run_id=run_id,
            tool_version=TOOL_VERSION,
            generated_at=generated_at,
            repo_root=repo_root,
            sha=sha,
            sha_source=sha_source,
            feature_resolution=resolution.to_dict(),
            effective_config=config.to_dict(),
            config_origins=config.origins,
            config_file=config.config_path,
            config_digest=config_digest,
            features_file=config.features_file,
            features_digest=next(
                str(entry["sha256"])
                for entry in digests
                if entry["kind"] == "features"
            ),
            input_digests=digests,
            collectors=collector_statuses,
            metric_versions=config.metric_versions,
            command_line=command_line(raw_argv),
            previous_file=previous_path,
            previous_digest=previous_digest,
            inventories=[
                inventory.to_dict() for inventory in merged.inventories
            ],
        )

        output_path = _publish_report(
            config=config,
            resolution=resolution,
            report=report,
            manifest=manifest,
            diagnostics=diagnostics,
            input_copies=_input_copy_plan(explicit_inputs),
            input_digests=digests,
        )
        _print_success(
            [
                ("report", os.path.join(output_path, "report.json")),
                ("markdown", os.path.join(output_path, "report.md")),
                ("manifest", os.path.join(output_path, "run-manifest.json")),
                (
                    "diagnostics",
                    os.path.join(output_path, "diagnostics", "diagnostics.json"),
                ),
            ]
        )
        return 0
    except CoverageReportError as error:
        return _fail(error, run_id)
    except Exception as error:  # noqa: BLE001 - last-resort internal error
        internal = CoverageReportError(
            "{0}: {1}".format(type(error).__name__, error),
            recovery="This is a bug in the coverage reporter; re-run with the "
            "same inputs and report it with the run id.",
        )
        return _fail(internal, run_id)


if __name__ == "__main__":
    sys.exit(main())
