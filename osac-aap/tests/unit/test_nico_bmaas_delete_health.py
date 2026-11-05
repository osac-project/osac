"""NICo BMI release checks against the Machine GET aggregate health response."""

import importlib.util
from pathlib import Path

import pytest


MODULE_PATH = (
    Path(__file__).resolve().parents[2]
    / "collections/ansible_collections/nico/steps/plugins/modules/bare_metal_instance.py"
)
SPEC = importlib.util.spec_from_file_location("nico_bare_metal_instance_delete", MODULE_PATH)
nico = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(nico)


class FakeClient:
    def __init__(self, machine):
        self.params = {
            "instance_id": "instance-id",
            "machine_id": "machine-id",
            "timeout": 0,
            "poll_interval": 1,
        }
        self.machine = machine
        self.calls = []

    def request(self, scope, method, resource, body=None):
        self.calls.append((scope, method, resource))
        if (scope, method, resource) == ("tenant", "GET", "instance/instance-id"):
            raise nico.NicoHTTPError(404, "already deleted")
        if (scope, method, resource) == ("provider", "GET", "machine/machine-id"):
            return 200, self.machine
        raise AssertionError(f"unexpected NICo request: {scope} {method} {resource}")


def ready_machine(alerts):
    return {"status": "Ready", "instanceId": None,
            "health": {"source": "aggregate-host-health", "alerts": alerts}}


@pytest.mark.parametrize("alerts", [[], [{"id": "Informational", "classifications": []}]])
def test_delete_releases_only_from_machine_get_with_clear_aggregate_health(alerts):
    client = FakeClient(ready_machine(alerts))

    result = nico.ensure_absent(client)

    assert result == {"changed": False, "instance_id": "instance-id",
                      "machine_id": "machine-id", "status": "Released"}
    assert client.calls == [
        ("tenant", "GET", "instance/instance-id"),
        ("tenant", "GET", "instance/instance-id"),
        ("provider", "GET", "machine/machine-id"),
    ]


@pytest.mark.parametrize("machine", [
    ready_machine([{"id": "HeartbeatTimeout", "classifications": ["PreventAllocations"]}]),
    {"status": "Initializing", "instanceId": None},
    {"status": "Ready", "instanceId": "another-instance", "health": {"alerts": []}},
])
def test_delete_retains_instance_until_original_machine_is_unassigned_ready_and_clear(machine):
    client = FakeClient(machine)

    with pytest.raises(nico.NicoError, match="cleanup incomplete"):
        nico.ensure_absent(client)


@pytest.mark.parametrize("health", [
    None,
    {},
    {"alerts": None},
    {"alerts": [None]},
    {"alerts": [{"id": "HeartbeatTimeout"}]},
    {"alerts": [{"classifications": "PreventAllocations"}]},
])
def test_delete_fails_closed_when_ready_machine_aggregate_health_is_unusable(health):
    client = FakeClient({"status": "Ready", "instanceId": None, "health": health})

    with pytest.raises(nico.NicoError, match="aggregate health|classifications"):
        nico.ensure_absent(client)
