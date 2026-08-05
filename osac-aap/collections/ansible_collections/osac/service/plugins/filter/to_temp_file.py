#!/usr/bin/python3

import tempfile
import os
from typing import Optional


def to_temp_file(
    content: str,
    suffix: Optional[str] = None,
    prefix: Optional[str] = None,
    dir: Optional[str] = None,
) -> str:
    """
    Create a temporary file with the given content and return its path.

    Args:
        content: The content to write to the temporary file
        suffix: Optional suffix for the temporary file (e.g., '.yaml', '.json')
        prefix: Optional prefix for the temporary file
        dir: Optional directory to create the temp file in

    Returns:
        str: The path to the created temporary file

    Example:
        {{ content | to_temp_file(suffix='.yaml') }}
    """
    # Create a temporary file
    fd, path = tempfile.mkstemp(suffix=suffix, prefix=prefix, dir=dir, text=True)

    with os.fdopen(fd, "w") as tmp_file:
        tmp_file.write(content)

    return path


class FilterModule(object):
    def filters(self):
        return {
            "to_temp_file": to_temp_file,
        }
