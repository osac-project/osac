"""VAST product version validation for module compatibility."""

from typing import Optional, Tuple

from ansible.module_utils.basic import AnsibleModule

from .client import VastClient
from .errors import VastAPIError


class VersionAwareMixin:
    """Shared cluster-version gating for resource base classes.

    Owns the multi-version metadata attributes plus the parse/enforce logic that
    would otherwise be duplicated between ``BaseResource`` and
    ``SubEndpointResource``. Subclasses set ``self.module`` before calling
    ``_set_cluster_mm``/``_enforce_version_compatibility`` and override
    ``_version_entity`` to label themselves in error messages.
    """

    # --- multi-version metadata (auto-generated per subclass) ---------------
    resource_min_version: Optional[Tuple[int, int]] = None
    resource_max_version: Optional[Tuple[int, int]] = None
    # Oldest VMS version these modules were generated for. field_versions is
    # relative to it, so a cluster below it cannot be served correctly.
    generated_min_version: Optional[Tuple[int, int]] = None
    # Live cluster (major, minor); set against a real cluster during __init__.
    cluster_mm: Optional[Tuple[int, int]] = None

    # Supported VMS product version band, shared by all base classes.
    _MIN_VERSION: Tuple[int, int, int] = (5, 4, 0)
    _MAX_VERSION: Optional[Tuple[int, int, int]] = (5, 6, 0)

    def _init_vast_connection(self, module: "AnsibleModule") -> None:
        """Shared setup for all resource base classes.

        Validates auth, builds the client, validates and captures the cluster
        version, and enforces resource/field version compatibility. Sets
        ``module``, ``params``, ``check_mode``, ``client``, ``cluster_version``
        and ``cluster_mm`` on the instance.
        """
        # Imported here to avoid a circular import (auth/client import version).
        from .auth import build_connection, validate_auth
        from .client import VastClient

        self.module = module
        self.params = module.params
        self.check_mode = module.check_mode

        try:
            validate_auth(self.params)
        except Exception as e:
            module.fail_json(msg=str(e))

        conn = build_connection(self.params)
        try:
            self.client = VastClient(conn)
        except RuntimeError as e:
            module.fail_json(msg=str(e))

        self.client.debug = conn.debug

        product_version = ensure_supported_version(
            module, self.client, min_version=self._MIN_VERSION, max_version=self._MAX_VERSION
        )
        self.cluster_version = product_version
        self._set_cluster_mm(product_version)
        self._enforce_version_compatibility()

    @staticmethod
    def _fmt_mm(mm: Optional[Tuple[int, int]]) -> str:
        """Format a (major, minor) tuple as 'X.Y'."""
        return f"{mm[0]}.{mm[1]}" if mm else "unknown"

    def _version_entity(self) -> str:
        """Human-readable label for this entity in version error messages."""
        return "Resource"

    def _set_cluster_mm(self, product_version: str) -> None:
        """Capture the cluster's (major, minor) from a product version string."""
        try:
            parts = parse_version(product_version)
            self.cluster_mm = (parts[0], parts[1])
        except ValueError:
            self.cluster_mm = None

    def _enforce_version_compatibility(self) -> None:
        """Fail the module if the cluster is incompatible with these modules."""
        if self.cluster_mm is None:
            return

        # Self-describing generation floor: field_versions is relative to it, so
        # a cluster below it would have universal fields silently assumed present.
        gen_min = self.generated_min_version
        if gen_min and self.cluster_mm < tuple(gen_min):
            self.module.fail_json(
                msg=(
                    f"These modules were generated for VMS {self._fmt_mm(gen_min)} or later, "
                    f"but the cluster is {self._fmt_mm(self.cluster_mm)}. Regenerate the collection "
                    f"including the swagger for VMS {self._fmt_mm(self.cluster_mm)} "
                    f"(tools/generate_from_swagger.py --swagger ...) to support this version."
                )
            )

        entity = self._version_entity()
        rmin = self.resource_min_version
        if rmin and self.cluster_mm < tuple(rmin):
            self.module.fail_json(
                msg=(
                    f"{entity} was introduced in VMS {self._fmt_mm(rmin)}, " f"but the cluster is {self._fmt_mm(self.cluster_mm)}."
                )
            )

        rmax = self.resource_max_version
        if rmax and self.cluster_mm > tuple(rmax):
            self.module.fail_json(
                msg=(
                    f"{entity} was removed after VMS {self._fmt_mm(rmax)}, " f"but the cluster is {self._fmt_mm(self.cluster_mm)}."
                )
            )


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
    version_str: str, min_version: Tuple[int, int, int] = (5, 4, 0), max_version: Optional[Tuple[int, int, int]] = (5, 6, 0)
) -> Tuple[bool, str]:
    """
    Check if a product version is within the supported range.

    Args:
        version_str: Version string to check (e.g., '5.4.0')
        min_version: Minimum supported version (inclusive), default (5, 4, 0)
        max_version: Maximum supported version (exclusive), default (5, 6, 0) covering the
                     5.4.x and 5.5.x series. If None, no upper bound is enforced.

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
    max_version: Optional[Tuple[int, int, int]] = (5, 6, 0),
) -> str:
    """
    Validate that the target VAST product version is supported.

    This should be called early in module execution (before making changes).
    Fails the module with a clear error message if the version is unsupported.

    Args:
        module: AnsibleModule instance
        client: VastClient instance
        min_version: Minimum supported version (inclusive), default (5, 4, 0)
        max_version: Maximum supported version (exclusive), default (5, 6, 0).
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

    # Greppable detection log. Gated on debug so production runs stay quiet but
    # the per-task version fetch is observable when troubleshooting.
    if getattr(client, "debug", False):
        module.warn(f"[VAST] Detected VMS version: {product_version}")

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
