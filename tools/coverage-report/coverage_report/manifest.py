"""Run manifest construction.

The manifest explains how a report was produced: which revision, which
configuration layer supplied each value, which files were hashed, and which
collectors ran. It is the audit record. Input file paths are recorded relative
to the repository when they live underneath it, so two checkouts of the same
revision can be compared directly; resolved configuration values keep their
absolute form because that is exactly what the run used.

Only ``generated_at`` is volatile (see :data:`coverage_report.serialize.VOLATILE_FIELDS`).
Collector status is recorded as a status string, never a measured duration.
"""

import os
from typing import Dict, List, Mapping, Optional, Sequence, Tuple

MANIFEST_SCHEMA_VERSION = "run-manifest/v1"


def relative_path(repo_root: str, path: str) -> str:
    """Return ``path`` relative to ``repo_root`` when it lives underneath it.

    Paths outside the repository stay absolute, since a relative form would be
    misleading.
    """
    absolute = os.path.abspath(path)
    root = os.path.abspath(repo_root)
    try:
        common = os.path.commonpath([absolute, root])
    except ValueError:
        return absolute
    if common != root:
        return absolute
    return os.path.relpath(absolute, root).replace(os.sep, "/")


def build_manifest(
    *,
    run_id: str,
    tool_version: str,
    generated_at: str,
    repo_root: str,
    sha: str,
    sha_source: str,
    feature_resolution: Mapping[str, object],
    effective_config: Mapping[str, object],
    config_origins: Mapping[str, str],
    config_file: Optional[str],
    config_digest: Optional[str],
    features_file: str,
    features_digest: str,
    input_digests: Sequence[Mapping[str, object]],
    collectors: Sequence[Mapping[str, object]],
    metric_versions: Mapping[str, str],
    command_line: Sequence[str],
    inventories: Sequence[Mapping[str, object]] = (),
    previous_file: Optional[str] = None,
    previous_digest: Optional[str] = None,
) -> Dict[str, object]:
    """Assemble the manifest dictionary in its stable shape."""
    manifest: Dict[str, object] = {
        "schema_version": MANIFEST_SCHEMA_VERSION,
        "run_id": run_id,
        "tool_version": tool_version,
        "generated_at": generated_at,
        "repo": {"sha": sha, "sha_source": sha_source},
        "feature_resolution": {
            key: feature_resolution[key]
            for key in ("input", "matched_by", "features")
        },
        "effective_config": dict(effective_config),
        "config_origins": {
            key: config_origins[key] for key in sorted(config_origins)
        },
        "config_file": (
            {
                "path": relative_path(repo_root, config_file),
                "sha256": config_digest,
            }
            if config_file is not None
            else {"path": None, "sha256": None}
        ),
        "features_file": {
            "path": relative_path(repo_root, features_file),
            "sha256": features_digest,
        },
        "input_digests": [dict(entry) for entry in input_digests],
        "collectors": [dict(entry) for entry in collectors],
        "metric_versions": {
            tier: metric_versions[tier] for tier in sorted(metric_versions)
        },
        "inventories": [dict(entry) for entry in inventories],
        "command_line": list(command_line),
    }
    if previous_file is not None:
        manifest["previous_report"] = {
            "path": relative_path(repo_root, previous_file),
            "sha256": previous_digest,
        }
    return manifest


def collector_status(
    name: str, status: str, *, diagnostic_codes: Sequence[str] = ()
) -> Dict[str, object]:
    """Build one collector status entry.

    Args:
        name: Collector name.
        status: ``ok`` or ``error``.
        diagnostic_codes: Diagnostic codes the collector emitted, sorted.
    """
    return {
        "name": name,
        "status": status,
        "diagnostic_codes": sorted(diagnostic_codes),
    }


def input_digest(
    *, kind: str, repo_root: str, path: str, sha256: str, size_bytes: int
) -> Dict[str, object]:
    """Build one input digest entry with a repository-relative path."""
    return {
        "kind": kind,
        "path": relative_path(repo_root, path),
        "sha256": sha256,
        "size_bytes": size_bytes,
    }


def sorted_input_digests(
    entries: Sequence[Mapping[str, object]]
) -> List[Dict[str, object]]:
    """Sort input digests by kind then path for deterministic output."""
    return sorted(
        (dict(entry) for entry in entries),
        key=lambda entry: (str(entry["kind"]), str(entry["path"])),
    )


def command_line(argv: Sequence[str]) -> Tuple[str, ...]:
    """Normalize argv for the manifest."""
    return tuple(str(argument) for argument in argv)
