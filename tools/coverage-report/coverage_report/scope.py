"""Feature scope resolution against the version-controlled registry.

A feature is selected by Jira key, registry name, or alias, case-insensitively.
Resolution happens before any collector runs: an unknown or ambiguous request
must fail fast with concrete candidates, not after minutes of collection.

No network and no Jira calls. A Jira key is only a lookup key into
``config/features.yaml``.
"""

import difflib
import os
import re
import subprocess
from dataclasses import dataclass, field
from typing import Dict, List, Optional, Tuple

import yaml

from .config import expect_mapping, expect_string_list
from .errors import ConfigError, RepoError, ScopeError
from .model import TIER_ORDER

FEATURES_VERSION = 1
_UNIT_PACKAGE_KEYS = frozenset({"package", "rationale"})
_SUITE_TIERS = frozenset(tier.value for tier in TIER_ORDER)


@dataclass(frozen=True)
class UnitPackage:
    """A Go package included in unit coverage, with the reason it belongs."""

    package: str
    rationale: str = ""


@dataclass(frozen=True)
class FeatureEntry:
    """One feature from ``config/features.yaml``."""

    key: str
    name: str
    aliases: Tuple[str, ...] = ()
    testplan: Optional[str] = None
    components: Tuple[str, ...] = ()
    unit_packages: Tuple[UnitPackage, ...] = ()
    suites: Dict[str, Tuple[str, ...]] = field(default_factory=dict)


@dataclass(frozen=True)
class FeatureRegistry:
    """Parsed feature registry."""

    version: int
    features: Tuple[FeatureEntry, ...]
    source: str

    def by_key(self, key: str) -> Optional[FeatureEntry]:
        for feature in self.features:
            if feature.key == key:
                return feature
        return None


@dataclass(frozen=True)
class ScopeResolution:
    """The outcome of scope resolution, including its audit trail."""

    requested: Optional[str]
    matched_by: str
    features: Tuple[FeatureEntry, ...]
    sha: str
    sha_source: str

    @property
    def label(self) -> str:
        """Directory-name label for the resolved scope."""
        if len(self.features) == 1 and self.requested is not None:
            return self.features[0].key
        return "ALL"

    def to_dict(self) -> Dict[str, object]:
        return {
            "input": self.requested,
            "matched_by": self.matched_by,
            "features": [feature.key for feature in self.features],
            "sha": self.sha,
            "sha_source": self.sha_source,
        }


def _parse_unit_packages(value: object, field: str) -> Tuple[UnitPackage, ...]:
    if not isinstance(value, list):
        raise ConfigError(
            "field {0} must be a list".format(field),
            field=field,
            recovery="Use a list of {package, rationale} mappings or package strings.",
        )
    packages: List[UnitPackage] = []
    for index, item in enumerate(value):
        item_field = "{0}[{1}]".format(field, index)
        if isinstance(item, str):
            packages.append(UnitPackage(package=item))
            continue
        if not isinstance(item, dict):
            raise ConfigError(
                "field {0} must be a string or mapping".format(item_field),
                field=item_field,
                recovery="Use {package: ..., rationale: ...}.",
            )
        unknown = sorted(set(item) - _UNIT_PACKAGE_KEYS)
        if unknown:
            raise ConfigError(
                "unknown unit_packages key(s): {0}".format(", ".join(unknown)),
                field="{0}.{1}".format(item_field, unknown[0]),
                candidates=sorted(_UNIT_PACKAGE_KEYS),
                recovery="Allowed keys are package and rationale.",
            )
        package = item.get("package")
        if not isinstance(package, str) or not package:
            raise ConfigError(
                "field {0}.package must be a non-empty string".format(item_field),
                field="{0}.package".format(item_field),
                recovery="Set package to the import path or directory.",
            )
        rationale = item.get("rationale", "")
        if not isinstance(rationale, str):
            raise ConfigError(
                "field {0}.rationale must be a string".format(item_field),
                field="{0}.rationale".format(item_field),
                recovery="Explain why the package is in scope.",
            )
        packages.append(UnitPackage(package=package, rationale=rationale))
    return tuple(packages)


def _parse_suites(value: object, field: str) -> Dict[str, Tuple[str, ...]]:
    mapping = expect_mapping(
        value, field, recovery="Fix the structure of the feature registry."
    )
    unknown = sorted(set(mapping) - _SUITE_TIERS)
    if unknown:
        raise ConfigError(
            "unknown suite tier(s): {0}".format(", ".join(str(item) for item in unknown)),
            field="{0}.{1}".format(field, unknown[0]),
            candidates=sorted(_SUITE_TIERS),
            recovery="Allowed suite tiers are unit, integration, e2e.",
        )
    return {
        str(tier): tuple(
            expect_string_list(
                mapping[tier],
                "{0}.{1}".format(field, tier),
                recovery="Use a YAML list, for example [].",
            )
        )
        for tier in sorted(mapping)
    }



def load_registry(path: str) -> FeatureRegistry:
    """Load and validate ``features.yaml``.

    Raises:
        ConfigError: If the file is missing, malformed, has a duplicate key, or
            has a field of the wrong type.
    """
    if not os.path.isfile(path):
        raise ConfigError(
            "feature registry not found: {0}".format(path),
            code="FEATURES_FILE_NOT_FOUND",
            field=path,
            recovery="Create the registry or point features_file at an existing file.",
        )
    try:
        with open(path, "r", encoding="utf-8") as handle:
            data = yaml.safe_load(handle)
    except yaml.YAMLError as error:
        raise ConfigError(
            "failed to parse feature registry {0}: {1}".format(path, error),
            code="FEATURES_FILE_PARSE_ERROR",
            field=path,
            recovery="Fix the YAML syntax in {0}.".format(path),
        )
    if not isinstance(data, dict):
        raise ConfigError(
            "feature registry must contain a YAML mapping at the top level",
            field=path,
            recovery="Start the file with version: 1 and a features: list.",
        )

    version = data.get("version", FEATURES_VERSION)
    if isinstance(version, bool) or not isinstance(version, int):
        raise ConfigError(
            "feature registry version must be an integer",
            field="version",
            recovery="Use version: {0}.".format(FEATURES_VERSION),
        )
    if version != FEATURES_VERSION:
        raise ConfigError(
            "unsupported feature registry version {0}".format(version),
            code="FEATURES_VERSION_UNSUPPORTED",
            field="version",
            recovery="Use version: {0}.".format(FEATURES_VERSION),
        )

    raw_features = data.get("features")
    if not isinstance(raw_features, list):
        raise ConfigError(
            "field features must be a list",
            field="features",
            recovery="Add features: [] with one entry per feature.",
        )

    features: List[FeatureEntry] = []
    seen_keys: Dict[str, int] = {}
    for index, raw_feature in enumerate(raw_features):
        field_name = "features[{0}]".format(index)
        entry = expect_mapping(
            raw_feature,
            field_name,
            recovery="Fix the structure of the feature registry.",
        )
        key = entry.get("key")
        if not isinstance(key, str) or not key:
            raise ConfigError(
                "field {0}.key must be a non-empty string".format(field_name),
                field="{0}.key".format(field_name),
                recovery="Use the Jira key, for example OSAC-2540.",
            )
        normalized_key = key.casefold()
        if normalized_key in seen_keys:
            raise ConfigError(
                "duplicate feature key {0!r} (also at index {1})".format(
                    key, seen_keys[normalized_key]
                ),
                code="FEATURES_DUPLICATE_KEY",
                field="{0}.key".format(field_name),
                recovery="Give each feature a unique Jira key.",
            )
        seen_keys[normalized_key] = index

        name = entry.get("name")
        if not isinstance(name, str) or not name:
            raise ConfigError(
                "field {0}.name must be a non-empty string".format(field_name),
                field="{0}.name".format(field_name),
                recovery="Use a short lowercase feature name, for example disk-image.",
            )

        aliases = tuple(
            expect_string_list(
                entry.get("aliases", []),
                "{0}.aliases".format(field_name),
                recovery="Use a YAML list, for example [].",
            )
        )
        components = tuple(
            expect_string_list(
                entry.get("components", []),
                "{0}.components".format(field_name),
                recovery="Use a YAML list, for example [].",
            )
        )
        testplan = entry.get("testplan")
        if testplan is not None and not isinstance(testplan, str):
            raise ConfigError(
                "field {0}.testplan must be a string or null".format(field_name),
                field="{0}.testplan".format(field_name),
                recovery="Use a repository-relative path.",
            )
        rationale = entry.get("rationale")
        if rationale is not None and not isinstance(rationale, str):
            raise ConfigError(
                "field {0}.rationale must be a string".format(field_name),
                field="{0}.rationale".format(field_name),
                recovery="Explain why the feature is in scope.",
            )

        features.append(
            FeatureEntry(
                key=key,
                name=name,
                aliases=aliases,
                testplan=testplan,
                components=components,
                unit_packages=_parse_unit_packages(
                    entry.get("unit_packages", []),
                    "{0}.unit_packages".format(field_name),
                ),
                suites=_parse_suites(
                    entry.get("suites", {}), "{0}.suites".format(field_name)
                ),
            )
        )

    return FeatureRegistry(
        version=version,
        features=tuple(sorted(features, key=lambda feature: feature.key)),
        source=path,
    )


def resolve_sha(repo_root: str, sha_override: Optional[str] = None) -> Tuple[str, str]:
    """Return ``(sha, sha_source)`` for the run.

    Args:
        repo_root: Repository root used to query git.
        sha_override: Full or abbreviated commit supplied with ``--sha``.

    Raises:
        RepoError: When no override is given and ``git rev-parse HEAD`` fails.
    """
    if sha_override is not None:
        value = sha_override.strip()
        if not re.fullmatch(r"[0-9a-fA-F]{4,64}", value):
            raise RepoError(
                "invalid --sha value {0!r}".format(sha_override),
                code="SHA_INVALID",
                recovery="Pass a hexadecimal commit sha (at least 4 characters).",
            )
        return value.lower(), "cli"

    try:
        completed = subprocess.run(
            ["git", "rev-parse", "HEAD"],
            cwd=repo_root,
            check=True,
            capture_output=True,
            text=True,
        )
    except (OSError, subprocess.CalledProcessError) as error:
        raise RepoError(
            "could not resolve HEAD in {0}: {1}".format(repo_root, error),
            code="GIT_UNAVAILABLE",
            recovery="Run inside a git checkout, or pass --sha explicitly.",
        )
    sha = completed.stdout.strip()
    if not re.fullmatch(r"[0-9a-f]{40,64}", sha):
        raise RepoError(
            "git returned an unexpected HEAD value {0!r}".format(sha),
            code="GIT_INVALID_SHA",
            recovery="Pass --sha explicitly.",
        )
    return sha, "git"


def _match_terms(feature: FeatureEntry) -> List[Tuple[str, str]]:
    terms: List[Tuple[str, str]] = [(feature.key, "key"), (feature.name, "name")]
    terms.extend((alias, "alias") for alias in feature.aliases)
    return terms


def _candidate_names(registry: FeatureRegistry) -> List[str]:
    names = set()
    for feature in registry.features:
        names.add(feature.key)
        names.add(feature.name)
        names.update(feature.aliases)
    return sorted(names)


def _close_matches(registry: FeatureRegistry, requested: str) -> List[str]:
    lookup = {name.casefold(): name for name in _candidate_names(registry)}
    matches = difflib.get_close_matches(
        requested.casefold(), sorted(lookup), n=5, cutoff=0.5
    )
    return [lookup[match] for match in matches]


def _registry_snippet(requested: str) -> str:
    slug = re.sub(r"[^a-z0-9]+", "-", requested.casefold()).strip("-") or "new-feature"
    return (
        "features:\n"
        "  - key: {0}\n"
        "    name: {1}\n"
        "    aliases: []\n"
        "    testplan: enhancement-proposals/enhancements/{0}-{1}/testplan.md\n"
        "    components: []\n"
        "    unit_packages: []\n"
        "    suites: {{integration: [], e2e: []}}\n".format(requested, slug)
    )


def resolve_scope(
    registry: FeatureRegistry, requested: Optional[str]
) -> Tuple[Tuple[FeatureEntry, ...], str]:
    """Resolve a user-supplied feature selector.

    Args:
        registry: Parsed feature registry.
        requested: Jira key, feature name, or alias. ``None`` selects every
            feature in the registry (the full plan).

    Returns:
        ``(features, matched_by)`` with features sorted by key.

    Raises:
        ScopeError: ``SCOPE_UNRESOLVED`` with close candidates plus a registry
            snippet, or ``SCOPE_AMBIGUOUS`` naming every conflicting key.
    """
    if requested is None:
        if not registry.features:
            raise ScopeError(
                "the feature registry is empty; there is nothing to report on",
                code="SCOPE_UNRESOLVED",
                recovery="Add at least one feature to {0}.".format(registry.source),
            )
        return registry.features, "full_plan"

    needle = requested.strip()
    if not needle:
        raise ScopeError(
            "feature selector must not be empty",
            code="SCOPE_UNRESOLVED",
            candidates=_candidate_names(registry),
            recovery="Pass a Jira key, feature name, or alias, or omit it for all features.",
        )
    folded = needle.casefold()

    matched: Dict[str, Tuple[FeatureEntry, str]] = {}
    for feature in registry.features:
        for term, term_kind in _match_terms(feature):
            if term.casefold() == folded:
                matched.setdefault(feature.key, (feature, term_kind))

    if len(matched) == 1:
        feature, matched_by = next(iter(matched.values()))
        return (feature,), matched_by

    if len(matched) > 1:
        candidates = sorted(matched)
        raise ScopeError(
            "feature selector {0!r} is ambiguous; it matches {1}".format(
                requested, ", ".join(candidates)
            ),
            code="SCOPE_AMBIGUOUS",
            candidates=candidates,
            details={"input": requested, "matches": candidates},
            recovery="Re-run with the exact Jira key instead of the shared name or alias.",
        )

    candidates = _close_matches(registry, needle)
    raise ScopeError(
        "feature selector {0!r} did not match any entry in {1}".format(
            requested, registry.source
        ),
        code="SCOPE_UNRESOLVED",
        candidates=candidates,
        details={
            "input": requested,
            "registry": registry.source,
            "snippet": _registry_snippet(needle),
        },
        recovery="Use one of the candidates, or add the feature to the registry:\n{0}".format(
            _registry_snippet(needle)
        ),
    )


def resolve(
    registry: FeatureRegistry,
    requested: Optional[str],
    *,
    repo_root: str,
    sha_override: Optional[str] = None,
) -> ScopeResolution:
    """Resolve scope and snapshot the revision the report describes."""
    features, matched_by = resolve_scope(registry, requested)
    sha, sha_source = resolve_sha(repo_root, sha_override)
    return ScopeResolution(
        requested=requested,
        matched_by=matched_by,
        features=features,
        sha=sha,
        sha_source=sha_source,
    )
