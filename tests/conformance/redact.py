"""One redaction boundary for conformance output and evidence."""

from __future__ import annotations

import json
import re
from collections.abc import Iterable

from tests.e2e.core.sanitize import REDACTED, sanitize_obj, sanitize_text

# A decoded YAML document separates a credential's name from its value, so the
# textual ``name=value`` rules no longer see the pair. Match the name here too.
_CREDENTIAL_NAMES = frozenset(
    {
        "password",
        "passwd",
        "passphrase",
        "token",
        "apikey",
        "accesskey",
        "secretkey",
        "clientsecret",
        "secret",
        "sessionid",
        "authorization",
        "credential",
        "credentials",
        "privatekey",
        "bearertoken",
    }
)
_CREDENTIAL_SUFFIXES = (
    "password",
    "passwd",
    "passphrase",
    "token",
    "apikey",
    "accesskey",
    "secretkey",
    "clientsecret",
    "privatekey",
    "credentials",
    "sessionid",
    # DOCKER_AUTH_CONFIG and friends carry registry credentials as base64.
    "authconfig",
)


def _is_credential_name(value: object) -> bool:
    if isinstance(value, bytes):
        value = value.decode(errors="replace")
    normalized = re.sub(r"[^a-z0-9]", "", str(value).lower())
    return normalized in _CREDENTIAL_NAMES or normalized.endswith(_CREDENTIAL_SUFFIXES)


class Redactor:
    def __init__(self, *, secrets: Iterable[str] = (), hosts: Iterable[str] = ()) -> None:
        self._values: set[str] = set()
        for value in (*secrets, *hosts):
            self.add_secret(value)

    def add_secret(self, value: str) -> None:
        if value:
            self._values.add(value)

    def add_host(self, value: str) -> None:
        self.add_secret(value)

    def text(self, value: str) -> str:
        for known in sorted(self._values, key=len, reverse=True):
            value = value.replace(known, REDACTED)
        return sanitize_text(value)

    def obj(self, value: object) -> object:
        return self._structure(sanitize_obj(value))

    def _structure(self, value: object) -> object:
        if isinstance(value, dict):
            # A container environment entry keeps the credential name and the
            # value in separate fields: {"name": "API_KEY", "value": "..."}.
            secret_entry = "value" in value and _is_credential_name(value.get("name", ""))
            cleaned: dict[object, object] = {}
            for key, item in value.items():
                name = self.text(str(key))
                if _is_credential_name(key) or (secret_entry and str(key) == "value"):
                    cleaned[name] = REDACTED
                else:
                    cleaned[name] = self._structure(item)
            return cleaned
        if isinstance(value, list):
            return [self._structure(item) for item in value]
        if isinstance(value, str):
            return self._serialized(value)
        return value

    def _serialized(self, value: str) -> str:
        """Redact a value that may embed a serialized JSON document.

        A ``kubectl.kubernetes.io/last-applied-configuration`` annotation holds
        JSON in which a credential's name and value are separate fields, so the
        text rules cannot pair them.
        """
        stripped = value.strip()
        if stripped[:1] in {"{", "["}:
            try:
                parsed = json.loads(stripped)
            except ValueError:
                parsed = None
            if isinstance(parsed, (dict, list)):
                cleaned = self._structure(parsed)
                if cleaned != parsed:
                    return json.dumps(cleaned)
        return self.text(value)
