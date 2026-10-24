"""Shared reader for ``<artifact>.meta.json`` provenance sidecars.

Collectors use the same file convention and only differ in how they report a
problem, so the read and shape check live here.
"""

import json
import os
from typing import Mapping, Optional, Tuple


def read_sidecar(path: str) -> Tuple[Optional[Mapping[str, object]], Optional[str]]:
    """Return ``(data, error)`` for ``<path>.meta.json``.

    ``(None, None)`` means there is no sidecar. A sidecar that cannot be read,
    is not JSON, or is not a JSON object returns ``(None, reason)``; the reason
    is a short phrase the caller prefixes with its own artifact context.
    """
    sidecar = path + ".meta.json"
    if not os.path.isfile(sidecar):
        return None, None
    try:
        with open(sidecar, "r", encoding="utf-8") as handle:
            data = json.load(handle)
    except (OSError, ValueError) as error:
        return None, "could not read sidecar metadata: {0}".format(error)
    if not isinstance(data, dict):
        return None, "sidecar metadata must be a JSON object"
    return data, None
