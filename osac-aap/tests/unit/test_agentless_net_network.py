import subprocess
import sys
from pathlib import Path

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


def test_ensure_uplink_creates_pair_addresses_and_default_route(monkeypatch):
    commands = []
    link_probes = {}

    def run(command, check=True):
        commands.append(command)
        if command == ["ip", "netns", "list"]:
            return subprocess.CompletedProcess(command, 0, stdout="vn-test\\n", stderr="")
        if command[:3] == ["ip", "-o", "link"] and "show" in command:
            interface = command[-1]
            link_probes[interface] = link_probes.get(interface, 0) + 1
            if link_probes[interface] == 1:
                return subprocess.CompletedProcess(command, 1, stdout="", stderr="")
            return subprocess.CompletedProcess(
                command, 0, stdout=f"2: {interface}: <BROADCAST,UP,LOWER_UP>\\n", stderr=""
            )
        if command[:2] == ["ip", "netns"] and "link" in command:
            interface = command[-1]
            link_probes[interface] = link_probes.get(interface, 0) + 1
            if link_probes[interface] == 1:
                return subprocess.CompletedProcess(command, 1, stdout="", stderr="")
            return subprocess.CompletedProcess(
                command, 0, stdout=f"2: {interface}: <BROADCAST,UP,LOWER_UP>\\n", stderr=""
            )
        if "address" in command or command[-2:] == ["show", "default"]:
            return subprocess.CompletedProcess(command, 0, stdout="", stderr="")
        return subprocess.CompletedProcess(command, 0, stdout="", stderr="")

    monkeypatch.setattr(agentless_net_network, "run_command", run)

    changed = agentless_net_network.ensure_uplink(
        "vn-test", "vn-ns", "vn-host", "10.20.0.1/30", "10.20.0.2/30", "10.20.0.2"
    )

    assert changed is True
    assert ["ip", "link", "add", "vn-host", "type", "veth", "peer", "name", "vn-ns"] in commands
    assert ["ip", "link", "set", "vn-ns", "netns", "vn-test"] in commands
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
        "10.20.0.2",
        "dev",
        "vn-ns",
    ] in commands


def test_ensure_ipv4_forwarding_only_changes_namespace(monkeypatch):
    commands = []

    def run(command, check=True):
        commands.append(command)
        if "sysctl" in command and "-n" in command:
            return subprocess.CompletedProcess(command, 0, stdout="0\\n", stderr="")
        return subprocess.CompletedProcess(command, 0, stdout="", stderr="")

    monkeypatch.setattr(agentless_net_network, "run_command", run)

    changed = agentless_net_network.ensure_ipv4_forwarding("vn-test")

    assert changed is True
    assert [
        "ip",
        "netns",
        "exec",
        "vn-test",
        "sysctl",
        "-w",
        "net.ipv4.ip_forward=1",
    ] in commands
    assert ["sysctl", "-w", "net.ipv4.ip_forward=1"] not in commands


def test_delete_uplink_is_idempotent_when_already_absent(monkeypatch):
    def run(command, check=True):
        if command == ["ip", "netns", "list"]:
            return subprocess.CompletedProcess(command, 0, stdout="", stderr="")
        return subprocess.CompletedProcess(command, 1, stdout="", stderr="not found")

    monkeypatch.setattr(agentless_net_network, "run_command", run)

    assert agentless_net_network.delete_uplink("vn-test", "vn-host") is False
