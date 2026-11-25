"""Contract tests for the atomic Realm claim in the pure_storage role.

Listing the Realm pool and then writing the claim Secret is not atomic: two
tenant setups can read the same free Realm before either writes. The claim
Lease is the serialization point, so these tests pin the properties that make
it one: a name derived only from the Realm, server-side apply with a stable
per-tenant field manager and no conflict forcing, a conflict treated as
"another run took it" rather than a failure, and release on rollback and
teardown.
"""

from pathlib import Path

import yaml  # type: ignore[import-untyped]
from ansible.parsing.dataloader import DataLoader  # type: ignore[import-not-found]
from ansible.template import Templar  # type: ignore[import-not-found]

REPO_ROOT = Path(__file__).resolve().parents[2]
PURE_ROLE_DIR = (
    REPO_ROOT
    / "collections"
    / "ansible_collections"
    / "osac"
    / "templates"
    / "roles"
    / "pure_storage"
)
CLAIM_TASK_PREFIX = "Attempt atomic claim of Realm"


def _load_yaml(path: Path):
    """Parse and return the YAML document at the given path."""
    return yaml.safe_load(path.read_text(encoding="utf-8"))


def _find_task(tasks: list[dict], name: str) -> dict:
    """Return the first task whose name matches exactly, descending into
    block/rescue/always so callers need not know a task's nesting."""
    for task in tasks:
        if not isinstance(task, dict):
            continue
        if task.get("name") == name:
            return task
        for section in ("block", "rescue", "always"):
            nested = task.get(section)
            if not isinstance(nested, list):
                continue
            try:
                return _find_task(nested, name)
            except AssertionError:
                pass
    raise AssertionError(f"Task {name!r} not found")


def _find_prefixed_task(tasks: list[dict], prefix: str) -> dict:
    """Return the first task whose name starts with the given prefix."""
    for task in tasks:
        if not isinstance(task, dict):
            continue
        if str(task.get("name", "")).startswith(prefix):
            return task
        for section in ("block", "rescue", "always"):
            nested = task.get(section)
            if not isinstance(nested, list):
                continue
            try:
                return _find_prefixed_task(nested, prefix)
            except AssertionError:
                pass
    raise AssertionError(f"No task name starts with {prefix!r}")


def _render(expression, variables: dict):
    """Render a Jinja expression or data structure using Ansible's Templar."""
    templar = Templar(loader=DataLoader(), variables=variables)
    return templar.template(expression)


def _defaults() -> dict:
    return _load_yaml(PURE_ROLE_DIR / "defaults" / "main.yaml")


def _claim_tasks() -> list[dict]:
    return _load_yaml(PURE_ROLE_DIR / "tasks" / "_claim_realm.yaml")


def _setup_tasks() -> list[dict]:
    return _load_yaml(PURE_ROLE_DIR / "tasks" / "setup.yaml")


def _teardown_tasks() -> list[dict]:
    return _load_yaml(PURE_ROLE_DIR / "tasks" / "teardown_backend.yaml")


def _lease_name(realm: str) -> str:
    defaults = _defaults()
    task = _load_yaml(PURE_ROLE_DIR / "tasks" / "_realm_claim_lease_name.yaml")[0]
    return _render(
        task["ansible.builtin.set_fact"]["_pure_realm_lease_name"],
        {
            "pure_storage_realm_claim_prefix": defaults["pure_storage_realm_claim_prefix"],
            "_pure_realm_lease_realm": realm,
        },
    )


def test_claim_lease_name_depends_only_on_the_realm():
    """Every tenant derives the same Lease name for the same Realm."""
    assert _lease_name("ps-realm") == "pure-realm-claim-ps-realm"
    # Realm-qualified resource names contain '::', which is not valid in a
    # DNS subdomain, so the name must be sanitized rather than rejected.
    assert _lease_name("Realm::A") == "pure-realm-claim-realm--a"


def test_claim_lease_name_has_no_leading_or_trailing_separator():
    """A sanitized Realm name must still be a valid object name."""
    name = _lease_name("realm::")
    assert name.startswith("pure-realm-claim-")
    assert not name.endswith("-") and not name.endswith(".")
    assert "--" not in name[len("pure-realm-claim-"):].rstrip("-")


def test_claim_task_applies_holder_identity_with_a_tenant_field_manager():
    """The claim is server-side applied without forcing another manager out."""
    task = _find_prefixed_task(_claim_tasks(), CLAIM_TASK_PREFIX)
    k8s = task["kubernetes.core.k8s"]

    assert k8s["state"] == "present"
    assert k8s["apply"] is True
    assert k8s["server_side_apply"]["field_manager"] == (
        "{{ pure_storage_realm_claim_field_manager }}"
    )
    assert k8s["server_side_apply"]["force_conflicts"] is False

    definition = k8s["definition"]
    assert definition["apiVersion"] == "coordination.k8s.io/v1"
    assert definition["kind"] == "Lease"
    assert definition["metadata"]["name"] == "{{ _pure_realm_lease_name }}"
    assert definition["metadata"]["namespace"] == "{{ pure_storage_config_namespace }}"
    assert definition["metadata"]["labels"]["osac.openshift.io/pure-realm"] == (
        "{{ _pure_candidate.realm_name }}"
    )
    assert definition["spec"]["holderIdentity"] == (
        "{{ pure_storage_realm_claim_holder }}"
    )


def test_conflicting_claim_is_treated_as_taken_not_broken():
    """A 409 must send the caller to the next candidate, not abort the setup."""
    tasks = _claim_tasks()
    claim = _find_prefixed_task(tasks, CLAIM_TASK_PREFIX)

    # The module result stays failed so the outcome is inspectable, and the
    # play continues to classify it.
    assert claim["ignore_errors"] is True

    guard = _find_task(
        tasks, "Fail when the Realm claim failed for a reason other than a conflict"
    )
    assert guard["when"] == [
        "_pure_claim_attempt is failed",
        "_pure_claim_attempt.reason | default('') != 'Conflict'",
        "(_pure_claim_attempt.msg | default('') | string) is not search('(?i)conflict')",
    ]

    taken = _find_task(tasks, "Record the Realm as claimed by another run")
    assert "Conflict" in str(taken["when"])
    assert "_pure_realm_claim_conflicts" in str(
        taken["ansible.builtin.set_fact"]["_pure_realm_claim_conflicts"]
    )

    claimed = _find_task(tasks, "Record the claimed Realm")
    assert claimed["when"] == "not (_pure_claim_attempt is failed)"
    facts = claimed["ansible.builtin.set_fact"]
    assert facts["_pure_selected_realm_name"] == "{{ _pure_candidate.realm_name }}"
    assert facts["_pure_claimed_realm_lease_name"] == "{{ _pure_realm_lease_name }}"
    assert facts["_pure_realm_claim_created"] is True


def test_setup_claims_every_candidate_until_one_succeeds():
    """A lost race must fall through to the next candidate Realm."""
    loop_task = _find_task(
        _setup_tasks(), "Claim the first Realm another run has not taken"
    )
    assert loop_task["ansible.builtin.include_tasks"] == "_claim_realm.yaml"
    assert loop_task["loop"] == "{{ _pure_available_realms }}"
    assert loop_task["loop_control"]["loop_var"] == "_pure_candidate"
    # A conditional on a looped include is not re-evaluated between iterations,
    # so the "already claimed" guard must live on the included tasks — a second
    # candidate would otherwise be claimed as well.
    assert "when" not in loop_task
    guarded = _find_task(
        _claim_tasks(), "Claim a Realm unless this dispatch already holds one"
    )
    assert guarded["when"] == "not (_pure_realm_claim_created | default(false) | bool)"
    assert len(guarded["block"]) == 5

    exhaustion = _find_task(
        _setup_tasks(), "Fail when every available Realm was claimed concurrently"
    )
    assert exhaustion["when"] == "_pure_selected_realm_name | length == 0"
    assert "stale claim Lease" in exhaustion["ansible.builtin.fail"]["msg"]


def test_setup_resets_claim_state_before_the_block_can_fail():
    """set_fact is host-scoped, so a second backend must not inherit a claim."""
    tasks = _setup_tasks()
    names = [task.get("name") for task in tasks if isinstance(task, dict)]
    reset_index = names.index("Reset Realm claim state left by an earlier dispatch")
    block_index = names.index("Provision Pure Storage Realm for tenant")
    assert reset_index < block_index

    facts = tasks[reset_index]["ansible.builtin.set_fact"]
    assert facts["_pure_realm_claim_created"] is False
    assert facts["_pure_selected_realm_name"] == ""
    assert facts["_pure_claimed_realm_lease_name"] == ""


def test_reonboarding_reclaims_the_realm_it_already_records():
    """Allocations made before claims existed must gain a claim Lease."""
    tasks = _setup_tasks()
    re_claim = _find_task(tasks, "Re-claim the Realm this tenant already owns")
    assert re_claim["ansible.builtin.include_tasks"] == "_claim_realm.yaml"
    assert re_claim["vars"]["_pure_candidate"]["realm_name"] == (
        "{{ _pure_existing_config.realm_name }}"
    )

    duplicate = _find_task(
        tasks, "Fail when another tenant holds the claim for the reused Realm"
    )
    assert duplicate["when"] == "not (_pure_realm_claim_created | default(false) | bool)"


def test_rollback_releases_only_a_claim_this_run_created():
    """A pre-existing tenant keeps its Realm when a later step fails."""
    rollback = _find_task(
        _setup_tasks(), "Rollback - release the Realm claim Lease this run created"
    )
    assert rollback["when"] == [
        "not (_pure_secret_preexisting | default(false) | bool)",
        "_pure_realm_claim_created | default(false) | bool",
        "_pure_claimed_realm_lease_name | default('') | length > 0",
    ]
    k8s = rollback["kubernetes.core.k8s"]
    assert k8s["state"] == "absent"
    assert k8s["kind"] == "Lease"
    assert k8s["name"] == "{{ _pure_claimed_realm_lease_name }}"


def test_teardown_releases_the_realm_claim():
    """Teardown must hand the Realm back, otherwise it stays unclaimable."""
    release = _find_task(_teardown_tasks(), "Release the Realm claim Lease")
    assert release["when"] == "_pure_teardown_realm_name | default('') | length > 0"
    k8s = release["kubernetes.core.k8s"]
    assert k8s["state"] == "absent"
    assert k8s["api_version"] == "coordination.k8s.io/v1"
    assert k8s["kind"] == "Lease"
    assert k8s["name"] == "{{ _pure_realm_lease_name }}"


def test_claim_identity_is_unique_per_tenant_and_stable_per_rerun():
    """One tenant cannot impersonate another's claim, and re-runs are idempotent."""
    defaults = _defaults()
    expression = defaults["pure_storage_realm_claim_field_manager"]
    holder_expression = defaults["pure_storage_realm_claim_holder"]

    def manager(tenant_name: str, backend_id: str) -> str:
        return _render(
            expression, {"tenant_name": tenant_name, "_pure_backend_id": backend_id}
        )

    first = manager("ps-new", "be-pure")
    assert first == manager("ps-new", "be-pure")
    assert first.startswith("osac-pure-realm-claim-")
    assert first != manager("ps-other", "be-pure")
    assert first != manager("ps-new", "be-pure-2")
    # Field managers are capped at 128 characters, and neither a tenant name
    # nor a backend ID has a length that guarantees that.
    assert len(first) <= 128
    assert len(manager("ps-" + "n" * 63, "b" * 64)) <= 128

    holder = _render(
        holder_expression,
        {"tenant_name": "ps-new", "_pure_backend_id": "be-pure"},
    )
    assert holder == "ps-new/be-pure"


def test_setup_no_longer_selects_a_realm_by_position():
    """The list-then-select race must not come back."""
    setup_source = (PURE_ROLE_DIR / "tasks" / "setup.yaml").read_text(encoding="utf-8")
    assert "TODO: Add atomic Realm claim" not in setup_source
    assert "_pure_available_realms[0]" not in setup_source
