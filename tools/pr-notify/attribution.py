"""Resolve the effective author for bot-authored PRs.

When a known bot (e.g. redhat-chai-bot) opens a PR on behalf of a human
team member, the PR body contains an attribution line such as:

    @username requested in [Slack thread](...)

This module extracts that human author so the PR appears on the correct
team dashboard.
"""

import re

_BOT_AUTHORS: frozenset[str] = frozenset({"redhat-chai-bot"})

_ATTRIBUTION_RE = re.compile(
    r"^@([a-zA-Z\d][\w-]*)\s+requested\s+(?:in\b|via\b)",
    re.MULTILINE,
)


def resolve_effective_author(author: str, body: str) -> str:
    """Return the human requester if *author* is a known bot, else *author*.

    Args:
        author: The GitHub login that opened the PR.
        body: The PR description text.

    Returns:
        The attributed human login when a match is found; otherwise
        the original *author* unchanged.
    """
    if author.lower() not in _BOT_AUTHORS:
        return author
    match = _ATTRIBUTION_RE.search(body)
    if match:
        return match.group(1)
    return author
