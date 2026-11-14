"""Read the three report inputs: feature config, coverprofiles, test plans.

Everything here is pure parsing plus filesystem existence checks. No metric is
computed and no verdict is decided in this module.
"""

from __future__ import annotations

import glob
import os
import re
from dataclasses import dataclass
from typing import Dict, Iterable, List, Optional, Sequence, Tuple

import yaml

UNIT = "unit"
INTEGRATION = "integration"
E2E = "e2e"
NONE = "none"


class InputError(Exception):
    """An input file is missing or malformed."""


@dataclass(frozen=True)
class Feature:
    key: str
    name: str
    aliases: Tuple[str, ...]
    testplans: Tuple[str, ...]
    unit_packages: Tuple[Tuple[str, str], ...]
    group: str = ""
    behaviors: Tuple[dict, ...] = ()
    mappings_reviewed_by: str = ""
    mappings: Tuple[dict, ...] = ()


@dataclass(frozen=True)
class Behavior:
    tc_id: str
    title: str
    priority: str
    automation: str
    tier: str
    execution: str
    id: str = ""
    excluded_reason: str = ""

    @property
    def excluded(self) -> bool:
        return bool(self.excluded_reason)


@dataclass
class PackageCoverage:
    name: str
    statements: int = 0
    covered: int = 0


def _is_slug(value: str) -> bool:
    """Allow a value to be used as a single output-path component."""
    return bool(re.fullmatch(r"[A-Za-z0-9._-]+", value)) and value not in {".", ".."}


class _UniqueKeyLoader(yaml.SafeLoader):
    """A SafeLoader that rejects duplicate mapping keys instead of last-wins."""

    def construct_mapping(self, node, deep=False):
        seen = set()
        for key_node, _value_node in node.value:
            key = self.construct_object(key_node, deep=deep)
            if key in seen:
                raise yaml.constructor.ConstructorError(
                    None, None, "duplicate key: {0!r}".format(key), key_node.start_mark
                )
            seen.add(key)
        return super().construct_mapping(node, deep)


def load_features(path: str) -> List[Feature]:
    """Load and minimally validate ``features.yaml``."""
    try:
        with open(path, encoding="utf-8") as handle:
            loader = _UniqueKeyLoader(handle)
            try:
                document = loader.get_single_data() or {}
            finally:
                loader.dispose()
    except (OSError, UnicodeDecodeError) as exc:
        raise InputError("cannot read {0}: {1}".format(path, exc)) from exc
    except yaml.YAMLError as exc:
        raise InputError("cannot parse {0}: {1}".format(path, exc)) from exc

    entries = document.get("features") if isinstance(document, dict) else None
    if not entries:
        raise InputError("{0}: no features defined".format(path))
    default_group = str(document.get("group") or "").strip()

    features: List[Feature] = []
    for entry in entries:
        if not isinstance(entry, dict) or not entry.get("key"):
            raise InputError("{0}: every feature needs a key".format(path))
        unit_packages = entry.get("unit_packages") or []
        aliases = entry.get("aliases") or []
        if not isinstance(unit_packages, list):
            raise InputError("{0}: {1} unit_packages must be a list".format(path, entry["key"]))
        if not isinstance(aliases, list):
            raise InputError("{0}: {1} aliases must be a list".format(path, entry["key"]))
        packages = []
        for item in unit_packages:
            if isinstance(item, dict):
                packages.append(
                    (
                        str(item.get("package", "")).strip(),
                        " ".join(str(item.get("rationale", "")).split()),
                    )
                )
            else:
                packages.append((str(item).strip(), ""))
        for package, _ in packages:
            if not package:
                raise InputError(
                    "{0}: {1} has an empty unit package".format(path, entry["key"])
                )
        mappings = entry.get("mappings") or []
        if not isinstance(mappings, list) or not all(isinstance(item, dict) for item in mappings):
            raise InputError(
                "{0}: {1} mappings must be a list of mappings".format(path, entry["key"])
            )
        key = str(entry["key"]).strip()
        if not _is_slug(key):
            raise InputError("{0}: invalid feature key {1!r}".format(path, key))
        group = str(entry.get("group") or default_group).strip()
        if group and not _is_slug(group):
            raise InputError("{0}: invalid group {1!r}".format(path, group))
        testplans = entry.get("testplans") or []
        if not isinstance(testplans, list):
            raise InputError("{0}: {1} testplans must be a list".format(path, key))
        inline = entry.get("behaviors") or []
        if not isinstance(inline, list) or not all(isinstance(item, dict) for item in inline):
            raise InputError("{0}: {1} behaviors must be a list of mappings".format(path, key))
        if not all(str(item.get("id", "")).strip() and str(item.get("title", "")).strip() for item in inline):
            raise InputError("{0}: {1} behaviors need id and title".format(path, key))
        features.append(
            Feature(
                key=key,
                name=str(entry.get("name", "")).strip(),
                aliases=tuple(str(alias).strip() for alias in aliases),
                testplans=tuple(str(plan).strip() for plan in testplans if str(plan).strip()),
                unit_packages=tuple(packages),
                group=group,
                behaviors=tuple(inline),
                mappings_reviewed_by=str(entry.get("mappings_reviewed_by", "")).strip(),
                mappings=tuple(mappings),
            )
        )
    return features


def load_feature_paths(paths: Sequence[str]) -> List[Feature]:
    """Load and merge config files, or directories of ``*.yaml`` files."""
    files: List[str] = []
    for path in paths:
        if os.path.isdir(path):
            files.extend(sorted(glob.glob(os.path.join(path, "*.yaml"))))
        else:
            files.append(path)
    features: List[Feature] = []
    origin: Dict[str, str] = {}
    for path in files:
        for feature in load_features(path):
            if feature.key in origin:
                raise InputError("duplicate area key {0!r} in {1} and {2}".format(
                    feature.key, origin[feature.key], path))
            origin[feature.key] = path
            features.append(feature)
    if not features:
        raise InputError("no features found in {0}".format(", ".join(paths)))
    return features


def resolve_feature(features: Sequence[Feature], query: str) -> Optional[Feature]:
    """Return the feature named by a key, name, or alias (case-insensitive)."""
    wanted = query.strip().casefold()
    for feature in features:
        names = [feature.key.casefold(), feature.name.casefold()]
        names.extend(alias.casefold() for alias in feature.aliases)
        if wanted in names:
            return feature
    return None


# --- Go coverprofiles -------------------------------------------------------


def parse_coverprofiles(paths: Iterable[str]) -> Dict[str, PackageCoverage]:
    """Merge coverprofiles into per-package statement totals.

    Identical blocks from several profiles are deduplicated, keeping the
    highest execution count. Blocks are keyed by file and source range, so two
    ``go test -coverprofile`` runs over the same code do not double count.
    """
    blocks: Dict[Tuple[str, str], Tuple[int, int]] = {}
    for path in paths:
        mode_seen = False
        try:
            with open(path, encoding="utf-8") as handle:
                for raw in handle:
                    line = raw.strip()
                    if not line:
                        continue
                    if not mode_seen:
                        if not line.startswith("mode:"):
                            raise InputError("{0}: missing 'mode:' line".format(path))
                        mode_seen = True
                        continue
                    try:
                        file_part, rest = line.rsplit(":", 1)
                        block, statements, count = rest.split()
                        statements = int(statements)
                        count = int(count)
                    except ValueError as exc:
                        raise InputError(
                            "{0}: malformed coverage line: {1}".format(path, line)
                        ) from exc
                    key = (file_part, block)
                    previous = blocks.get(key)
                    if previous is None or count > previous[0]:
                        blocks[key] = (count, statements)
        except (OSError, UnicodeDecodeError) as exc:
            raise InputError("cannot read coverprofile {0}: {1}".format(path, exc)) from exc
        if not mode_seen:
            raise InputError("{0}: missing 'mode:' line".format(path))

    packages: Dict[str, PackageCoverage] = {}
    for (file_part, _block), (count, statements) in blocks.items():
        name = file_part.rsplit("/", 1)[0] if "/" in file_part else ""
        package = packages.setdefault(name, PackageCoverage(name))
        package.statements += statements
        if count > 0:
            package.covered += statements
    return packages


def round_percent(covered: int, statements: int) -> Optional[float]:
    """Return ``covered/statements*100`` rounded half-up to one decimal."""
    if statements <= 0:
        return None
    from decimal import ROUND_HALF_UP, Decimal

    value = Decimal(covered) * Decimal(100) / Decimal(statements)
    return float(value.quantize(Decimal("0.1"), rounding=ROUND_HALF_UP))


def contained_path(repo_root: str, relative: str) -> Optional[str]:
    """Resolve a repo-relative path; ``None`` when it escapes the repository."""
    if not relative or os.path.isabs(relative):
        return None
    root = os.path.realpath(repo_root)
    candidate = os.path.realpath(os.path.join(root, relative))
    try:
        if os.path.commonpath([root, candidate]) != root:
            return None
    except ValueError:
        return None
    return candidate


def _within(root: str, path: str) -> bool:
    try:
        return os.path.commonpath([root, path]) == root
    except ValueError:
        return False


def testplan_path(
    repo_root: str, testplan: str, allow_external: Sequence[str] = ()
) -> Tuple[str, str]:
    """Resolve a test plan and the external root trusted for it ("" if none).

    Rejects absolute and ``..`` paths; returns a missing file so the caller
    reports ``na``. A symlinked ``enhancement-proposals`` checkout and
    ``--allow-external-root`` entries may resolve outside the repository.
    """
    if not testplan:
        return "", ""
    if os.path.isabs(testplan) or ".." in testplan.replace("\\", "/").split("/"):
        raise InputError("invalid testplan path: {0}".format(testplan))
    path = os.path.join(repo_root, testplan)
    if not os.path.exists(path):
        return path, ""
    if not os.path.isfile(path):
        raise InputError("invalid testplan path (not a file): {0}".format(testplan))
    root = os.path.realpath(repo_root)
    resolved = os.path.realpath(path)
    if _within(root, resolved):
        return path, ""
    roots = [os.path.realpath(item) for item in allow_external]
    proposals = os.path.join(repo_root, "enhancement-proposals")
    roots += [os.path.realpath(proposals)] if os.path.islink(proposals) else []
    for candidate in roots:
        if _within(candidate, resolved):
            return path, candidate
    raise InputError("testplan path escapes the repository: {0}".format(testplan))


def plan_key(testplan: str) -> str:
    """Return a short unique key for a test plan: its Jira key or directory."""
    directory = os.path.basename(os.path.dirname(testplan))
    match = re.search(r"OSAC-\d+", directory)
    return match.group(0) if match else (directory or "plan")


# --- Test plans -------------------------------------------------------------

_TC_ID = r"TC-[A-Za-z0-9_](?:[A-Za-z0-9._-]*[A-Za-z0-9_])?"
_TC_HEADING_RE = re.compile(r"^(#{3,4})\s+(" + _TC_ID + r")\s*:\s*(.+?)\s*$")
_HEADING_RE = re.compile(r"^(#{1,6})\s")
_BULLET_RE = re.compile(r"^-\s+\*\*(?P<name>[^:*]+):\*\*\s*(?P<value>.*)$")
_BACKTICK_RE = re.compile(r"`([^`]+)`")
_TC_ID_RE = re.compile(r"\b" + _TC_ID + r"\b")
_TEST_PATH_RE = re.compile(r".+\.(?:go|py|ts|tsx|js)$")

_INTEGRATION_PREFIXES = ("component integration", "contract", "envtest", "ui integration")


def normalize_tier(value: str) -> str:
    """Map a test-plan tier label onto ``unit``/``integration``/``e2e``/``none``."""
    label = " ".join((value or "").replace("_", " ").split()).casefold()
    if label.startswith("unit"):
        return UNIT
    if label.startswith("e2e"):
        return E2E
    if label.startswith(_INTEGRATION_PREFIXES):
        return INTEGRATION
    return NONE


def _cells(row: str) -> List[str]:
    return [cell.strip() for cell in row.strip().strip("|").split("|")]


def _is_separator(cells: Sequence[str]) -> bool:
    return bool(cells) and all(
        re.fullmatch(r":?-{2,}:?", cell.replace(" ", "")) for cell in cells
    )


def _tables(lines: Sequence[str]) -> List[List[List[str]]]:
    tables: List[List[List[str]]] = []
    current: List[List[str]] = []
    for line in lines:
        if line.strip().startswith("|"):
            current.append(_cells(line))
        elif current:
            tables.append(current)
            current = []
    if current:
        tables.append(current)
    return tables


def _table_fields(lines: Sequence[str]) -> Dict[str, str]:
    """Return the Priority/Automation cells of the first scoring table."""
    for rows in _tables(lines):
        if len(rows) < 2:
            continue
        header = [cell.casefold() for cell in rows[0]]
        if "priority" not in header or "automation" not in header:
            continue
        data = next((row for row in rows[1:] if not _is_separator(row)), None)
        if data is None:
            continue
        fields = {}
        for name in ("priority", "automation"):
            index = header.index(name)
            fields[name] = data[index] if index < len(data) else ""
        return fields
    return {}


def _bullet_fields(lines: Sequence[str]) -> Dict[str, str]:
    """Return ``- **Name:** value`` bullets, joining wrapped continuation lines."""
    fields: Dict[str, str] = {}
    index = 0
    while index < len(lines):
        match = _BULLET_RE.match(lines[index])
        if not match:
            index += 1
            continue
        name = " ".join(match.group("name").split()).casefold()
        parts = [match.group("value").strip()]
        index += 1
        while index < len(lines):
            following = lines[index]
            stripped = following.strip()
            if (
                not stripped
                or not following.startswith((" ", "\t"))
                or stripped.startswith(("-", "#", "|"))
            ):
                break
            parts.append(stripped)
            index += 1
        fields.setdefault(name, " ".join(part for part in parts if part))
    return fields


def _split_cases(text: str) -> List[Tuple[str, str, List[str]]]:
    """Return ``(tc_id, title, body_lines)`` for every TC heading."""
    lines = text.splitlines()
    headings = []
    for index, line in enumerate(lines):
        match = _TC_HEADING_RE.match(line)
        if match:
            headings.append((index, len(match.group(1)), match.group(2), match.group(3)))
    cases = []
    for index, level, tc_id, title in headings:
        end = len(lines)
        for next_index in range(index + 1, len(lines)):
            heading = _HEADING_RE.match(lines[next_index])
            if heading and len(heading.group(1)) <= level:
                end = next_index
                break
        cases.append((tc_id, title.strip(), lines[index + 1 : end]))
    return cases


def parse_testplan(text: str, prefix: str = "") -> List[Behavior]:
    """Parse behaviors from test-plan text, prefixing ids for multi-plan features."""
    behaviors = []
    for tc_id, title, body in _split_cases(text):
        bullets = _bullet_fields(body)
        table = _table_fields(body)
        automation = " ".join(table.get("automation", "").split()).casefold()
        if "manual" in automation:
            excluded = "test case is manual"
        elif "not automat" in automation or "non-automat" in automation:
            excluded = "test case is not automatable"
        else:
            excluded = ""
        behaviors.append(
            Behavior(
                tc_id=tc_id,
                title=title,
                priority=" ".join(table.get("priority", "").split()).casefold(),
                automation=automation,
                tier=normalize_tier(
                    bullets.get("tier") or bullets.get("tier / owner", "")
                ),
                execution=bullets.get("execution", ""),
                id=prefix + tc_id,
                excluded_reason=excluded,
            )
        )
    return behaviors


def execution_matrix(text: str) -> Dict[str, str]:
    """Return ``tc_id -> path cell`` from a Planning Evidence Matrix, if present."""
    rows: Dict[str, str] = {}
    for table in _tables(text.splitlines()):
        if len(table) < 2:
            continue
        header = [cell.casefold() for cell in table[0]]
        if "test case ids" not in header:
            continue
        ids_index = header.index("test case ids")
        names = ("existing or proposed path and command", "path and command")
        path_index = next((header.index(name) for name in names if name in header), None)
        if path_index is None:
            continue
        for row in table[1:]:
            if _is_separator(row):
                continue
            if ids_index >= len(row) or path_index >= len(row):
                continue
            path_text = row[path_index]
            for tc_id in _TC_ID_RE.findall(row[ids_index]):
                previous = rows.get(tc_id)
                rows[tc_id] = path_text if previous is None else previous + "; " + path_text
    return rows


def _path_tokens(text: str) -> List[str]:
    """Return test-file candidates from a path cell or Execution bullet."""
    found: List[str] = []
    for span in _BACKTICK_RE.findall(text or ""):
        candidate = span.strip()
        if not candidate:
            continue
        words = candidate.split() if " " in candidate else [candidate]
        for word in words:
            word = word.strip("\"'(),;")
            if _TEST_PATH_RE.match(word):
                found.append(word)
    return list(dict.fromkeys(found))


def candidate_test_paths(
    execution: str, matrix_text: str, repo_root: str
) -> Tuple[List[str], List[str]]:
    """Split plan-named paths into ``(declared, planned)``.

    A path is declared when at least one mention of it is not marked
    "proposed" and the file exists under ``repo_root``. Everything else is a
    planned path: either explicitly proposed or named but missing.
    """
    proposed: Dict[str, bool] = {}
    for text in (execution, matrix_text):
        if not text:
            continue
        for segment in re.split(r"[;\n]", text):
            is_proposed = "proposed" in segment.casefold()
            for token in _path_tokens(segment):
                proposed[token] = proposed.get(token, True) and is_proposed

    declared: List[str] = []
    planned: List[str] = []
    for token in sorted(proposed):
        resolved = contained_path(repo_root, token)
        exists = resolved is not None and os.path.isfile(resolved)
        if exists and not proposed[token]:
            declared.append(token)
        else:
            planned.append(token)
    return declared, planned
