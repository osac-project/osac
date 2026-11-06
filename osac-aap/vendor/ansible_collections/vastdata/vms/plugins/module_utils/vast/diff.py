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


def flatten_subresources(resource: Dict[str, Any], keys: Any) -> Dict[str, Any]:
    """Lift fields from nested sub-objects to the top level.

    This is the canonical mechanism for the case where the API nests a group
    of *sibling* writable fields under a wrapper object (e.g. AD returns
    LDAP-delegated fields under ``ldap``) while the user supplies them flat at
    the top level. Configure it declaratively via the ``flatten_subresources``
    schema override.

    It is distinct from the ``entity`` handling in
    ``schema_overrides.normalize_list_by_user_schema``: that reconciles an
    ``entity`` sub-object *inside list items* and keeps only user-schema keys
    with a conflict override, whereas this promotes a whole sub-object's keys
    to top-level siblings. Do not add a third flattening idiom — extend one of
    these.

    For each key in ``keys`` that maps to a dict in ``resource``, copy its
    entries onto ``resource`` at the top level (without overwriting existing
    top-level values) and drop the original nested key.

    Args:
        resource: The resource dictionary (mutated in place and returned).
        keys: Iterable of sub-object keys to flatten.

    Returns:
        The same resource dict with sub-objects flattened.
    """
    if not resource or not keys:
        return resource
    for key in keys:
        nested = resource.get(key)
        if not isinstance(nested, dict):
            continue
        for inner_key, inner_value in nested.items():
            if resource.get(inner_key) is None:
                resource[inner_key] = inner_value
        resource.pop(key, None)
    return resource


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

    flatten_keys = overrides.get("flatten_subresources", set())
    if flatten_keys:
        resource = flatten_subresources(dict(resource), flatten_keys)

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


def values_equal(
    current_val: Any,
    desired_val: Any,
    set_like: bool = False,
    exact_dict: bool = False,
) -> bool:
    """Compare a current API value against a desired (user-supplied) value.

    The comparison is asymmetric by design: the function answers "does the
    current state already satisfy what the user asked for?", not "are these
    two values structurally identical". Specifically:

    - For non-empty dict-vs-dict comparisons, ``desired_val`` is treated as a
      partial spec: every key in ``desired_val`` must match the corresponding
      key in ``current_val``, but extra keys present in ``current_val`` (e.g.
      server-injected defaults) are ignored. This avoids spurious patches when
      the API echoes additional fields the user did not specify.
    - When ``exact_dict`` is True, dict comparison uses strict equality instead
      (replace semantics). Use this for maps where omitting a key means removal
      (e.g. webhook ``headers``).
    - An empty ``desired_val`` dict bypasses the subset path and falls back to
      strict equality.
    - ``None`` on the desired side and ``False`` on the desired side with
      ``None`` on the current side are both treated as equal, matching APIs
      that omit booleans when they are ``False``.

    Args:
        current_val: Current value as returned by the API.
        desired_val: Desired value as supplied by the user.
        set_like: If True, compare lists as sets (only at the top level —
            this flag is intentionally not propagated into nested-dict
            recursion).
        exact_dict: If True, compare dicts with ``==`` instead of subset match.

    Returns:
        True if ``current_val`` already satisfies ``desired_val``.
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

    if isinstance(current_val, dict) and isinstance(desired_val, dict) and desired_val:
        if exact_dict:
            return current_val == desired_val
        # Subset comparison for nested dicts (API may return extra default keys).
        return all(values_equal(current_val.get(k), v) for k, v in desired_val.items())

    return current_val == desired_val


def compute_patch(
    current: Dict[str, Any],
    desired: Dict[str, Any],
    overrides: Optional[Dict[str, Any]] = None,
    clear_fields: Optional[Any] = None,
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
        - Treats None as "not provided" (omit from patch), EXCEPT for keys in
          clear_fields, which are emitted as null to reset a currently-set value.
        - Respects set_like_lists from overrides for order-insensitive comparison.
        - Respects exact_dict_fields from overrides for replace-semantics dicts.
        - For fields in renamed_on_response, reads current under the response
          name but keys the patch by the request name
        - Ephemeral fields (e.g. passwords) are excluded from patches to maintain idempotency,
          since they are never returned by the API and cannot be verified for changes.
    """
    if overrides is None:
        overrides = {}

    set_like_lists = overrides.get("set_like_lists", set())
    exact_dict_fields = overrides.get("exact_dict_fields", set())
    renamed_on_response = overrides.get("renamed_on_response", {})
    clear_fields = set(clear_fields or ())

    patch: Dict[str, Any] = {}
    for key, desired_val in desired.items():
        if desired_val is None:
            # Explicit clear: emit null only when the field currently has a value
            # (keeps the operation idempotent when it is already unset).
            if key in clear_fields:
                response_field = renamed_on_response.get(key, key)
                if current.get(response_field) is not None:
                    patch[key] = None
            continue

        response_field = renamed_on_response.get(key, key)
        current_val = current.get(response_field)
        is_set_like = key in set_like_lists or response_field in set_like_lists
        is_exact_dict = key in exact_dict_fields or response_field in exact_dict_fields

        if not values_equal(current_val, desired_val, set_like=is_set_like, exact_dict=is_exact_dict):
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
