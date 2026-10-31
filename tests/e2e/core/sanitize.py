"""Scrub credentials and payloads from text, command lines and API objects before they are logged or stored."""

from __future__ import annotations

import re
from collections.abc import Iterable

REDACTED = "<redacted>"

_JWT = re.compile(r"eyJ[\w-]+\.eyJ[\w-]+\.[\w-]*")
# "Basic" is matched capitalised only, so prose such as "basic connectivity" survives.
_AUTH_SCHEME = re.compile(r"\b((?i:bearer)|Basic)(\s+)[A-Za-z0-9._~+/=-]{8,}")
_KEY_VALUE = re.compile(r"(?i)\b(password|passwd|client_secret)(\s*=\s*)\S+")
_TOKEN_FLAG = re.compile(r"(--token(?:-script)?)([ =])\S+")
_JSON_SECRET = re.compile(r'(?i)("[\w-]*(?:password|token|secret)[\w-]*"\s*:\s*)"(?:[^"\\]|\\.)*"')

# The value after one of these flags is dropped whole: a header holds the token, -d holds the request payload.
_VALUE_FLAGS = frozenset({"-H", "--header", "-d", "--data", "--token", "--token-script", "--password"})

# Compared without case, underscores and hyphens: user_data, userData and UserData are one key.
_DROPPED_KEYS = frozenset({"userdata", "cloudinituserdata", "templateparameters", "sshkey", "password", "token"})


def sanitize_text(text: str) -> str:
    """Replace bearer/basic tokens, JWTs, password and secret assignments and JSON secret values."""
    text = _JWT.sub(REDACTED, text)
    text = _AUTH_SCHEME.sub(lambda m: f"{m.group(1)}{m.group(2)}{REDACTED}", text)
    text = _KEY_VALUE.sub(lambda m: f"{m.group(1)}{m.group(2)}{REDACTED}", text)
    text = _TOKEN_FLAG.sub(lambda m: f"{m.group(1)}{m.group(2)}{REDACTED}", text)
    return _JSON_SECRET.sub(lambda m: f'{m.group(1)}"{REDACTED}"', text)


def sanitize_args(args: Iterable[object]) -> list[str]:
    """A command line safe to show: header, data and credential flag values are dropped, the rest is scrubbed."""
    cleaned: list[str] = []
    redact_next = False
    for arg in map(str, args):
        if redact_next:
            cleaned.append(REDACTED)
            redact_next = False
            continue
        flag, equals, _ = arg.partition("=")
        if flag.startswith("--") and equals and flag in _VALUE_FLAGS:
            cleaned.append(f"{flag}={REDACTED}")
            continue
        redact_next = arg in _VALUE_FLAGS
        cleaned.append(sanitize_text(arg))
    return cleaned


def _dropped(key: object) -> bool:
    normalized = re.sub(r"[_-]", "", str(key)).lower()
    return normalized in _DROPPED_KEYS or normalized.endswith("secret")


def sanitize_obj(value: object) -> object:
    """Recursive copy without secret-bearing keys, with every string leaf scrubbed."""
    if isinstance(value, dict):
        return {key: sanitize_obj(item) for key, item in value.items() if not _dropped(key)}
    if isinstance(value, (list, tuple)):
        return [sanitize_obj(item) for item in value]
    if isinstance(value, str):
        return sanitize_text(value)
    return value
