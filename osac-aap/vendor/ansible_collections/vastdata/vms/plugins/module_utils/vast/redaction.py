"""Redact credential values from Ansible module results."""

import re


SENSITIVE_KEY_PARTS = (
    "access_key",
    "api_key",
    "authorization",
    "password",
    "private_key",
    "ssl_keyfile",
    "secret",
    "token",
)
SENSITIVE_HEADER_KEY_PARTS = (
    "api_key",
    "auth",
    "cookie",
    "credential",
    "key",
    "password",
    "secret",
    "signature",
    "token",
)


def collect_sensitive_values(data):
    """Collect string values under credential-shaped keys for no_log_values."""
    values = set()

    def normalize_key(key):
        normalized = re.sub(r"([a-z0-9])([A-Z])", r"\1_\2", str(key))
        return normalized.casefold().replace("-", "_")

    def collect_all(value):
        if isinstance(value, str):
            if value:
                values.add(value)
        elif isinstance(value, dict):
            for child in value.values():
                collect_all(child)
        elif isinstance(value, (list, tuple)):
            for child in value:
                collect_all(child)

    def visit(value):
        if isinstance(value, dict):
            for key, child in value.items():
                normalized = normalize_key(key)
                if normalized == "headers" and isinstance(child, dict):
                    for header, header_value in child.items():
                        normalized_header = normalize_key(header)
                        if any(part in normalized_header for part in SENSITIVE_HEADER_KEY_PARTS):
                            collect_all(header_value)
                elif normalized == "headers":
                    collect_all(child)
                elif normalized in {"credential", "credentials"} or any(
                    part in normalized for part in SENSITIVE_KEY_PARTS
                ):
                    collect_all(child)
                else:
                    visit(child)
        elif isinstance(value, (list, tuple)):
            for child in value:
                visit(child)

    visit(data)
    return values


def redact_module_result(module, data):
    """Register sensitive output values when running under AnsibleModule."""
    no_log_values = getattr(module, "no_log_values", None)
    if no_log_values is not None:
        no_log_values.update(collect_sensitive_values(data))
