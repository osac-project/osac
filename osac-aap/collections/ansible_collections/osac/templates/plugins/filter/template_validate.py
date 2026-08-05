import re
import yaml

from pathlib import Path
from typing import Any

from ansible.errors import AnsibleFilterError


def _load_osac_metadata(role_path: str) -> dict[str, Any]:
    """Load and return the parsed meta/osac.yaml for a role."""
    path = Path(role_path) / "meta" / "osac.yaml"
    if not path.exists():
        path = Path(role_path) / "meta" / "osac.yml"
    if not path.exists():
        raise AnsibleFilterError(
            f"No osac.yaml found at {role_path}/meta/"
        )
    with path.open("r", encoding="utf-8") as fd:
        data = yaml.safe_load(fd)
    if not isinstance(data, dict):
        raise AnsibleFilterError(
            f"Invalid osac.yaml at {path}: expected a YAML mapping"
        )
    return data


def template_spec_defaults(role_path: str) -> dict[str, Any]:
    """Load spec_defaults from osac.yaml and return in camelCase for CRD merging.

    Usage in Ansible:
        {{ role_path | osac.templates.template_spec_defaults }}
    """
    metadata = _load_osac_metadata(role_path)
    spec_defaults = metadata.get("spec_defaults")
    if not spec_defaults or not isinstance(spec_defaults, dict):
        return {}

    camel_map = {
        "memory_gib": "memoryGiB",
        "boot_disk": "bootDisk",
        "size_gib": "sizeGiB",
        "source_type": "sourceType",
        "source_ref": "sourceRef",
        "run_strategy": "runStrategy",
    }

    def to_camel(d: dict[str, Any]) -> dict[str, Any]:
        result: dict[str, Any] = {}
        for key, value in d.items():
            camel_key = camel_map.get(key, key)
            if isinstance(value, dict):
                result[camel_key] = to_camel(value)
            else:
                result[camel_key] = value
        return result

    return to_camel(spec_defaults)


def template_validate_params(
    user_params: dict[str, Any], role_path: str
) -> dict[str, Any]:
    """Validate and merge template parameters against osac.yaml definitions.

    1. Load parameter definitions from meta/osac.yaml
    2. Build defaults dict from parameter definitions
    3. Merge: defaults <- user-provided params
    4. Validate required fields are present
    5. Validate patterns where specified
    6. Return merged+validated params dict

    Usage in Ansible:
        {{ (template_parameters | default({})) | osac.templates.template_validate_params(role_path) }}
    """
    metadata = _load_osac_metadata(role_path)
    param_defs = metadata.get("parameters", [])
    if not isinstance(param_defs, list):
        raise AnsibleFilterError(
            f"'parameters' in osac.yaml must be a list, got {type(param_defs).__name__}"
        )

    defaults: dict[str, Any] = {}
    for defn in param_defs:
        if not isinstance(defn, dict):
            continue
        name = defn.get("name")
        if not name:
            continue
        if "default" in defn:
            defaults[name] = defn["default"]

    merged = {**defaults, **user_params}

    for defn in param_defs:
        if not isinstance(defn, dict):
            continue
        name = defn.get("name")
        if not name:
            continue

        if defn.get("required", False) and name not in merged:
            raise AnsibleFilterError(
                f"Required template parameter '{name}' is missing"
            )

        validation = defn.get("validation")
        if validation and isinstance(validation, dict) and name in merged:
            pattern = validation.get("pattern")
            if pattern and isinstance(merged[name], str):
                if not re.match(pattern, merged[name]):
                    raise AnsibleFilterError(
                        f"Template parameter '{name}' value '{merged[name]}' "
                        f"does not match pattern: {pattern}"
                    )

    return merged


class FilterModule:
    def filters(self) -> dict[str, Any]:
        return {
            "template_spec_defaults": template_spec_defaults,
            "template_validate_params": template_validate_params,
        }
