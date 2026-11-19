"""VAST product version validation for module compatibility."""

from typing import Optional, Tuple

from ansible.module_utils.basic import AnsibleModule

from .client import VastClient
from .errors import VastAPIError


def parse_version(version_str: str) -> Tuple[int, int, int]:
    """
    Parse a version string like '5.4.0' or '5.4.0-123' into (major, minor, patch).

    Raises ValueError if the version string is invalid.
    """
    try:
        # Strip any build metadata or pre-release info (e.g., '5.4.0-123' -> '5.4.0')
        base_version = version_str.split("-")[0].split("+")[0]
        parts = base_version.split(".")
        if len(parts) < 2:
            raise ValueError("Version must have at least major.minor")
        major = int(parts[0])
        minor = int(parts[1])
        patch = int(parts[2]) if len(parts) >= 3 else 0
        return (major, minor, patch)
    except (ValueError, IndexError, AttributeError) as e:
        raise ValueError(f"Invalid version string {version_str!r}: {e}") from e


def get_product_version(client: VastClient) -> str:
    """
    Query the VAST product version from the API.

    Returns version string (e.g., '5.4.0.20.10960402906660116571').
    Raises VastAPIError if the version cannot be retrieved.
    """
    try:
        clusters = client.api.clusters.get()
        if not clusters:
            raise VastAPIError("Failed to retrieve cluster information: clusters.get() returned empty or invalid data")

        sw_version = clusters[0].get("sw_version")
        if not sw_version:
            raise VastAPIError("Failed to retrieve product version: sw_version field not found in cluster data")

        return sw_version

    except VastAPIError:
        raise
    except Exception as e:
        raise VastAPIError(f"Failed to retrieve product version: {e}") from e


def is_version_supported(
    version_str: str, min_version: Tuple[int, int, int] = (5, 4, 0), max_version: Optional[Tuple[int, int, int]] = (5, 5, 0)
) -> Tuple[bool, str]:
    """
    Check if a product version is within the supported range.

    Args:
        version_str: Version string to check (e.g., '5.4.0')
        min_version: Minimum supported version (inclusive), default (5, 4, 0)
        max_version: Maximum supported version (exclusive), default (5, 5, 0) for 5.4.x series.
                     If None, no upper bound is enforced.

    Returns:
        Tuple of (is_supported, reason_message)
    """
    try:
        version = parse_version(version_str)
    except ValueError as e:
        return (False, f"Invalid version format: {e}")

    if version < min_version:
        min_str = ".".join(map(str, min_version))
        return (False, f"Version {version_str} is below minimum supported version {min_str}")

    if max_version is not None and version >= max_version:
        max_str = ".".join(map(str, max_version))
        return (
            False,
            f"Version {version_str} is at or above unsupported version {max_str} "
            f"(supports up to {max_version[0]}.{max_version[1] - 1}.x)",
        )

    return (True, "")


def ensure_supported_version(
    module: AnsibleModule,
    client: VastClient,
    min_version: Tuple[int, int, int] = (5, 4, 0),
    max_version: Optional[Tuple[int, int, int]] = (5, 5, 0),
) -> str:
    """
    Validate that the target VAST product version is supported.

    This should be called early in module execution (before making changes).
    Fails the module with a clear error message if the version is unsupported.

    Args:
        module: AnsibleModule instance
        client: VastClient instance
        min_version: Minimum supported version (inclusive), default (5, 4, 0)
        max_version: Maximum supported version (exclusive), default (5, 5, 0).
                     Set to None to disable upper bound checking.

    Returns:
        The detected product version string (if supported).
    """
    try:
        product_version = get_product_version(client)
    except VastAPIError as e:
        module.fail_json(
            msg="Failed to validate product version compatibility",
            details=str(e),
        )

    supported, reason = is_version_supported(product_version, min_version, max_version)

    if not supported:
        min_str = ".".join(map(str, min_version))
        if max_version:
            max_ver = (max_version[0], max_version[1] - 1)
            support_range = f"{min_str[:3]}.x (up to {max_ver[0]}.{max_ver[1]}.x)"
        else:
            support_range = f"{min_str} and later"

        module.fail_json(
            msg="VAST product version not supported",
            detected_version=product_version,
            supported_versions=support_range,
            reason=reason,
        )

    return product_version
