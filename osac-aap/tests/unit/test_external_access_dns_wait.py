"""Regression tests for OSAC-6342.

The external_access DNS readiness checks must be able to use the deployment's
configured recursive resolver (``dns_server``) instead of being forced to query
the zone's authoritative nameserver directly. Direct-authoritative querying,
introduced by PR #1558 to bypass a resolver's negative (NXDOMAIN) cache, must
remain available as an explicit opt-in with a clear precedence.

These tests render the actual Jinja expressions from the shipped task files with
Ansible's ``Templar`` (the same approach as ``test_netris_validate_certs.py``),
so they stay coupled to the real role/caller code rather than a copy.
"""

from pathlib import Path

import pytest
import yaml
from ansible.parsing.dataloader import DataLoader
from ansible.template import Templar


AAP_ROOT = Path(__file__).parents[2]
COLLECTIONS = AAP_ROOT / "collections" / "ansible_collections"
CONFIGURATION = AAP_ROOT / "group_vars" / "all" / "configuration.yaml"
WAIT_FOR_ROLE = COLLECTIONS / "osac" / "service" / "roles" / "wait_for"
WAIT_FOR_DNS_TASKS = WAIT_FOR_ROLE / "tasks" / "wait_for_dns.yaml"
WAIT_FOR_DEFAULTS = WAIT_FOR_ROLE / "defaults" / "main.yaml"

# Every network-class caller that runs the external_access DNS wait.
CALLER_CREATE_FILES = {
    "osac": COLLECTIONS / "osac" / "steps" / "roles" / "external_access" / "tasks" / "create.yaml",
    "ci": COLLECTIONS
    / "osac"
    / "service"
    / "roles"
    / "external_access"
    / "tasks"
    / "caas_dns_create.yaml",
    "netris": COLLECTIONS / "netris" / "steps" / "roles" / "external_access" / "tasks" / "create.yaml",
    "nico": COLLECTIONS / "nico" / "steps" / "roles" / "external_access" / "tasks" / "create.yaml",
    "ocp_ci_small": COLLECTIONS
    / "osac"
    / "service"
    / "roles"
    / "external_access"
    / "tasks"
    / "caas_dns_create.yaml",
    "agentless_net": COLLECTIONS
    / "agentless_net"
    / "steps"
    / "roles"
    / "external_access"
    / "tasks"
    / "create.yaml",
}

# A representative negative-cache (SOA minimum) TTL, in seconds. The DNS wait
# budget must exceed this so a record that appears after an initial NXDOMAIN is
# still detected when querying a recursive resolver, without a global flush.
NEGATIVE_CACHE_TTL_SECONDS = 15 * 60


def render(expression, variables):
    """Render a full Jinja expression string with the given variables."""
    templar = Templar(loader=DataLoader(), variables=variables)
    return templar.template(expression)


def render_when(when_expression, variables):
    """Evaluate an Ansible ``when:`` expression to a bool."""
    result = render("{{ " + when_expression + " }}", variables)
    if isinstance(result, bool):
        return result
    return str(result).strip().lower() == "true"


def load_tasks(path):
    return yaml.safe_load(path.read_text())


def find_task(tasks, name_fragment):
    for task in tasks:
        if isinstance(task, dict) and name_fragment in task.get("name", ""):
            return task
    raise AssertionError(f"task containing {name_fragment!r} not found")


def wait_for_dns_server_expression(caller):
    tasks = load_tasks(CALLER_CREATE_FILES[caller])
    wait_task = find_task(tasks, "Wait for DNS records")
    return wait_task["vars"]["wait_for_dns_server"]


def ns_discovery_when_expression(caller):
    tasks = load_tasks(CALLER_CREATE_FILES[caller])
    ns_task = find_task(tasks, "Resolve authoritative nameserver")
    return ns_task["when"]


def base_vars(**overrides):
    variables = {
        "external_access_base_domain": "box.massopen.cloud",
        "external_access_supported_base_domains": ["box.massopen.cloud"],
        "dns_server": "",
        "external_access_dns_wait_use_authoritative": False,
        "_external_access_zone_ns": {"stdout_lines": ["ns-1.example.com."]},
    }
    variables.update(overrides)
    return variables


# --------------------------------------------------------------------------- #
# wait_for role: the dig command honours wait_for_dns_server
# --------------------------------------------------------------------------- #

def _dig_command():
    task = find_task(load_tasks(WAIT_FOR_DNS_TASKS), "Wait for DNS record")
    return task["ansible.builtin.command"]


def test_dig_uses_server_when_set():
    command = render(
        _dig_command(),
        {"wait_for_dns_server": "192.0.2.53", "wait_for_dns_record": "api.example.com"},
    )
    assert "@192.0.2.53" in command
    assert "+noall +answer api.example.com" in command


def test_dig_omits_server_when_empty():
    command = render(
        _dig_command(),
        {"wait_for_dns_server": "", "wait_for_dns_record": "api.example.com"},
    )
    assert "@" not in command
    assert command.split()[0] == "dig"
    assert "+noall +answer api.example.com" in command


# --------------------------------------------------------------------------- #
# Caller precedence: dns_server > explicit-authoritative NS > system resolver
# --------------------------------------------------------------------------- #

@pytest.mark.parametrize("caller", sorted(CALLER_CREATE_FILES))
def test_configured_recursive_resolver_is_preferred(caller):
    """AC1/AC2: a configured dns_server is used, no authoritative query needed."""
    expr = wait_for_dns_server_expression(caller)
    value = render(expr, base_vars(dns_server="192.0.2.53"))
    assert value == "192.0.2.53"


@pytest.mark.parametrize("caller", sorted(CALLER_CREATE_FILES))
def test_recursive_resolver_wins_even_if_authoritative_discovered(caller):
    """dns_server takes precedence over any discovered authoritative NS."""
    expr = wait_for_dns_server_expression(caller)
    value = render(
        expr,
        base_vars(
            dns_server="192.0.2.53",
            external_access_dns_wait_use_authoritative=True,
            _external_access_zone_ns={"stdout_lines": ["ns-1.example.com."]},
        ),
    )
    assert value == "192.0.2.53"


@pytest.mark.parametrize("caller", sorted(CALLER_CREATE_FILES))
def test_explicit_authoritative_mode_uses_discovered_ns(caller):
    """AC4: direct-authoritative remains an explicit option (dot stripped)."""
    expr = wait_for_dns_server_expression(caller)
    value = render(
        expr,
        base_vars(
            dns_server="",
            external_access_dns_wait_use_authoritative=True,
            _external_access_zone_ns={"stdout_lines": ["ns-1.example.com."]},
        ),
    )
    assert value == "ns-1.example.com"


@pytest.mark.parametrize("caller", sorted(CALLER_CREATE_FILES))
def test_default_falls_back_to_system_resolver(caller):
    """No dns_server and no authoritative opt-in -> empty (system resolver)."""
    expr = wait_for_dns_server_expression(caller)
    value = render(
        expr,
        base_vars(dns_server="", _external_access_zone_ns={"stdout_lines": []}),
    )
    assert value == ""


# --------------------------------------------------------------------------- #
# NS discovery only runs in explicit-authoritative mode and without dns_server
# --------------------------------------------------------------------------- #

@pytest.mark.parametrize("caller", sorted(CALLER_CREATE_FILES))
def test_ns_discovery_skipped_by_default(caller):
    when = ns_discovery_when_expression(caller)
    assert render_when(when, base_vars()) is False


@pytest.mark.parametrize("caller", sorted(CALLER_CREATE_FILES))
def test_ns_discovery_skipped_when_recursive_resolver_configured(caller):
    when = ns_discovery_when_expression(caller)
    assert (
        render_when(
            when,
            base_vars(external_access_dns_wait_use_authoritative=True, dns_server="192.0.2.53"),
        )
        is False
    )


@pytest.mark.parametrize("caller", sorted(CALLER_CREATE_FILES))
def test_ns_discovery_runs_only_in_explicit_authoritative_mode(caller):
    when = ns_discovery_when_expression(caller)
    assert (
        render_when(when, base_vars(external_access_dns_wait_use_authoritative=True, dns_server=""))
        is True
    )


@pytest.mark.parametrize("caller", sorted(CALLER_CREATE_FILES))
def test_ns_discovery_skipped_for_unsupported_base_domain(caller):
    when = ns_discovery_when_expression(caller)
    variables = base_vars(
        external_access_dns_wait_use_authoritative=True,
        dns_server="",
        external_access_base_domain="unmanaged.example.net",
    )
    assert render_when(when, variables) is False


# --------------------------------------------------------------------------- #
# Negative-cache handling: wait budget exceeds the negative-cache TTL
# --------------------------------------------------------------------------- #

def test_wait_budget_exceeds_negative_cache_ttl():
    """AC3: retries * delay must outlast the negative-cache TTL (no flush)."""
    defaults = yaml.safe_load(WAIT_FOR_DEFAULTS.read_text())
    retries = int(defaults["wait_for_dns_retries"])
    delay = int(defaults["wait_for_dns_delay"])
    assert retries * delay >= NEGATIVE_CACHE_TTL_SECONDS


# --------------------------------------------------------------------------- #
# Configuration variables resolve from the environment with safe defaults
# --------------------------------------------------------------------------- #

def _resolve_configuration(key):
    configuration = yaml.safe_load(CONFIGURATION.read_text())
    return render(configuration[key], {"external_access_base_domain": "box.massopen.cloud"})


def test_dns_server_defaults_to_empty(monkeypatch):
    monkeypatch.delenv("DNS_SERVER", raising=False)
    assert _resolve_configuration("dns_server") == ""


def test_dns_server_reads_environment(monkeypatch):
    monkeypatch.setenv("DNS_SERVER", "192.0.2.53")
    assert _resolve_configuration("dns_server") == "192.0.2.53"


def test_use_authoritative_defaults_to_false(monkeypatch):
    monkeypatch.delenv("EXTERNAL_ACCESS_DNS_WAIT_USE_AUTHORITATIVE", raising=False)
    assert _resolve_configuration("external_access_dns_wait_use_authoritative") is False


def test_use_authoritative_can_be_enabled(monkeypatch):
    monkeypatch.setenv("EXTERNAL_ACCESS_DNS_WAIT_USE_AUTHORITATIVE", "true")
    assert _resolve_configuration("external_access_dns_wait_use_authoritative") is True
