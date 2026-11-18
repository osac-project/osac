"""Duration parsing and normalization helpers shared across modules."""

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

    # "[D ]HH:MM:SS[.ffffff]" — grace_period may echo a day prefix ("1 00:00:00").
    _HMS_RE = re.compile(r"^(?:(\d+)\s+)?(\d+):([0-5]?\d):([0-5]?\d)(?:\.\d+)?$")
    _NUM_UNIT_RE = re.compile(r"(\d*(?:\.\d+)?)?(\w*)")

    def __new__(cls, value):
        if isinstance(value, str):
            hms = cls._HMS_RE.match(value)
            if hms:
                days, h, m, s = (int(x) if x else 0 for x in hms.groups())
                value = days * 86400 + h * 3600 + m * 60 + s
            else:
                i, u = cls._NUM_UNIT_RE.match(value).groups()
                i = float(i) if i else 1.0
                value = i * (cls.NAMED_UNITS[u] if u else 1)

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


def normalize_duration(api_value, user_value):
    """If api and user duration strings are equivalent, return user_value so diff matches."""
    if _equivalent_duration(api_value, user_value):
        return user_value
    return api_value


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
