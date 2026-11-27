"""Contracts for the reusable Kind Netris fake lifecycle."""

from __future__ import annotations

import importlib.util
import json
import subprocess
from pathlib import Path
from unittest.mock import Mock

import pytest

FIXTURE_PATH = (
    Path(__file__).resolve().parents[1]
    / "integration"
    / "fixtures"
    / "netris"
    / "kind_fixture.py"
)
SPEC = importlib.util.spec_from_file_location("netris_kind_fixture_test", FIXTURE_PATH)
assert SPEC and SPEC.loader
kind_fixture = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(kind_fixture)

FIXTURE_DIR = FIXTURE_PATH.parent


def test_start_creates_a_private_service_with_the_existing_fake(monkeypatch):
    calls = []

    def run(command, *, input=None, text, capture_output, check, timeout=None):
        calls.append((command, input, timeout))
        return subprocess.CompletedProcess(command, 0, stdout="", stderr="")

    monkeypatch.setattr(kind_fixture.subprocess, "run", run)
    fixture = kind_fixture.NetrisKindFixture(
        name="netris-fake-test",
        namespace="osac-tests",
        image="registry.example/osac-aap:test",
        kubectl="kubectl",
        readiness_timeout=15,
    )

    assert fixture.start() == "http://netris-fake-test.osac-tests.svc.cluster.local:8080"

    created = [
        json.loads(payload)
        for command, payload, _ in calls
        if "create" in command
    ]
    assert [resource["kind"] for resource in created] == ["ConfigMap", "Service", "Deployment"]
    run_ids = {
        resource["metadata"]["labels"]["osac.openshift.io/test-run-id"]
        for resource in created
    }
    assert len(run_ids) == 1
    assert all(
        timeout == fixture.readiness_timeout
        for command, _, timeout in calls
        if "create" in command
    )
    config_map, service, deployment = created
    assert config_map["data"]["fake_netris.py"] == (
        FIXTURE_DIR / "fake_netris.py"
    ).read_text(encoding="utf-8")
    assert service["spec"]["type"] == "ClusterIP"
    assert deployment["spec"]["template"]["spec"]["containers"][0]["image"] == (
        "registry.example/osac-aap:test"
    )
    container = deployment["spec"]["template"]["spec"]["containers"][0]
    assert container["args"] == [
        "--host",
        "0.0.0.0",
        "--port",
        "8080",
        "--port-file",
        "/state/port",
        "--state-file",
        "/state/state.json",
    ]
    assert any(
        "rollout" in command and "status" in command
        for command, _, _ in calls
    )


def test_start_cleans_up_a_resource_persisted_before_create_times_out(monkeypatch):
    calls = []
    server_resources = {}

    def run(command, *, input=None, text, capture_output, check, timeout=None):
        calls.append((command, input, timeout))
        if "create" in command:
            resource = json.loads(input)
            resource_type = resource["kind"].lower()
            server_resources[(resource_type, resource["metadata"]["name"])] = resource
            if resource["kind"] == "Service":
                raise subprocess.TimeoutExpired(command, timeout)
            return subprocess.CompletedProcess(command, 0, stdout="", stderr="")
        if "delete" in command:
            resource_types = set(command[command.index("delete") + 1].split(","))
            selector = command[command.index("--selector") + 1]
            selected_labels = dict(item.split("=", 1) for item in selector.split(","))
            for key, resource in list(server_resources.items()):
                labels = resource["metadata"]["labels"]
                if key[0] in resource_types and all(
                    labels.get(label) == value for label, value in selected_labels.items()
                ):
                    del server_resources[key]
        return subprocess.CompletedProcess(command, 0, stdout="", stderr="")

    monkeypatch.setattr(kind_fixture.subprocess, "run", run)
    fixture = kind_fixture.NetrisKindFixture(
        name="netris-fake-test",
        namespace="osac-tests",
        image="registry.example/osac-aap:test",
        kubectl="kubectl",
        readiness_timeout=15,
    )

    with pytest.raises(subprocess.TimeoutExpired):
        fixture.start()

    create_calls = [call for call in calls if "create" in call[0]]
    assert [timeout for _, _, timeout in create_calls] == [15, 15]
    service = json.loads(create_calls[-1][1])
    run_id = service["metadata"]["labels"]["osac.openshift.io/test-run-id"]

    delete = next(command for command, _, _ in calls if "delete" in command)
    selector_index = delete.index("--selector")
    assert delete[delete.index("delete") + 1 : selector_index] == [
        "configmap,service,deployment"
    ]
    assert f"osac.openshift.io/test-run-id={run_id}" in delete[selector_index + 1]
    assert server_resources == {}


def test_start_cleanup_preserves_prior_run_resources_with_the_same_name(monkeypatch):
    fixture = kind_fixture.NetrisKindFixture(
        name="netris-fake-test",
        namespace="osac-tests",
        image="registry.example/osac-aap:test",
        kubectl="kubectl",
    )
    prior_resources = kind_fixture.build_resources(
        fixture.name,
        fixture.namespace,
        fixture.image,
        run_id="prior-run",
    )
    server_resources = {
        (resource["kind"].lower(), fixture.name): resource
        for resource in prior_resources
    }
    calls = []

    def run(command, *, input=None, text, capture_output, check, timeout=None):
        calls.append(command)
        if "create" in command:
            resource = json.loads(input)
            key = (resource["kind"].lower(), resource["metadata"]["name"])
            if key in server_resources:
                return subprocess.CompletedProcess(
                    command,
                    1,
                    stdout="",
                    stderr=f'Error from server (AlreadyExists): {key[0]} "{key[1]}" already exists',
                )
            server_resources[key] = resource
        elif "delete" in command:
            resource_types = set(command[command.index("delete") + 1].split(","))
            selector = command[command.index("--selector") + 1]
            selected_labels = dict(item.split("=", 1) for item in selector.split(","))
            for key, resource in list(server_resources.items()):
                labels = resource["metadata"]["labels"]
                if key[0] in resource_types and all(
                    labels.get(label) == value for label, value in selected_labels.items()
                ):
                    del server_resources[key]
        return subprocess.CompletedProcess(command, 0, stdout="", stderr="")

    monkeypatch.setattr(kind_fixture.subprocess, "run", run)

    with pytest.raises(RuntimeError, match="AlreadyExists"):
        fixture.start()

    assert len(server_resources) == len(prior_resources)
    assert all(
        resource["metadata"]["labels"]["osac.openshift.io/test-run-id"] == "prior-run"
        for resource in server_resources.values()
    )
    delete = next(command for command in calls if "delete" in command)
    assert f"osac.openshift.io/test-run-id={fixture._run_id}" in delete[
        delete.index("--selector") + 1
    ]


def test_admin_port_forward_listens_on_loopback(monkeypatch):
    calls = []

    def run(command, *, input=None, text, capture_output, check, timeout=None):
        return subprocess.CompletedProcess(command, 0, stdout="", stderr="")

    class PortForward:
        def poll(self):
            return 0

    def popen(command, *, stdout, stderr, text):
        calls.append(command)
        return PortForward()

    monkeypatch.setattr(kind_fixture.subprocess, "run", run)
    monkeypatch.setattr(kind_fixture.subprocess, "Popen", popen)
    fixture = kind_fixture.NetrisKindFixture(
        name="netris-fake-test",
        namespace="osac-tests",
        image="registry.example/osac-aap:test",
        kubectl="kubectl",
    )
    monkeypatch.setattr(fixture, "_wait_for_admin", Mock())

    fixture.start()
    admin_url = fixture.start_admin_forward()
    fixture.stop_port_forward()

    assert admin_url.startswith("http://127.0.0.1:")
    assert "service/netris-fake-test" in calls[0]
    assert "--address=127.0.0.1" in calls[0]


def test_profile_command_gets_network_settings_and_always_cleans_up(monkeypatch):
    fixture = kind_fixture.NetrisKindFixture(
        name="netris-fake-test",
        namespace="osac-tests",
        image="registry.example/osac-aap:test",
        kubectl="kubectl",
    )
    monkeypatch.setattr(
        fixture,
        "start",
        Mock(return_value="http://netris-fake-test.osac-tests.svc.cluster.local:8080"),
    )

    def start_admin_forward():
        fixture.admin_url = "http://127.0.0.1:18080"
        return fixture.admin_url

    monkeypatch.setattr(fixture, "start_admin_forward", Mock(side_effect=start_admin_forward))
    monkeypatch.setattr(fixture, "stop_port_forward", Mock())
    stop = Mock()
    monkeypatch.setattr(fixture, "stop", stop)
    run = Mock(return_value=subprocess.CompletedProcess(["profile"], 17))
    monkeypatch.setattr(kind_fixture.subprocess, "run", run)

    assert kind_fixture.run_profile_command(fixture, ["profile"]) == 17

    environment = run.call_args.kwargs["env"]
    assert environment["NETRIS_CONTROLLER_URL"].endswith(".svc.cluster.local:8080")
    assert environment["NETRIS_USERNAME"] == "osac"
    assert environment["NETRIS_PASSWORD"]
    assert environment["NETRIS_FAKE_ADMIN_URL"] == "http://127.0.0.1:18080"
    stop.assert_called_once_with()
    fixture.stop_port_forward.assert_called_once_with()


def test_profile_command_cleans_up_when_the_command_cannot_start(monkeypatch):
    fixture = kind_fixture.NetrisKindFixture(
        name="netris-fake-test",
        namespace="osac-tests",
        image="registry.example/osac-aap:test",
        kubectl="kubectl",
    )
    monkeypatch.setattr(fixture, "start", Mock(return_value=fixture.service_url))

    def start_admin_forward():
        fixture.admin_url = "http://127.0.0.1:18080"
        return fixture.admin_url

    monkeypatch.setattr(fixture, "start_admin_forward", Mock(side_effect=start_admin_forward))
    monkeypatch.setattr(fixture, "stop_port_forward", Mock())
    stop = Mock()
    monkeypatch.setattr(fixture, "stop", stop)
    monkeypatch.setattr(
        kind_fixture.subprocess,
        "run",
        Mock(side_effect=FileNotFoundError("missing")),
    )

    with pytest.raises(FileNotFoundError, match="missing"):
        kind_fixture.run_profile_command(fixture, ["missing-profile-command"])

    stop.assert_called_once_with()


def test_stop_command_deletes_the_named_resources_without_an_image(monkeypatch):
    calls = []

    def run(command, *, input=None, text, capture_output, check, timeout=None):
        calls.append(command)
        return subprocess.CompletedProcess(command, 0, stdout="", stderr="")

    monkeypatch.setattr(kind_fixture.subprocess, "run", run)

    assert (
        kind_fixture.main(
            ["stop", "--namespace", "osac-tests", "--name", "netris-fake-test"]
        )
        == 0
    )

    delete = calls[0]
    selector_index = delete.index("--selector")
    assert delete[delete.index("delete") + 1 : selector_index] == [
        "configmap,service,deployment"
    ]
    assert "configmap/netris-fake-test" not in delete
    assert any("app.kubernetes.io/instance=netris-fake-test" in arg for arg in calls[0])
    assert "--cascade=foreground" in delete
    assert "--ignore-not-found=true" in calls[0]


@pytest.mark.parametrize("name", ["Uppercase", "bad/name", "a" * 64])
def test_fixture_rejects_invalid_resource_names_before_kubectl(name):
    with pytest.raises(ValueError, match="DNS label"):
        kind_fixture.NetrisKindFixture(
            name=name,
            namespace="osac-tests",
            image="registry.example/osac-aap:test",
            kubectl="kubectl",
        )
