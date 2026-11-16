"""Function-local isolation for shared E2E diagnostic state used by unit consumers."""

from collections.abc import Iterator

import pytest

from tests.e2e.core import sanitize


@pytest.fixture(autouse=True)
def isolated_endpoint_registry() -> Iterator[None]:
    """Restore learned endpoints after each unit while retaining higher-scoped registrations."""
    with sanitize._ENDPOINT_LOCK:
        previous = sanitize._ENDPOINT_HOSTS
    try:
        yield
    finally:
        with sanitize._ENDPOINT_LOCK:
            sanitize._ENDPOINT_HOSTS = previous
