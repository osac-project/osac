"""Reviewer identity validation.

A mapping file's ``reviewed_by`` field decides whether its entries count as
declared evidence. The check must fail closed: a value is reviewed only when it
is clearly a GitHub user handle (``@name``) or a team (``org/team``, with or
without ``@``). A bare user name is not a handle. Placeholders and boolean-like
words are unreviewed regardless of case or separator style.
"""

import re

#: ``@user`` or ``org/team`` / ``@org/team``.
REVIEWER_PATTERN = re.compile(
    r"^(?:"
    r"@[A-Za-z0-9](?:[A-Za-z0-9-]{0,38})"
    r"|@?[A-Za-z0-9](?:[A-Za-z0-9-]{0,38})/[A-Za-z0-9][A-Za-z0-9._-]*"
    r")$"
)

#: Compacted markers that mean "nobody has reviewed this yet".
PLACEHOLDERS = frozenset(
    {
        "pendingreview",
        "pending",
        "todo",
        "tbd",
        "unreviewed",
        "none",
        "n/a",
        "na",
        "true",
        "false",
        "yes",
        "no",
    }
)

_COMPACT_RE = re.compile(r"[-_\s]")


def compact_reviewer(value: str) -> str:
    """Return the placeholder-comparison form of a reviewer value."""
    return _COMPACT_RE.sub("", value).casefold()


def is_placeholder(value: object) -> bool:
    """Return whether the value is a recognized not-reviewed marker."""
    return isinstance(value, str) and compact_reviewer(value) in PLACEHOLDERS


def is_reviewed(value: object) -> bool:
    """Return whether ``value`` names a real reviewer.

    Only a non-empty string matching :data:`REVIEWER_PATTERN` that is not a
    placeholder counts.
    """
    if not isinstance(value, str):
        return False
    text = value.strip()
    if not text or is_placeholder(text):
        return False
    return REVIEWER_PATTERN.match(text) is not None


def is_invalid_reviewer(value: object) -> bool:
    """Return whether a present value was meant as a reviewer but cannot be one.

    ``None`` (field absent) and recognized placeholders are not invalid; a list,
    a boolean, or malformed prose is.
    """
    if value is None or is_placeholder(value):
        return False
    if not isinstance(value, str):
        return True
    text = value.strip()
    if not text:
        return False
    return REVIEWER_PATTERN.match(text) is None
