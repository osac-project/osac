"""Typed errors for the coverage reporter.

Every error carries a stable machine-readable ``code``, an exit code, and a
``recovery`` hint. The CLI prints all three so a caller (or an AI skill) can
decide what to do next without parsing prose.
"""

from typing import Dict, List, Optional, Sequence


class CoverageReportError(Exception):
    """Base class for all expected failures.

    Args:
        message: Human-readable description of the failure.
        code: Stable machine-readable code. Defaults to the class code.
        recovery: Concrete next step for the operator.
        field: Dotted config/schema path the error refers to, when applicable.
        candidates: Concrete alternatives to show the caller.
        details: Extra structured context for diagnostics.
        exit_code: Process exit code. Defaults to the class exit code.
    """

    code = "INTERNAL_ERROR"
    exit_code = 1

    def __init__(
        self,
        message: str,
        *,
        code: Optional[str] = None,
        recovery: Optional[str] = None,
        field: Optional[str] = None,
        candidates: Optional[Sequence[str]] = None,
        details: Optional[Dict[str, object]] = None,
        exit_code: Optional[int] = None,
    ) -> None:
        super().__init__(message)
        self.message = message
        self.code = code or type(self).code
        self.recovery = recovery or ""
        self.field = field
        self.candidates: List[str] = list(candidates or ())
        self.details: Dict[str, object] = dict(details or {})
        self.exit_code = type(self).exit_code if exit_code is None else exit_code

    def to_dict(self) -> Dict[str, object]:
        """Return a JSON-ready description of the error."""
        return {
            "code": self.code,
            "message": self.message,
            "field": self.field,
            "recovery": self.recovery,
            "candidates": list(self.candidates),
            "details": dict(self.details),
        }

    def render(self) -> str:
        """Render the error the way the CLI prints it."""
        lines = ["error {0}: {1}".format(self.code, self.message)]
        if self.field:
            lines.append("field: {0}".format(self.field))
        if self.candidates:
            lines.append("candidates: {0}".format(", ".join(self.candidates)))
        if self.recovery:
            lines.append("recovery: {0}".format(self.recovery))
        return "\n".join(lines)


class ConfigError(CoverageReportError):
    """Invalid or unreadable configuration."""

    code = "CONFIG_INVALID"
    exit_code = 3


class ScopeError(CoverageReportError):
    """The requested feature could not be resolved to exactly one registry entry.

    Uses ``SCOPE_UNRESOLVED`` or ``SCOPE_AMBIGUOUS`` explicitly.
    """

    code = "SCOPE_UNRESOLVED"
    exit_code = 2


class EvidencePromotionError(CoverageReportError):
    """An evidence class was promoted beyond the single allowed transition."""

    code = "EVIDENCE_PROMOTION"
    exit_code = 1


class ModelError(CoverageReportError):
    """A model invariant was violated (for example conflicting evidence ids)."""

    code = "MODEL_INVARIANT"
    exit_code = 1


class PublishError(CoverageReportError):
    """Output could not be written atomically."""

    code = "PUBLISH_FAILED"
    exit_code = 1


class RepoError(CoverageReportError):
    """The repository state required for a run is unavailable."""

    code = "REPO_UNAVAILABLE"
    exit_code = 1
