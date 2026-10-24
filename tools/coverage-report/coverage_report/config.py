"""Configuration loading, validation, and precedence.

Precedence is fixed: CLI flags > repository ``.coverage-skill.yaml`` > built-in
defaults. The returned :class:`EffectiveConfig` carries an ``origins`` map
recording which layer supplied each key, so the run manifest can explain where
a value came from.

Relative paths in configuration resolve against the repository root. The one
exception is the bundled feature registry, which defaults to an absolute path
inside this tool so the reporter works in a checkout that has no
``.coverage-skill.yaml`` of its own.
"""

import os
from dataclasses import dataclass
from typing import Dict, List, Mapping, Optional, Tuple

import yaml

from .errors import ConfigError

CONFIG_FILENAME = ".coverage-skill.yaml"
CONFIG_VERSION = 1

DEFAULT_ENHANCEMENT_ROOTS: Tuple[str, ...] = (
    "enhancement-proposals/enhancements",
    ".artifacts/design",
)
DEFAULT_OUTPUT_DIR = ".artifacts/coverage-report"

#: Allowed metric implementations per tier. The first entry is the default.
ALLOWED_METRIC_VERSIONS: Dict[str, Tuple[str, ...]] = {
    "unit": ("legacy-package-average-v1", "statement-weighted-v2"),
    "integration": ("behavior-inventory-v1",),
    "e2e": ("behavior-inventory-v1",),
}
DEFAULT_METRIC_VERSIONS: Dict[str, str] = {
    "unit": "statement-weighted-v2",
    "integration": "behavior-inventory-v1",
    "e2e": "behavior-inventory-v1",
}

_TOP_LEVEL_KEYS = frozenset(
    {
        "version",
        "features_file",
        "enhancement_roots",
        "artifacts",
        "metric_versions",
        "output_dir",
        "policy",
    }
)
_ARTIFACT_KEYS = frozenset({"coverprofiles", "junit"})
_POLICY_KEYS = frozenset({"require_execution", "unit_low_coverage_percent"})

DEFAULT_REQUIRE_EXECUTION = False
DEFAULT_UNIT_LOW_COVERAGE_PERCENT = 50.0


def bundled_features_file() -> str:
    """Return the absolute path of the feature registry shipped with the tool."""
    return os.path.join(
        os.path.dirname(os.path.dirname(os.path.abspath(__file__))),
        "config",
        "features.yaml",
    )


@dataclass(frozen=True)
class Policy:
    """Interpretation rules that change what a metric means.

    ``require_execution`` gates the behavior-inventory numerator on a current
    passing execution. It defaults to ``False`` in v1 so the metric measures
    mapping coverage; execution status is still reported alongside it.

    ``unit_low_coverage_percent`` is the statement-coverage threshold below
    which a measured unit package becomes a gap.
    """

    require_execution: bool = DEFAULT_REQUIRE_EXECUTION
    unit_low_coverage_percent: float = DEFAULT_UNIT_LOW_COVERAGE_PERCENT

    def to_dict(self) -> Dict[str, object]:
        return {
            "require_execution": self.require_execution,
            "unit_low_coverage_percent": self.unit_low_coverage_percent,
        }


@dataclass(frozen=True)
class EffectiveConfig:
    """Validated configuration with resolved absolute paths."""

    version: int
    features_file: str
    enhancement_roots: Tuple[str, ...]
    coverprofiles: Tuple[str, ...]
    junit: Tuple[str, ...]
    metric_versions: Dict[str, str]
    output_dir: str
    origins: Dict[str, str]
    config_path: Optional[str] = None
    policy: Policy = Policy()

    def to_dict(self) -> Dict[str, object]:
        """Return the effective values (no origins) for hashing and manifests."""
        return {
            "version": self.version,
            "features_file": self.features_file,
            "enhancement_roots": list(self.enhancement_roots),
            "artifacts": {
                "coverprofiles": list(self.coverprofiles),
                "junit": list(self.junit),
            },
            "metric_versions": {
                tier: self.metric_versions[tier]
                for tier in sorted(self.metric_versions)
            },
            "output_dir": self.output_dir,
            "policy": self.policy.to_dict(),
        }


@dataclass(frozen=True)
class CliOverrides:
    """Configuration values supplied on the command line."""

    output_dir: Optional[str] = None
    coverprofiles: Tuple[str, ...] = ()
    junit: Tuple[str, ...] = ()
    config_path: Optional[str] = None


def _resolve_path(root: str, value: str) -> str:
    expanded = os.path.expanduser(value)
    if os.path.isabs(expanded):
        return os.path.normpath(expanded)
    return os.path.normpath(os.path.join(root, expanded))


def _expect_string(value: object, field: str) -> str:
    if not isinstance(value, str) or not value.strip():
        raise ConfigError(
            "field {0} must be a non-empty string".format(field),
            field=field,
            recovery="Set {0} to a non-empty string.".format(field),
        )
    return value


def expect_string_list(
    value: object, field: str, *, recovery: Optional[str] = None
) -> List[str]:
    if not isinstance(value, list) or not all(isinstance(item, str) for item in value):
        raise ConfigError(
            "field {0} must be a list of strings".format(field),
            field=field,
            recovery=(
                recovery or "Use a YAML list of paths/globs, for example []."
            ),
        )
    return list(value)


def expect_mapping(
    value: object, field: str, *, recovery: Optional[str] = None
) -> Mapping[object, object]:
    if not isinstance(value, dict):
        raise ConfigError(
            "field {0} must be a mapping".format(field),
            field=field,
            recovery=recovery or "Use a YAML mapping for {0}.".format(field),
        )
    return value


def _read_config_file(path: str) -> Mapping[str, object]:
    if not os.path.isfile(path):
        raise ConfigError(
            "configuration file not found: {0}".format(path),
            code="CONFIG_NOT_FOUND",
            field=path,
            recovery="Create {0} or pass --config with an existing file.".format(
                CONFIG_FILENAME
            ),
        )
    try:
        with open(path, "r", encoding="utf-8") as handle:
            data = yaml.safe_load(handle)
    except yaml.YAMLError as error:
        raise ConfigError(
            "failed to parse {0}: {1}".format(path, error),
            code="CONFIG_PARSE_ERROR",
            field=path,
            recovery="Fix the YAML syntax in {0}.".format(path),
        )
    except OSError as error:
        raise ConfigError(
            "failed to read {0}: {1}".format(path, error),
            code="CONFIG_UNREADABLE",
            field=path,
            recovery="Check file permissions on {0}.".format(path),
        )
    if data is None:
        return {}
    if not isinstance(data, dict):
        raise ConfigError(
            "configuration file must contain a YAML mapping at the top level",
            field=path,
            recovery="Make {0} a mapping with keys such as version and features_file.".format(
                path
            ),
        )
    return data


def _validate_version(value: object, field: str) -> int:
    if isinstance(value, bool) or not isinstance(value, int):
        raise ConfigError(
            "field {0} must be an integer".format(field),
            field=field,
            recovery="Use version: {0}.".format(CONFIG_VERSION),
        )
    if value != CONFIG_VERSION:
        raise ConfigError(
            "unsupported config version {0}".format(value),
            code="CONFIG_VERSION_UNSUPPORTED",
            field=field,
            recovery="Use version: {0}.".format(CONFIG_VERSION),
        )
    return value


def _validate_metric_versions(value: object, field: str) -> Dict[str, str]:
    mapping = expect_mapping(value, field)
    unknown_tiers = sorted(set(mapping) - set(ALLOWED_METRIC_VERSIONS))
    if unknown_tiers:
        raise ConfigError(
            "unknown metric tier(s) in {0}: {1}".format(
                field, ", ".join(str(tier) for tier in unknown_tiers)
            ),
            field=field,
            recovery="Allowed tiers are unit, integration, e2e.",
        )
    result: Dict[str, str] = {}
    for tier in sorted(mapping):
        version = mapping[tier]
        if not isinstance(version, str):
            raise ConfigError(
                "field {0}.{1} must be a string".format(field, tier),
                field="{0}.{1}".format(field, tier),
                recovery="Choose one of: {0}.".format(
                    ", ".join(ALLOWED_METRIC_VERSIONS[tier])
                ),
            )
        if version not in ALLOWED_METRIC_VERSIONS[tier]:
            raise ConfigError(
                "unsupported metric version {0!r} for tier {1}".format(version, tier),
                code="CONFIG_METRIC_VERSION_UNSUPPORTED",
                field="{0}.{1}".format(field, tier),
                candidates=list(ALLOWED_METRIC_VERSIONS[tier]),
                recovery="Choose one of: {0}.".format(
                    ", ".join(ALLOWED_METRIC_VERSIONS[tier])
                ),
            )
        result[tier] = version
    return result


def _validate_policy(value: object, field: str) -> Policy:
    mapping = expect_mapping(value, field)
    unknown = sorted(set(mapping) - _POLICY_KEYS)
    if unknown:
        raise ConfigError(
            "unknown policy key(s): {0}".format(", ".join(str(item) for item in unknown)),
            field="{0}.{1}".format(field, unknown[0]),
            candidates=sorted(_POLICY_KEYS),
            recovery="Allowed policy keys are require_execution and "
            "unit_low_coverage_percent.",
        )

    require_execution = mapping.get("require_execution", DEFAULT_REQUIRE_EXECUTION)
    if not isinstance(require_execution, bool):
        raise ConfigError(
            "field {0}.require_execution must be a boolean".format(field),
            field="{0}.require_execution".format(field),
            recovery="Use true or false.",
        )

    percent = mapping.get(
        "unit_low_coverage_percent", DEFAULT_UNIT_LOW_COVERAGE_PERCENT
    )
    if isinstance(percent, bool) or not isinstance(percent, (int, float)):
        raise ConfigError(
            "field {0}.unit_low_coverage_percent must be a number".format(field),
            field="{0}.unit_low_coverage_percent".format(field),
            recovery="Use a percentage between 0 and 100, for example 50.",
        )
    if not 0.0 <= float(percent) <= 100.0:
        raise ConfigError(
            "field {0}.unit_low_coverage_percent must be between 0 and 100".format(
                field
            ),
            field="{0}.unit_low_coverage_percent".format(field),
            recovery="Use a percentage between 0 and 100, for example 50.",
        )

    return Policy(
        require_execution=require_execution,
        unit_low_coverage_percent=float(percent),
    )


def load_config(
    repo_root: str, overrides: Optional[CliOverrides] = None
) -> EffectiveConfig:
    """Load, validate, and layer configuration.

    Args:
        repo_root: Absolute path to the repository being reported on.
        overrides: Values supplied on the command line (highest precedence).

    Returns:
        The effective configuration with absolute paths and an origins map.

    Raises:
        ConfigError: On a missing explicit config file, unreadable YAML, an
            unknown key, or a value of the wrong type.
    """
    overrides = overrides or CliOverrides()
    repo_root = os.path.abspath(repo_root)

    origins: Dict[str, str] = {}
    config_path: Optional[str] = None
    raw: Mapping[str, object] = {}

    if overrides.config_path is not None:
        config_path = _resolve_path(repo_root, overrides.config_path)
        raw = _read_config_file(config_path)
        origin = "repo"
    else:
        candidate = os.path.join(repo_root, CONFIG_FILENAME)
        if os.path.isfile(candidate):
            config_path = candidate
            raw = _read_config_file(candidate)
            origin = "repo"
        else:
            origin = "default"

    unknown_keys = sorted(set(raw) - _TOP_LEVEL_KEYS)
    if unknown_keys:
        raise ConfigError(
            "unknown configuration key(s): {0}".format(", ".join(unknown_keys)),
            field=unknown_keys[0],
            candidates=sorted(_TOP_LEVEL_KEYS),
            recovery="Remove the unknown key or use one of the supported keys.",
        )

    if "version" in raw:
        version = _validate_version(raw["version"], "version")
        origins["version"] = origin
    else:
        version = CONFIG_VERSION
        origins["version"] = "default"

    features_file_raw = raw.get("features_file")
    if features_file_raw is not None:
        features_file = _resolve_path(
            repo_root, _expect_string(features_file_raw, "features_file")
        )
        origins["features_file"] = origin
    else:
        features_file = bundled_features_file()
        origins["features_file"] = "default"

    if "enhancement_roots" in raw:
        enhancement_roots = tuple(
            _resolve_path(repo_root, entry)
            for entry in expect_string_list(
                raw["enhancement_roots"], "enhancement_roots"
            )
        )
        origins["enhancement_roots"] = origin
    else:
        enhancement_roots = tuple(
            _resolve_path(repo_root, entry) for entry in DEFAULT_ENHANCEMENT_ROOTS
        )
        origins["enhancement_roots"] = "default"

    artifacts_raw = raw.get("artifacts")
    if artifacts_raw is not None:
        artifacts = expect_mapping(artifacts_raw, "artifacts")
        unknown_artifacts = sorted(set(artifacts) - _ARTIFACT_KEYS)
        if unknown_artifacts:
            raise ConfigError(
                "unknown artifacts key(s): {0}".format(", ".join(unknown_artifacts)),
                field="artifacts.{0}".format(unknown_artifacts[0]),
                candidates=sorted(_ARTIFACT_KEYS),
                recovery="Allowed artifacts keys are coverprofiles and junit.",
            )
        coverprofile_values = expect_string_list(
            artifacts.get("coverprofiles", []), "artifacts.coverprofiles"
        )
        junit_values = expect_string_list(
            artifacts.get("junit", []), "artifacts.junit"
        )
        origins["artifacts"] = origin
    else:
        coverprofile_values = []
        junit_values = []
        origins["artifacts"] = "default"

    # Sort CLI-provided inputs: the order they were typed is not part of the
    # configuration, and the run id must not change when it varies.
    if overrides.coverprofiles:
        coverprofile_values = sorted(overrides.coverprofiles)
        origins["artifacts"] = "cli"
    if overrides.junit:
        junit_values = sorted(overrides.junit)
        origins["artifacts"] = "cli"

    if "metric_versions" in raw:
        metric_versions = dict(DEFAULT_METRIC_VERSIONS)
        metric_versions.update(
            _validate_metric_versions(raw["metric_versions"], "metric_versions")
        )
        origins["metric_versions"] = origin
    else:
        metric_versions = dict(DEFAULT_METRIC_VERSIONS)
        origins["metric_versions"] = "default"

    output_dir_raw = raw.get("output_dir")
    if overrides.output_dir is not None:
        output_dir = _resolve_path(repo_root, overrides.output_dir)
        origins["output_dir"] = "cli"
    elif output_dir_raw is not None:
        output_dir = _resolve_path(
            repo_root, _expect_string(output_dir_raw, "output_dir")
        )
        origins["output_dir"] = origin
    else:
        output_dir = _resolve_path(repo_root, DEFAULT_OUTPUT_DIR)
        origins["output_dir"] = "default"

    if "policy" in raw:
        policy = _validate_policy(raw["policy"], "policy")
        origins["policy"] = origin
    else:
        policy = Policy()
        origins["policy"] = "default"

    return EffectiveConfig(
        version=version,
        features_file=features_file,
        enhancement_roots=enhancement_roots,
        coverprofiles=tuple(
            _resolve_path(repo_root, entry) for entry in coverprofile_values
        ),
        junit=tuple(_resolve_path(repo_root, entry) for entry in junit_values),
        metric_versions={
            tier: metric_versions[tier]
            for tier in sorted(metric_versions)
        },
        output_dir=output_dir,
        origins={key: origins[key] for key in sorted(origins)},
        config_path=config_path,
        policy=policy,
    )
