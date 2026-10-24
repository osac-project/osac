"""Minimal JSON Schema checker used by the coverage reporter tests.

``jsonschema`` is not a dependency of this tool, so the test suite validates
generated documents with this focused subset of draft 2020-12:

* ``type`` (single or list), including ``integer``/``number`` distinction,
* ``required``, ``properties``, ``additionalProperties`` (bool or schema),
* ``items``, ``minItems``, ``maxItems``,
* ``enum``, ``const``,
* ``minLength``, ``pattern``,
* ``minimum``, ``maximum``,
* local ``$ref`` to ``#/$defs/<name>``.

It deliberately does not implement composition keywords. The schemas in
``schemas/`` are written to stay inside this subset.
"""

import re
from typing import Any, Dict, List, Optional, Sequence

_TYPE_MAP = {
    "object": dict,
    "array": list,
    "string": str,
    "integer": int,
    "number": (int, float),
    "boolean": bool,
    "null": type(None),
}


def _matches_type(instance: Any, expected: str) -> bool:
    if expected == "integer":
        return isinstance(instance, int) and not isinstance(instance, bool)
    if expected == "number":
        return isinstance(instance, (int, float)) and not isinstance(instance, bool)
    if expected == "boolean":
        return isinstance(instance, bool)
    expected_type = _TYPE_MAP.get(expected)
    if expected_type is None:
        raise ValueError("unsupported schema type: {0}".format(expected))
    if expected == "object":
        return isinstance(instance, dict)
    if expected == "array":
        return isinstance(instance, list)
    return isinstance(instance, expected_type)


def _resolve_ref(ref: str, root: Dict[str, Any]) -> Dict[str, Any]:
    prefix = "#/$defs/"
    if not ref.startswith(prefix):
        raise ValueError("only local $defs references are supported: {0}".format(ref))
    name = ref[len(prefix) :]
    if name not in root.get("$defs", {}):
        raise ValueError("unknown $ref definition: {0}".format(name))
    return root["$defs"][name]


def validate(
    instance: Any,
    schema: Dict[str, Any],
    *,
    root: Optional[Dict[str, Any]] = None,
    path: str = "$",
) -> List[str]:
    """Return a list of human-readable validation errors (empty means valid)."""
    root_schema = schema if root is None else root
    errors: List[str] = []

    if "$ref" in schema:
        return validate(
            instance, _resolve_ref(schema["$ref"], root_schema), root=root_schema, path=path
        )

    expected_type = schema.get("type")
    if expected_type is not None:
        candidates: Sequence[str]
        if isinstance(expected_type, list):
            candidates = expected_type
        else:
            candidates = [expected_type]
        if not any(_matches_type(instance, candidate) for candidate in candidates):
            errors.append(
                "{0}: expected type {1}, got {2}".format(
                    path, "/".join(candidates), type(instance).__name__
                )
            )
            return errors

    if "const" in schema and instance != schema["const"]:
        errors.append(
            "{0}: expected const {1!r}, got {2!r}".format(
                path, schema["const"], instance
            )
        )

    if "enum" in schema and instance not in schema["enum"]:
        errors.append(
            "{0}: value {1!r} is not one of {2!r}".format(
                path, instance, schema["enum"]
            )
        )

    if isinstance(instance, dict):
        errors.extend(_validate_object(instance, schema, root_schema, path))
    elif isinstance(instance, list):
        errors.extend(_validate_array(instance, schema, root_schema, path))
    elif isinstance(instance, str):
        errors.extend(_validate_string(instance, schema, path))
    elif isinstance(instance, (int, float)) and not isinstance(instance, bool):
        errors.extend(_validate_number(instance, schema, path))

    return errors


def _validate_object(
    instance: Dict[str, Any],
    schema: Dict[str, Any],
    root: Dict[str, Any],
    path: str,
) -> List[str]:
    errors: List[str] = []
    properties = schema.get("properties", {})
    for required in schema.get("required", []):
        if required not in instance:
            errors.append("{0}: missing required property {1!r}".format(path, required))

    additional = schema.get("additionalProperties", True)
    for key in sorted(instance):
        child_path = "{0}.{1}".format(path, key)
        if key in properties:
            errors.extend(
                validate(instance[key], properties[key], root=root, path=child_path)
            )
        elif additional is False:
            errors.append("{0}: unexpected property {1!r}".format(path, key))
        elif isinstance(additional, dict):
            errors.extend(
                validate(instance[key], additional, root=root, path=child_path)
            )
    return errors


def _validate_array(
    instance: List[Any],
    schema: Dict[str, Any],
    root: Dict[str, Any],
    path: str,
) -> List[str]:
    errors: List[str] = []
    minimum = schema.get("minItems")
    if minimum is not None and len(instance) < minimum:
        errors.append("{0}: expected at least {1} items".format(path, minimum))
    maximum = schema.get("maxItems")
    if maximum is not None and len(instance) > maximum:
        errors.append("{0}: expected at most {1} items".format(path, maximum))
    item_schema = schema.get("items")
    if isinstance(item_schema, dict):
        for index, item in enumerate(instance):
            errors.extend(
                validate(
                    item,
                    item_schema,
                    root=root,
                    path="{0}[{1}]".format(path, index),
                )
            )
    return errors


def _validate_string(
    instance: str, schema: Dict[str, Any], path: str
) -> List[str]:
    errors: List[str] = []
    minimum = schema.get("minLength")
    if minimum is not None and len(instance) < minimum:
        errors.append("{0}: expected at least {1} characters".format(path, minimum))
    pattern = schema.get("pattern")
    if pattern is not None and re.search(pattern, instance) is None:
        errors.append(
            "{0}: value {1!r} does not match {2!r}".format(path, instance, pattern)
        )
    return errors


def _validate_number(
    instance: Any, schema: Dict[str, Any], path: str
) -> List[str]:
    errors: List[str] = []
    minimum = schema.get("minimum")
    if minimum is not None and instance < minimum:
        errors.append("{0}: expected >= {1}, got {2}".format(path, minimum, instance))
    maximum = schema.get("maximum")
    if maximum is not None and instance > maximum:
        errors.append("{0}: expected <= {1}, got {2}".format(path, maximum, instance))
    return errors
