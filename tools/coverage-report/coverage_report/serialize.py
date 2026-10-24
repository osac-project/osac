"""Canonical JSON serialization.

Every generated JSON document must be reproducible byte-for-byte given the same
inputs and the same ``--now`` value. That means:

* object keys are sorted,
* lists are pre-sorted by the model layer,
* indentation is fixed,
* the file ends with exactly one trailing newline,
* non-ASCII is emitted as-is (files are UTF-8).

``VOLATILE_FIELDS`` names the fields that may differ between two otherwise
identical runs. Keep it to one entry: ``generated_at``.
"""

import hashlib
import json
import os
import tempfile
from typing import Any

#: Report fields allowed to vary between two runs over identical inputs.
VOLATILE_FIELDS = ("generated_at",)

_JSON_INDENT = 2


def canonical_json(value: Any) -> str:
    """Serialize ``value`` deterministically, with a trailing newline."""
    return (
        json.dumps(
            value,
            sort_keys=True,
            indent=_JSON_INDENT,
            ensure_ascii=False,
            separators=(",", ": "),
        )
        + "\n"
    )


def compact_json(value: Any) -> str:
    """Serialize ``value`` deterministically without whitespace.

    Used for equality comparisons (deduplication), where the human-readable
    canonical form is unnecessary.
    """
    return json.dumps(
        value,
        sort_keys=True,
        ensure_ascii=False,
        separators=(",", ":"),
    )


def canonical_digest(value: Any) -> str:
    """Return the sha256 of the canonical JSON form of ``value``."""
    return hashlib.sha256(canonical_json(value).encode("utf-8")).hexdigest()


def file_digest(path: str) -> str:
    """Return the sha256 of a file's bytes."""
    digest = hashlib.sha256()
    with open(path, "rb") as handle:
        for chunk in iter(lambda: handle.read(1024 * 1024), b""):
            digest.update(chunk)
    return digest.hexdigest()


def write_text_atomic(path: str, text: str) -> None:
    """Write ``text`` to ``path`` via a sibling temp file and ``os.replace``."""
    directory = os.path.dirname(os.path.abspath(path))
    os.makedirs(directory, exist_ok=True)
    handle = tempfile.NamedTemporaryFile(
        mode="w",
        encoding="utf-8",
        dir=directory,
        prefix=".{0}.".format(os.path.basename(path)),
        suffix=".tmp",
        delete=False,
    )
    temp_path = handle.name
    try:
        with handle:
            handle.write(text)
            handle.flush()
            os.fsync(handle.fileno())
        os.replace(temp_path, path)
    except BaseException:
        try:
            os.unlink(temp_path)
        except OSError:
            pass
        raise


def write_json(path: str, value: Any) -> None:
    """Serialize ``value`` canonically and write it atomically."""
    write_text_atomic(path, canonical_json(value))
