"""Tests for the dns.rfc2136.dns DNS driver.

Each case runs the real driver role (and its community.general.nsupdate
calls) in a child ansible-playbook process against an in-memory RFC 2136
server (``rfc2136_fake_server.py``) that verifies TSIG, prerequisites and the
zone. No BIND, Kubernetes or AAP is involved. The static tests at the end parse
the shipped task files to keep credentials and record data out of logs.
"""

from __future__ import annotations

import json
import os
import subprocess
import sys
from pathlib import Path

import pytest
import yaml

sys.path.insert(0, str(Path(__file__).parent))
from rfc2136_fake_server import FakeRFC2136Server, free_port, random_secret  # noqa: E402

ROOT = Path(__file__).parents[2]
ANSIBLE_PLAYBOOK = Path(sys.executable).parent / "ansible-playbook"
ROLE = ROOT / "collections" / "ansible_collections" / "dns" / "rfc2136" / "roles" / "dns"

ZONE = "clusters.example.test"
KEY_NAME = "osac-update"
CLUSTER = "c1a2b3"
API_IP = "192.0.2.10"
INGRESS_IP = "192.0.2.20"

# The same three records the CaaS external_access step manages.
CAAS_RECORDS = [
    {"name": f"api.{CLUSTER}.{ZONE}", "addr": API_IP},
    {"name": f"api-int.{CLUSTER}.{ZONE}", "addr": API_IP},
    {"name": f"*.apps.{CLUSTER}.{ZONE}", "addr": INGRESS_IP},
]

PLAYBOOK = """
- name: Drive the DNS driver like the CaaS external_access step
  hosts: localhost
  gather_facts: false
  tasks:
    - name: Run DNS driver entry point
      ansible.builtin.include_role:
        name: "{{ driver_role }}"
        tasks_from: "{{ operation }}"
      vars:
        dns_zone: "{{ zone }}"
        dns_record_name: "{{ item.name }}"
        dns_record_type: A
        dns_record_ttl: "{{ ttl | default(1800) }}"
        dns_record_value: "{{ item.addr }}"
        dns_record_overwrite: "{{ overwrite | default(true) }}"
      loop: "{{ records }}"
      loop_control:
        label: dns-record
      # The CaaS callers set a literal no_log: true on this loop. A templated
      # value would not hide the include's own result, so run() substitutes
      # the literal.
      no_log: CALLER_NO_LOG
"""


@pytest.fixture
def secret():
    return random_secret()


@pytest.fixture
def server(secret):
    with FakeRFC2136Server(f"{ZONE}.", KEY_NAME, secret) as srv:
        yield srv


def driver_env(server, secret, **overrides):
    env = {
        "DNS_RFC2136_SERVER": "127.0.0.1",
        "DNS_RFC2136_PORT": str(server.port),
        "DNS_RFC2136_KEY_NAME": KEY_NAME,
        "DNS_RFC2136_KEY_SECRET": secret,
    }
    env.update(overrides)
    return {key: value for key, value in env.items() if value is not None}


def run(tmp_path, operation, env, records=None, role="dns.rfc2136.dns", caller_no_log=False, **extra):
    playbook = tmp_path / "play.yml"
    playbook.write_text(PLAYBOOK.replace("CALLER_NO_LOG", "true" if caller_no_log else "false"))
    extra_vars = tmp_path / "vars.json"
    extra_vars.write_text(
        json.dumps(
            {
                "driver_role": role,
                "operation": operation,
                "zone": ZONE,
                "records": CAAS_RECORDS if records is None else records,
                **extra,
            }
        )
    )
    environ = {k: v for k, v in os.environ.items() if not k.startswith("DNS_RFC2136_")}
    environ.update(
        {
            "ANSIBLE_LOCALHOST_WARNING": "False",
            "ANSIBLE_INVENTORY_UNPARSED_WARNING": "False",
            "ANSIBLE_PYTHON_INTERPRETER": sys.executable,
            "ANSIBLE_NOCOLOR": "1",
            **env,
        }
    )
    proc = subprocess.run(
        [str(ANSIBLE_PLAYBOOK), "-i", "localhost,", "-c", "local", str(playbook), "-e", f"@{extra_vars}"],
        cwd=ROOT,
        env=environ,
        capture_output=True,
        text=True,
        timeout=300,
    )
    return proc.returncode, proc.stdout + proc.stderr


def caas_records_on(server, api=API_IP, ingress=INGRESS_IP, ttl=1800):
    return {
        (f"api.{CLUSTER}.{ZONE}.", "A"): (ttl, [api]),
        (f"api-int.{CLUSTER}.{ZONE}.", "A"): (ttl, [api]),
        (f"*.apps.{CLUSTER}.{ZONE}.", "A"): (ttl, [ingress]),
    }


def assert_no_leaks(output, secret):
    assert secret not in output
    for record in CAAS_RECORDS:
        assert record["name"] not in output
    assert API_IP not in output
    assert INGRESS_IP not in output


# --- Record lifecycle --------------------------------------------------------


def test_create_rerun_update_and_delete_caas_records(tmp_path, server, secret):
    env = driver_env(server, secret)

    rc, out = run(tmp_path, "create", env)
    assert rc == 0, out
    assert server.records() == caas_records_on(server)
    assert len(server.state.applied_updates) == 3
    assert_no_leaks(out, secret)

    rc, out = run(tmp_path, "create", env)
    assert rc == 0, out
    assert len(server.state.applied_updates) == 3, "an unchanged rerun must not send updates"

    moved = [dict(r, addr="192.0.2.99") if r["name"].startswith("*.") else r for r in CAAS_RECORDS]
    rc, out = run(tmp_path, "create", env, records=moved)
    assert rc == 0, out
    assert server.records() == caas_records_on(server, ingress="192.0.2.99")

    rc, out = run(tmp_path, "delete", env)
    assert rc == 0, out
    assert server.records() == {}
    assert_no_leaks(out, secret)

    rc, out = run(tmp_path, "delete", env)
    assert rc == 0, out


def test_record_names_with_trailing_dot_and_upper_case_stay_in_zone(tmp_path, server, secret):
    records = [{"name": f"API.{CLUSTER}.{ZONE.upper()}.", "addr": API_IP}]
    rc, out = run(tmp_path, "create", driver_env(server, secret), records=records, zone=f"{ZONE}.")
    assert rc == 0, out
    assert server.records() == {(f"api.{CLUSTER}.{ZONE}.", "A"): (1800, [API_IP])}


def test_ttl_change_updates_record(tmp_path, server, secret):
    env = driver_env(server, secret)
    assert run(tmp_path, "create", env, ttl=1800)[0] == 0
    rc, out = run(tmp_path, "create", env, ttl=60)
    assert rc == 0, out
    assert server.records() == caas_records_on(server, ttl=60)


def test_udp_and_non_default_algorithm(tmp_path):
    secret = random_secret()
    with FakeRFC2136Server(f"{ZONE}.", KEY_NAME, secret, algorithm="hmac-sha512") as server:
        env = driver_env(server, secret, DNS_RFC2136_PROTOCOL="UDP", DNS_RFC2136_KEY_ALGORITHM="hmac-sha512")
        rc, out = run(tmp_path, "create", env)
        assert rc == 0, out
        assert server.records() == caas_records_on(server)


def test_dispatcher_selects_rfc2136_driver(tmp_path, server, secret):
    rc, out = run(tmp_path, "create", driver_env(server, secret), role="dns.api.dns", dns_class="dns.rfc2136.dns")
    assert rc == 0, out
    assert server.records() == caas_records_on(server)


def test_overwrite_false_keeps_existing_record(tmp_path, server, secret):
    server.add_record(f"api.{CLUSTER}.{ZONE}.", "A", 1800, "198.51.100.1")
    rc, out = run(tmp_path, "create", driver_env(server, secret), records=CAAS_RECORDS[:1], overwrite=False)
    assert rc != 0
    assert "already exists and dns_record_overwrite is false" in out
    assert server.records() == {(f"api.{CLUSTER}.{ZONE}.", "A"): (1800, ["198.51.100.1"])}


def test_overwrite_false_creates_missing_record(tmp_path, server, secret):
    rc, out = run(tmp_path, "create", driver_env(server, secret), records=CAAS_RECORDS[:1], overwrite=False)
    assert rc == 0, out
    assert server.records() == {(f"api.{CLUSTER}.{ZONE}.", "A"): (1800, [API_IP])}


# --- Server-side failures ----------------------------------------------------


def test_wrong_tsig_secret_fails_without_changes(tmp_path, server, secret):
    wrong = random_secret()
    rc, out = run(tmp_path, "create", driver_env(server, secret, DNS_RFC2136_KEY_SECRET=wrong))
    assert rc != 0
    assert "rejected the TSIG signature" in out
    assert server.records() == {}
    assert_no_leaks(out, wrong)


def test_unknown_key_name_fails(tmp_path, server, secret):
    rc, out = run(tmp_path, "delete", driver_env(server, secret, DNS_RFC2136_KEY_NAME="other-key"))
    assert rc != 0
    assert "rejected the TSIG signature" in out


def test_algorithm_mismatch_fails(tmp_path, server, secret):
    rc, out = run(tmp_path, "create", driver_env(server, secret, DNS_RFC2136_KEY_ALGORITHM="hmac-sha512"))
    assert rc != 0
    assert "rejected the TSIG signature" in out


def test_invalid_base64_secret_fails(tmp_path, server, secret):
    rc, out = run(tmp_path, "create", driver_env(server, secret, DNS_RFC2136_KEY_SECRET="not*base64"))
    assert rc != 0
    assert "not a valid base64 TSIG secret" in out
    assert "not*base64" not in out


def test_refused_delete_fails(tmp_path, server, secret):
    """The server answers the existence check but refuses the delete itself."""
    env = driver_env(server, secret)
    assert run(tmp_path, "create", env)[0] == 0
    server.state.refuse_updates = True
    rc, out = run(tmp_path, "delete", env)
    assert rc != 0
    assert "(REFUSED)" in out
    assert server.records() == caas_records_on(server)


def test_refused_existence_check_on_delete_is_not_reported_as_success(tmp_path, server, secret):
    """nsupdate alone reports a REFUSED existence check as 'already absent'.

    The module returns changed=false without failing, so only the driver's
    response-code check turns this into a failure and keeps the records from
    being left behind silently.
    """
    env = driver_env(server, secret)
    assert run(tmp_path, "create", env)[0] == 0
    server.state.refuse_all_updates = True
    rc, out = run(tmp_path, "delete", env)
    assert rc != 0
    assert "(REFUSED)" in out
    assert "update policy" in out
    assert server.records() == caas_records_on(server)
    assert_no_leaks(out, secret)


def test_refused_existence_check_on_create_fails(tmp_path, server, secret):
    server.state.refuse_all_updates = True
    rc, out = run(tmp_path, "create", driver_env(server, secret))
    assert rc != 0
    assert "(REFUSED)" in out
    assert server.records() == {}


def test_refused_create_fails(tmp_path, server, secret):
    server.state.refuse_updates = True
    rc, out = run(tmp_path, "create", driver_env(server, secret))
    assert rc != 0
    assert "(REFUSED)" in out
    assert "update policy" in out


def test_refused_query_on_rerun_fails_with_clear_message(tmp_path, server, secret):
    """nsupdate queries an existing record to compare its TTL on a rerun."""
    env = driver_env(server, secret)
    assert run(tmp_path, "create", env)[0] == 0
    server.state.refuse_queries = True
    rc, out = run(tmp_path, "create", env)
    assert rc != 0
    assert "refused a query for the existing record" in out
    assert server.records() == caas_records_on(server)
    assert_no_leaks(out, secret)


@pytest.mark.parametrize("operation", ["create", "delete"])
def test_zone_not_served_fails(tmp_path, operation):
    """DNS_ZONE that the server is not authoritative for must fail, also on delete."""
    secret = random_secret()
    with FakeRFC2136Server("other.example.test.", KEY_NAME, secret) as server:
        rc, out = run(tmp_path, operation, driver_env(server, secret))
        assert rc != 0
        assert "(NOTAUTH)" in out
        assert "not authoritative for DNS_ZONE" in out
        assert server.records() == {}
        assert_no_leaks(out, secret)


def test_unreachable_server_fails(tmp_path, secret):
    env = {
        "DNS_RFC2136_SERVER": "127.0.0.1",
        "DNS_RFC2136_PORT": str(free_port()),
        "DNS_RFC2136_KEY_NAME": KEY_NAME,
        "DNS_RFC2136_KEY_SECRET": secret,
    }
    rc, out = run(tmp_path, "create", env)
    assert rc != 0
    assert "no answer from the server" in out
    assert_no_leaks(out, secret)


# --- Settings validation (no request reaches the server) ---------------------


@pytest.mark.parametrize(
    ("overrides", "message"),
    [
        ({"DNS_RFC2136_SERVER": None}, "DNS_RFC2136_SERVER must be"),
        ({"DNS_RFC2136_SERVER": "dns.example.test"}, "DNS_RFC2136_SERVER must be"),
        ({"DNS_RFC2136_PORT": "0"}, "DNS_RFC2136_PORT must be"),
        ({"DNS_RFC2136_PORT": "domain"}, "DNS_RFC2136_PORT must be"),
        ({"DNS_RFC2136_PROTOCOL": "tls"}, "DNS_RFC2136_PROTOCOL must be"),
        ({"DNS_RFC2136_KEY_NAME": None}, "DNS_RFC2136_KEY_NAME must"),
        ({"DNS_RFC2136_KEY_ALGORITHM": "hmac-sha3"}, "DNS_RFC2136_KEY_ALGORITHM must be"),
        ({"DNS_RFC2136_KEY_SECRET": None}, "DNS_RFC2136_KEY_SECRET must"),
    ],
)
@pytest.mark.parametrize("operation", ["create", "delete"])
def test_invalid_settings_fail_before_contacting_server(tmp_path, server, secret, operation, overrides, message):
    rc, out = run(tmp_path, operation, driver_env(server, secret, **overrides))
    assert rc != 0
    assert message in out
    assert server.state.requests == 0
    assert secret not in out


def test_record_outside_zone_fails_before_contacting_server(tmp_path, server, secret):
    records = [{"name": f"api.{CLUSTER}.elsewhere.test", "addr": API_IP}]
    rc, out = run(tmp_path, "create", driver_env(server, secret), records=records)
    assert rc != 0
    assert "not inside DNS_ZONE" in out
    assert server.state.requests == 0
    assert f"api.{CLUSTER}.elsewhere.test" not in out


def test_suffix_match_is_on_label_boundary(tmp_path, server, secret):
    records = [{"name": f"api.{CLUSTER}.x{ZONE}", "addr": API_IP}]
    rc, out = run(tmp_path, "create", driver_env(server, secret), records=records)
    assert rc != 0
    assert "not inside DNS_ZONE" in out


# --- Full job event data -----------------------------------------------------
#
# AAP stores every task result as a job event, not only the default console
# lines. The JSON callback at verbosity 3 records the same per-task results,
# so these runs check that the secret never appears in them and that the
# driver's own results carry no record name or address.

EVENT_DATA_ENV = {"ANSIBLE_STDOUT_CALLBACK": "ansible.posix.json", "ANSIBLE_VERBOSITY": "3"}
RECORD_DATA = [record["name"] for record in CAAS_RECORDS] + [API_IP, INGRESS_IP]


def _event_tasks(output):
    """Return [(task name, serialized host results)] for every task occurrence.

    The driver runs the same task names once per record, so a mapping keyed by
    name would keep only the last record's results.
    """
    document = json.loads(output[output.index("{") : output.rindex("}") + 1])
    return [
        (task["task"]["name"], json.dumps(task["hosts"])) for play in document["plays"] for task in play["tasks"]
    ]


@pytest.mark.parametrize(
    ("operation", "scenario"),
    [
        ("create", "success"),
        ("delete", "success"),
        ("delete", "refused"),
        ("create", "wrong_secret"),
        ("create", "invalid_setting"),
    ],
)
def test_event_data_holds_no_secret_or_record_data(tmp_path, server, secret, operation, scenario):
    if operation == "delete":
        assert run(tmp_path, "create", driver_env(server, secret))[0] == 0
    env = driver_env(server, secret)
    leaked_secret = secret
    if scenario == "refused":
        server.state.refuse_all_updates = True
    elif scenario == "wrong_secret":
        leaked_secret = random_secret()
        env = driver_env(server, secret, DNS_RFC2136_KEY_SECRET=leaked_secret)
    elif scenario == "invalid_setting":
        env = driver_env(server, secret, DNS_RFC2136_PORT="domain")

    rc, out = run(tmp_path, operation, {**env, **EVENT_DATA_ENV}, caller_no_log=True)
    assert (rc == 0) == (scenario == "success"), out
    assert leaked_secret not in out
    tasks = _event_tasks(out)
    validations = [name for name, _ in tasks if "Validate RFC 2136" in name]
    # A success runs the driver once per record; a failure stops at the first.
    assert len(validations) == (len(CAAS_RECORDS) if scenario == "success" else 1), validations
    for name, results in tasks:
        for value in RECORD_DATA:
            assert value not in results, f"{value!r} in results of task {name!r}"


def test_event_data_check_detects_unprotected_callers(tmp_path, server, secret):
    """Control: without the caller's no_log, the loop item exposes record data."""
    rc, out = run(tmp_path, "create", {**driver_env(server, secret), **EVENT_DATA_ENV})
    assert rc == 0, out
    assert secret not in out
    caller_results = [results for name, results in _event_tasks(out) if name == "Run DNS driver entry point"]
    assert caller_results
    assert all(record["name"] in caller_results[0] for record in CAAS_RECORDS)


# --- Static checks on the shipped role ---------------------------------------

RECORD_VARS = ["dns_record_name", "dns_record_value", "dns_zone", "dns_rfc2136_record", "dns_rfc2136_zone"]
SECRET_VARS = ["dns_rfc2136_key_secret"]


def _task_lists(tasks):
    """Yield the task list and the task lists nested in its blocks."""
    yield tasks
    for task in tasks:
        for key in ("block", "rescue", "always"):
            if key in task:
                yield from _task_lists(task[key])


def _tasks(name):
    return yaml.safe_load((ROLE / "tasks" / name).read_text())


def _all_tasks():
    for path in sorted((ROLE / "tasks").glob("*.yaml")):
        for tasks in _task_lists(yaml.safe_load(path.read_text())):
            for task in tasks:
                yield path.name, task


def test_nsupdate_tasks_use_no_log():
    found = 0
    for file_name, task in _all_tasks():
        if "community.general.nsupdate" in task:
            found += 1
            assert task.get("no_log") is True, f"{file_name}: {task['name']}"
            assert task.get("register"), f"{file_name}: {task['name']} result must be checked"
    assert found == 3


def test_task_names_and_messages_do_not_expose_record_or_secret():
    for file_name, task in _all_tasks():
        texts = [task.get("name", "")]
        for module in ("ansible.builtin.fail", "ansible.builtin.assert", "ansible.builtin.debug"):
            if module in task:
                args = task[module]
                texts += [str(args.get("msg", "")), str(args.get("fail_msg", "")), str(args.get("success_msg", ""))]
        for text in texts:
            for var in RECORD_VARS + SECRET_VARS:
                assert var not in text, f"{file_name}: {var} in {text!r}"


def test_every_nsupdate_result_is_checked():
    """Each nsupdate call is followed by check_result with explicit success codes."""
    checked = 0
    for file_name in ("create.yaml", "delete.yaml"):
        for tasks in _task_lists(_tasks(file_name)):
            for index, task in enumerate(tasks):
                if "community.general.nsupdate" not in task:
                    continue
                registered = task["register"]
                follow_up = tasks[index + 1]
                assert follow_up["ansible.builtin.include_tasks"] == "check_result.yaml"
                assert registered in follow_up["vars"]["dns_rfc2136_result"]
                assert follow_up["vars"]["dns_rfc2136_ok_rcodes"]
                checked += 1
    assert checked == 3


# The CaaS endpoint DNS callers. Their loop over the records must keep a
# literal no_log: true so record names and addresses stay out of job events
# (see test_event_data_holds_no_secret_or_record_data).
CAAS_DNS_CALLERS = [
    "osac/service/roles/external_access/tasks/caas_dns_publish.yaml",
    "osac/service/roles/external_access/tasks/caas_dns_delete.yaml",
    "osac/service/roles/cluster_endpoints/tasks/publish_public_dns_api.yaml",
    "osac/service/roles/cluster_endpoints/tasks/publish_public_dns_ingress.yaml",
    "osac/service/roles/cluster_endpoints/tasks/remove_public_dns_api.yaml",
    "osac/service/roles/cluster_endpoints/tasks/remove_public_dns_ingress.yaml",
]


@pytest.mark.parametrize("caller", CAAS_DNS_CALLERS)
def test_caas_dns_callers_hide_record_data(caller):
    path = ROOT / "collections" / "ansible_collections" / caller
    tasks = yaml.safe_load(path.read_text())
    driver_calls = [
        task
        for task_list in _task_lists(tasks)
        for task in task_list
        if task.get("ansible.builtin.include_role", {}).get("name") == "{{ dns_class }}"
    ]
    assert driver_calls, f"{caller} no longer calls the DNS driver"
    for task in driver_calls:
        assert task.get("no_log") is True, f"{caller}: {task['name']}"


def test_settings_come_from_dns_rfc2136_environment_variables():
    defaults = (ROLE / "defaults" / "main.yaml").read_text()
    for name in ("SERVER", "PORT", "PROTOCOL", "KEY_NAME", "KEY_ALGORITHM", "KEY_SECRET"):
        assert f"lookup('env', 'DNS_RFC2136_{name}')" in defaults
    values = yaml.safe_load(defaults)
    assert "hmac-sha256" in values["dns_rfc2136_key_algorithm"]
