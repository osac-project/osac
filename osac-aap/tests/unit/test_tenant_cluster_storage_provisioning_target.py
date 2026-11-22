from pathlib import Path

import pytest
import yaml
from jinja2 import Environment, StrictUndefined


ROOT = Path(__file__).resolve().parents[2]
PLAYBOOKS = [
    ROOT / "playbook_osac_create_tenant_cluster_storage.yml",
    ROOT / "playbook_osac_delete_tenant_cluster_storage.yml",
]
STORAGE_PROVIDER = "osac.service.storage_provider"
TARGET_VAR = "_storage_provisioning_target"


def _play(path):
    return yaml.safe_load(path.read_text())[0]


def _walk_tasks(tasks):
    for task in tasks:
        yield task
        for key in ("block", "rescue", "always"):
            yield from _walk_tasks(task.get(key, []))


def _all_tasks(play):
    for section in ("pre_tasks", "tasks", "post_tasks"):
        yield from _walk_tasks(play.get(section, []))


def _storage_provider_includes(play):
    return [
        task
        for task in _all_tasks(play)
        if isinstance(task.get("ansible.builtin.include_role"), dict)
        and task["ansible.builtin.include_role"].get("name") == STORAGE_PROVIDER
    ]


@pytest.mark.parametrize("path", PLAYBOOKS, ids=lambda p: p.name)
def test_every_storage_provider_include_passes_the_play_level_target(path):
    """The routing target is computed once and passed at every dispatch."""
    includes = _storage_provider_includes(_play(path))

    assert includes
    for task in includes:
        assert task["vars"]["storage_provider_provisioning_target"] == (
            "{{ " + TARGET_VAR + " }}"
        ), task["name"]


def test_create_playbook_dispatches_in_main_and_rescue():
    includes = _storage_provider_includes(_play(PLAYBOOKS[0]))

    assert len(includes) == 2


@pytest.mark.parametrize("path", PLAYBOOKS, ids=lambda p: p.name)
@pytest.mark.parametrize(
    ("kind", "expected"), [("Tenant", "vmaas"), ("ClusterOrder", "caas")]
)
def test_play_level_target_maps_kind_explicitly(path, kind, expected):
    template = _play(path)["vars"][TARGET_VAR]
    env = Environment(undefined=StrictUndefined)

    rendered = env.from_string(template).render(
        osac_job_vars={"resource": {"kind": kind}}
    )

    assert rendered == expected


@pytest.mark.parametrize("path", PLAYBOOKS, ids=lambda p: p.name)
def test_play_level_target_rejects_an_unmapped_kind(path):
    template = _play(path)["vars"][TARGET_VAR]
    env = Environment(undefined=StrictUndefined)

    with pytest.raises(Exception):
        env.from_string(template).render(osac_job_vars={"resource": {"kind": "Bogus"}})


@pytest.mark.parametrize("path", PLAYBOOKS, ids=lambda p: p.name)
def test_unsupported_kind_is_asserted_before_it_is_used(path):
    """A clear message precedes the first use of the resource kind."""
    tasks = _play(path)["pre_tasks"]
    guard = next(
        index
        for index, task in enumerate(tasks)
        if "ansible.builtin.assert" in task
        and "['Tenant', 'ClusterOrder']" in " ".join(task["ansible.builtin.assert"]["that"])
    )
    first_kind_use = next(
        index
        for index, task in enumerate(tasks)
        if index != guard and "resource.kind" in yaml.safe_dump(task)
    )

    assert guard < first_kind_use
    assert "Tenant" in tasks[guard]["ansible.builtin.assert"]["fail_msg"]
    assert "ClusterOrder" in tasks[guard]["ansible.builtin.assert"]["fail_msg"]
