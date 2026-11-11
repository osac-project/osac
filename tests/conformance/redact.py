"""One redaction boundary for conformance output and evidence."""

from __future__ import annotations

from collections.abc import Iterable

from tests.e2e.core.sanitize import REDACTED, sanitize_obj, sanitize_text


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
        cleaned = sanitize_obj(value)
        if isinstance(cleaned, str):
            return self.text(cleaned)
        if isinstance(cleaned, dict):
            return {self.text(str(key)): self.obj(item) for key, item in cleaned.items()}
        if isinstance(cleaned, list):
            return [self.obj(item) for item in cleaned]
        return cleaned
