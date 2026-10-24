"""Raw reader for ``config/mappings/<KEY>.yaml``.

The mapping collector and the tier-override reader need the same file with the
same resolution rules. This module owns the path convention and the read; each
caller applies its own validation and diagnostics.
"""

import os
from typing import Mapping, Optional, Tuple

import yaml

from .collectors import CollectorContext
from .config import bundled_features_file
from .manifest import relative_path

CONFIG_DIR_NAME = "mappings"


def mapping_config_path(ctx: CollectorContext, feature_key: str) -> Optional[str]:
    """Return the mapping file for a feature, repository first, then bundled.

    ``.yaml`` wins over ``.yml`` in each location; this is the single place the
    extension convention is decided.
    """
    directories = (
        os.path.join(ctx.repo_root, "config", CONFIG_DIR_NAME),
        os.path.join(os.path.dirname(bundled_features_file()), CONFIG_DIR_NAME),
    )
    for directory in directories:
        for extension in (".yaml", ".yml"):
            candidate = os.path.join(directory, feature_key + extension)
            if os.path.isfile(candidate):
                return candidate
    return None


def read_mapping_file(
    ctx: CollectorContext, feature_key: str
) -> Tuple[Optional[Mapping[str, object]], Optional[str], Optional[str]]:
    """Return ``(data, display_path, error)`` for a feature's mapping file.

    ``(None, None, None)`` means there is no mapping file. A file that cannot
    be read, is not valid YAML, or is not a mapping returns a short error the
    caller can turn into its own diagnostic.
    """
    path = mapping_config_path(ctx, feature_key)
    if path is None:
        return None, None, None

    display = relative_path(ctx.repo_root, path)
    try:
        with open(path, "r", encoding="utf-8") as handle:
            data = yaml.safe_load(handle)
    except (OSError, yaml.YAMLError) as error:
        return None, display, "could not read mapping file {0}: {1}".format(
            display, error
        )
    if not isinstance(data, dict):
        return None, display, "mapping file {0} is not a YAML mapping".format(
            display
        )
    return data, display, None
