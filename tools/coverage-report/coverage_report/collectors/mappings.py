"""Test-to-behavior mapping collector.

Emits one :class:`~coverage_report.model.EvidenceRecord` per (behavior, test)
association, strongest source first:

1. The approved test plan itself. A test path named in a case's ``Execution``
   bullet or in the Planning Evidence Matrix, not marked "proposed", that
   exists in the repository is a declared mapping. A proposed or missing path
   is a planned-but-unimplemented gap. Test plans under
   ``enhancement-proposals/`` are merged and reviewed; drafts under
   ``.artifacts/design/`` are inferred at most. Commands without a path are
   kept in the behavior details, not treated as tests.
2. A reviewed map at ``config/mappings/<KEY>.yaml`` (or the bundled
   ``tools/coverage-report/config/mappings/<KEY>.yaml``). A map whose
   ``reviewed_by`` is still ``PENDING-REVIEW`` is a suggestion, not a review,
   and is emitted as inferred evidence.
3. An explicit ``TC-...`` reference in test code, declared only when the same
   file or the enclosing describe/test also names the feature.
4. A ``docs/INTEGRATION-TESTING.md`` coverage-note bullet that names the feature
   and lists ``Related test-plan cases:``.
5. Filename/describe keyword overlap with the behavior title.

Nothing here can produce ``measured`` or ``observed_execution`` evidence: a
mapping says a test *ought* to cover a behavior, never that it ran. Heuristics
never exceed ``inferred_mapping``.
"""

import os
import re
from dataclasses import dataclass
from typing import Dict, List, Optional, Sequence, Set, Tuple

from ..errors import CoverageReportError
from ..manifest import relative_path
from ..mapping_file import read_mapping_file
from ..model import Boundary, CollectorBatch, Diagnostic, EvidenceClass, EvidenceRecord, Tier, make_evidence_id
from ..review import is_invalid_reviewer, is_reviewed
from . import CollectorContext
from .behaviors import (
    TestplanInventory,
    _boundary_kinds,
    _bullet_fields,
    _is_separator_row,
    _split_cases,
    _table_fields,
    _tables,
    collect_inventory,
    infer_tier_from_path,
    normalize_testplan_text,
)

COLLECTOR_NAME = "mappings"

_OPERATION = "map_tests"

CONFIG_VERSION = 1

KIND_CONFIG = "config-mapping"
KIND_TESTPLAN_EXECUTION = "testplan-execution"
KIND_PLANNED_MISSING = "planned-test-missing"
KIND_TEST_REFERENCE = "test-case-reference"
KIND_COVERAGE_NOTE = "coverage-note-reference"
KIND_KEYWORD = "keyword-inference"
KIND_MISSING_TEST = "declared-test-missing"

HEURISTIC_UNREVIEWED_CONFIG = "unreviewed-config"
HEURISTIC_UNANCHORED_TC = "tc-id-without-feature-anchor"
HEURISTIC_KEYWORD_OVERLAP = "keyword-overlap"
HEURISTIC_DRAFT_TESTPLAN = "draft-testplan"

_CONFIDENCE_UNREVIEWED_CONFIG = 0.7
_CONFIDENCE_UNANCHORED_TC = 0.4
_CONFIDENCE_DRAFT_TESTPLAN = 0.5

#: Test plans under this prefix are merged and reviewed.
APPROVED_TESTPLAN_PREFIX = "enhancement-proposals/"

_TEST_FILE_SUFFIXES = (".go", ".py", ".ts", ".tsx", ".js")
_BACKTICK_RE = re.compile(r"`([^`]+)`")
_BARE_PATH_RE = re.compile(
    r"(?<![\w`/-])((?:[\w.-]+/)*[\w.-]+\.(?:go|py|ts|tsx|js))"
)
_TC_RANGE_RE = re.compile(r"(TC-[A-Za-z0-9]+-)(\d+)[–-](\d+)")
_TC_TOKEN_RE = re.compile(r"TC-[A-Za-z0-9._-]+")

#: Directory names that are working directories, not test locations.
_NOT_TEST_DIRS = frozenset(
    {
        "fulfillment-service",
        "osac-ui",
        "osac-installer",
        "osac-operator",
        "osac-aap",
        "osac-csi-driver",
        "osac-metering",
        "tests",
        "proto",
        "tools",
    }
)
_EMPTY_DEPENDENCIES = frozenset({"", "none", "n/a", "na", "-", "omitted"})

_INTEGRATION_DOC = "docs/INTEGRATION-TESTING.md"

#: ``TC-FR1-01``, ``TC-NFR2-01``, ``TC-E2E-01``, and ``TC-1.01``.
_TC_ID_RE = re.compile(
    r"\bTC-(?:[A-Za-z]+[0-9]+-[0-9]+|[A-Za-z]+-[0-9]+|[0-9]+\.[0-9]+)\b"
)
_RELATED_CASES_RE = re.compile(
    r"related test-plan cases?\s*:\s*(?P<cases>[^.]*)", re.IGNORECASE
)
_DOC_TEST_PATH_RE = re.compile(
    r"`([^`\n]+\.(?:go|py))`"
    r"|(?<![\w/])((?:[\w.-]+/)*[\w.-]+_test\.go|(?:[\w.-]+/)*test_[\w.-]+\.py)"
)
_RELATED_CASE_TOKEN_RE = re.compile(r"TC-[A-Za-z0-9._-]+")

_DESCRIBE_RE = re.compile(r"(?:Describe|Context)\(\s*\"([^\"]+)\"")
_PYTEST_DEF_RE = re.compile(r"^\s*def (test_[A-Za-z0-9_]+)\s*\(", re.MULTILINE)
_TEST_HEADER_RE = re.compile(
    r"Describe\(|Context\(|func Test|^\s*def test_|^class Test"
)

_SECTION_RE = re.compile(r"^##\s+(?P<name>.+?)\s*$")
_BULLET_START_RE = re.compile(r"^-\s+")

_SKIP_DIRS = frozenset({".git", "node_modules", "__pycache__", "vendor", ".venv"})

_STOPWORDS = frozenset(
    {
        "the",
        "and",
        "with",
        "for",
        "from",
        "that",
        "this",
        "into",
        "when",
        "without",
        "test",
        "tests",
        "should",
        "spec",
        "new",
        "all",
        "not",
        "has",
        "does",
        "can",
        "use",
        "via",
        "per",
        "are",
        "was",
        "its",
        "cannot",
        "referenced",
        "each",
        "every",
        "only",
        "more",
        "than",
        "one",
        "two",
        "expected",
        "actual",
        "given",
        "under",
    }
)

#: A keyword match needs at least this share of the behavior title's tokens.
_KEYWORD_MIN_RATIO = 0.6

#: Hard cap on keyword records per feature, so a broad title cannot flood the
#: evidence list. Truncation is reported, never silent.
_MAX_KEYWORD_RECORDS_PER_FEATURE = 40


@dataclass(frozen=True)
class _MappingTest:
    """One test entry in a reviewed map."""

    path: str
    name: str
    tier: Optional[Tier]


def _diagnostic(
    ctx: CollectorContext,
    *,
    code: str,
    message: str,
    severity: str,
    recovery: str = "",
) -> Diagnostic:
    return Diagnostic(
        run_id=ctx.run_id,
        collector=COLLECTOR_NAME,
        operation=_OPERATION,
        code=code,
        message=message,
        severity=severity,
        recovery=recovery,
    )


def _suite_tier_for_path(feature, path: str) -> Optional[Tier]:
    """Return the feature's declared suite tier that contains ``path``."""
    normalized = path.replace(os.sep, "/").strip("/") + "/"
    for tier_name in sorted(feature.suites):
        for directory in feature.suites[tier_name]:
            prefix = directory.replace(os.sep, "/").strip("/") + "/"
            if normalized.startswith(prefix):
                try:
                    return Tier(tier_name)
                except ValueError:
                    continue
    return None


def _tier_for_path(feature, path: str) -> Optional[Tier]:
    return infer_tier_from_path(path) or _suite_tier_for_path(feature, path)


def _is_test_file(filename: str) -> bool:
    return filename.endswith("_test.go") or (
        filename.startswith("test_") and filename.endswith(".py")
    )


def _iter_test_files(ctx: CollectorContext, feature) -> Tuple[str, ...]:
    """Return repo-relative test file paths under the feature's suite roots."""
    roots: Set[str] = set()
    for tier_name in sorted(feature.suites):
        for directory in feature.suites[tier_name]:
            roots.add(os.path.normpath(os.path.join(ctx.repo_root, directory)))

    deduped_roots: List[str] = []
    for root in sorted(roots):
        if any(
            root == parent or root.startswith(parent + os.sep)
            for parent in deduped_roots
        ):
            continue
        deduped_roots.append(root)

    found: Set[str] = set()
    for root in deduped_roots:
        if not os.path.isdir(root):
            continue
        for current, dirnames, filenames in os.walk(root):
            dirnames[:] = sorted(
                name for name in dirnames if name not in _SKIP_DIRS and not name.startswith(".")
            )
            for filename in sorted(filenames):
                if not _is_test_file(filename):
                    continue
                absolute = os.path.join(current, filename)
                found.add(relative_path(ctx.repo_root, absolute))
    return tuple(sorted(found))


def _read_test_files(
    ctx: CollectorContext, feature
) -> Tuple[Tuple[str, ...], Dict[str, str], Dict[str, OSError]]:
    """Walk the feature's suite roots once and read each file once."""
    paths = _iter_test_files(ctx, feature)
    texts: Dict[str, str] = {}
    errors: Dict[str, OSError] = {}
    for path in paths:
        try:
            with open(
                os.path.join(ctx.repo_root, path), "r", encoding="utf-8"
            ) as handle:
                texts[path] = handle.read()
        except OSError as error:
            errors[path] = error
    return paths, texts, errors


def _anchor_patterns(feature) -> Tuple[re.Pattern[str], ...]:
    """Return whole-word patterns for the feature's Jira keys.

    Only the registry key and aliases shaped like a Jira key count as an anchor.
    A prose alias ("diskimage") appears in ordinary test names, so treating it
    as an anchor would promote a coincidence to declared evidence.
    """
    terms = [feature.key]
    terms.extend(
        alias
        for alias in feature.aliases
        if re.fullmatch(r"[A-Z][A-Z0-9]*-\d+", alias or "")
    )
    return tuple(
        re.compile(r"(?<![A-Za-z0-9])" + re.escape(term) + r"(?![0-9])", re.IGNORECASE)
        for term in terms
        if term
    )


def _text_has_anchor(text: str, patterns: Sequence[re.Pattern[str]]) -> bool:
    return any(pattern.search(text) for pattern in patterns)


def _record(
    *,
    behavior_id: str,
    feature: str,
    tier: Tier,
    source: str,
    kind: str,
    evidence_class: EvidenceClass,
    boundary: Boundary,
    details: Dict[str, object],
    confidence: Optional[float] = None,
    heuristic: Optional[str] = None,
) -> EvidenceRecord:
    return EvidenceRecord(
        id=make_evidence_id(COLLECTOR_NAME, kind, behavior_id, source),
        evidence_class=evidence_class,
        tier=tier,
        feature=feature,
        subject=behavior_id,
        source=source,
        collector=COLLECTOR_NAME,
        kind=kind,
        confidence=confidence,
        heuristic=heuristic,
        boundary=boundary,
        details=details,
    )


# --------------------------------------------------------------------------
# Source 1: the approved test plan
# --------------------------------------------------------------------------


def _looks_like_test_path(token: str) -> bool:
    """Return whether a token names a test location rather than a command."""
    token = token.strip().strip("`").rstrip(".,;)")
    if not token or "*" in token or token.startswith("-") or "://" in token:
        return False
    if token.endswith(_TEST_FILE_SUFFIXES):
        return True
    if token.endswith("/") and "/" in token.strip("/"):
        last = token.strip("/").rsplit("/", 1)[-1]
        return last not in _NOT_TEST_DIRS and not last.startswith(".")
    return False


def _path_tokens(text: str) -> Tuple[str, ...]:
    """Return test-path candidates from an Execution bullet or matrix cell.

    Backticked spans are checked whole and, when they are shell commands, word
    by word. A bare ``*_test.go`` path is also accepted.
    """
    found: List[str] = []
    for span in _BACKTICK_RE.findall(text or ""):
        candidate = span.strip()
        if not candidate:
            continue
        if " " in candidate:
            found.extend(
                word for word in candidate.split() if _looks_like_test_path(word)
            )
            continue
        if _looks_like_test_path(candidate):
            found.append(candidate)
    found.extend(_BARE_PATH_RE.findall(text or ""))
    return tuple(dict.fromkeys(found))


def _resolve_test_path(
    ctx: CollectorContext, feature, raw: str, tier: Tier
) -> Tuple[str, bool]:
    """Resolve a plan path to a repository file, trying suite roots for bare names.

    A missing bare name is still reported under its most likely suite root, so
    the same planned file named once fully and once by basename becomes one gap
    instead of two.
    """
    token = raw.strip().strip("`").rstrip(".,;)")
    candidates = [token]
    preferred_base = None
    if "/" not in token:
        bases: List[str] = []
        for tier_name in sorted(feature.suites):
            bases.extend(feature.suites[tier_name])
        tier_dirs = feature.suites.get(tier.value, ())
        if tier_dirs:
            preferred_base = tier_dirs[0]
        bases.extend(
            [
                "fulfillment-service/it",
                "fulfillment-service",
                "osac-ui",
                "tests/e2e",
            ]
        )
        candidates.extend(
            "{0}/{1}".format(base.rstrip("/"), token) for base in bases
        )
    for candidate in candidates:
        absolute = os.path.normpath(os.path.join(ctx.repo_root, candidate))
        if os.path.isfile(absolute):
            return relative_path(ctx.repo_root, absolute), True
    if preferred_base:
        return "{0}/{1}".format(preferred_base.rstrip("/"), token), False
    return token.replace(os.sep, "/"), False


def _expand_tc_ids(cell: str) -> Tuple[str, ...]:
    """Return every test-case id in a matrix cell, expanding ``01-06`` ranges."""
    text = cell or ""
    ids: List[str] = []
    for match in _TC_RANGE_RE.finditer(text):
        prefix, start, end = match.groups()
        width = len(start)
        for number in range(int(start), int(end) + 1):
            ids.append("{0}{1:0{2}d}".format(prefix, number, width))
    remaining = _TC_RANGE_RE.sub(" ", text)
    ids.extend(token.rstrip(".") for token in _TC_TOKEN_RE.findall(remaining))
    return tuple(dict.fromkeys(ids))


def _execution_matrix(text: str) -> Dict[str, Dict[str, str]]:
    """Return ``tc id -> matrix row`` for the Planning Evidence Matrix."""
    rows: Dict[str, Dict[str, str]] = {}
    for table in _tables(normalize_testplan_text(text).split("\n")):
        cleaned = [row for row in table if not _is_separator_row(row)]
        if len(cleaned) < 2:
            continue
        header = [cell.casefold().strip() for cell in cleaned[0]]
        if "test case ids" not in header:
            continue

        def column(*names: str) -> Optional[int]:
            for name in names:
                if name in header:
                    return header.index(name)
            return None

        ids_index = header.index("test case ids")
        path_index = column(
            "existing or proposed path and command", "path and command"
        )
        real_index = column("real dependencies")
        simulated_index = column(
            "simulated or omitted dependencies", "simulated dependencies"
        )
        for row in cleaned[1:]:
            if ids_index >= len(row):
                continue
            row_values = {
                "path_text": (
                    row[path_index]
                    if path_index is not None and path_index < len(row)
                    else ""
                ),
                "real": (
                    row[real_index]
                    if real_index is not None and real_index < len(row)
                    else ""
                ),
                "simulated": (
                    row[simulated_index]
                    if simulated_index is not None and simulated_index < len(row)
                    else ""
                ),
            }
            for tc_id in _expand_tc_ids(row[ids_index]):
                rows[tc_id] = row_values
    return rows


_SIMULATED_SEGMENT_RE = re.compile(
    r"(?:^|[.;]\s+|\s)"
    r"(?:simulated(?:\s+or\s+omitted)?|omitted|faked|mocked)\s*:\s*",
    re.IGNORECASE,
)
_REAL_PREFIX_RE = re.compile(r"^real\s*:\s*", re.IGNORECASE)
_TRAILING_SEPARATORS = " \t;.,"


def _boundary_from_bullet(bullet: str) -> Boundary:
    """Parse one ``Boundary`` sentence without copying it into both kinds.

    An explicit ``Simulated:`` (or ``simulated or omitted:``) segment splits
    the sentence. Without one, the sentence describes the real boundary; a
    trailing "providers may be omitted" is a qualifier, not a simulated
    boundary. A bullet that names only a simulated dependency is simulated.
    """
    text = " ".join((bullet or "").split()).strip().rstrip(".")
    if not text:
        return Boundary()

    match = _SIMULATED_SEGMENT_RE.search(text)
    if match is not None:
        real_text = text[: match.start()].strip(_TRAILING_SEPARATORS)
        real_text = _REAL_PREFIX_RE.sub("", real_text).strip()
        simulated_text = text[match.end() :].strip(_TRAILING_SEPARATORS)
        return Boundary(
            real=(real_text,) if real_text else (),
            simulated=(simulated_text,) if simulated_text else (),
        )

    kinds = _boundary_kinds(text)
    if "simulated" in kinds and "real" not in kinds:
        return Boundary(simulated=(text,))
    return Boundary(real=(text,))


def _dependency(value: str) -> Tuple[str, ...]:
    text = " ".join((value or "").split()).strip().rstrip(".")
    if text.casefold() in _EMPTY_DEPENDENCIES:
        return ()
    return (text,)


def _case_boundary(fields: Dict[str, str], row: Optional[Dict[str, str]]) -> Boundary:
    """Return the boundary from the matrix row, falling back to the bullet."""
    if row is not None:
        real = _dependency(row.get("real", ""))
        simulated = _dependency(row.get("simulated", ""))
        if real or simulated:
            return Boundary(real=real, simulated=simulated)

    return _boundary_from_bullet(fields.get("boundary", ""))


def _testplan_records(
    ctx: CollectorContext,
    feature,
    inventory: TestplanInventory,
    *,
    diagnostics: List[Diagnostic],
) -> List[EvidenceRecord]:
    """Emit mappings declared by the approved test plan itself."""
    if not inventory.path:
        return []
    absolute = os.path.normpath(os.path.join(ctx.repo_root, inventory.path))
    try:
        with open(absolute, "r", encoding="utf-8") as handle:
            raw = handle.read()
    except OSError:
        return []

    reviewed = inventory.path.startswith(APPROVED_TESTPLAN_PREFIX)
    by_tc = {
        behavior.id.split("/", 1)[-1]: behavior
        for behavior in inventory.behaviors
    }
    matrix = _execution_matrix(raw)
    records: List[EvidenceRecord] = []

    for case in _split_cases(normalize_testplan_text(raw)):
        behavior = by_tc.get(case.tc_id)
        if behavior is None or behavior.required_tier is None:
            # A tier-unassigned behavior cannot carry tiered evidence; its
            # planned paths remain visible only through the plan itself.
            continue

        fields = _bullet_fields(case.lines)
        fields.update(_table_fields(case.lines))
        execution = fields.get("execution") or ""
        row = matrix.get(case.tc_id)

        # One entry per path. A path named in a non-"proposed" context wins
        # over a proposed mention in the other source.
        candidates: Dict[str, bool] = {}
        for text in (execution, row.get("path_text", "") if row else ""):
            if not text:
                continue
            proposed = "proposed" in text.casefold()
            for token in _path_tokens(text):
                candidates[token] = candidates.get(token, True) and proposed

        if not candidates:
            if execution:
                diagnostics.append(
                    _diagnostic(
                        ctx,
                        code="TESTPLAN_EXECUTION_NO_PATH",
                        message=(
                            "{0} {1} execution names no test path; the command "
                            "is kept in the behavior detail only: {2}".format(
                                inventory.path, case.tc_id, execution
                            )
                        ),
                        severity="info",
                        recovery="Name the test file in the Execution bullet or "
                        "the Planning Evidence Matrix so the mapping can be "
                        "verified.",
                    )
                )
            continue

        boundary = _case_boundary(fields, row)
        seen: set = set()
        for token in sorted(candidates):
            proposed = candidates[token]
            display, exists = _resolve_test_path(
                ctx, feature, token, behavior.required_tier
            )
            if (behavior.id, display) in seen:
                continue
            seen.add((behavior.id, display))
            details: Dict[str, object] = {
                "path": display,
                "configured_path": token,
                "name": "",
                "tier": behavior.required_tier.value,
                "tier_label": behavior.tier_label,
                "execution": execution,
                "plan": inventory.path,
                "review_status": "reviewed" if reviewed else "draft",
                "proposed": proposed,
                "test_exists": exists,
            }
            source = "{0}::{1}::{2}".format(inventory.path, case.tc_id, display)

            if proposed or not exists:
                records.append(
                    _record(
                        behavior_id=behavior.id,
                        feature=feature.key,
                        tier=behavior.required_tier,
                        source=source,
                        kind=KIND_PLANNED_MISSING,
                        evidence_class=EvidenceClass.GAP_UNKNOWN,
                        boundary=boundary,
                        details=details,
                    )
                )
            elif reviewed:
                records.append(
                    _record(
                        behavior_id=behavior.id,
                        feature=feature.key,
                        tier=behavior.required_tier,
                        source=source,
                        kind=KIND_TESTPLAN_EXECUTION,
                        evidence_class=EvidenceClass.DECLARED_MAPPING,
                        boundary=boundary,
                        details=details,
                    )
                )
            else:
                details["review_status"] = "draft"
                records.append(
                    _record(
                        behavior_id=behavior.id,
                        feature=feature.key,
                        tier=behavior.required_tier,
                        source=source,
                        kind=KIND_TESTPLAN_EXECUTION,
                        evidence_class=EvidenceClass.INFERRED_MAPPING,
                        boundary=boundary,
                        details=details,
                        confidence=_CONFIDENCE_DRAFT_TESTPLAN,
                        heuristic=HEURISTIC_DRAFT_TESTPLAN,
                    )
                )
    return records


# --------------------------------------------------------------------------
# Source 2: reviewed map
# --------------------------------------------------------------------------


def _parse_mapping_tests(
    entry: Dict[str, object],
) -> List[_MappingTest]:
    raw_tests = entry.get("tests")
    if not isinstance(raw_tests, list):
        return []
    tests: List[_MappingTest] = []
    for raw in raw_tests:
        if not isinstance(raw, dict):
            continue
        path = raw.get("path")
        if not isinstance(path, str) or not path.strip():
            continue
        name = raw.get("name")
        raw_tier = raw.get("tier")
        tier: Optional[Tier] = None
        if isinstance(raw_tier, str) and raw_tier.strip():
            try:
                tier = Tier(raw_tier.strip())
            except ValueError:
                tier = None
        tests.append(
            _MappingTest(
                path=path.strip().replace(os.sep, "/"),
                name=name.strip() if isinstance(name, str) else "",
                tier=tier,
            )
        )
    return tests


def mapping_test_entries(data: object) -> List[Dict[str, str]]:
    """Return ``mappings[].tests[]`` entries as path/name dictionaries.

    Shared with the JUnit collector so mapping files are parsed in one place.
    """
    if not isinstance(data, dict):
        return []
    entries = data.get("mappings")
    if not isinstance(entries, list):
        return []
    result: List[Dict[str, str]] = []
    for entry in entries:
        if not isinstance(entry, dict):
            continue
        raw_tests = entry.get("tests")
        if not isinstance(raw_tests, list):
            continue
        for raw in raw_tests:
            if not isinstance(raw, dict):
                continue
            # Attribution accepts a name-only entry; the mapping collector
            # separately requires a path before it can emit evidence.
            path = raw.get("path")
            name = raw.get("name")
            path_text = path.strip() if isinstance(path, str) else ""
            name_text = name.strip() if isinstance(name, str) else ""
            if path_text or name_text:
                result.append({"path": path_text, "name": name_text})
    return result


def load_mapping_config(
    ctx: CollectorContext,
    feature,
    *,
    diagnostics: List[Diagnostic],
) -> Optional[Dict[str, object]]:
    data, display, error = read_mapping_file(ctx, feature.key)
    if error:
        diagnostics.append(
            _diagnostic(
                ctx,
                code="MAPPING_CONFIG_INVALID",
                message=error,
                severity="error",
                recovery="Fix the YAML syntax and file permissions.",
            )
        )
        return None
    if data is None:
        return None

    if data.get("version") != CONFIG_VERSION:
        diagnostics.append(
            _diagnostic(
                ctx,
                code="MAPPING_CONFIG_INVALID",
                message="mapping file {0} has unsupported version {1!r}".format(
                    display, data.get("version")
                ),
                severity="error",
                recovery="Use version: {0}.".format(CONFIG_VERSION),
            )
        )
        return None

    declared_feature = data.get("feature")
    if declared_feature != feature.key:
        diagnostics.append(
            _diagnostic(
                ctx,
                code="MAPPING_FEATURE_MISMATCH",
                message="mapping file {0} is for feature {1!r}, not {2}".format(
                    display, declared_feature, feature.key
                ),
                severity="error",
                recovery="Move the file to config/mappings/{0}.yaml or fix its "
                "feature field.".format(feature.key),
            )
        )
        return None

    if not isinstance(data.get("mappings"), list):
        diagnostics.append(
            _diagnostic(
                ctx,
                code="MAPPING_CONFIG_INVALID",
                message="mapping file {0} has no mappings list".format(display),
                severity="error",
                recovery="Add mappings: [] with one entry per behavior.",
            )
        )
        return None

    if is_invalid_reviewer(data.get("reviewed_by")):
        diagnostics.append(
            _diagnostic(
                ctx,
                code="MAPPING_REVIEWER_INVALID",
                message="mapping file {0} has an invalid reviewed_by value {1!r}; "
                "its entries are treated as unreviewed".format(
                    display, data.get("reviewed_by")
                ),
                severity="warning",
                recovery="Use a GitHub user or team handle such as @itzik or "
                "osac-project/qe, or PENDING-REVIEW.",
            )
        )

    data["_path"] = display
    return data


def _config_records(
    ctx: CollectorContext,
    feature,
    inventory: TestplanInventory,
    *,
    diagnostics: List[Diagnostic],
) -> List[EvidenceRecord]:
    data = load_mapping_config(ctx, feature, diagnostics=diagnostics)
    if data is None:
        return []

    display = str(data["_path"])
    reviewed_by = data.get("reviewed_by")
    reviewed = is_reviewed(reviewed_by)
    known = {behavior.id for behavior in inventory.behaviors}

    records: List[EvidenceRecord] = []
    for index, entry in enumerate(data.get("mappings") or []):
        if not isinstance(entry, dict):
            diagnostics.append(
                _diagnostic(
                    ctx,
                    code="MAPPING_CONFIG_INVALID",
                    message="{0} mappings[{1}] is not a mapping".format(display, index),
                    severity="warning",
                    recovery="Use a mapping with behavior, tests, and rationale.",
                )
            )
            continue

        behavior_ref = entry.get("behavior")
        if not isinstance(behavior_ref, str) or not behavior_ref.strip():
            diagnostics.append(
                _diagnostic(
                    ctx,
                    code="MAPPING_CONFIG_INVALID",
                    message="{0} mappings[{1}] has no behavior".format(display, index),
                    severity="warning",
                    recovery="Set behavior to a TC id such as TC-FR1-01.",
                )
            )
            continue
        behavior_ref = behavior_ref.strip()
        behavior_id = (
            behavior_ref if "/" in behavior_ref else "{0}/{1}".format(feature.key, behavior_ref)
        )
        if behavior_id not in known:
            diagnostics.append(
                _diagnostic(
                    ctx,
                    code="MAPPING_BEHAVIOR_UNKNOWN",
                    message="{0} references behavior {1}, which is not in the "
                    "inventory for {2}".format(display, behavior_id, feature.key),
                    severity="error",
                    recovery="Use a TC id from the feature test plan, or fix the "
                    "test plan path in the feature registry.",
                )
            )
            continue

        try:
            boundary = Boundary.from_any(entry.get("boundary"))
        except CoverageReportError:
            boundary = Boundary()
            diagnostics.append(
                _diagnostic(
                    ctx,
                    code="MAPPING_CONFIG_INVALID",
                    message="{0} has an invalid boundary for {1}".format(
                        display, behavior_id
                    ),
                    severity="warning",
                    recovery="Use boundary: {real: [...], simulated: [...]}.",
                )
            )

        rationale = entry.get("rationale")
        rationale_text = rationale.strip() if isinstance(rationale, str) else ""

        mapping_tests = _parse_mapping_tests(entry)
        if not mapping_tests:
            diagnostics.append(
                _diagnostic(
                    ctx,
                    code="MAPPING_CONFIG_INVALID",
                    message="{0} mappings[{1}] has no usable tests".format(
                        display, index
                    ),
                    severity="warning",
                    recovery="Add tests: [{path: ..., name: ..., tier: ...}].",
                )
            )
            continue

        for test in mapping_tests:
            tier = test.tier or _tier_for_path(feature, test.path)
            if tier is None:
                diagnostics.append(
                    _diagnostic(
                        ctx,
                        code="MAPPING_TIER_UNKNOWN",
                        message="cannot determine a tier for {0} referenced by "
                        "{1}; the entry is skipped".format(test.path, display),
                        severity="warning",
                        recovery="Add tier: unit|integration|e2e to the test entry.",
                    )
                )
                continue

            absolute = os.path.normpath(os.path.join(ctx.repo_root, test.path))
            exists = os.path.isfile(absolute)
            resolved = (
                relative_path(ctx.repo_root, absolute)
                if exists
                else test.path.replace(os.sep, "/")
            )
            source = "{0}::{1}::{2}".format(display, resolved, test.name)
            details: Dict[str, object] = {
                "path": resolved,
                "name": test.name,
                "tier": tier.value,
                "configured_path": test.path,
                "rationale": rationale_text,
                "reviewed_by": "" if reviewed_by is None else str(reviewed_by),
                "config": display,
                "test_exists": exists,
            }

            if not exists:
                diagnostics.append(
                    _diagnostic(
                        ctx,
                        code="MAPPING_TEST_MISSING",
                        message="{0} maps {1} to {2}, which does not exist".format(
                            display, behavior_id, test.path
                        ),
                        severity="warning",
                        recovery="Fix the path, or remove the entry; the record "
                        "stays gap_unknown until then.",
                    )
                )
                records.append(
                    _record(
                        behavior_id=behavior_id,
                        feature=feature.key,
                        tier=tier,
                        source=source,
                        kind=KIND_MISSING_TEST,
                        evidence_class=EvidenceClass.GAP_UNKNOWN,
                        boundary=boundary,
                        details=details,
                    )
                )
                continue

            if reviewed:
                records.append(
                    _record(
                        behavior_id=behavior_id,
                        feature=feature.key,
                        tier=tier,
                        source=source,
                        kind=KIND_CONFIG,
                        evidence_class=EvidenceClass.DECLARED_MAPPING,
                        boundary=boundary,
                        details=details,
                    )
                )
            else:
                details["review_status"] = "unreviewed"
                records.append(
                    _record(
                        behavior_id=behavior_id,
                        feature=feature.key,
                        tier=tier,
                        source=source,
                        kind=KIND_CONFIG,
                        evidence_class=EvidenceClass.INFERRED_MAPPING,
                        boundary=boundary,
                        details=details,
                        confidence=_CONFIDENCE_UNREVIEWED_CONFIG,
                        heuristic=HEURISTIC_UNREVIEWED_CONFIG,
                    )
                )
    return records


# --------------------------------------------------------------------------
# Source 2: explicit TC references in test code
# --------------------------------------------------------------------------


def _test_reference_records(
    ctx: CollectorContext,
    feature,
    inventory: TestplanInventory,
    *,
    test_files: Sequence[str],
    texts: Dict[str, str],
    read_errors: Dict[str, OSError],
    diagnostics: List[Diagnostic],
) -> List[EvidenceRecord]:
    known = {behavior.id.split("/", 1)[-1]: behavior.id for behavior in inventory.behaviors}
    patterns = _anchor_patterns(feature)
    records: List[EvidenceRecord] = []

    for path in test_files:
        tier = _tier_for_path(feature, path)
        if path in read_errors:
            error = read_errors[path]
            diagnostics.append(
                _diagnostic(
                    ctx,
                    code="MAPPING_SOURCE_UNREADABLE",
                    message="could not read test file {0}: {1}".format(path, error),
                    severity="warning",
                    recovery="Check file permissions.",
                )
            )
            continue
        text = texts.get(path)
        if text is None:
            continue
        if tier is None:
            continue

        lines = text.split("\n")
        file_anchored = _text_has_anchor(text, patterns)
        # One forward pass records the effective anchor of the nearest
        # describe/test header for every line.
        enclosing_anchor: List[bool] = []
        current_anchor = False
        for line in lines:
            if _TEST_HEADER_RE.search(line):
                current_anchor = _text_has_anchor(line, patterns) or current_anchor
            enclosing_anchor.append(current_anchor)

        seen: Dict[str, int] = {}
        first_line: Dict[str, int] = {}
        anchored: Dict[str, str] = {}
        for index, line in enumerate(lines):
            for tc_id in _TC_ID_RE.findall(line):
                if tc_id not in seen:
                    seen[tc_id] = 0
                    first_line[tc_id] = index + 1
                seen[tc_id] += 1
                if tc_id in anchored:
                    continue
                if _text_has_anchor(line, patterns) or enclosing_anchor[index]:
                    anchored[tc_id] = "enclosing"
                elif file_anchored:
                    anchored[tc_id] = "file"

        for tc_id in sorted(seen):
            if tc_id in known:
                behavior_id = known[tc_id]
            else:
                diagnostics.append(
                    _diagnostic(
                        ctx,
                        code="MAPPING_TEST_CASE_UNKNOWN",
                        message="{0} references {1}, which is not in the inventory "
                        "for {2}".format(path, tc_id, feature.key),
                        severity="info",
                        recovery="Check the TC id, or update the feature test plan.",
                    )
                )
                continue

            source = "{0}::{1}".format(path, tc_id)
            details: Dict[str, object] = {
                "path": path,
                "line": first_line[tc_id],
                "occurrences": seen[tc_id],
                "anchored_by": anchored.get(tc_id, ""),
            }
            if tc_id in anchored:
                records.append(
                    _record(
                        behavior_id=behavior_id,
                        feature=feature.key,
                        tier=tier,
                        source=source,
                        kind=KIND_TEST_REFERENCE,
                        evidence_class=EvidenceClass.DECLARED_MAPPING,
                        boundary=Boundary(),
                        details=details,
                    )
                )
            else:
                details["anchor"] = "missing"
                records.append(
                    _record(
                        behavior_id=behavior_id,
                        feature=feature.key,
                        tier=tier,
                        source=source,
                        kind=KIND_TEST_REFERENCE,
                        evidence_class=EvidenceClass.INFERRED_MAPPING,
                        boundary=Boundary(),
                        details=details,
                        confidence=_CONFIDENCE_UNANCHORED_TC,
                        heuristic=HEURISTIC_UNANCHORED_TC,
                    )
                )
    return records


# --------------------------------------------------------------------------
# Source 3: INTEGRATION-TESTING.md coverage notes
# --------------------------------------------------------------------------


def _doc_bullets(text: str) -> List[Tuple[str, str]]:
    """Return ``(section, bullet_text)`` for each coverage-note bullet."""
    bullets: List[Tuple[str, str]] = []
    section = ""
    current: Optional[List[str]] = None
    for line in text.split("\n"):
        section_match = _SECTION_RE.match(line)
        if section_match:
            section = section_match.group("name").strip()
            current = None
            continue
        if _BULLET_START_RE.match(line):
            if current is not None:
                bullets.append((section, "\n".join(current)))
            current = [line[1:].strip()]
            continue
        if current is not None and line.strip() and line.startswith((" ", "\t")):
            current.append(line.strip())
            continue
        if current is not None:
            bullets.append((section, "\n".join(current)))
            current = None
    if current is not None:
        bullets.append((section, "\n".join(current)))
    return bullets


def _resolve_doc_path(repo_root: str, section: str, raw: str) -> Tuple[str, bool]:
    """Resolve a test file named in the doc, allowing a bare filename."""
    name = raw.strip().lstrip("./")
    candidates = [name]
    if "/" not in name and section:
        candidates.extend(
            [
                "{0}/it/{1}".format(section, name),
                "{0}/{1}".format(section, name),
            ]
        )
    for candidate in candidates:
        absolute = os.path.normpath(os.path.join(repo_root, candidate))
        if os.path.isfile(absolute):
            return relative_path(repo_root, absolute), True
    return name, False


def _coverage_note_records(
    ctx: CollectorContext,
    feature,
    inventory: TestplanInventory,
    *,
    diagnostics: List[Diagnostic],
) -> List[EvidenceRecord]:
    doc_path = os.path.join(ctx.repo_root, _INTEGRATION_DOC)
    if not os.path.isfile(doc_path):
        return []
    try:
        with open(doc_path, "r", encoding="utf-8") as handle:
            text = handle.read()
    except OSError:
        return []

    known = {behavior.id.split("/", 1)[-1]: behavior.id for behavior in inventory.behaviors}
    patterns = _anchor_patterns(feature)
    records: List[EvidenceRecord] = []

    for section, bullet in _doc_bullets(text):
        if not _text_has_anchor(bullet, patterns):
            continue
        related = _RELATED_CASES_RE.search(bullet)
        if related is None:
            continue

        tc_ids = sorted(set(_RELATED_CASE_TOKEN_RE.findall(related.group("cases"))))
        paths: List[str] = []
        for backticked, bare in _DOC_TEST_PATH_RE.findall(bullet):
            raw = backticked or bare
            if raw and raw not in paths:
                paths.append(raw)
        if not tc_ids or not paths:
            continue

        for tc_id in tc_ids:
            behavior_id = known.get(tc_id)
            if behavior_id is None:
                diagnostics.append(
                    _diagnostic(
                        ctx,
                        code="MAPPING_TEST_CASE_UNKNOWN",
                        message="{0} lists {1} for {2}, which is not in the "
                        "inventory".format(_INTEGRATION_DOC, tc_id, feature.key),
                        severity="info",
                        recovery="Check the TC id, or update the feature test plan.",
                    )
                )
                continue
            for raw_path in paths:
                resolved, exists = _resolve_doc_path(ctx.repo_root, section, raw_path)
                tier = _tier_for_path(feature, resolved)
                if tier is None:
                    diagnostics.append(
                        _diagnostic(
                            ctx,
                            code="MAPPING_TIER_UNKNOWN",
                            message="cannot determine a tier for {0} named in "
                            "{1}".format(resolved, _INTEGRATION_DOC),
                            severity="warning",
                            recovery="Name the file with its component path, or "
                            "add the directory to the feature suites.",
                        )
                    )
                    continue

                source = "{0}::{1}::{2}".format(_INTEGRATION_DOC, tc_id, resolved)
                details: Dict[str, object] = {
                    "path": resolved,
                    "section": section,
                    "test_exists": exists,
                    "association": "document-bullet",
                }
                if not exists:
                    diagnostics.append(
                        _diagnostic(
                            ctx,
                            code="MAPPING_TEST_MISSING",
                            message="{0} names {1} for {2}, which does not "
                            "exist".format(_INTEGRATION_DOC, raw_path, tc_id),
                            severity="warning",
                            recovery="Fix the path; the record stays gap_unknown.",
                        )
                    )
                    records.append(
                        _record(
                            behavior_id=behavior_id,
                            feature=feature.key,
                            tier=tier,
                            source=source,
                            kind=KIND_MISSING_TEST,
                            evidence_class=EvidenceClass.GAP_UNKNOWN,
                            boundary=Boundary(),
                            details=details,
                        )
                    )
                    continue

                records.append(
                    _record(
                        behavior_id=behavior_id,
                        feature=feature.key,
                        tier=tier,
                        source=source,
                        kind=KIND_COVERAGE_NOTE,
                        evidence_class=EvidenceClass.DECLARED_MAPPING,
                        boundary=Boundary(),
                        details=details,
                    )
                )
    return records


# --------------------------------------------------------------------------
# Source 4: keyword heuristics
# --------------------------------------------------------------------------


def _tokens(text: str) -> Set[str]:
    """Return lowercased, plural-folded word tokens from free text."""
    spaced = re.sub(r"([a-z0-9])([A-Z])", r"\1 \2", text)
    tokens: Set[str] = set()
    for part in re.split(r"[^A-Za-z0-9]+", spaced):
        token = part.casefold()
        if len(token) < 3 or token in _STOPWORDS:
            continue
        tokens.add(re.sub(r"s$", "", token) if len(token) > 4 else token)
    return tokens


def _describe_names(text: str) -> Tuple[str, ...]:
    names = set(_DESCRIBE_RE.findall(text))
    names.update(_PYTEST_DEF_RE.findall(text))
    return tuple(sorted(names))


def _keyword_records(
    ctx: CollectorContext,
    feature,
    inventory: TestplanInventory,
    *,
    test_files: Sequence[str],
    texts: Dict[str, str],
    covered_paths: Set[Tuple[str, str]],
    diagnostics: List[Diagnostic],
) -> List[EvidenceRecord]:
    records: List[EvidenceRecord] = []
    behaviors = sorted(inventory.behaviors, key=lambda behavior: behavior.id)
    if not behaviors:
        return records
    title_tokens = {
        behavior.id: _tokens(behavior.title) for behavior in behaviors
    }

    for path in test_files:
        tier = _tier_for_path(feature, path)
        if tier is None:
            continue
        text = texts.get(path)
        if text is None:
            continue

        stem = os.path.splitext(os.path.basename(path))[0]
        sources: List[Tuple[str, Set[str]]] = [(stem, _tokens(stem))]
        for name in _describe_names(text):
            sources.append((name, _tokens(name)))

        # One record per (behavior, file): the best-scoring label wins, so a
        # suite with many specs cannot multiply the same weak claim.
        best: Dict[str, Tuple[float, List[str], str]] = {}
        for label, source_tokens in sources:
            if not source_tokens:
                continue
            for behavior in behaviors:
                if (behavior.id, path) in covered_paths:
                    continue
                tokens = title_tokens[behavior.id]
                if not tokens:
                    continue
                overlap = sorted(source_tokens & tokens)
                if len(overlap) < 2:
                    continue
                ratio = len(overlap) / len(tokens)
                if ratio < _KEYWORD_MIN_RATIO:
                    continue
                confidence = round(min(0.6, 0.2 + 0.5 * ratio), 2)
                current = best.get(behavior.id)
                if (
                    current is None
                    or confidence > current[0]
                    or (confidence == current[0] and label < current[2])
                ):
                    best[behavior.id] = (confidence, overlap, label)

        for behavior_id in sorted(best):
            confidence, overlap, label = best[behavior_id]
            source = "{0}::{1}".format(path, label)
            records.append(
                _record(
                    behavior_id=behavior_id,
                    feature=feature.key,
                    tier=tier,
                    source=source,
                    kind=KIND_KEYWORD,
                    evidence_class=EvidenceClass.INFERRED_MAPPING,
                    boundary=Boundary(),
                    details={
                        "path": path,
                        "describe": label,
                        "matched_keywords": overlap,
                    },
                    confidence=confidence,
                    heuristic=HEURISTIC_KEYWORD_OVERLAP,
                )
            )

    if len(records) > _MAX_KEYWORD_RECORDS_PER_FEATURE:
        kept = sorted(
            records,
            key=lambda record: (-(record.confidence or 0.0), record.subject, record.source),
        )[:_MAX_KEYWORD_RECORDS_PER_FEATURE]
        diagnostics.append(
            _diagnostic(
                ctx,
                code="MAPPING_HEURISTICS_TRUNCATED",
                message="kept the {0} strongest of {1} keyword matches for "
                "{2}".format(len(kept), len(records), feature.key),
                severity="info",
                recovery="Review the feature's mapping file under "
                "config/mappings/ to replace heuristics with reviewed entries.",
            )
        )
        records = kept
    return records


# --------------------------------------------------------------------------
# Collector
# --------------------------------------------------------------------------


def _finalize(
    records: Sequence[EvidenceRecord],
    diagnostics: List[Diagnostic],
    *,
    ctx: CollectorContext,
) -> Tuple[Tuple[EvidenceRecord, ...], Tuple[Diagnostic, ...]]:
    """Collapse identical ids and reject conflicting content deterministically."""
    chosen: Dict[str, EvidenceRecord] = {}
    for record in records:
        existing = chosen.get(record.id)
        if existing is None:
            chosen[record.id] = record
            continue
        if existing.to_dict() != record.to_dict():
            diagnostics.append(
                _diagnostic(
                    ctx,
                    code="MAPPING_DUPLICATE_EVIDENCE",
                    message="two sources produced different content for evidence "
                    "{0}; the first record produced is kept".format(record.id),
                    severity="warning",
                    recovery="Remove the duplicate mapping or make the entries "
                    "identical.",
                )
            )
    ordered = tuple(sorted(chosen.values(), key=lambda record: record.sort_key()))
    ordered_diagnostics = tuple(
        sorted(diagnostics, key=lambda diagnostic: diagnostic.sort_key())
    )
    return ordered, ordered_diagnostics


class TestMappingCollector:
    """Collector that links tests to behaviors with explicit evidence strength."""

    name = COLLECTOR_NAME

    def collect(self, ctx: CollectorContext) -> CollectorBatch:
        # The inventory is read through the shared parser, not through the
        # behaviors collector's batch, so this collector does not depend on
        # registration order.
        inventory_by_feature = {
            inventory.feature_key: inventory
            for inventory in collect_inventory(ctx)
        }

        records: List[EvidenceRecord] = []
        diagnostics: List[Diagnostic] = []

        for feature in sorted(ctx.features, key=lambda entry: entry.key):
            inventory = inventory_by_feature.get(feature.key)
            if inventory is None or not inventory.behaviors:
                # The behaviors collector reports INVENTORY_UNAVAILABLE or
                # INVENTORY_NO_TEST_CASES; there is no subject to map to.
                continue

            test_files, texts, read_errors = _read_test_files(ctx, feature)

            testplan_records = _testplan_records(
                ctx, feature, inventory, diagnostics=diagnostics
            )
            records.extend(testplan_records)

            config_records = _config_records(
                ctx, feature, inventory, diagnostics=diagnostics
            )
            records.extend(config_records)

            reference_records = _test_reference_records(
                ctx,
                feature,
                inventory,
                test_files=test_files,
                texts=texts,
                read_errors=read_errors,
                diagnostics=diagnostics,
            )
            records.extend(reference_records)

            note_records = _coverage_note_records(
                ctx, feature, inventory, diagnostics=diagnostics
            )
            records.extend(note_records)

            covered = {
                (record.subject, str(record.details.get("path", "")))
                for record in (
                    testplan_records
                    + config_records
                    + reference_records
                    + note_records
                )
            }
            records.extend(
                _keyword_records(
                    ctx,
                    feature,
                    inventory,
                    test_files=test_files,
                    texts=texts,
                    covered_paths=covered,
                    diagnostics=diagnostics,
                )
            )

        finalized_records, finalized_diagnostics = _finalize(
            records, diagnostics, ctx=ctx
        )
        return CollectorBatch(
            collector=self.name,
            records=finalized_records,
            diagnostics=finalized_diagnostics,
        )
