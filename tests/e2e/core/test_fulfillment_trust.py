"""Unit tests for the deployed fulfillment trust assertions."""

import pytest

from tests.e2e.core.fulfillment_trust import assert_management_tls


class FakeK8sClient:
    def __init__(self, operator: dict[str, object]) -> None:
        self.operator = operator

    def get_json(self, *, resource: str, name: str) -> dict[str, object]:
        assert resource == "deployment"
        assert name == "osac-operator"
        return self.operator


def make_operator(containers: list[dict[str, object]]) -> dict[str, object]:
    return {
        "spec": {"template": {"spec": {"containers": containers, "volumes": [{"configMap": {"name": "ca-bundle"}}]}}}
    }


def test_assert_management_tls_combines_command_and_args() -> None:
    client = FakeK8sClient(
        make_operator([{"name": "manager", "command": ["--fulfillment-ca-file=/etc/ca-bundle/bundle.pem"], "args": []}])
    )

    assert_management_tls(client)


def test_assert_management_tls_reports_missing_manager() -> None:
    client = FakeK8sClient(make_operator([]))

    with pytest.raises(AssertionError, match="Deployment has no manager container"):
        assert_management_tls(client)
