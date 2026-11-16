"""Exercise endpoint registry isolation through actual serial, cross-module pytest runs."""

from collections.abc import Iterator
from pathlib import Path

import pytest

from tests.e2e.core import sanitize
from tests.e2e.core.grpc_client import GRPCClient
from tests.e2e.core.sanitize import sanitize_text
from tests.unit.test_e2e_failure_plugin import run_pytest


@pytest.fixture(scope="module")
def module_endpoint() -> Iterator[str]:
    """Keep a module client's configured endpoint available across function-level isolation."""
    with sanitize._ENDPOINT_LOCK:
        previous = sanitize._ENDPOINT_HOSTS
    host = "module-fixture.synthetic.example.invalid"
    GRPCClient(address=f"{host}:443")
    try:
        yield host
    finally:
        with sanitize._ENDPOINT_LOCK:
            sanitize._ENDPOINT_HOSTS = previous


@pytest.mark.parametrize("iteration", [0, 1])
def test_function_isolation_retains_module_fixture_endpoint(module_endpoint: str, iteration: int) -> None:
    """Repeated function snapshots must preserve the already-configured module client target."""
    assert sanitize_text(module_endpoint) == sanitize.REDACTED
    assert sanitize_text(f"function-{1 - iteration}.synthetic.example.invalid") != sanitize.REDACTED
    GRPCClient(address=f"function-{iteration}.synthetic.example.invalid:443")


def test_unit_endpoint_registry_has_no_prior_function_targets() -> None:
    """Endpoints learned by unrelated unit functions must not change this function's diagnostics."""
    detail = "fulfillment.example fulfillment-api.example.test"
    assert sanitize_text(detail) == detail


@pytest.mark.parametrize("reverse", [False, True])
def test_endpoint_isolation_across_real_unit_module_orders(tmp_path: Path, reverse: bool) -> None:
    """Run real learning consumers and an observer in both module orders in one serial process."""
    root = Path(__file__).resolve().parents[2]
    seed = str(root / "tests/unit/test_e2e_failure_report.py") + (
        "::TestSanitizeArgs::test_grpcurl_argv_drops_token_and_payload"
    )
    observer = str(Path(__file__).resolve()) + "::test_unit_endpoint_registry_has_no_prior_function_targets"
    nodes = (observer, seed) if reverse else (seed, observer)
    result = run_pytest(tmp_path, "", conftest="", args=nodes)
    assert result.returncode == 0, result.stdout
    assert "2 passed" in result.stdout
