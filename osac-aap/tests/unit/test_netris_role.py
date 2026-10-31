"""Tests of the shared contract helpers of the Netris Fabric Manager role.

The pure rules live in plugins/filter/netris_contract.py and are tested
directly. The helper task files (_settings, _input, _network_type, _names,
_fail, _rescue, _noop, _publish) run through real Ansible in a child process;
the failure sanitizer case talks to a small in-process HTTP server that plays
a failing Netris controller.
"""

from __future__ import annotations

import http.server
import importlib.util
import json
import os
import subprocess
import sys
import threading
from pathlib import Path

import pytest
import yaml

ROOT = Path(__file__).resolve().parents[2]
ROLE = ROOT / "collections/ansible_collections/osac/templates/roles/netris"
FILTER = ROOT / "collections/ansible_collections/osac/templates/plugins/filter/netris_contract.py"
ANSIBLE_PLAYBOOK = Path(sys.executable).parent / "ansible-playbook"

VN = "3f1c9a2e-7b4d-4e8a-9c6f-0d2e4b6a8c10"
SUBNET = "8a2b4c6d-1e3f-4a5b-9c7d-2e4f6a8b0c12"
POOL = "5d6a0f1e-2b3c-4d5e-8f90-a1b2c3d4e5f6"
EIP = "7b6a5c4d-3e2f-4a1b-9c8d-7e6f5a4b3c2d"
MANAGER = {"name": "netris", "type": "fabric", "ansible_role": "osac.templates.netris"}

GOOD_ENV = {
    "NETRIS_CONTROLLER_URL": "https://netris.example.test",
    "NETRIS_USERNAME": "osac",
    "NETRIS_PASSWORD": "pw-not-a-secret",
    "NETRIS_SITE_ID": "1",
    "NETRIS_TENANT_ID": "2",
    "NETRIS_MGMT_VPC_ID": "3",
    "NETRIS_BM_PROVISIONING_VNET": "provisioning",
    "NETRIS_RESOURCE_CLASS_MAP": '{"gpu": {"server_cluster_template_id": 7}}',
}

_spec = importlib.util.spec_from_file_location("netris_contract_filter", FILTER)
assert _spec and _spec.loader
nc = importlib.util.module_from_spec(_spec)
_spec.loader.exec_module(nc)


def snap(kind: str, api_id: str) -> dict:
    return {"kind": kind, "id": api_id, "metadata": {"name": "x"}, "spec": {}}


def job(task: str = "create_subnet", **overrides) -> dict:
    base = {
        "manager": MANAGER,
        "task": task,
        "resource": snap("Subnet", SUBNET),
        "virtual_network": snap("VirtualNetwork", VN),
    }
    base.update(overrides)
    return base


# ---------------------------------------------------------------------------
# Pure rules
# ---------------------------------------------------------------------------

SETTINGS = {
    "controller_url": "https://netris.example.test",
    "username": "osac",
    "has_password": True,
    "site_id": 1,
    "tenant_id": 2,
    "mgmt_vpc_id": 3,
    "bm_provisioning_vnet": "provisioning",
    "resource_class_map": '{"gpu": {}}',
}


def test_all_groups_valid() -> None:
    assert nc.netris_contract_settings_error(SETTINGS, list(nc.SETTING_GROUPS)) == ""


@pytest.mark.parametrize(
    ("group", "key", "value", "variable"),
    [
        ("controller", "controller_url", "", "NETRIS_CONTROLLER_URL"),
        ("controller", "controller_url", "netris.example.test", "NETRIS_CONTROLLER_URL"),
        ("credentials", "username", "", "NETRIS_USERNAME"),
        ("credentials", "has_password", False, "NETRIS_PASSWORD"),
        ("site", "site_id", 0, "NETRIS_SITE_ID"),
        ("tenant", "tenant_id", 0, "NETRIS_TENANT_ID"),
        ("mgmt_vpc", "mgmt_vpc_id", 0, "NETRIS_MGMT_VPC_ID"),
        ("bm_provisioning_vnet", "bm_provisioning_vnet", "", "NETRIS_BM_PROVISIONING_VNET"),
        ("resource_class_map", "resource_class_map", "{}", "NETRIS_RESOURCE_CLASS_MAP"),
        ("resource_class_map", "resource_class_map", "[1]", "NETRIS_RESOURCE_CLASS_MAP"),
        ("resource_class_map", "resource_class_map", "{not json", "NETRIS_RESOURCE_CLASS_MAP"),
    ],
)
def test_a_bad_setting_is_named(group: str, key: str, value, variable: str) -> None:
    message = nc.netris_contract_settings_error({**SETTINGS, key: value}, [group])
    assert message.startswith("Missing or malformed setting in network-fulfillment-ig: ")
    assert variable in message


def test_only_required_groups_are_checked() -> None:
    assert nc.netris_contract_settings_error({}, []) == ""
    assert nc.netris_contract_settings_error({**SETTINGS, "controller_url": ""}, ["site"]) == ""


def test_unknown_group_is_a_programming_error() -> None:
    with pytest.raises(ValueError):
        nc.netris_contract_settings_error(SETTINGS, ["nope"])


def test_input_accepts_a_subnet_task() -> None:
    out = nc.netris_contract_input(job(), ["virtual_network"])
    assert out["error"] is None
    assert (out["resource_id"], out["resource_kind"]) == (SUBNET, "Subnet")
    assert (out["virtual_network_id"], out["subnet_id"], out["external_ip_pool_id"]) == (VN, SUBNET, "")


def test_input_resource_fills_its_own_parent_id() -> None:
    out = nc.netris_contract_input(job("create_virtual_network", resource=snap("VirtualNetwork", VN)), [])
    assert out["virtual_network_id"] == VN
    pool = nc.netris_contract_input({"resource": snap("ExternalIPPool", POOL)}, [])
    assert pool["external_ip_pool_id"] == POOL


@pytest.mark.parametrize(
    "bad",
    [
        {"resource": None},
        {"resource": {"kind": "Subnet", "id": "not-a-uuid"}},
        {"resource": {"kind": "Subnet", "id": SUBNET.upper()}},
        {"resource": {"kind": "", "id": SUBNET}},
        {"virtual_network": {"id": "x"}},
        {"virtual_network": "text"},
        {"subnet": {"id": 5}},
    ],
)
def test_malformed_input_is_an_error(bad: dict) -> None:
    out = nc.netris_contract_input({**job(), **bad}, [])
    assert out["error"] and out["error"].startswith("Netris invalid task input")


def test_missing_required_parent_is_an_error() -> None:
    out = nc.netris_contract_input({"resource": snap("ExternalIP", EIP)}, ["external_ip_pool"])
    assert "external_ip_pool is required" in out["error"]


@pytest.mark.parametrize("value", [None, "evpn"])
def test_network_type_absent_or_evpn(value) -> None:
    assert nc.netris_network_type_error({"network_type": value}) == ""
    assert nc.netris_network_type_error({}) == ""


@pytest.mark.parametrize("value", ["vlan", "", "EVPN", 5])
def test_other_network_types_are_errors(value) -> None:
    assert "evpn only" in nc.netris_network_type_error({"network_type": value})


def test_names_follow_d7() -> None:
    ids = nc.netris_contract_input(job(), ["virtual_network"])
    names = nc.netris_contract_names(ids)
    assert names["vpc"] == names["vn_ipam"] == f"osac-vn-{VN}"
    assert "dns_snat" not in names
    assert names["vnet"] == names["subnet_ipam"] == f"osac-sn-{SUBNET}"
    assert names["pool_ipam"] == ""
    pool = nc.netris_contract_names(nc.netris_contract_input({"resource": snap("ExternalIPPool", POOL)}, []))
    assert pool["pool_ipam"] == f"osac-pool-{POOL}"
    eip = nc.netris_contract_names(nc.netris_contract_input({"resource": snap("ExternalIP", EIP)}, []))
    assert eip["natgw_snat"] == f"osac-natgw-{EIP}"
    assert eip["eipa_dnat"] == f"osac-eipa-{EIP}"
    assert eip["server_cluster"] == f"osac-fd-{EIP}"
    assert eip["eip_reservation"] == f"osac-eip-{EIP}"


def test_longest_name_is_47_characters() -> None:
    names = nc.netris_contract_names(nc.netris_contract_input({"resource": snap("ExternalIP", EIP)}, []))
    assert max(len(n) for n in names.values()) == len("osac-natgw-") + 36 == 47


def test_names_of_unknown_ids_are_empty() -> None:
    assert set(nc.netris_contract_names({}).values()) == {""}


def test_result_shapes() -> None:
    out = nc.netris_contract_result({
        "backend_id": 12, "l3_vni": "10001",
        "network_segment": {"type": "evpn", "l2_vni": "10100"},
        "external_ip": {"address": "198.51.100.7"},
        "dhcp_lease": {"ip_address": "10.0.1.20", "mac_address": "52:54:00:aa:bb:cc"},
    })
    assert out == {
        "backend_id": "12", "l3_vni": 10001,
        "network_segment": {"type": "evpn", "l2_vni": 10100},
        "external_ip": {"address": "198.51.100.7"},
        "dhcp_lease": {"ip_address": "10.0.1.20", "mac_address": "52:54:00:aa:bb:cc"},
    }
    assert isinstance(out["l3_vni"], int) and isinstance(out["backend_id"], str)
    assert nc.netris_contract_result({"backend_id": "5", "l3_vni": None}) == {"backend_id": "5"}
    with pytest.raises(ValueError):
        nc.netris_contract_result({"vni": 1})


# ---------------------------------------------------------------------------
# Role files
# ---------------------------------------------------------------------------

HELPERS = ["_settings", "_input", "_network_type", "_names", "_fail", "_rescue", "_noop", "_publish"]


@pytest.mark.parametrize("name", HELPERS)
def test_helper_file_exists_and_parses(name: str) -> None:
    path = ROLE / "tasks" / f"{name}.yaml"
    assert path.is_file()
    assert isinstance(yaml.safe_load(path.read_text()), list)


# Settings removed with the per-resource DNS.
REMOVED_DNS_SETTINGS = ("NETRIS_DNS_RESOLVERS", "NETRIS_DNS_SNAT_CIDR")


def test_defaults_carry_every_setting() -> None:
    text = (ROLE / "defaults/main.yaml").read_text()
    for variable in ("NETRIS_SITE_ID", "NETRIS_TENANT_ID", "NETRIS_MGMT_VPC_ID", "NETRIS_BM_PROVISIONING_VNET",
                     "NETRIS_RESOURCE_CLASS_MAP",
                     "NETRIS_CONTROLLER_URL", "NETRIS_USERNAME", "NETRIS_PASSWORD"):
        assert variable in text
    for dropped in (*REMOVED_DNS_SETTINGS, "NETRIS_VNI_RANGE", "NETRIS_HUB_AS", "NETRIS_DHCP_OPTION_SET_ID",
                    "NETRIS_PROVISIONING_VNET"):
        assert dropped not in text


# ---------------------------------------------------------------------------
# Helper task files through real Ansible
# ---------------------------------------------------------------------------


def run_helper(tmp_path: Path, steps: list[dict], job_vars: dict, env: dict | None = None,
               probe: dict | None = None, extra: dict | None = None) -> tuple[int, dict]:
    """Run steps in a block with the role's _rescue as its rescue, as a task file does."""
    tasks = [{"name": "step", "ansible.builtin.include_role": {"name": "osac.templates.netris", "tasks_from": s["from"]},
              **({"vars": s["vars"]} if "vars" in s else {})} for s in steps]
    if probe:
        tasks.append({"name": "probe", "ansible.builtin.set_stats": {
            "aggregate": False, "per_host": False, "data": {"probe": probe}}})
    play = [{
        "hosts": "localhost", "gather_facts": False,
        "tasks": [{"name": "run", "block": tasks, "rescue": [
            {"name": "rescue", "ansible.builtin.include_role": {"name": "osac.templates.netris", "tasks_from": "_rescue"}}]}],
    }]
    playbook = tmp_path / "play.yaml"
    playbook.write_text(yaml.safe_dump(play))
    extra_vars = tmp_path / "extra.json"
    extra_vars.write_text(json.dumps({"osac_job_vars": job_vars, **(extra or {})}))
    environ = {k: v for k, v in os.environ.items() if not k.startswith("NETRIS_")}
    environ.update({
        "ANSIBLE_STDOUT_CALLBACK": "ansible.posix.json",
        "ANSIBLE_SHOW_CUSTOM_STATS": "true",
        "ANSIBLE_LOCALHOST_WARNING": "False",
        "ANSIBLE_INVENTORY_UNPARSED_WARNING": "False",
        "ANSIBLE_PYTHON_INTERPRETER": sys.executable,
    })
    environ.update(env if env is not None else GOOD_ENV)
    proc = subprocess.run(
        [str(ANSIBLE_PLAYBOOK), "-i", "localhost,", "-c", "local", str(playbook), "-e", f"@{extra_vars}"],
        cwd=ROOT, env=environ, capture_output=True, text=True, timeout=300,
    )
    doc = json.loads(proc.stdout[proc.stdout.index("{"):])
    return proc.returncode, doc.get("global_custom_stats") or {}


def assert_error(rc: int, stats: dict, retryable: bool) -> str:
    assert rc != 0
    err = stats["osac_error"]
    assert set(err) == {"message", "retryable"}
    assert err["retryable"] is retryable
    assert "osac_result" not in stats
    return err["message"]


def test_settings_pass(tmp_path: Path) -> None:
    rc, stats = run_helper(tmp_path, [{"from": "_settings", "vars": {"netris_require": list(nc.SETTING_GROUPS)}}], job())
    assert rc == 0 and "osac_error" not in stats


@pytest.mark.parametrize("variable", sorted(GOOD_ENV))
def test_a_missing_setting_is_retryable_and_named(tmp_path: Path, variable: str) -> None:
    env = {k: v for k, v in GOOD_ENV.items() if k != variable}
    rc, stats = run_helper(tmp_path, [{"from": "_settings", "vars": {"netris_require": list(nc.SETTING_GROUPS)}}], job(), env)
    message = assert_error(rc, stats, retryable=True)
    assert "Missing or malformed setting in network-fulfillment-ig" in message
    assert variable in message
    assert "pw-not-a-secret" not in message


def test_a_setting_the_task_does_not_need_is_not_checked(tmp_path: Path) -> None:
    rc, _ = run_helper(tmp_path, [{"from": "_settings", "vars": {"netris_require": ["site"]}}], job(),
                       {"NETRIS_SITE_ID": "1"})
    assert rc == 0


def test_input_and_names_set_the_facts(tmp_path: Path) -> None:
    rc, stats = run_helper(
        tmp_path,
        [{"from": "_input", "vars": {"netris_require_parents": ["virtual_network"]}}, {"from": "_names"}],
        job(), probe={"in": "{{ netris_in }}", "names": "{{ netris_names }}"},
    )
    assert rc == 0
    assert stats["probe"]["in"]["subnet_id"] == SUBNET
    assert stats["probe"]["names"]["vnet"] == f"osac-sn-{SUBNET}"
    assert stats["probe"]["names"]["vpc"] == f"osac-vn-{VN}"


def test_malformed_input_is_final(tmp_path: Path) -> None:
    rc, stats = run_helper(tmp_path, [{"from": "_input"}], job(resource={"kind": "Subnet", "id": "nope"}))
    message = assert_error(rc, stats, retryable=False)
    assert "resource.id" in message


def test_network_type_evpn_and_absent(tmp_path: Path) -> None:
    for jv, want in ((job(network_type="evpn"), "evpn"), (job(), "")):
        rc, stats = run_helper(tmp_path, [{"from": "_network_type"}], jv, probe={"nt": "{{ netris_network_type }}"})
        assert rc == 0 and stats["probe"]["nt"] == want


def test_other_network_type_is_final(tmp_path: Path) -> None:
    rc, stats = run_helper(tmp_path, [{"from": "_network_type"}], job(network_type="vlan"))
    assert "evpn only" in assert_error(rc, stats, retryable=False)


@pytest.mark.parametrize("retryable", [True, False])
def test_fail_publishes_the_classified_error(tmp_path: Path, retryable: bool) -> None:
    fail = {"message": "Netris says no", "retryable": retryable}
    rc, stats = run_helper(tmp_path, [{"from": "_fail", "vars": {"netris_fail": fail}}], job())
    assert stats["osac_error"] == fail
    assert rc != 0


def test_noop_publishes_nothing(tmp_path: Path) -> None:
    rc, stats = run_helper(tmp_path, [{"from": "_noop", "vars": {"netris_noop": "apply an ACL"}}], job())
    assert rc == 0
    assert "osac_result" not in stats and "osac_error" not in stats


def test_publish_shapes_the_result(tmp_path: Path) -> None:
    result = {"backend_id": 7, "l3_vni": "20001", "network_segment": {"type": "evpn", "l2_vni": "10100"}}
    rc, stats = run_helper(tmp_path, [{"from": "_publish", "vars": {"netris_result": result}}], job())
    assert rc == 0
    assert stats["osac_result"] == {"backend_id": "7", "l3_vni": 20001,
                                    "network_segment": {"type": "evpn", "l2_vni": 10100}}
    assert "osac_error" not in stats


class _FailingNetris(http.server.BaseHTTPRequestHandler):
    def do_GET(self) -> None:  # noqa: N802
        body = json.dumps({"message": "overlap connect.sid=s%3Aabc123; boom", "internal": "LEAKED-BODY"}).encode()
        self.send_response(500)
        self.send_header("Content-Type", "application/json")
        self.send_header("Set-Cookie", "connect.sid=COOKIE-VALUE")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def log_message(self, *args) -> None:
        pass


def test_unclassified_failure_is_retryable_and_sanitized(tmp_path: Path) -> None:
    server = http.server.HTTPServer(("127.0.0.1", 0), _FailingNetris)
    threading.Thread(target=server.serve_forever, daemon=True).start()
    try:
        url = f"http://127.0.0.1:{server.server_port}/api/x"
        step = {"name": "Read the V-Net", "ansible.builtin.uri": {"url": url, "headers": {"Cookie": "connect.sid=SENT"}}}
        play = [{"hosts": "localhost", "gather_facts": False, "tasks": [{"name": "run", "block": [step], "rescue": [
            {"name": "rescue", "ansible.builtin.include_role": {"name": "osac.templates.netris", "tasks_from": "_rescue"}}]}]}]
        playbook = tmp_path / "play.yaml"
        playbook.write_text(yaml.safe_dump(play))
        extra = tmp_path / "extra.json"
        extra.write_text(json.dumps({"osac_job_vars": job("create_subnet")}))
        proc = subprocess.run(
            [str(ANSIBLE_PLAYBOOK), "-i", "localhost,", "-c", "local", str(playbook), "-e", f"@{extra}"],
            cwd=ROOT, capture_output=True, text=True, timeout=300,
            env={**os.environ, "ANSIBLE_STDOUT_CALLBACK": "ansible.posix.json", "ANSIBLE_SHOW_CUSTOM_STATS": "true",
                 "ANSIBLE_PYTHON_INTERPRETER": sys.executable, "ANSIBLE_LOCALHOST_WARNING": "False"},
        )
    finally:
        server.shutdown()
    stats = json.loads(proc.stdout[proc.stdout.index("{"):]).get("global_custom_stats") or {}
    message = assert_error(proc.returncode, stats, retryable=True)
    assert "create_subnet" in message and "Read the V-Net" in message and "HTTP 500" in message
    for secret in ("COOKIE-VALUE", "LEAKED-BODY", "abc123", "SENT"):
        assert secret not in message


def test_class_map_accepts_text_or_mapping() -> None:
    for value in ('{"gpu": {}}', {"gpu": {}}):
        assert nc.netris_contract_settings_error({**SETTINGS, "resource_class_map": value}, ["resource_class_map"]) == ""
    assert nc.netris_contract_settings_error({**SETTINGS, "resource_class_map": {}}, ["resource_class_map"])
