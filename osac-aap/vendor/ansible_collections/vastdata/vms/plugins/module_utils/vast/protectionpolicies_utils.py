"""Helpers for the ``protectionpolicies`` Ansible module."""

import re


class Duration(float):
    NAMED_UNITS = dict(
        ms=1 / 1000,
        s=1,
        S=1,
        m=60,
        h=60 * 60,
        H=60 * 60,
        d=24 * 60 * 60,
        D=24 * 60 * 60,
        w=7 * 24 * 60 * 60,
        W=7 * 24 * 60 * 60,
        M=30 * 24 * 60 * 60,
        y=365 * 24 * 60 * 60,
        Y=365 * 24 * 60 * 60,
    )

    def __new__(cls, value):
        if isinstance(value, str):
            try:
                i, u = float(value), None
            except ValueError:
                i, u = re.match(r"(\d*(?:\.\d+)?)?(\w*)", value).groups()
                i = 1.0 if not i else float(i)
            value = i * (cls.NAMED_UNITS[u] if u else 1)
            if isinstance(value, cls):
                return value

        return super().__new__(cls, value)


_FRAME_DURATION_KEYS = {"every", "keep-local", "keep-remote"}


def _equivalent_duration(a, b):
    """Return True iff two duration strings represent the same length of time."""
    if not isinstance(a, str) or not isinstance(b, str):
        return False
    try:
        return Duration(a) == Duration(b)
    except (KeyError, ValueError, AttributeError):
        return False


def normalize_frames(api_value, user_value):
    """Normalize API frames against user frames so equivalent durations don't diff."""
    if api_value is None or user_value is None:
        return api_value
    if not isinstance(api_value, list) or not isinstance(user_value, list):
        return api_value
    if len(api_value) != len(user_value):
        return api_value

    normalized = []
    for api_frame, user_frame in zip(api_value, user_value):
        if not isinstance(api_frame, dict) or not isinstance(user_frame, dict):
            normalized.append(api_frame)
            continue

        new_frame = {}
        for key, user_v in user_frame.items():
            if key not in api_frame:
                continue
            api_v = api_frame[key]
            if key in _FRAME_DURATION_KEYS and _equivalent_duration(api_v, user_v):
                new_frame[key] = user_v
            else:
                new_frame[key] = api_v
        normalized.append(new_frame)

    return normalized
