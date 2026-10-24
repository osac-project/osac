"""Artifact discovery shared by collectors.

The rule is the same for every artifact kind: explicit CLI paths win over the
configured globs, explicit paths are returned even when they do not exist so
the owning collector can report an unreadable input, and the result is sorted
so filesystem enumeration order cannot change the report.
"""

import glob
import os
from typing import Sequence, Tuple


def discover_artifacts(
    explicit: Sequence[str], patterns: Sequence[str]
) -> Tuple[str, ...]:
    """Return the artifact paths to read.

    Args:
        explicit: Paths passed on the command line. They win over the globs and
            are kept even when missing so the collector can report them.
        patterns: Configured globs, used only when no explicit path was given.
    """
    if explicit:
        return tuple(sorted(os.path.abspath(path) for path in explicit))

    matches = set()
    for pattern in patterns:
        for candidate in glob.glob(pattern, recursive=True):
            if os.path.isfile(candidate):
                matches.add(os.path.abspath(candidate))
    return tuple(sorted(matches))
