"""Exception classes for vastdata.vms collection."""


class VastError(RuntimeError):
    """Base exception for VAST-related errors."""

    pass


class VastAuthError(VastError):
    """Authentication or authorization error."""

    pass


class VastNotFoundError(VastError):
    """Resource not found."""

    pass


class VastAPIError(VastError):
    """API or network error."""

    pass
