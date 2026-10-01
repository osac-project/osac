import json
import subprocess
import sys
from pathlib import Path

import pytest

MODULE_UTILS = (
    Path(__file__).resolve().parents[2]
    / "collections"
    / "ansible_collections"
    / "osac"
    / "templates"
    / "plugins"
    / "module_utils"
)
sys.path.insert(0, str(MODULE_UTILS))

import agentless_net_network  # noqa: E402


def completed(command, stdout="", returncode=0):
    return subprocess.CompletedProcess(command, returncode, stdout=stdout, stderr="")


def test_configure_uplink_sets_endpoints_and_default_route_on_expected_interface(monkeypatch):
    commands = []

    def run(command, check=True):
        commands.append(command)
        if "address" in command and "show" in command:
            interface = command[-1]
            address = "10.20.0.12" if interface == "vn-host" else "10.20.0.13"
            if interface == "vn-ns":
                address = "10.20.0.130"  # Similar text must not satisfy exact-address checks.
            return completed(
                command,
                json.dumps(
                    [
                        {
                            "ifname": interface,
                            "addr_info": [
                                {"family": "inet", "local": address, "prefixlen": 31}
                            ],
                        }
                    ]
                ),
            )
        if "-j" in command and "link" in command:
            return completed(
                command,
                json.dumps(
                    [{"ifname": command[-1], "flags": ["BROADCAST", "UP"], "ifalias": ""}]
                ),
            )
        if "route" in command and "show" in command:
            return completed(
                command,
                json.dumps([{"dst": "default", "gateway": "10.20.0.12", "dev": "other"}]),
            )
        return completed(command)

    monkeypatch.setattr(agentless_net_network, "run_command", run)
    changed = agentless_net_network.configure_uplink(
        "vn-test", "vn-ns", "vn-host", "10.20.0.13/31", "10.20.0.12/31", "10.20.0.12"
    )

    assert changed is True
    assert [
        "ip",
        "address",
        "replace",
        "10.20.0.12/31",
        "dev",
        "vn-host",
    ] in commands
    assert [
        "ip",
        "netns",
        "exec",
        "vn-test",
        "ip",
        "address",
        "replace",
        "10.20.0.13/31",
        "dev",
        "vn-ns",
    ] in commands
    assert [
        "ip",
        "netns",
        "exec",
        "vn-test",
        "ip",
        "route",
        "replace",
        "default",
        "via",
        "10.20.0.12",
        "dev",
        "vn-ns",
    ] in commands


def test_address_and_default_route_helpers_require_exact_parsed_fields(monkeypatch):
    def run(command, check=True):
        if "address" in command:
            return completed(
                command,
                json.dumps(
                    [
                        {
                            "ifname": "vn-host",
                            "addr_info": [
                                {"family": "inet", "local": "10.0.0.10", "prefixlen": 31}
                            ],
                        }
                    ]
                ),
            )
        return completed(
            command,
            json.dumps([{"dst": "default", "gateway": "10.0.0.0", "dev": "other"}]),
        )

    monkeypatch.setattr(agentless_net_network, "run_command", run)
    assert not agentless_net_network._address_present(None, "vn-host", "10.0.0.1/31")
    assert not agentless_net_network._default_route_present("vn", "10.0.0.0", "vn-host")


def test_link_status_uses_json_flags_and_rejects_malformed_state(monkeypatch):
    monkeypatch.setattr(
        agentless_net_network,
        "run_command",
        lambda command, check=True: completed(
            command,
            json.dumps([{"ifname": "vn-host", "flags": ["BROADCAST", "UP"]}]),
        ),
    )
    assert agentless_net_network._link_is_up(None, "vn-host") is True

    monkeypatch.setattr(
        agentless_net_network,
        "run_command",
        lambda command, check=True: completed(
            command,
            json.dumps([{"ifname": "vn-host", "flags": ["BROADCAST"]}]),
        ),
    )
    assert agentless_net_network._link_is_up(None, "vn-host") is False

    monkeypatch.setattr(
        agentless_net_network,
        "run_command",
        lambda command, check=True: completed(command, "not json"),
    )
    with pytest.raises(agentless_net_network.NetworkCommandError, match="parse link details JSON"):
        agentless_net_network.link_details(None, "vn-host")


def test_ensure_veth_pair_sets_uid_ownership_alias(monkeypatch):
    commands = []

    def run(command, check=True):
        commands.append(command)
        if command == ["ip", "netns", "list"]:
            return completed(command, "")
        if command[:4] == ["ip", "-o", "link", "show"]:
            return completed(command, returncode=1)
        if command[:5] == ["ip", "netns", "exec", "vn-test", "ip"]:
            return completed(command, returncode=1)
        return completed(command)

    monkeypatch.setattr(agentless_net_network, "run_command", run)
    assert agentless_net_network.ensure_veth_pair(
        "vn-test", "vn-ns", "vn-host", owner_alias="osac-vn:11111111-1111-4111-8111-111111111111"
    ) is True

    assert ["ip", "link", "add", "vn-host", "type", "veth", "peer", "name", "vn-ns"] in commands
    assert ["ip", "link", "set", "vn-ns", "netns", "vn-test"] in commands
    assert [
        "ip",
        "link",
        "set",
        "dev",
        "vn-host",
        "alias",
        "osac-vn:11111111-1111-4111-8111-111111111111",
    ] in commands


def test_ensure_veth_pair_rejects_foreign_host_interface(monkeypatch):
    def run(command, check=True):
        if command == ["ip", "netns", "list"]:
            return completed(command, "vn-test\n")
        if command[:4] == ["ip", "-o", "link", "show"]:
            return completed(command)
        if command[:5] == ["ip", "netns", "exec", "vn-test", "ip"]:
            return completed(command, returncode=1)
        if command[:4] == ["ip", "-j", "-d", "link"]:
            return completed(
                command,
                json.dumps(
                    [
                        {
                            "ifname": "vn-host",
                            "flags": ["UP"],
                            "ifalias": "osac-vn:another-uid",
                            "linkinfo": {"info_kind": "veth"},
                        }
                    ]
                ),
            )
        return completed(command)

    monkeypatch.setattr(agentless_net_network, "run_command", run)
    with pytest.raises(agentless_net_network.NetworkCommandError, match="alias does not match"):
        agentless_net_network.ensure_veth_pair(
            "vn-test", "vn-ns", "vn-host", owner_alias="osac-vn:expected-uid"
        )


def test_run_command_bounds_subprocess_and_reports_timeout(monkeypatch):
    def timeout(command, **kwargs):
        assert kwargs["timeout"] == agentless_net_network.COMMAND_TIMEOUT_SECONDS
        raise subprocess.TimeoutExpired(command, kwargs["timeout"])

    monkeypatch.setattr(agentless_net_network.subprocess, "run", timeout)
    with pytest.raises(agentless_net_network.NetworkCommandError, match="timed out after 30"):
        agentless_net_network.run_command(["ip", "netns", "list"])


def test_delete_uplink_is_idempotent_when_already_absent(monkeypatch):
    def run(command, check=True):
        if command == ["ip", "netns", "list"]:
            return completed(command, "")
        return completed(command, returncode=1)

    monkeypatch.setattr(agentless_net_network, "run_command", run)
    assert agentless_net_network.delete_uplink("vn-test", "vn-host") is False
