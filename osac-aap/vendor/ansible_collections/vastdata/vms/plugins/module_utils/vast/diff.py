"""Compute minimal patch between current and desired state.

Provides functions for normalizing resources and computing patches,
with support for read-only field filtering and set-like list comparison.
"""

from typing import Any, Dict, Optional


def normalize_value(value: Any, set_like: bool = False) -> Any:
    """Normalize a value for comparison.

    Args:
        value: The value to normalize.
        set_like: If True, treat lists as sets (sort and dedupe).

    Returns:
        Normalized value.
    """
    if value is None:
        return None

    if set_like and isinstance(value, list):
        # Sort and dedupe for order-insensitive comparison
        try:
            return sorted(set(value))
        except TypeError:
            # Items not sortable, just dedupe
            seen = []
            for item in value:
                if item not in seen:
                    seen.append(item)
            return seen

    return value


def normalize_resource(
    resource: Dict[str, Any],
    overrides: Dict[str, Any],
    exclude_immutable: bool = False,
    include_ephemeral: bool = False,
    user_resource: Optional[Dict[str, Any]] = None,
) -> Dict[str, Any]:
    """Normalize a resource for comparison.

    Excludes read-only, optionally ephemeral, and optionally immutable fields;
    normalizes set-like lists and applies field-specific normalizers.

    Args:
        resource: The resource dictionary to normalize.
        overrides: Schema overrides containing read_only_fields, ephemeral_fields,
                   immutable_fields, set_like_lists, and field_normalizers.
        exclude_immutable: If True, also exclude immutable fields (for update diffs).
        include_ephemeral: If True, keep ephemeral fields (e.g. password) in the result.
        user_resource: Optional user-provided resource (desired state) to pass to
                       field normalizers. Normalizers can use this to extract schema
                       from user input.

    Returns:
        Normalized resource dictionary.
    """
    if not resource:
        return {}

    read_only = overrides.get("read_only_fields", set())
    ephemeral = overrides.get("ephemeral_fields", set())
    immutable = overrides.get("immutable_fields", set()) if exclude_immutable else set()
    set_like = overrides.get("set_like_lists", set())
    field_normalizers = overrides.get("field_normalizers", {})

    result = {}
    for key, value in resource.items():
        # Skip read-only fields
        if key in read_only:
            continue

        # Skip ephemeral fields unless include_ephemeral (current state never has them; desired keeps them for patch)
        if not include_ephemeral and key in ephemeral:
            continue

        # Skip immutable fields when computing update diffs
        if key in immutable:
            continue

        # Apply field-specific normalizer if defined
        if key in field_normalizers:
            # Pass both API value and user value to normalizer
            user_value = user_resource.get(key) if user_resource else None
            value = field_normalizers[key](value, user_value)

        # Normalize the value
        is_set_like = key in set_like
        result[key] = normalize_value(value, set_like=is_set_like)

    return result


def values_equal(current_val: Any, desired_val: Any, set_like: bool = False) -> bool:
    """Compare two values for equality.

    Args:
        current_val: Current value.
        desired_val: Desired value.
        set_like: If True, compare lists as sets.

    Returns:
        True if values are equal.
    """
    if current_val is None and desired_val is None:
        return True

    # Some APIs omit boolean fields when their value is False (absent == False).
    # Treat a missing field (None) as equal to False to avoid spurious patches.
    if desired_val is False and current_val is None:
        return True

    if set_like and isinstance(current_val, list) and isinstance(desired_val, list):
        # Compare as sets
        try:
            return set(current_val) == set(desired_val)
        except TypeError:
            # Items not hashable, fall back to sorted comparison
            pass

    return current_val == desired_val


def compute_patch(
    current: Dict[str, Any],
    desired: Dict[str, Any],
    overrides: Optional[Dict[str, Any]] = None,
) -> Dict[str, Any]:
    """Compute minimal dict patch from current to desired.

    Args:
        current: Current resource state (should be normalized to exclude read-only/ephemeral fields).
        desired: Desired resource state (should be normalized to exclude read-only/ephemeral fields).
        overrides: Schema overrides for set-like lists. If None, uses simple comparison.

    Returns:
        Dict suitable for PATCH request, containing only changed values.

    Notes:
        - Only includes keys present in desired.
        - Treats None as "not provided" (omit from patch).
        - Respects set_like_lists from overrides for order-insensitive comparison.
        - Ephemeral fields (e.g. passwords) are excluded from patches to maintain idempotency,
          since they are never returned by the API and cannot be verified for changes.
    """
    if overrides is None:
        overrides = {}

    set_like_lists = overrides.get("set_like_lists", set())

    patch: Dict[str, Any] = {}
    for key, desired_val in desired.items():
        if desired_val is None:
            continue

        current_val = current.get(key)
        is_set_like = key in set_like_lists

        if not values_equal(current_val, desired_val, set_like=is_set_like):
            patch[key] = desired_val

    return patch


def has_changes(
    current: Dict[str, Any],
    desired: Dict[str, Any],
    overrides: Optional[Dict[str, Any]] = None,
) -> bool:
    """Check if there are any changes between current and desired state.

    Args:
        current: Current resource state.
        desired: Desired resource state.
        overrides: Schema overrides.

    Returns:
        True if there are changes.
    """
    return bool(compute_patch(current, desired, overrides))
