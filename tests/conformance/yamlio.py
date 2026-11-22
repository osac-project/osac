"""Strict YAML loading shared by the configuration and catalog readers."""

from __future__ import annotations

from typing import Any

import yaml

from tests.conformance.errors import ConfigError


class _UniqueKeyLoader(yaml.SafeLoader):
    """SafeLoader that refuses duplicate mapping keys instead of keeping the last one."""


def _construct_mapping(loader: yaml.SafeLoader, node: yaml.MappingNode, deep: bool = False) -> dict[Any, Any]:
    mapping: dict[Any, Any] = {}
    for key_node, value_node in node.value:
        key = loader.construct_object(key_node, deep=deep)
        try:
            duplicated = key in mapping
        except TypeError:
            raise yaml.constructor.ConstructorError(
                "while constructing a mapping", node.start_mark, f"found unhashable key {key!r}", key_node.start_mark
            ) from None
        if duplicated:
            raise yaml.constructor.ConstructorError(
                "while constructing a mapping", node.start_mark, f"found duplicate key {key!r}", key_node.start_mark
            )
        mapping[key] = loader.construct_object(value_node, deep=deep)
    return mapping


_UniqueKeyLoader.add_constructor(yaml.resolver.BaseResolver.DEFAULT_MAPPING_TAG, _construct_mapping)


def loads(text: str, source: str) -> object:
    """Parse one YAML document from ``text``, rejecting duplicate mapping keys."""
    try:
        return yaml.load(text, Loader=_UniqueKeyLoader)
    except yaml.YAMLError as exc:
        raise ConfigError([f"{source} is not valid YAML: {exc}"]) from None
