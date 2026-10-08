import hashlib
import ipaddress
import json
import os
import subprocess
import sys
from pathlib import Path
from types import SimpleNamespace

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
sys.path.insert(0, str(MODULE_UTILS.parents[4]))

import agentless_net_subnet  # noqa: E402

VN_UID = "11111111-1111-4111-8111-111111111111"
SUBNET_A_UID = "22222222-2222-4222-8222-222222222222"
SUBNET_B_UID = "33333333-3333-4333-8333-333333333333"
NAMESPACE = f"n{hashlib.sha256(VN_UID.encode()).hexdigest()[:14]}"


def parent_entry():
    return {"uid": VN_UID, "namespace_name": NAMESPACE}


def subnet_entry(uid, cidr, vlan_id, *, vip_cidr=""):
    subnet = ipaddress.ip_network(cidr)
    dhcp_end = subnet.broadcast_address - 1
    if vip_cidr:
        dhcp_end = ipaddress.ip_network(vip_cidr).network_address - 1
    return {
        "uid": uid,
        "virtual_network_uid": VN_UID,
        "tenant_id": "tenant-a",
        "ipv4_cidr": str(subnet),
        "vlan_id": vlan_id,
        "vlan_interface": f"s{hashlib.sha256(uid.encode()).hexdigest()[:12]}",
        "trunk_interface": "eth1",
        "gateway_ipv4": str(subnet.network_address + 1),
        "dhcp_range_start": str(subnet.network_address + 2),
        "dhcp_range_end": str(dhcp_end),
        "vip_cidr": vip_cidr,
        "phase": "provisioning",
    }


def test_parse_switch_vlan_exclusions_includes_ranges_pvids_and_nvue_reservations():
    bridge = json.dumps(
        [
            {
                "ifname": "bridge",
                "vlans": [{"vlan": 1, "flags": ["PVID", "Egress Untagged"]}],
            },
            {
                "ifname": "swp1",
                "vlans": [{"vlan": 100, "vlanEnd": 102}, {"vlan": 220}],
            },
        ]
    )
    reserved = json.dumps(
        {
            "internal": {"range": {"operational": "3700-3702", "applied": "3700-3702"}},
            "l3-vni-vlan": {
                "begin": {"operational": 4000, "applied": 4001},
                "end": {"operational": 4005, "applied": 4006},
            },
        }
    )

    observed = agentless_net_subnet.parse_switch_vlan_exclusions(bridge, reserved)

    assert observed == {1, 100, 101, 102, 220, 3700, 3701, 3702, 4000, 4001, 4002, 4003, 4004, 4005, 4006}


@pytest.mark.parametrize(
    ("bridge", "reserved"),
    [
        ("not-json", '{"internal":{"range":"10-20"},"l3-vni-vlan":{"begin":30,"end":40}}'),
        ('[{"ifname":"swp1","vlans":[{"vlan":"10"}]}]', '{"internal":{"range":"10-20"},"l3-vni-vlan":{"begin":30,"end":40}}'),
        ('[{"ifname":"swp1","vlans":[{"vlan":0}]}]', '{"internal":{"range":"10-20"},"l3-vni-vlan":{"begin":30,"end":40}}'),
        ('[{"ifname":"swp1","vlans":[]}]', '{"internal":{"range":"bad"},"l3-vni-vlan":{"begin":30,"end":40}}'),
        ('[{"ifname":"swp1","vlans":[]}]', '{"internal":{"range":"10-20"}}'),
    ],
)
def test_parse_switch_vlan_exclusions_rejects_unverifiable_output(bridge, reserved):
    with pytest.raises(agentless_net_subnet.SubnetProviderError):
        agentless_net_subnet.parse_switch_vlan_exclusions(bridge, reserved)


def test_resolve_dhcp_manager_reuses_existing_service_file(tmp_path, monkeypatch):
    paths = {
        "systemd_unit": tmp_path / "agentless-dhcp@unit.service",
        "supervisor_program": tmp_path / "agentless-dhcp-program.ini",
    }
    monkeypatch.setattr(agentless_net_subnet, "_state_paths", lambda uid: paths)
    monkeypatch.setattr(agentless_net_subnet, "_systemd_available", lambda: True)
    monkeypatch.setattr(agentless_net_subnet, "_supervisor_available", lambda: True)
    paths["supervisor_program"].write_text("[program:agentless-dhcp]\n")

    assert agentless_net_subnet.resolve_dhcp_supervisor(VN_UID) == "supervisor"

    paths["systemd_unit"].write_text("[Unit]\n")
    with pytest.raises(agentless_net_subnet.SubnetProviderError, match="both systemd and Supervisor"):
        agentless_net_subnet.resolve_dhcp_supervisor(VN_UID)


@pytest.mark.parametrize(
    ("systemd_available", "supervisor_available", "expected"),
    [(True, True, "systemd"), (False, True, "supervisor")],
)
def test_resolve_dhcp_manager_selects_available_fallback(
    tmp_path, monkeypatch, systemd_available, supervisor_available, expected
):
    paths = {
        "systemd_unit": tmp_path / "agentless-dhcp@unit.service",
        "supervisor_program": tmp_path / "agentless-dhcp-program.ini",
    }
    monkeypatch.setattr(agentless_net_subnet, "_state_paths", lambda uid: paths)
    monkeypatch.setattr(agentless_net_subnet, "_systemd_available", lambda: systemd_available)
    monkeypatch.setattr(agentless_net_subnet, "_supervisor_available", lambda: supervisor_available)

    assert agentless_net_subnet.resolve_dhcp_supervisor(VN_UID) == expected


def test_resolve_dhcp_manager_fails_when_no_manager_is_usable(tmp_path, monkeypatch):
    paths = {
        "systemd_unit": tmp_path / "agentless-dhcp@unit.service",
        "supervisor_program": tmp_path / "agentless-dhcp-program.ini",
    }
    monkeypatch.setattr(agentless_net_subnet, "_state_paths", lambda uid: paths)
    monkeypatch.setattr(agentless_net_subnet, "_systemd_available", lambda: False)
    monkeypatch.setattr(agentless_net_subnet, "_supervisor_available", lambda: False)

    with pytest.raises(agentless_net_subnet.SubnetProviderError, match="neither a running systemd"):
        agentless_net_subnet.resolve_dhcp_supervisor(VN_UID)


def provider_testbed(monkeypatch, tmp_path, *, reject_dnsmasq=False):
    paths = {
        "config": tmp_path / "etc/agentless-net/dhcp",
        "leases": tmp_path / "var/lib/agentless-net/dhcp",
        "runtime": tmp_path / "run/agentless-net/dhcp",
        "logs": tmp_path / "var/log/agentless-net/dhcp",
        "units": tmp_path / "etc/systemd/system",
        "supervisor": tmp_path / "etc/agentless-net/supervisor.d",
    }
    monkeypatch.setattr(agentless_net_subnet, "AGENTLESS_NET_CONFIG_ROOT", tmp_path / "etc/agentless-net")
    monkeypatch.setattr(agentless_net_subnet, "AGENTLESS_NET_STATE_ROOT", tmp_path / "var/lib/agentless-net")
    monkeypatch.setattr(agentless_net_subnet, "AGENTLESS_NET_RUNTIME_ROOT", tmp_path / "run/agentless-net")
    monkeypatch.setattr(agentless_net_subnet, "AGENTLESS_NET_LOG_ROOT", tmp_path / "var/log/agentless-net")
    monkeypatch.setattr(agentless_net_subnet, "DHCP_CONFIG_ROOT", paths["config"])
    monkeypatch.setattr(agentless_net_subnet, "DHCP_LEASE_ROOT", paths["leases"])
    monkeypatch.setattr(agentless_net_subnet, "DHCP_RUNTIME_ROOT", paths["runtime"])
    monkeypatch.setattr(agentless_net_subnet, "DHCP_LOG_ROOT", paths["logs"])
    monkeypatch.setattr(agentless_net_subnet, "SYSTEMD_UNIT_ROOT", paths["units"])
    monkeypatch.setattr(agentless_net_subnet, "SUPERVISOR_PROGRAM_ROOT", paths["supervisor"])
    monkeypatch.setattr(
        agentless_net_subnet,
        "SUPERVISOR_BASE_CONFIG",
        tmp_path / "etc/agentless-net/supervisord.conf",
    )
    monkeypatch.setattr(
        agentless_net_subnet.shutil,
        "which",
        lambda name: {
            "dnsmasq": "/usr/sbin/dnsmasq",
            "ip": "/usr/sbin/ip",
            "ss": "/usr/bin/ss",
        }.get(name),
    )

    host_links = {
        "eth1": {
            "ifname": "eth1",
            "flags": ["BROADCAST", "MULTICAST"],
            "ifalias": "",
        }
    }
    namespace_links = {NAMESPACE: {}}
    addresses = {NAMESPACE: {}}
    commands = []
    service = {
        "active": False,
        "restarts": 0,
        "starts": 0,
        "stops": 0,
        "systemd_restart_pending": False,
        "supervisor_running": False,
        "supervisor_restarts": 0,
        "supervisor_starts": 0,
        "supervisor_stops": 0,
        "supervisor_restart_pending": False,
        "supervisor_starting_statuses": 0,
        "supervisor_stuck_starting": False,
        "on_stop": None,
    }

    def completed(command, *, stdout="", returncode=0, stderr=""):
        return subprocess.CompletedProcess(command, returncode, stdout, stderr)

    def flush_dnsmasq_lease_stream():
        callback = service.pop("on_stop", None)
        if callback is not None:
            callback()

    def fake_run(command, check=True):
        command = list(command)
        commands.append(command)
        if Path(command[0]).name == "dnsmasq" and "--test" in command:
            if reject_dnsmasq:
                raise agentless_net_subnet.network.NetworkCommandError(
                    "dnsmasq failed (exit status 1)"
                )
            return completed(command)
        if Path(command[0]).name == "systemctl":
            if command[1:3] == ["is-active", "--quiet"]:
                return completed(command, returncode=0 if service["active"] else 3)
            if command[1:3] == ["enable", "--now"]:
                service["starts"] += 1
                service["active"] = True
            elif command[1] == "restart":
                flush_dnsmasq_lease_stream()
                service["restarts"] += 1
                service["active"] = True
            elif command[1] == "stop":
                service["stops"] += 1
                service["systemd_restart_pending"] = service["active"]
                flush_dnsmasq_lease_stream()
                service["active"] = False
            elif command[1] == "start":
                service["starts"] += 1
                if service["systemd_restart_pending"]:
                    service["restarts"] += 1
                    service["systemd_restart_pending"] = False
                service["active"] = True
            return completed(command)
        if Path(command[0]).name == "supervisorctl":
            assert command[1:3] == ["-c", str(agentless_net_subnet.SUPERVISOR_BASE_CONFIG)]
            action = command[3]
            if action == "status":
                if service["supervisor_running"]:
                    if service["supervisor_stuck_starting"]:
                        return completed(command, stdout=f"{command[4]} STARTING pid 123\n")
                    if service["supervisor_starting_statuses"] > 0:
                        service["supervisor_starting_statuses"] -= 1
                        return completed(command, stdout=f"{command[4]} STARTING pid 123\n")
                    return completed(command, stdout=f"{command[4]} RUNNING pid 123\n")
                return completed(command, returncode=1)
            if action == "start":
                service["supervisor_starts"] += 1
                if service["supervisor_restart_pending"]:
                    service["supervisor_restarts"] += 1
                    service["supervisor_restart_pending"] = False
                service["supervisor_running"] = True
            elif action == "restart":
                flush_dnsmasq_lease_stream()
                service["supervisor_restarts"] += 1
                service["supervisor_running"] = True
            elif action == "stop":
                service["supervisor_stops"] += 1
                service["supervisor_restart_pending"] = service["supervisor_running"]
                flush_dnsmasq_lease_stream()
                service["supervisor_running"] = False
            elif action == "update":
                assert len(command) == 5
                program_file = paths["supervisor"] / f"{command[4].removeprefix('agentless-dhcp-')}.ini"
                service["supervisor_running"] = program_file.exists()
                if service["supervisor_running"] and service["supervisor_restart_pending"]:
                    service["supervisor_restarts"] += 1
                    service["supervisor_restart_pending"] = False
            return completed(command)

        namespace = None
        local = command
        if len(command) >= 5 and Path(command[0]).name == "ip" and command[1:3] == ["netns", "exec"]:
            namespace = command[3]
            local = command[4:]
        if local and Path(local[0]).name == "ss":
            return completed(command, stdout="UNCONN 0 0 0.0.0.0:67 0.0.0.0:*\n")
        if local == ["true"]:
            return completed(command)
        if local[:4] == ["ip", "-j", "-d", "link"] and local[4:6] == ["show", "dev"]:
            interface = local[-1]
            state = namespace_links.get(namespace, {}) if namespace else host_links
            details = state.get(interface)
            if details is None:
                return completed(command, returncode=1)
            return completed(command, stdout=json.dumps([details]))
        if local[:4] == ["ip", "-j", "-4", "address"] and local[4:6] == ["show", "dev"]:
            interface = local[-1]
            values = addresses.get(namespace, {}).get(interface, [])
            return completed(
                command,
                stdout=json.dumps(
                    [
                        {
                            "ifname": interface,
                            "addr_info": [
                                {
                                    "family": "inet",
                                    "local": address.split("/", 1)[0],
                                    "prefixlen": int(address.split("/", 1)[1]),
                                }
                                for address in values
                            ],
                        }
                    ]
                ),
            )
        if local[:2] == ["ip", "link"] and local[2] == "add":
            trunk, interface = local[4], local[6]
            vlan_id = int(local[-1])
            host_links[interface] = {
                "ifname": interface,
                "flags": ["BROADCAST", "MULTICAST"],
                "ifalias": "",
                "link": trunk,
                "linkinfo": {"info_kind": "vlan", "info_data": {"id": vlan_id}},
            }
            return completed(command)
        if local[:4] == ["ip", "link", "set", "dev"] and namespace is None:
            interface = local[4]
            if "netns" in local:
                target_namespace = local[-1]
                namespace_links.setdefault(target_namespace, {})[interface] = host_links.pop(interface)
            elif "alias" in local:
                host_links[interface]["ifalias"] = local[-1]
            elif "up" in local:
                host_links[interface]["flags"].append("UP")
            return completed(command)
        if local[:2] == ["ip", "link"] and local[2] == "set" and namespace:
            interface = local[4]
            if "up" in local:
                namespace_links[namespace][interface]["flags"].append("UP")
            return completed(command)
        if local[:3] == ["ip", "address", "replace"] and namespace:
            address = local[3]
            interface = local[-1]
            addresses[namespace].setdefault(interface, []).append(address)
            return completed(command)
        if local[:2] == ["ip", "link"] and local[2] == "delete":
            interface = local[-1]
            (namespace_links.get(namespace, {}) if namespace else host_links).pop(interface, None)
            addresses.get(namespace, {}).pop(interface, None)
            return completed(command)
        return completed(command)

    monkeypatch.setattr(agentless_net_subnet.network, "run_command", fake_run)
    return paths, commands, host_links, namespace_links, addresses, service


def test_vlan_interface_is_created_verified_and_removed(monkeypatch, tmp_path):
    _, commands, host_links, namespace_links, addresses, _ = provider_testbed(
        monkeypatch, tmp_path
    )
    entry = subnet_entry(SUBNET_A_UID, "10.20.1.0/24", 100)

    changed = agentless_net_subnet._ensure_subnet_interfaces(
        parent_entry(), [entry]
    )

    assert changed is True
    assert entry["vlan_interface"] not in host_links
    assert entry["vlan_interface"] in namespace_links[NAMESPACE]
    assert namespace_links[NAMESPACE][entry["vlan_interface"]]["linkinfo"]["info_data"]["id"] == 100
    assert namespace_links[NAMESPACE][entry["vlan_interface"]]["ifalias"] == f"osac-subnet:{SUBNET_A_UID}"
    assert addresses[NAMESPACE][entry["vlan_interface"]] == ["10.20.1.1/24"]
    assert any(command[:3] == ["ip", "link", "add"] for command in commands)

    agentless_net_subnet.verify_subnet_interface(NAMESPACE, entry)
    assert agentless_net_subnet.remove_subnet_interface(NAMESPACE, entry) is True
    assert entry["vlan_interface"] not in namespace_links[NAMESPACE]


def test_retry_moves_an_owned_vlan_left_on_the_host_into_the_parent_namespace(monkeypatch, tmp_path):
    _, _, host_links, namespace_links, _, _ = provider_testbed(monkeypatch, tmp_path)
    entry = subnet_entry(SUBNET_A_UID, "10.20.1.0/24", 100)
    host_links[entry["vlan_interface"]] = {
        "ifname": entry["vlan_interface"],
        "flags": ["BROADCAST", "MULTICAST", "UP"],
        "ifalias": f"osac-subnet:{SUBNET_A_UID}",
        "link": "eth1",
        "linkinfo": {"info_kind": "vlan", "info_data": {"id": 100}},
    }

    assert agentless_net_subnet._ensure_subnet_interfaces(parent_entry(), [entry]) is True
    assert entry["vlan_interface"] not in host_links
    assert entry["vlan_interface"] in namespace_links[NAMESPACE]


def test_dhcp_config_contains_sorted_scopes_gateway_and_vip_exclusions(tmp_path):
    paths = agentless_net_subnet._state_paths(VN_UID)
    paths["config"] = tmp_path / "dnsmasq.conf"
    paths["leases"] = tmp_path / "dnsmasq.leases"
    paths["pid"] = tmp_path / "dnsmasq.pid"
    first = subnet_entry(SUBNET_A_UID, "10.20.1.0/24", 100)
    second = subnet_entry(
        SUBNET_B_UID,
        "10.20.2.0/30",
        101,
        vip_cidr="10.20.2.3/32",
    )

    config = agentless_net_subnet._render_dhcp_config(
        parent_entry(), [second, first], paths
    ).decode()

    assert config.index(f"interface={first['vlan_interface']}") < config.index(
        f"interface={second['vlan_interface']}"
    )
    assert "port=0\n" in config
    assert "no-resolv\n" in config
    assert "dhcp-option=option:dns-server\n" in config
    assert "dhcp-range=set:" + first["vlan_interface"] + ",10.20.1.2,10.20.1.254,255.255.255.0,12h" in config
    assert "dhcp-range=set:" + second["vlan_interface"] + ",10.20.2.2,10.20.2.2,255.255.255.252,12h" in config
    assert "option:router,10.20.2.1" in config


def test_dhcp_restart_preserves_existing_valid_lease_bytes(monkeypatch, tmp_path):
    paths, commands, _, _, _, service = provider_testbed(monkeypatch, tmp_path)
    first = subnet_entry(SUBNET_A_UID, "10.20.1.0/24", 100)
    second = subnet_entry(SUBNET_B_UID, "10.20.2.0/24", 101)
    state_paths = agentless_net_subnet._state_paths(VN_UID)
    state_paths["lease_dir"].mkdir(parents=True)
    original_leases = b"1798765432 aa:bb:cc:dd:ee:ff 10.20.1.22 client-a *\n"
    state_paths["leases"].write_bytes(original_leases)
    state_paths["leases"].chmod(0o600)
    state_paths["marker"].write_bytes(b"initialized\n")
    state_paths["marker"].chmod(0o600)

    first_changed = agentless_net_subnet.ensure_subnet_data_plane(
        parent_entry(), [first], "systemd"
    )
    assert first_changed is True
    assert service["active"] is True
    assert state_paths["leases"].read_bytes() == original_leases

    second_changed = agentless_net_subnet.ensure_subnet_data_plane(
        parent_entry(), [first, second], "systemd"
    )

    assert second_changed is True
    assert service["restarts"] == 1
    assert state_paths["leases"].read_bytes() == original_leases
    assert b"10.20.2.2,10.20.2.254" in state_paths["config"].read_bytes()
    assert paths["units"].joinpath(f"agentless-dhcp@{VN_UID}.service").exists()
    lifecycle = [
        command[1]
        for command in commands
        if Path(command[0]).name == "systemctl"
        and command[1] in {"stop", "start", "restart"}
    ]
    assert lifecycle == ["stop", "start"]


@pytest.mark.parametrize("dhcp_supervisor", ["systemd", "supervisor"])
def test_dhcp_prune_stops_dnsmasq_before_reading_and_replacing_open_lease_file(
    monkeypatch, tmp_path, dhcp_supervisor
):
    _, _, _, _, _, service = provider_testbed(monkeypatch, tmp_path)
    first = subnet_entry(SUBNET_A_UID, "10.20.1.0/24", 100)
    removed = subnet_entry(SUBNET_B_UID, "10.20.2.0/24", 101)
    agentless_net_subnet.ensure_subnet_data_plane(
        parent_entry(), [first, removed], dhcp_supervisor
    )

    lease_path = agentless_net_subnet._state_paths(VN_UID)["leases"]
    active_lease = b"1798765432 aa:bb:cc:dd:ee:ff 10.20.1.22 client-a *\n"
    removed_lease = b"1798765432 4a:83:40:2c:97:ca 10.20.2.172 client-b *\n"
    final_active_lease = b"1798765432 aa:bb:cc:dd:ee:11 10.20.1.23 client-a *\n"
    lease_path.write_bytes(active_lease + removed_lease)
    lease_path.chmod(0o600)
    lease_fd = os.open(lease_path, os.O_WRONLY | os.O_APPEND)

    with os.fdopen(lease_fd, "wb", buffering=0) as lease_stream:
        def flush_open_lease_stream():
            os.write(lease_stream.fileno(), final_active_lease)
            lease_stream.close()

        service["on_stop"] = flush_open_lease_stream
        changed = agentless_net_subnet.prepare_subnet_delete_data_plane(
            parent_entry(), removed, [first], dhcp_supervisor
        )

    assert changed is True
    assert lease_path.read_bytes() == active_lease + final_active_lease
    assert service.get("on_stop") is None
    if dhcp_supervisor == "systemd":
        assert service["stops"] == 1
        assert service["restarts"] == 1
        assert service["active"] is True
    else:
        assert service["supervisor_stops"] == 1
        assert service["supervisor_restarts"] == 1
        assert service["supervisor_running"] is True


def active_dhcp_update_testbed(monkeypatch, tmp_path, dhcp_supervisor):
    _, _, _, _, _, service = provider_testbed(monkeypatch, tmp_path)
    first = subnet_entry(SUBNET_A_UID, "10.20.1.0/24", 100)
    removed = subnet_entry(SUBNET_B_UID, "10.20.2.0/24", 101)
    agentless_net_subnet.ensure_subnet_data_plane(
        parent_entry(), [first, removed], dhcp_supervisor
    )
    paths = agentless_net_subnet._state_paths(VN_UID)
    service_path = paths[
        "systemd_unit" if dhcp_supervisor == "systemd" else "supervisor_program"
    ]
    return first, removed, paths, service_path, service


def dhcp_service_is_running(service, dhcp_supervisor):
    return (
        service["active"]
        if dhcp_supervisor == "systemd"
        else service["supervisor_running"]
    )


@pytest.mark.parametrize("dhcp_supervisor", ["systemd", "supervisor"])
def test_dhcp_update_rejects_malformed_leases_before_stopping_service(
    monkeypatch, tmp_path, dhcp_supervisor
):
    first, _, paths, _, service = active_dhcp_update_testbed(
        monkeypatch, tmp_path, dhcp_supervisor
    )
    paths["leases"].write_bytes(b"malformed lease state\n")
    paths["leases"].chmod(0o600)
    original_config = paths["config"].read_bytes()

    with pytest.raises(agentless_net_subnet.SubnetProviderError, match="malformed"):
        agentless_net_subnet.ensure_subnet_data_plane(
            parent_entry(), [first], dhcp_supervisor
        )

    assert dhcp_service_is_running(service, dhcp_supervisor) is True
    if dhcp_supervisor == "systemd":
        assert service["stops"] == 0
    else:
        assert service["supervisor_stops"] == 0
    assert paths["config"].read_bytes() == original_config


@pytest.mark.parametrize("dhcp_supervisor", ["systemd", "supervisor"])
def test_dhcp_update_recovers_if_final_lease_read_fails_after_stop(
    monkeypatch, tmp_path, dhcp_supervisor
):
    first, _, paths, service_path, service = active_dhcp_update_testbed(
        monkeypatch, tmp_path, dhcp_supervisor
    )
    lease_path = paths["leases"]
    active_lease = b"1798765432 aa:bb:cc:dd:ee:ff 10.20.1.22 client-a *\n"
    final_lease = b"1798765432 aa:bb:cc:dd:ee:11 10.20.1.23 client-a *\n"
    lease_path.write_bytes(active_lease)
    lease_path.chmod(0o600)
    original_config = paths["config"].read_bytes()
    original_service = service_path.read_bytes()

    def flush_open_lease_stream():
        with lease_path.open("ab") as stream:
            stream.write(final_lease)

    service["on_stop"] = flush_open_lease_stream
    ensure_lease_file = agentless_net_subnet._ensure_lease_file
    injected = False

    def fail_after_stop(lease_paths):
        nonlocal injected
        if not dhcp_service_is_running(service, dhcp_supervisor) and not injected:
            injected = True
            raise agentless_net_subnet.SubnetProviderError(
                "injected final lease read failure"
            )
        return ensure_lease_file(lease_paths)

    monkeypatch.setattr(agentless_net_subnet, "_ensure_lease_file", fail_after_stop)
    with pytest.raises(
        agentless_net_subnet.SubnetProviderError, match="injected final lease"
    ):
        agentless_net_subnet.ensure_subnet_data_plane(
            parent_entry(), [first], dhcp_supervisor
        )

    assert injected is True
    assert dhcp_service_is_running(service, dhcp_supervisor) is True
    assert lease_path.read_bytes() == active_lease + final_lease
    assert paths["config"].read_bytes() == original_config
    assert service_path.read_bytes() == original_service


@pytest.mark.parametrize("dhcp_supervisor", ["systemd", "supervisor"])
def test_dhcp_update_recovers_if_stop_raises_after_stopping_service(
    monkeypatch, tmp_path, dhcp_supervisor
):
    first, _, paths, _, service = active_dhcp_update_testbed(
        monkeypatch, tmp_path, dhcp_supervisor
    )
    lease_path = paths["leases"]
    active_lease = b"1798765432 aa:bb:cc:dd:ee:ff 10.20.1.22 client-a *\n"
    final_lease = b"1798765432 aa:bb:cc:dd:ee:11 10.20.1.23 client-a *\n"
    lease_path.write_bytes(active_lease)
    lease_path.chmod(0o600)
    stop_service = agentless_net_subnet._stop_dhcp_service

    def flush_open_lease_stream():
        with lease_path.open("ab") as stream:
            stream.write(final_lease)

    service["on_stop"] = flush_open_lease_stream

    def stop_then_raise(supervisor, service_name):
        stop_service(supervisor, service_name)
        raise agentless_net_subnet.SubnetProviderError("injected stop failure")

    monkeypatch.setattr(agentless_net_subnet, "_stop_dhcp_service", stop_then_raise)
    with pytest.raises(agentless_net_subnet.SubnetProviderError, match="injected stop"):
        agentless_net_subnet.ensure_subnet_data_plane(
            parent_entry(), [first], dhcp_supervisor
        )

    assert dhcp_service_is_running(service, dhcp_supervisor) is True
    assert lease_path.read_bytes() == active_lease + final_lease


@pytest.mark.parametrize("dhcp_supervisor", ["systemd", "supervisor"])
def test_dhcp_update_restores_snapshot_if_service_definition_write_fails(
    monkeypatch, tmp_path, dhcp_supervisor
):
    first, _, paths, service_path, service = active_dhcp_update_testbed(
        monkeypatch, tmp_path, dhcp_supervisor
    )
    lease_path = paths["leases"]
    active_lease = b"1798765432 aa:bb:cc:dd:ee:ff 10.20.1.22 client-a *\n"
    removed_lease = b"1798765432 4a:83:40:2c:97:ca 10.20.2.172 client-b *\n"
    final_lease = b"1798765432 aa:bb:cc:dd:ee:11 10.20.1.23 client-a *\n"
    lease_path.write_bytes(active_lease + removed_lease)
    lease_path.chmod(0o600)
    paths["config"].chmod(0o640)
    service_path.chmod(0o640)
    original_files = {
        path: (path.read_bytes(), path.stat().st_mode & 0o777)
        for path in (paths["config"], lease_path, service_path)
    }
    # dnsmasq flushes this valid lease to the open file during shutdown.
    original_files[lease_path] = (active_lease + removed_lease + final_lease, 0o600)

    def flush_open_lease_stream():
        with lease_path.open("ab") as stream:
            stream.write(final_lease)

    service["on_stop"] = flush_open_lease_stream
    atomic_write = agentless_net_subnet._atomic_write
    injected = False

    def fail_after_service_file_write(path, content, *, mode=0o600):
        nonlocal injected
        if path == service_path and not injected:
            injected = True
            atomic_write(path, b"partially installed service definition\n", mode=mode)
            raise agentless_net_subnet.SubnetProviderError(
                "injected service file write failure"
            )
        return atomic_write(path, content, mode=mode)

    monkeypatch.setattr(
        agentless_net_subnet, "_atomic_write", fail_after_service_file_write
    )
    with pytest.raises(
        agentless_net_subnet.SubnetProviderError, match="injected service file"
    ):
        agentless_net_subnet.ensure_subnet_data_plane(
            parent_entry(), [first], dhcp_supervisor
        )

    assert injected is True
    assert dhcp_service_is_running(service, dhcp_supervisor) is True
    assert {
        path: (path.read_bytes(), path.stat().st_mode & 0o777)
        for path in original_files
    } == original_files
    assert lease_path.read_bytes().endswith(final_lease)


@pytest.mark.parametrize("dhcp_supervisor", ["systemd", "supervisor"])
def test_dhcp_update_restores_snapshot_if_start_fails_after_taking_effect(
    monkeypatch, tmp_path, dhcp_supervisor
):
    first, _, paths, service_path, service = active_dhcp_update_testbed(
        monkeypatch, tmp_path, dhcp_supervisor
    )
    lease_path = paths["leases"]
    lease_path.write_bytes(b"1798765432 aa:bb:cc:dd:ee:ff 10.20.1.22 client-a *\n")
    lease_path.chmod(0o600)
    original_files = {
        path: (path.read_bytes(), path.stat().st_mode & 0o777)
        for path in (paths["config"], lease_path, service_path)
    }
    run = agentless_net_subnet._run
    injected = False

    def fail_after_start(command, check=True):
        nonlocal injected
        result = run(command, check=check)
        starts_service = (
            Path(command[0]).name == "systemctl" and command[1] == "start"
        ) or (
            Path(command[0]).name == "supervisorctl" and command[3] == "start"
        )
        if starts_service and not injected:
            injected = True
            raise agentless_net_subnet.SubnetProviderError(
                "injected service start failure"
            )
        return result

    monkeypatch.setattr(agentless_net_subnet, "_run", fail_after_start)
    with pytest.raises(
        agentless_net_subnet.SubnetProviderError, match="injected service start"
    ):
        agentless_net_subnet._ensure_dhcp_service(
            parent_entry(),
            [first],
            dhcp_supervisor,
            paths,
            interface_changed=True,
        )

    assert injected is True
    assert dhcp_service_is_running(service, dhcp_supervisor) is True
    assert {
        path: (path.read_bytes(), path.stat().st_mode & 0o777)
        for path in original_files
    } == original_files


@pytest.mark.parametrize("dhcp_supervisor", ["systemd", "supervisor"])
def test_dhcp_update_does_not_start_service_if_snapshot_restore_fails(
    monkeypatch, tmp_path, dhcp_supervisor
):
    first, _, paths, service_path, service = active_dhcp_update_testbed(
        monkeypatch, tmp_path, dhcp_supervisor
    )
    atomic_write = agentless_net_subnet._atomic_write
    writes = 0

    def fail_service_writes(path, content, *, mode=0o600):
        nonlocal writes
        if path == service_path:
            writes += 1
            if writes == 1:
                atomic_write(
                    path, b"partially installed service definition\n", mode=mode
                )
                raise agentless_net_subnet.SubnetProviderError("injected update failure")
            raise agentless_net_subnet.SubnetProviderError("injected restore failure")
        return atomic_write(path, content, mode=mode)

    monkeypatch.setattr(agentless_net_subnet, "_atomic_write", fail_service_writes)
    with pytest.raises(
        agentless_net_subnet.SubnetProviderError,
        match="service recovery did not complete",
    ):
        agentless_net_subnet.ensure_subnet_data_plane(
            parent_entry(), [first], dhcp_supervisor
        )

    assert writes == 2
    assert dhcp_service_is_running(service, dhcp_supervisor) is False


def test_dhcp_prune_closes_lease_stream_if_provider_raises_before_stop_callback(
    monkeypatch, tmp_path
):
    first, removed, paths, _, service = active_dhcp_update_testbed(
        monkeypatch, tmp_path, "systemd"
    )
    lease_path = paths["leases"]
    lease_path.write_bytes(b"1798765432 aa:bb:cc:dd:ee:ff 10.20.1.22 client-a *\n")
    lease_path.chmod(0o600)
    lease_fd = os.open(lease_path, os.O_WRONLY | os.O_APPEND)

    def fail_before_stop(parent, target, remaining, supervisor):
        raise agentless_net_subnet.SubnetProviderError("injected provider failure")

    monkeypatch.setattr(
        agentless_net_subnet,
        "prepare_subnet_delete_data_plane",
        fail_before_stop,
    )
    with os.fdopen(lease_fd, "wb", buffering=0) as lease_stream:

        def flush_open_lease_stream():
            os.write(
                lease_stream.fileno(),
                b"1798765432 aa:bb:cc:dd:ee:11 10.20.1.23 client-a *\n",
            )
            lease_stream.close()

        service["on_stop"] = flush_open_lease_stream
        with pytest.raises(
            agentless_net_subnet.SubnetProviderError, match="injected provider"
        ):
            agentless_net_subnet.prepare_subnet_delete_data_plane(
                parent_entry(), removed, [first], "systemd"
            )

    assert lease_stream.closed is True
    assert service["on_stop"] is not None


@pytest.mark.parametrize("dhcp_supervisor", ["systemd", "supervisor"])
def test_dhcp_service_noop_does_not_stop_or_restart_running_service(
    monkeypatch, tmp_path, dhcp_supervisor
):
    _, commands, _, _, _, service = provider_testbed(monkeypatch, tmp_path)
    first = subnet_entry(SUBNET_A_UID, "10.20.1.0/24", 100)
    agentless_net_subnet.ensure_subnet_data_plane(
        parent_entry(), [first], dhcp_supervisor
    )
    commands.clear()

    changed = agentless_net_subnet.ensure_subnet_data_plane(
        parent_entry(), [first], dhcp_supervisor
    )

    assert changed is False
    if dhcp_supervisor == "systemd":
        assert service["active"] is True
        assert service["stops"] == 0
        assert service["starts"] == 1
        assert not any(
            Path(command[0]).name == "systemctl"
            and command[1] in {"stop", "start", "restart", "enable"}
            for command in commands
        )
    else:
        assert service["supervisor_running"] is True
        assert service["supervisor_stops"] == 0
        assert service["supervisor_starts"] == 0
        assert not any(
            Path(command[0]).name == "supervisorctl"
            and command[3] in {"stop", "start", "restart", "update"}
            for command in commands
        )


def test_removed_subnet_leases_are_pruned_before_range_reuse(monkeypatch, tmp_path):
    paths, _, _, namespace_links, _, service = provider_testbed(monkeypatch, tmp_path)
    first = subnet_entry(SUBNET_A_UID, "10.20.1.0/24", 100)
    removed = subnet_entry(SUBNET_B_UID, "10.20.2.0/24", 101)
    replacement = subnet_entry(
        "44444444-4444-4444-8444-444444444444", "10.20.2.0/30", 101
    )
    agentless_net_subnet.ensure_subnet_data_plane(
        parent_entry(), [first, removed], "supervisor"
    )
    lease_path = agentless_net_subnet._state_paths(VN_UID)["leases"]
    active_lease = b"1798765432 aa:bb:cc:dd:ee:ff 10.20.1.22 client-a *\n"
    removed_subnet_lease = (
        b"1798765432 4a:83:40:2c:97:ca 10.20.2.172 client-b "
        b"01:4a:83:40:2c:97:ca\n"
    )
    lease_path.write_bytes(active_lease + removed_subnet_lease)
    lease_path.chmod(0o600)

    changed = agentless_net_subnet.prepare_subnet_delete_data_plane(
        parent_entry(), removed, [first], "supervisor"
    )

    assert changed is True
    assert lease_path.read_bytes() == active_lease
    assert removed["vlan_interface"] not in namespace_links[NAMESPACE]
    assert service["supervisor_restarts"] == 1

    agentless_net_subnet.ensure_subnet_data_plane(
        parent_entry(), [first, replacement], "supervisor"
    )

    assert lease_path.read_bytes() == active_lease
    expected_range = (
        f"dhcp-range=set:{replacement['vlan_interface']},"
        "10.20.2.2,10.20.2.2,255.255.255.252,12h"
    ).encode()
    config = paths["config"].joinpath(VN_UID, "dnsmasq.conf").read_bytes()
    assert expected_range in config
    assert service["supervisor_restarts"] == 2


def test_supervisor_mode_restarts_only_the_changed_vn_program(monkeypatch, tmp_path):
    paths, commands, _, _, _, service = provider_testbed(monkeypatch, tmp_path)
    first = subnet_entry(SUBNET_A_UID, "10.20.1.0/24", 100)
    second = subnet_entry(SUBNET_B_UID, "10.20.2.0/24", 101)

    agentless_net_subnet.ensure_subnet_data_plane(
        parent_entry(), [first], "supervisor"
    )
    agentless_net_subnet.ensure_subnet_data_plane(
        parent_entry(), [first, second], "supervisor"
    )

    program = f"agentless-dhcp-{VN_UID}"
    assert service["supervisor_running"] is True
    assert service["supervisor_restarts"] == 1
    assert paths["supervisor"].joinpath(f"{VN_UID}.ini").exists()
    assert all(command[1:3] == ["-c", str(agentless_net_subnet.SUPERVISOR_BASE_CONFIG)] for command in commands if Path(command[0]).name == "supervisorctl")
    assert [command[4] for command in commands if Path(command[0]).name == "supervisorctl" and command[3] == "update"] == [program]

    agentless_net_subnet.cleanup_virtual_network_dhcp(
        VN_UID, NAMESPACE, "supervisor", remove_leases=False
    )
    assert service["supervisor_running"] is False
    assert not paths["supervisor"].joinpath(f"{VN_UID}.ini").exists()
    assert not paths["config"].joinpath(VN_UID, "dnsmasq.conf").exists()


def test_supervisor_mode_waits_for_new_program_to_reach_running(monkeypatch, tmp_path):
    _, commands, _, _, _, service = provider_testbed(monkeypatch, tmp_path)
    first = subnet_entry(SUBNET_A_UID, "10.20.1.0/24", 100)
    now = [0.0]

    def sleep(seconds):
        now[0] += seconds

    monkeypatch.setattr(
        agentless_net_subnet,
        "time",
        SimpleNamespace(monotonic=lambda: now[0], sleep=sleep),
        raising=False,
    )
    service["supervisor_starting_statuses"] = 1

    changed = agentless_net_subnet.ensure_subnet_data_plane(
        parent_entry(), [first], "supervisor"
    )

    status_checks = [
        command
        for command in commands
        if Path(command[0]).name == "supervisorctl" and command[3] == "status"
    ]
    assert changed is True
    assert service["supervisor_running"] is True
    assert len(status_checks) == 3
    assert now[0] > 0


def test_supervisor_mode_fails_if_program_never_reaches_running(monkeypatch, tmp_path):
    _, _, _, _, _, service = provider_testbed(monkeypatch, tmp_path)
    first = subnet_entry(SUBNET_A_UID, "10.20.1.0/24", 100)
    now = [0.0]

    def sleep(seconds):
        now[0] += seconds

    monkeypatch.setattr(
        agentless_net_subnet,
        "time",
        SimpleNamespace(monotonic=lambda: now[0], sleep=sleep),
        raising=False,
    )
    service["supervisor_stuck_starting"] = True

    with pytest.raises(
        agentless_net_subnet.SubnetProviderError,
        match="AgentlessNet DHCP Supervisor program is not running",
    ):
        agentless_net_subnet.ensure_subnet_data_plane(
            parent_entry(), [first], "supervisor"
        )

    assert now[0] == pytest.approx(5.0)


def test_malformed_lease_state_fails_without_echoing_contents(tmp_path, monkeypatch):
    paths = {
        "lease_dir": tmp_path / "leases",
        "leases": tmp_path / "leases/dnsmasq.leases",
        "marker": tmp_path / "leases/.initialized",
    }
    paths["lease_dir"].mkdir()
    sensitive = b"not-a-lease dummy-sensitive-marker"
    paths["leases"].write_bytes(sensitive)
    paths["leases"].chmod(0o600)
    paths["marker"].write_bytes(b"initialized\n")
    paths["marker"].chmod(0o600)

    with pytest.raises(agentless_net_subnet.SubnetProviderError) as error:
        agentless_net_subnet._ensure_lease_file(paths)

    assert "malformed" in str(error.value)
    assert b"dummy-sensitive-marker" not in str(error.value).encode()


def test_initialized_missing_lease_file_fails_without_reinitializing(tmp_path):
    paths = {
        "lease_dir": tmp_path / "leases",
        "leases": tmp_path / "leases/dnsmasq.leases",
        "marker": tmp_path / "leases/.initialized",
    }
    paths["lease_dir"].mkdir()
    paths["marker"].write_bytes(b"initialized\n")
    paths["marker"].chmod(0o600)

    with pytest.raises(agentless_net_subnet.SubnetProviderError, match="lease file is missing"):
        agentless_net_subnet._ensure_lease_file(paths)

    assert not paths["leases"].exists()


def test_bad_dnsmasq_candidate_is_rejected_before_config_install(monkeypatch, tmp_path):
    _, _, _, _, _, _ = provider_testbed(
        monkeypatch, tmp_path, reject_dnsmasq=True
    )
    entry = subnet_entry(SUBNET_A_UID, "10.20.1.0/24", 100)
    state_paths = agentless_net_subnet._state_paths(VN_UID)
    state_paths["config"].parent.mkdir(parents=True)
    previous = b"previous valid config\n"
    state_paths["config"].write_bytes(previous)
    state_paths["config"].chmod(0o600)

    with pytest.raises(agentless_net_subnet.SubnetProviderError, match="dnsmasq rejected"):
        agentless_net_subnet.ensure_subnet_data_plane(
            parent_entry(), [entry], "systemd"
        )

    assert state_paths["config"].read_bytes() == previous
    assert not list(state_paths["config"].parent.glob("*.candidate.*"))
