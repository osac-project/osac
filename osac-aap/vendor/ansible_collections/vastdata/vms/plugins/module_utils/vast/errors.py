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


class VastTransportError(VastAPIError):
    """Transport-level failure (connection refused/reset, timeout, VIP 502/503/504).

    Distinct from an API-level answer: the VMS did not deliver a well-formed
    response, typically because it is restarting or an HA failover is in
    progress. Callers that poll (e.g. ``wait_for_task``) should retry these
    until the overall timeout rather than failing fast.
    """

    pass
