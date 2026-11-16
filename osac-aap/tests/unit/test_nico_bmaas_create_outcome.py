"""NICo BMI create outcome reported to the BMI controller on failure."""

import importlib.util
from pathlib import Path

import pytest

# Reuse the NICo REST fake and the loaded module from the network-mode tests.
_SPEC = importlib.util.spec_from_file_location(
    "nico_bmaas_network_mode_outcome_tests", Path(__file__).with_name("test_nico_bmaas_network_mode.py"))
_network_mode = importlib.util.module_from_spec(_SPEC)
_SPEC.loader.exec_module(_network_mode)
FakeClient, nico, params = _network_mode.FakeClient, _network_mode.nico, _network_mode.params
ready_operating_system = _network_mode.ready_operating_system

FNN_INTERFACES = [{"vpcPrefixId": "prefix-id", "ipAddresses": ["198.51.100.20"]}]


@pytest.fixture
def fast_clock(monkeypatch):
    """Advance a fake clock on every sleep so timeouts expire immediately."""
    now = [0.0]
    monkeypatch.setattr(nico.time, "monotonic", lambda: now[0])
    monkeypatch.setattr(nico.time, "sleep", lambda seconds: now.__setitem__(0, now[0] + seconds))


def with_instance(client, **overrides):
    """Make GET instance/instance-id return the Ready Instance with overrides."""
    original = client.request

    def response(scope, method, resource, body=None):
        code, value = original(scope, method, resource, body)
        if (method, resource) == ("GET", "instance/instance-id"):
            value.update(overrides)
        return code, value

    client.request = response
    return client


def test_failure_before_post_reports_not_created():
    client = FakeClient(params("FNN"), operating_system=ready_operating_system(status="Syncing"))
    progress = {}

    with pytest.raises(nico.NicoError, match="is not Ready"):
        nico.ensure_present(client, progress)

    assert client.posts == []
    assert progress == {"outcome": "NotCreated", "instance_id": "", "machine_id": ""}


def test_invalid_user_data_reports_not_created():
    client = FakeClient(params("FNN", user_data="#!/bin/bash\n"))
    progress = {}

    with pytest.raises(nico.NicoError):
        nico.ensure_present(client, progress)

    assert progress["outcome"] == "NotCreated"


def test_failed_post_reports_uncertain():
    client = FakeClient(params("FNN"))
    original = client.request

    def response(scope, method, resource, body=None):
        if (method, resource) == ("POST", "instance"):
            raise nico.NicoError("NICo request outcome is uncertain; inspect the Site before retrying")
        return original(scope, method, resource, body)

    client.request = response
    progress = {}

    with pytest.raises(nico.NicoError, match="requires Site verification"):
        nico.ensure_present(client, progress)

    assert progress == {"outcome": "Uncertain", "instance_id": "", "machine_id": ""}


def rejecting_post(client, status):
    original = client.request

    def response(scope, method, resource, body=None):
        if (method, resource) == ("POST", "instance"):
            raise nico.NicoHTTPError(status, "request rejected")
        return original(scope, method, resource, body)

    client.request = response
    return client


@pytest.mark.parametrize("status", [400, 403, 422])
def test_post_rejected_by_nico_reports_not_created(status):
    client = rejecting_post(FakeClient(params("FNN")), status)
    progress = {}

    with pytest.raises(nico.NicoError, match="NICo rejected the Instance create"):
        nico.ensure_present(client, progress)

    assert progress == {"outcome": "NotCreated", "instance_id": "", "machine_id": ""}


@pytest.mark.parametrize("status", [409, 500, 503])
def test_post_conflict_or_server_error_stays_uncertain(status):
    client = rejecting_post(FakeClient(params("FNN")), status)
    progress = {}

    with pytest.raises(nico.NicoError, match="requires Site verification"):
        nico.ensure_present(client, progress)

    assert progress["outcome"] == "Uncertain"


def test_boot_timeout_after_create_reports_the_ids_to_release(fast_clock):
    # NICo allocated the Machine but the OS never phoned home.
    client = with_instance(FakeClient(params("FNN", timeout=60, poll_interval=10), interfaces=FNN_INTERFACES),
                           status="Provisioning")
    progress = {}

    with pytest.raises(nico.NicoError, match="did not reach Ready before timeout"):
        nico.ensure_present(client, progress)

    assert len(client.posts) == 1
    assert progress == {"outcome": "Created", "instance_id": "instance-id", "machine_id": "machine-id"}


def test_instance_error_after_create_reports_the_ids_to_release():
    client = with_instance(FakeClient(params("FNN"), interfaces=FNN_INTERFACES), status="Error")
    progress = {}

    with pytest.raises(nico.NicoError, match="reached Error"):
        nico.ensure_present(client, progress)

    assert progress == {"outcome": "Created", "instance_id": "instance-id", "machine_id": "machine-id"}


def test_successful_create_reports_created():
    client = FakeClient(params("FNN"), interfaces=FNN_INTERFACES)
    progress = {}

    result = nico.ensure_present(client, progress)

    assert result["instance_id"] == "instance-id"
    assert progress == {"outcome": "Created", "instance_id": "instance-id", "machine_id": "machine-id"}


def test_saved_ids_are_reported_when_the_adopted_instance_fails():
    client = with_instance(
        FakeClient(params("FNN", instance_id="instance-id", machine_id="machine-id"), interfaces=FNN_INTERFACES),
        status="Error")
    progress = {}

    with pytest.raises(nico.NicoError, match="reached Error"):
        nico.ensure_present(client, progress)

    assert client.posts == []
    assert progress == {"outcome": "Created", "instance_id": "instance-id", "machine_id": "machine-id"}
