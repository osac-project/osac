# Copyright: (c) 2026, VAST Data
# Apache License 2.0 (see LICENSE or https://www.apache.org/licenses/LICENSE-2.0)
# SPDX-License-Identifier: Apache-2.0
"""Best-effort type coercions shared across wire transforms and field normalizers."""

from typing import Any


def to_string(value: Any) -> Any:
    """Coerce ``value`` to ``str``; ``None`` stays ``None``."""
    try:
        return value if value is None else str(value)
    except (TypeError, ValueError):
        return value


def to_int(value: Any) -> Any:
    """Coerce ``value`` to ``int`` (e.g. API int64 echoed as JSON string).

    Non-numeric input is returned unchanged. ``None`` stays ``None``.
    """
    try:
        return value if value is None else int(value)
    except (TypeError, ValueError):
        return value


def to_float(value: Any) -> Any:
    """Coerce ``value`` to ``float``; ``None`` stays ``None``."""
    try:
        return value if value is None else float(value)
    except (TypeError, ValueError):
        return value


def to_list(value: Any) -> Any:
    """Wrap a scalar in a one-element list; lists and ``None`` pass through."""
    if value is None or isinstance(value, list):
        return value
    return [value]


def first_of_list(value: Any) -> Any:
    """Return the first element of a list, or ``value`` unchanged if not a list."""
    if isinstance(value, list):
        return value[0] if value else None
    return value


_COERCERS = {
    "to_string": to_string,
    "to_int": to_int,
    "to_float": to_float,
    "to_list": to_list,
    "first_of_list": first_of_list,
}


def coerce(value: Any, fn: str) -> Any:
    """Apply a named coercion (``to_int``, ``to_string``, …). Unknown names are a no-op."""
    coercer = _COERCERS.get(fn)
    if coercer is None:
        return value
    return coercer(value)
