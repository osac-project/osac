"""What kind of failure was it: the API refused, an object is stuck, traffic is blocked, or our own access broke."""

from __future__ import annotations

import re
from dataclasses import dataclass, field
from enum import StrEnum
from typing import TypeVar

from tests.e2e.core.runner import CommandFailed, StuckState
from tests.e2e.core.sanitize import sanitize_text

_SUMMARY_LIMIT = 1000
_CHAIN_LIMIT = 6
_GRPC_LINE = re.compile(r"(?m)^\s*(Code|Message):\s*(.+?)\s*$")


class Kind(StrEnum):
    API_REFUSED = "api-refused"  # the API or CLI rejected a call
    STUCK_STATE = "stuck-state"  # an object never reached the state we waited for
    TRAFFIC_BLOCKED = "traffic-blocked"  # the guest path works, the traffic under test did not get through
    TEST_SSH_FAILED = "test-ssh-failed"  # our own access to the guest broke, the result says nothing on the network
    ASSERTION = "assertion"  # a plain assert
    UNCLASSIFIED = "unclassified"


class E2EFailure(AssertionError):
    """A failure that already knows its kind and the object IDs involved."""

    def __init__(self, kind: Kind, message: str, *, ids: dict[str, str] | None = None) -> None:
        super().__init__(message)
        self.kind: Kind = kind
        self.ids: dict[str, str] = dict(ids or {})


_E = TypeVar("_E", bound=BaseException)


def tagged(exc: _E, kind: Kind, ids: dict[str, str] | None = None) -> _E:
    """Give an exception a kind and IDs without changing its type, so callers that catch it keep working."""
    exc.kind = kind  # type: ignore[attr-defined]
    exc.ids = dict(ids or {})  # type: ignore[attr-defined]
    return exc


@dataclass(frozen=True)
class Classified:
    kind: Kind
    summary: str
    ids: dict[str, str] = field(default_factory=dict)


def _chain(exc: BaseException) -> list[BaseException]:
    """`exc`, then its __cause__, then its __context__, following one link at a time, at most six steps."""
    found: list[BaseException] = []
    current: BaseException | None = exc
    while current is not None and len(found) < _CHAIN_LIMIT and current not in found:
        found.append(current)
        current = current.__cause__ or current.__context__
    return found


def _grpc_summary(stderr: str | bytes | None) -> str:
    # TimeoutExpired keeps bytes even when the command ran with text=True.
    text = stderr.decode(errors="replace") if isinstance(stderr, bytes) else (stderr or "")
    fields = dict(_GRPC_LINE.findall(text))
    if "Code" not in fields:
        return ""
    return f"gRPC {fields['Code']}: {fields.get('Message', '')}".rstrip(": ")


def _kind_of(exc: BaseException) -> Kind | None:
    tag = getattr(exc, "kind", None)
    if isinstance(tag, Kind):
        return tag
    if isinstance(exc, CommandFailed):
        return Kind.API_REFUSED if exc.is_api_command else None
    if isinstance(exc, StuckState):
        return Kind.STUCK_STATE
    if isinstance(exc, AssertionError):
        return Kind.ASSERTION
    return None


def _classification_chain(exc: BaseException) -> list[BaseException]:
    """Prefer ordinary chain kinds, then the first known depth-first group branch within six links."""
    outer = _chain(exc)
    pending = [(outer, [])]
    seen: dict[int, int] = {}
    while pending:
        candidates, prefix = pending.pop()
        chain: list[BaseException] = []
        for candidate in candidates[: _CHAIN_LIMIT - len(prefix)]:
            depth = len(prefix) + len(chain)
            if seen.get(id(candidate), _CHAIN_LIMIT) <= depth:
                break
            seen[id(candidate)] = depth
            chain.append(candidate)
        if any(_kind_of(error) is not None for error in chain):
            return prefix + chain
        branches = [
            (_chain(member), prefix + chain[: index + 1])
            for index, error in enumerate(chain)
            if isinstance(error, BaseExceptionGroup)
            for member in error.exceptions
        ]
        pending.extend(reversed(branches))
    return outer


def classify(exc: BaseException) -> Classified:
    """Classify ordinary chains first, then ordered group members, keeping evidence on the selected branch."""
    chain = _classification_chain(exc)
    kind = next((k for k in map(_kind_of, chain) if k is not None), Kind.UNCLASSIFIED)
    text = str(exc) or type(exc).__name__
    if any(isinstance(error, BaseExceptionGroup) for error in chain):
        selected = next((error for error in chain if _kind_of(error) is not None), exc)
        if selected is not exc:
            text = f"{text}\n{selected}"
    if kind is Kind.UNCLASSIFIED:
        text = f"{type(exc).__name__}: {text}"
    # An outer message (a hint, a diagnosis) comes first; the gRPC status of an inner command rides along.
    grpc = next((s for s in (_grpc_summary(getattr(e, "stderr", "") or "") for e in chain) if s), "")
    if grpc and grpc not in text:
        text = f"{text}\n{grpc}"
    ids = next((e.ids for e in chain if isinstance(getattr(e, "kind", None), Kind) and e.ids), {})
    return Classified(kind, sanitize_text(text)[:_SUMMARY_LIMIT], {k: sanitize_text(str(v)) for k, v in ids.items()})
