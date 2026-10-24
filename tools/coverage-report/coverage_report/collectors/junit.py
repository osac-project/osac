"""Collector for execution evidence recorded in JUnit XML reports.

One JUnit ``<testcase>`` becomes one ``observed_execution`` evidence record.
The class is deliberately never ``measured``: a passing, failing, or skipped
test is an observation of a run, not a measurement of statement coverage.

The collector is not registered here. The CLI wires it during a later wave.

Identity
--------
``subject`` is :func:`normalize_test_identity`'s output:
``"<suite-or-classname>::<name>"`` with Ginkgo's ``"[It] "`` prefix removed and
whitespace collapsed. Later waves (mapping join, behavior attribution) should
call the same function so a mapping's ``tests[].name`` joins to execution
evidence by identity rather than by substring.

Provenance
----------
The revision comes from a ``<file>.meta.json`` sidecar first, then from
``<properties>`` on each ``<testsuite>`` (``revision``, ``git_sha``, or
``commit``). A revision equal to ``ctx.sha`` is ``current``; a different
revision is ``stale``; a missing revision is ``unknown`` and raises
``EXECUTION_REVISION_UNKNOWN``. Freshness is never assumed to be current.

Safety
------
``defusedxml`` is not a dependency, so a file containing a ``DOCTYPE`` or
``ENTITY`` declaration is rejected before parsing. That prevents entity
expansion (including external entities) without relying on the standard
library's parser settings.

Tier inference
--------------
Tiers are inferred from content first (see :func:`_infer_tier`), then from the
explicit filename convention (``*.unit.xml``, ``*.integration.xml``,
``*.e2e.xml``). A testcase whose tier cannot be established is skipped and
reported with ``EXECUTION_TIER_UNKNOWN``; ``EvidenceRecord`` requires a tier,
so guessing would be worse than reporting the gap.

Feature attribution
-------------------
A JUnit file is not feature-scoped. A testcase is attributed to a feature when
its identity matches the feature key, name, or an alias at a word boundary, or
when it matches a test listed for that feature in
``config/mappings/<KEY>.yaml`` (``mappings[].tests[].path``/``name``). The first
matching feature in scope order wins, which keeps output deterministic when
aliases overlap. Unattributed testcases are dropped and counted in one
``EXECUTION_UNATTRIBUTED`` diagnostic.
"""

import os
import re
from dataclasses import dataclass, replace
from typing import Dict, List, Optional, Sequence, Tuple
from xml.etree import ElementTree

from ..model import (
    CollectorBatch,
    Diagnostic,
    EvidenceClass,
    EvidenceRecord,
    Freshness,
    Tier,
    make_evidence_id,
    revision_freshness,
)
from ..manifest import relative_path
from ..sidecar import read_sidecar
from . import CollectorContext
from .behaviors import infer_tier_from_path
from .mappings import load_mapping_config, mapping_test_entries

COLLECTOR_NAME = "junit"
KIND = "junit-testcase"

OUTCOME_PASSED = "passed"
OUTCOME_FAILED = "failed"
OUTCOME_SKIPPED = "skipped"
OUTCOME_UNKNOWN = "unknown"

#: Status values that positively assert a pass.
_PASS_STATUSES = frozenset({"passed", "success", "pass"})
_FAIL_STATUSES = frozenset({"failed", "failure", "error"})
_SKIP_STATUSES = frozenset({"skipped", "pending", "disabled"})

#: Failure text is truncated to this many characters.
MESSAGE_LIMIT = 200

#: Property names that carry the commit a suite ran against.
_REVISION_PROPERTIES = ("revision", "git_sha", "commit")

#: Explicit ``--junit`` tier hint, by filename suffix.
_TIER_SUFFIXES = (
    (".unit.xml", Tier.UNIT),
    (".integration.xml", Tier.INTEGRATION),
    (".e2e.xml", Tier.E2E),
)

_DOCTYPE_OR_ENTITY = re.compile(r"<!\s*(?:DOCTYPE|ENTITY)", re.IGNORECASE)
_GINKGO_PREFIX = re.compile(r"^\s*\[It\]\s+", re.IGNORECASE)
_WHITESPACE = re.compile(r"\s+")

_OUTCOME_RANK = {OUTCOME_PASSED: 0, OUTCOME_SKIPPED: 1, OUTCOME_FAILED: 2}


def normalize_test_identity(suite: str, name: str) -> str:
    """Return the stable identity for one testcase.

    Args:
        suite: The test's ``classname``, or the enclosing ``<testsuite>`` name
            when ``classname`` is absent (Ginkgo puts the suite there).
        name: The testcase ``name``. Ginkgo's ``"[It] "`` prefix is removed.

    Returns:
        ``"<suite>::<name>"`` with interior whitespace collapsed to single
        spaces. When the suite is empty the name alone is returned.

    The function is exported so mapping records produced by other waves can
    build the same identity instead of re-implementing the rules.
    """
    suite_part = _WHITESPACE.sub(" ", (suite or "").strip())
    name_part = _WHITESPACE.sub(" ", (name or "").strip())
    name_part = _GINKGO_PREFIX.sub("", name_part).strip()
    if suite_part and name_part:
        return "{0}::{1}".format(suite_part, name_part)
    return suite_part or name_part


def match_key(value: object) -> str:
    """Normalize a test name or identity for comparison."""
    return _WHITESPACE.sub(" ", str(value or "").strip()).casefold()


def normalize_test_path(value: object) -> str:
    """Normalize a file path for identity matching."""
    text = str(value or "").replace("\\", "/")
    while text.startswith("./"):
        text = text[2:]
    return text.strip("/").casefold()


@dataclass(frozen=True)
class ExecutionMatchKeys:
    """Precomputed lookup keys for one execution record."""

    identity: str
    name: str
    prefixes: Tuple[str, ...]
    path_suffixes: Tuple[str, ...]


def execution_match_keys(
    identity: str, path: str = "", source: str = ""
) -> ExecutionMatchKeys:
    """Return the normalized keys a mapping can match against.

    ``prefixes`` are the ``::``-bounded prefixes of the identity, which cover a
    mapping recorded at suite/describe granularity. ``path_suffixes`` cover the
    ``endswith('/' + mapped_path)`` rule for both the reported file and the
    artifact path.
    """
    identity_key = match_key(identity)
    name_key = identity_key.rsplit("::", 1)[-1]
    prefixes: List[str] = []
    start = 0
    while True:
        index = identity_key.find("::", start)
        if index < 0:
            break
        prefixes.append(identity_key[:index])
        start = index + 2

    suffixes: List[str] = []
    for raw in (path, source):
        key = normalize_test_path(raw)
        if not key:
            continue
        parts = key.split("/")
        for index in range(len(parts)):
            suffix = "/".join(parts[index:])
            if suffix not in suffixes:
                suffixes.append(suffix)
    return ExecutionMatchKeys(
        identity=identity_key,
        name=name_key,
        prefixes=tuple(dict.fromkeys(prefixes)),
        path_suffixes=tuple(suffixes),
    )


@dataclass(frozen=True)
class _Observation:
    """A single testcase before cross-file deduplication."""

    identity: str
    outcome: str
    tier: Tier
    feature: str
    source: str
    revision: Optional[str]
    timestamp: Optional[str]
    freshness: Freshness
    details: Dict[str, object]


class JunitCollector:
    """Read JUnit XML files and emit ``observed_execution`` evidence."""

    name = COLLECTOR_NAME

    def collect(self, ctx: CollectorContext) -> CollectorBatch:
        """Return one batch for every JUnit input reachable from ``ctx``."""
        diagnostics: List[Diagnostic] = []
        paths = list(ctx.resolved_inputs.get("junit", ()))
        if not paths:
            diagnostics.append(
                _diagnostic(
                    ctx,
                    "EXECUTION_EVIDENCE_UNAVAILABLE",
                    (
                        "no JUnit XML inputs were found; execution evidence is "
                        "unavailable, which is not the same as untested"
                    ),
                    severity="warning",
                    operation="discover",
                    recovery=(
                        "Pass --junit PATH, or set artifacts.junit globs in "
                        ".coverage-skill.yaml to existing JUnit XML reports."
                    ),
                )
            )
            return CollectorBatch(
                collector=COLLECTOR_NAME, diagnostics=tuple(diagnostics)
            )

        attribution = _AttributionIndex(ctx)
        observations: List[_Observation] = []
        unattributed = 0

        for path in paths:
            file_observations, file_diagnostics, file_unattributed = _collect_file(
                ctx, path, attribution
            )
            observations.extend(file_observations)
            diagnostics.extend(file_diagnostics)
            unattributed += file_unattributed

        records, dedupe_diagnostics = _deduplicate(ctx, observations)
        diagnostics.extend(dedupe_diagnostics)

        if unattributed:
            diagnostics.append(
                _diagnostic(
                    ctx,
                    "EXECUTION_UNATTRIBUTED",
                    (
                        "{0} testcase(s) did not match a feature key, name, "
                        "alias, or mapping and were not recorded".format(unattributed)
                    ),
                    severity="info",
                    operation="attribute",
                    recovery=(
                        "Add the test to config/mappings/<KEY>.yaml or include "
                        "the feature key/name in the test name."
                    ),
                )
            )

        return CollectorBatch(
            collector=COLLECTOR_NAME,
            records=tuple(sorted(records, key=lambda record: record.sort_key())),
            diagnostics=tuple(diagnostics),
        )


# ---------------------------------------------------------------------------
# Discovery


# ---------------------------------------------------------------------------
# Per-file parsing


def _collect_file(
    ctx: CollectorContext, path: str, attribution: "_AttributionIndex"
) -> Tuple[List[_Observation], List[Diagnostic], int]:
    """Parse one JUnit file. Recoverable problems become diagnostics."""
    diagnostics: List[Diagnostic] = []
    source = relative_path(ctx.repo_root, path)
    basename = os.path.basename(path)
    source_tier = infer_tier_from_path(source)

    try:
        with open(path, "r", encoding="utf-8", errors="replace") as handle:
            text = handle.read()
    except OSError as error:
        diagnostics.append(
            _diagnostic(
                ctx,
                "JUNIT_UNREADABLE",
                "{0}: {1}".format(source, error),
                severity="warning",
                operation="parse",
                recovery="Check that the JUnit report exists and is readable.",
            )
        )
        return [], diagnostics, 0

    # Reject declarations that enable entity expansion before any parser sees
    # the document. ``defusedxml`` is not available to do this for us.
    if _DOCTYPE_OR_ENTITY.search(text):
        diagnostics.append(
            _diagnostic(
                ctx,
                "JUNIT_REJECTED",
                "{0}: DOCTYPE/ENTITY declarations are not accepted in JUnit XML".format(
                    source
                ),
                severity="warning",
                operation="parse",
                recovery=(
                    "Regenerate the report without a DTD; JUnit XML reports do "
                    "not need entity declarations."
                ),
            )
        )
        return [], diagnostics, 0

    try:
        root = ElementTree.fromstring(text)
    except (ElementTree.ParseError, ValueError) as error:
        diagnostics.append(
            _diagnostic(
                ctx,
                "JUNIT_UNREADABLE",
                "{0}: malformed XML ({1})".format(source, error),
                severity="warning",
                operation="parse",
                recovery=(
                    "Regenerate the JUnit report; truncated XML cannot be "
                    "trusted."
                ),
            )
        )
        return [], diagnostics, 0

    root_tag = _local_name(root.tag)
    if root_tag == "testsuites":
        suites = [
            element
            for element in root.iter()
            if _local_name(element.tag) == "testsuite"
        ]
    elif root_tag == "testsuite":
        suites = [root]
    else:
        diagnostics.append(
            _diagnostic(
                ctx,
                "JUNIT_UNREADABLE",
                "{0}: unexpected root element <{1}>".format(source, root_tag),
                severity="warning",
                operation="parse",
                recovery=(
                    "Point the collector at JUnit XML with a <testsuite> or "
                    "<testsuites> root."
                ),
            )
        )
        return [], diagnostics, 0

    sidecar_data, sidecar_error = read_sidecar(path)
    sidecar = sidecar_data or {}
    if sidecar_error:
        diagnostics.append(
            _diagnostic(
                ctx,
                "EXECUTION_METADATA_UNREADABLE",
                "{0}: {1}".format(source, sidecar_error),
                severity="warning",
                operation="provenance",
                recovery=(
                    "Fix or remove the sidecar; leftover metadata from another "
                    "run must not silently label this report."
                ),
            )
        )

    sidecar_revision = _clean(sidecar.get("revision"))
    sidecar_timestamp = _clean(sidecar.get("generated_at"))
    ci_run_id = _clean(sidecar.get("ci_run_id"))
    ci_url = _clean(sidecar.get("ci_url"))

    observations: List[_Observation] = []
    missing_revision = 0
    tier_unknown = 0
    unattributed = 0

    for suite in suites:
        properties = _suite_properties(suite)
        suite_name = _clean(suite.get("name")) or ""
        suite_revision = _first_property(properties, _REVISION_PROPERTIES)
        # The sidecar is written next to the artifact by CI and is the
        # file-level provenance; the embedded property is the fallback.
        revision = sidecar_revision or suite_revision
        timestamp = _clean(suite.get("timestamp")) or sidecar_timestamp

        testcases = [
            element
            for element in suite.iter()
            if _local_name(element.tag) == "testcase"
        ]
        if revision is None and testcases:
            missing_revision += 1

        for testcase in testcases:
            classname = _clean(testcase.get("classname")) or ""
            raw_name = _clean(testcase.get("name")) or ""
            name = _GINKGO_PREFIX.sub("", raw_name).strip()
            identity = normalize_test_identity(classname or suite_name, raw_name)
            file_attr = normalize_test_path(_clean(testcase.get("file")) or "")

            tier = _infer_tier(
                classname=classname,
                file_attr=file_attr,
                source_tier=source_tier,
                basename=basename,
            )
            if tier is None:
                tier_unknown += 1
                continue

            feature = attribution.attribute(
                identity=identity,
                name=name,
                file_attr=file_attr,
                source=source,
            )
            if feature is None:
                unattributed += 1
                continue

            outcome, message = _outcome_of(testcase)
            freshness = revision_freshness(revision, ctx.sha)

            details: Dict[str, object] = {
                "outcome": outcome,
                "duration_s": _duration_of(testcase),
                "file": file_attr or source,
                "ci_run_id": ci_run_id,
                "ci_url": ci_url,
            }
            line = _line_of(testcase)
            if line is not None:
                details["line"] = line
            if message:
                details["message"] = message

            observations.append(
                _Observation(
                    identity=identity,
                    outcome=outcome,
                    tier=tier,
                    feature=feature,
                    source=source,
                    revision=revision,
                    timestamp=timestamp,
                    freshness=freshness,
                    details=details,
                )
            )

    if tier_unknown:
        diagnostics.append(
            _diagnostic(
                ctx,
                "EXECUTION_TIER_UNKNOWN",
                (
                    "{0}: {1} testcase(s) have no inferable tier and no "
                    "*.unit.xml/*.integration.xml/*.e2e.xml filename hint; "
                    "they were not recorded".format(source, tier_unknown)
                ),
                severity="warning",
                operation="tier",
                recovery=(
                    "Rename the report to <name>.<tier>.xml, pass a file with "
                    "one, or use a classname/path that identifies the tier."
                ),
            )
        )

    if missing_revision:
        diagnostics.append(
            _diagnostic(
                ctx,
                "EXECUTION_REVISION_UNKNOWN",
                (
                    "{0}: no revision in a sidecar or <properties> for {1} "
                    "testsuite(s); freshness is unknown".format(
                        source, missing_revision
                    )
                ),
                severity="warning",
                operation="provenance",
                recovery=(
                    "Write <file>.meta.json with a revision, or add a revision/"
                    "git_sha/commit <property> to the testsuite."
                ),
            )
        )

    return observations, diagnostics, unattributed


def _suite_properties(suite: ElementTree.Element) -> Dict[str, str]:
    values: Dict[str, str] = {}
    for properties in suite:
        if _local_name(properties.tag) != "properties":
            continue
        for prop in properties:
            if _local_name(prop.tag) != "property":
                continue
            key = _clean(prop.get("name"))
            value = _clean(prop.get("value")) or _clean(prop.text)
            if key and value:
                values.setdefault(key.lower(), value)
    return values


def _first_property(properties: Dict[str, str], names: Sequence[str]) -> Optional[str]:
    for name in names:
        value = properties.get(name)
        if value:
            return value
    return None


def _outcome_of(testcase: ElementTree.Element) -> Tuple[str, str]:
    """Return ``(outcome, failure message)`` for one testcase.

    Passing is a positive claim: a testcase passes only when it has no
    failure/error/skipped child and either no ``status`` attribute or one of the
    known pass values. An unrecognized status such as ``notrun`` is
    ``unknown``, never a pass.
    """
    for child in testcase:
        tag = _local_name(child.tag)
        if tag in ("failure", "error"):
            return OUTCOME_FAILED, _truncate(
                _clean(child.get("message")) or _clean(child.text)
            )
        if tag == "skipped":
            return OUTCOME_SKIPPED, ""

    status = (_clean(testcase.get("status")) or "").lower()
    if not status or status in _PASS_STATUSES:
        return OUTCOME_PASSED, ""
    if status in _FAIL_STATUSES:
        return OUTCOME_FAILED, ""
    if status in _SKIP_STATUSES:
        return OUTCOME_SKIPPED, ""
    return OUTCOME_UNKNOWN, ""


def _duration_of(testcase: ElementTree.Element) -> Optional[float]:
    raw = _clean(testcase.get("time"))
    if raw is None:
        return None
    try:
        return float(raw)
    except ValueError:
        return None


def _line_of(testcase: ElementTree.Element) -> Optional[int]:
    raw = _clean(testcase.get("line"))
    if raw is None:
        return None
    try:
        return int(raw)
    except ValueError:
        return None


# ---------------------------------------------------------------------------
# Tier inference


def _infer_tier(
    *,
    classname: str,
    file_attr: str,
    source_tier: Optional[Tier],
    basename: str,
) -> Optional[Tier]:
    """Infer the tier of one testcase, or return ``None`` when unknowable.

    The path rules live in :func:`behaviors.infer_tier_from_path`; this
    function only adapts a dotted classname and adds the ``*.tier.xml``
    filename fallback. The category order is preserved: e2e, integration, then
    unit, with the classname considered before the artifact path.
    """
    classname_path = classname.replace(".", "/") if classname else ""
    class_tier = infer_tier_from_path(classname_path) if classname_path else None
    reference_tiers = [
        tier
        for tier in (infer_tier_from_path(file_attr), source_tier)
        if tier is not None
    ]

    for tier in (Tier.E2E, Tier.INTEGRATION):
        if class_tier is tier or tier in reference_tiers:
            return tier
    if class_tier is Tier.UNIT:
        return Tier.UNIT

    lowered = basename.lower()
    for suffix, tier in _TIER_SUFFIXES:
        if lowered.endswith(suffix):
            return tier
    return None


# ---------------------------------------------------------------------------
# Feature attribution


class _AttributionIndex:
    """Compiled attribution rules for one collection run.

    Feature needles are compiled once. Mapping files are loaded once per
    feature and indexed by test name and path, so attributing a testcase is a
    handful of dictionary lookups instead of a scan over every feature and
    every mapping entry.
    """

    def __init__(self, ctx: CollectorContext) -> None:
        self._ctx = ctx
        self._features = {feature.key: feature for feature in ctx.features}
        self._order = {
            feature.key: index for index, feature in enumerate(ctx.features)
        }
        self._needles = [
            (
                feature.key,
                tuple(
                    re.compile(
                        r"(?<![0-9A-Za-z])"
                        + re.escape(needle)
                        + r"(?![0-9A-Za-z])",
                        re.IGNORECASE,
                    )
                    for needle in self._feature_needles(feature)
                ),
            )
            for feature in ctx.features
        ]
        self._cache: Dict[str, Tuple[Dict[str, str], ...]] = {}
        self._by_name: Dict[str, List[str]] = {}
        self._by_path: Dict[str, List[str]] = {}
        for feature in ctx.features:
            for entry in self._tests(feature.key):
                name = match_key(entry.get("name"))
                if name:
                    self._by_name.setdefault(name, []).append(feature.key)
                path = normalize_test_path(entry.get("path"))
                if path:
                    self._by_path.setdefault(path, []).append(feature.key)

    @staticmethod
    def _feature_needles(feature) -> Tuple[str, ...]:
        needles = [feature.key, feature.name]
        needles.extend(feature.aliases)
        return tuple(needle for needle in needles if needle)

    def _tests(self, key: str) -> Tuple[Dict[str, str], ...]:
        if key not in self._cache:
            feature = self._features[key]
            data = load_mapping_config(
                self._ctx, feature, diagnostics=[]
            )
            self._cache[key] = (
                tuple(mapping_test_entries(data)) if data is not None else ()
            )
        return self._cache[key]

    def attribute(
        self,
        *,
        identity: str,
        name: str,
        file_attr: str,
        source: str,
    ) -> Optional[str]:
        """Return the first in-scope feature the testcase belongs to."""
        for key, patterns in self._needles:
            if any(pattern.search(identity) for pattern in patterns):
                return key

        keys = execution_match_keys(identity, file_attr, source)
        name_keys: set = set()
        for candidate in (keys.identity, match_key(name)):
            if not candidate:
                continue
            parts = candidate.split("::")
            for index in range(len(parts)):
                name_keys.add("::".join(parts[index:]))

        features: set = set()
        for key in name_keys:
            features.update(self._by_name.get(key, ()))
        for suffix in keys.path_suffixes:
            features.update(self._by_path.get(suffix, ()))
        if not features:
            return None
        return min(
            features,
            key=lambda key: self._order.get(key, len(self._order)),
        )


# ---------------------------------------------------------------------------
# Deduplication


def _deduplicate(
    ctx: CollectorContext, observations: Sequence[_Observation]
) -> Tuple[List[EvidenceRecord], List[Diagnostic]]:
    """Collapse duplicate testcases and flag contradictory outcomes.

    Two files reporting the same identity at the same revision are the same
    observation; the record lists every source. If those files disagree about
    the outcome, the record keeps both, sets ``details.contradiction``, and a
    diagnostic names the conflict so it is never silently resolved.
    """
    groups: Dict[Tuple[str, str, str, str], List[_Observation]] = {}
    for observation in observations:
        key = (
            observation.identity,
            observation.revision or "",
            observation.feature,
            observation.tier.value,
        )
        groups.setdefault(key, []).append(observation)

    diagnostics: List[Diagnostic] = []
    records: Dict[str, EvidenceRecord] = {}

    for key in sorted(groups):
        group = sorted(groups[key], key=lambda item: item.source)
        sources = sorted({item.source for item in group})
        canonical = group[0]
        outcomes = sorted({item.outcome for item in group})
        contradiction = len(outcomes) > 1

        details = dict(canonical.details)
        details["sources"] = sources
        if contradiction:
            details["contradiction"] = True
            details["outcomes"] = outcomes
            details["outcome"] = _worst_outcome(outcomes)
            diagnostics.append(
                _diagnostic(
                    ctx,
                    "EXECUTION_CONTRADICTION",
                    (
                        "{0} reported outcomes {1} at revision {2}; keeping both "
                        "in the record".format(
                            canonical.identity,
                            ", ".join(outcomes),
                            canonical.revision or "<unknown>",
                        )
                    ),
                    severity="warning",
                    operation="dedupe",
                    recovery=(
                        "Determine which report is stale or misconfigured; the "
                        "record intentionally does not pick a winner."
                    ),
                )
            )

        record = EvidenceRecord(
            id=make_evidence_id(
                COLLECTOR_NAME, KIND, canonical.identity, canonical.source
            ),
            evidence_class=EvidenceClass.OBSERVED_EXECUTION,
            tier=canonical.tier,
            feature=canonical.feature,
            subject=canonical.identity,
            source=canonical.source,
            collector=COLLECTOR_NAME,
            kind=KIND,
            revision=canonical.revision,
            timestamp=canonical.timestamp,
            freshness=canonical.freshness,
            details=details,
        )
        existing = records.get(record.id)
        if existing is None:
            records[record.id] = record
        else:
            # Same identity and same file but a different suite revision. The
            # evidence id excludes the revision, so merge instead of letting
            # the model reject two conflicting payloads.
            records[record.id] = _merge_revisions(existing, record, ctx)

    return list(records.values()), diagnostics


def _merge_revisions(
    first: EvidenceRecord, second: EvidenceRecord, ctx: CollectorContext
) -> EvidenceRecord:
    """Merge two records that share an id but differ only by revision."""
    revisions = sorted(
        {
            revision
            for revision in (first.revision, second.revision)
            if revision
        }
    )
    chosen = second if second.revision == ctx.sha else first
    details = dict(chosen.details)
    details["revisions"] = revisions
    return replace(
        chosen,
        freshness=revision_freshness(chosen.revision, ctx.sha),
        details=details,
    )


def _worst_outcome(outcomes: Sequence[str]) -> str:
    return max(outcomes, key=lambda outcome: _OUTCOME_RANK.get(outcome, 0))


# ---------------------------------------------------------------------------
# Small helpers


def _diagnostic(
    ctx: CollectorContext,
    code: str,
    message: str,
    *,
    severity: str,
    operation: str,
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


def _local_name(tag: object) -> str:
    """Strip an XML namespace, if any, from a tag."""
    if not isinstance(tag, str):
        return ""
    return tag.rsplit("}", 1)[-1]


def _clean(value: object) -> Optional[str]:
    if value is None:
        return None
    text = str(value).strip()
    return text or None


def _truncate(value: Optional[str]) -> str:
    if not value:
        return ""
    collapsed = _WHITESPACE.sub(" ", value).strip()
    return collapsed[:MESSAGE_LIMIT]


collector = JunitCollector()
