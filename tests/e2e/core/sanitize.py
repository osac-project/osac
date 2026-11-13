"""Scrub credentials, payloads and configured endpoints from outward diagnostics and saved API objects."""

from __future__ import annotations

import re
from collections.abc import Iterable
from ipaddress import ip_address
from threading import Lock
from urllib.parse import urlsplit

REDACTED = "<redacted>"

_ENDPOINT_HOSTS: frozenset[str] = frozenset()
_ENDPOINT_LOCK = Lock()
_ENDPOINT_URL = re.compile(r"^(?:https?|grpcs?)://", re.IGNORECASE)


def _is_transport_target(value: str) -> bool:
    """Infer only explicit network URLs or IP authorities outside a known RPC target position."""
    if _ENDPOINT_URL.match(value):
        return True
    try:
        parsed = urlsplit(f"//{value}")
        if parsed.port is None or parsed.path or parsed.query or parsed.fragment or parsed.username:
            return False
        ip_address(parsed.hostname or "")
    except ValueError:
        return False
    return True


def register_endpoint(address: str) -> None:
    """Remember configured endpoint authorities and bare hosts for shared outward diagnostics."""
    global _ENDPOINT_HOSTS
    try:
        parsed = urlsplit(address if "://" in address else f"//{address}")
        host = parsed.hostname
    except ValueError:
        return
    if host and host.rstrip("."):
        with _ENDPOINT_LOCK:
            _ENDPOINT_HOSTS = _ENDPOINT_HOSTS.union((host.rstrip("."), parsed.netloc.rsplit("@", 1)[-1]))


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


def sanitize_text(text: str | bytes) -> str:
    """Scrub credentials and known endpoint hosts without masking unrelated dotted identifiers."""
    if isinstance(text, bytes):
        text = text.decode(errors="replace")
    text = _JWT.sub(REDACTED, text)
    text = _AUTH_SCHEME.sub(lambda m: f"{m.group(1)}{m.group(2)}{REDACTED}", text)
    text = _KEY_VALUE.sub(lambda m: f"{m.group(1)}{m.group(2)}{REDACTED}", text)
    text = _TOKEN_FLAG.sub(lambda m: f"{m.group(1)}{m.group(2)}{REDACTED}", text)
    text = _JSON_SECRET.sub(lambda m: f'{m.group(1)}"{REDACTED}"', text)
    with _ENDPOINT_LOCK:
        hosts = _ENDPOINT_HOSTS
    for host in sorted(hosts, key=len, reverse=True):
        text = re.sub(r"(?<![\w.-])" + re.escape(host) + r"(?![\w-]|\.[\w-])", REDACTED, text, flags=re.I)
    return text


def sanitize_args(args: Iterable[object]) -> list[str]:
    """A command line safe to show: header, data and credential flag values are dropped, the rest is scrubbed."""
    values = list(map(str, args))
    if len(values) >= 3 and values[0].rsplit("/", 1)[-1] == "grpcurl" and re.fullmatch(r"[\w.]+/\w+", values[-1]):
        register_endpoint(values[-2])
    cleaned: list[str] = []
    redact_next = False
    for arg in values:
        if redact_next:
            cleaned.append(REDACTED)
            redact_next = False
            continue
        flag, equals, _ = arg.partition("=")
        if flag.startswith("--") and equals and flag in _VALUE_FLAGS:
            cleaned.append(f"{flag}={REDACTED}")
            continue
        redact_next = arg in _VALUE_FLAGS
        value = arg.partition("=")[2] if arg.startswith("--") and equals else arg
        if _is_transport_target(value):
            register_endpoint(value)
        cleaned.append(sanitize_text(arg))
    return [sanitize_text(arg) for arg in cleaned]


def _dropped(key: object) -> bool:
    """Apply the same secret and payload key policy to decoded and byte metadata."""
    if isinstance(key, bytes):
        key = key.decode(errors="replace")
    normalized = re.sub(r"[_-]", "", str(key)).lower()
    return normalized in _DROPPED_KEYS or normalized.endswith("secret")


def sanitize_obj(value: object) -> object:
    """Recursive copy without secret-bearing keys, with every string leaf scrubbed."""
    if isinstance(value, dict):
        return {
            sanitize_text(key) if isinstance(key, (str, bytes)) else key: sanitize_obj(item)
            for key, item in value.items()
            if not _dropped(key)
        }
    if isinstance(value, (list, tuple)):
        return [sanitize_obj(item) for item in value]
    if isinstance(value, (str, bytes)):
        return sanitize_text(value)
    if value is None or isinstance(value, (bool, int, float)):
        return value
    return sanitize_text(str(value))
