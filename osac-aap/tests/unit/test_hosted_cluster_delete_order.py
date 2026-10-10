"""Guard CaaS deletion ordering while Agents and Machines unbind."""

from pathlib import Path

import pytest
import yaml
from jinja2 import Environment, StrictUndefined

DELETE_TASKS = (
    Path(__file__).resolve().parents[2]
    / "collections/ansible_collections/osac/service/roles/hosted_cluster/tasks/delete_hosted_cluster.yaml"
)


def test_worker_teardown_precedes_hosted_cluster_removal() -> None:
    tasks = yaml.safe_load(DELETE_TASKS.read_text())
    by_name = {task["name"]: (index, task) for index, task in enumerate(tasks)}
    names = (
        "Delete NodePool resources",
        "Wait for NodePool resources to finish deleting",
        "Wait for baremetalworker to finish deleting workers",
        "Delete HostedCluster resource",
        "Wait for HostedCluster to finish deleting",
    )
    assert [by_name[name][0] for name in names] == sorted(by_name[name][0] for name in names)

    nodepool_wait = by_name[names[1]][1]
    assert nodepool_wait["register"] == "remaining_nodepools"
    assert "remaining_nodepools.resources | length == 0" in nodepool_wait["until"]
    assert nodepool_wait["retries"] >= 180

    worker_wait = by_name[names[2]][1]
    assert worker_wait["register"] == "worker_clusterorder"
    assert "baremetalworker-finalizer" in worker_wait["until"]
    assert worker_wait["retries"] >= 180

    # Guest credentials and cluster registration must survive until Machine
    # drain/unbinding is complete.
    for name in (
        "Delete Secret resource containing pull-secret",
        "Delete Secret resource containing ssh-key",
        "Delete ManagedCluster resource",
    ):
        assert by_name[name][0] > by_name[names[2]][0]


@pytest.mark.parametrize(
    ("resources", "should_proceed"),
    [
        ([], True),
        ([{"metadata": {"finalizers": ["osac.openshift.io/baremetalworker-finalizer"]}}], False),
        ([{"metadata": {"finalizers": ["osac.openshift.io/finalizer"]}}], True),
    ],
)
def test_hosted_cluster_removal_waits_only_for_worker_finalizer(
    resources: list[dict[str, dict[str, list[str]]]], should_proceed: bool
) -> None:
    tasks = yaml.safe_load(DELETE_TASKS.read_text())
    wait = next(task for task in tasks if task["name"] == "Wait for baremetalworker to finish deleting workers")
    condition = Environment(undefined=StrictUndefined).compile_expression(wait["until"])
    assert condition(worker_clusterorder={"resources": resources}) is should_proceed


DRAIN_ANNOTATION = "machine.cluster.x-k8s.io/exclude-node-draining"
CLUSTER_LABEL = "cluster.x-k8s.io/cluster-name={{ hosted_cluster_name }}"


def deletion_tasks_by_name() -> dict[str, tuple[int, dict]]:
    return {task["name"]: (index, task) for index, task in enumerate(yaml.safe_load(DELETE_TASKS.read_text()))}


@pytest.mark.parametrize("phase", ["before", "after"])
def test_machine_selection_is_scoped_and_validated(phase: str) -> None:
    tasks = deletion_tasks_by_name()
    list_index, listing = tasks[f"List cluster Machines {phase} NodePool deletion"]
    assert listing["kubernetes.core.k8s_info"] == {
        "api_version": "cluster.x-k8s.io/v1beta1",
        "kind": "Machine",
        "namespace": "{{ hosted_control_plane_namespace }}",
        "label_selectors": [CLUSTER_LABEL],
    }
    assert listing["register"] == f"cluster_machines_{phase}"
    assert listing["no_log"] is True

    validation_index, validation = tasks[f"Validate cluster Machines {phase} NodePool deletion"]
    assert list_index < validation_index
    assert validation["loop"] == f"{{{{ cluster_machines_{phase}.resources }}}}"
    checks = validation["ansible.builtin.assert"]["that"]
    assert any("item.spec.clusterName" in check and "hosted_cluster_name" in check for check in checks)
    assert any("item.metadata.namespace" in check and "hosted_control_plane_namespace" in check for check in checks)
    assert any("cluster.x-k8s.io/cluster-name" in check for check in checks)
    assert validation["no_log"] is True
    check_ownership = [Environment(undefined=StrictUndefined).compile_expression(check) for check in checks]
    for cluster, namespace, label, expected in (
        ("target", "hosted-target", "target", True),
        ("unrelated", "hosted-target", "target", False),
        ("target", "hosted-other", "target", False),
        ("target", "hosted-target", "unrelated", False),
    ):
        item = {
            "spec": {"clusterName": cluster},
            "metadata": {"namespace": namespace, "labels": {"cluster.x-k8s.io/cluster-name": label}},
        }
        assert (
            all(
                check(item=item, hosted_cluster_name="target", hosted_control_plane_namespace="hosted-target")
                for check in check_ownership
            )
            is expected
        )

    patch_index, patch = tasks[f"Skip drain for cluster Machines {phase} NodePool deletion"]
    assert validation_index < patch_index
    definition = patch["kubernetes.core.k8s"]["definition"]
    assert patch["kubernetes.core.k8s"]["state"] == "present"
    assert patch["kubernetes.core.k8s"]["merge_type"] == ["merge"]
    assert definition["metadata"]["namespace"] == "{{ hosted_control_plane_namespace }}"
    assert definition["metadata"]["name"] == "{{ item.metadata.name }}"
    assert definition["metadata"]["annotations"] == {DRAIN_ANNOTATION: ""}
    assert set(definition) == {"apiVersion", "kind", "metadata"}
    assert patch["no_log"] is True
    assert patch["loop"] == (
        f"{{{{ cluster_machines_{phase}.resources | selectattr('spec.clusterName', "
        "'equalto', hosted_cluster_name) | list }}"
    )

    selected = Environment(undefined=StrictUndefined).compile_expression(patch["loop"][3:-3].strip())

    def machine(name: str, cluster: str) -> dict:
        return {"metadata": {"name": name}, "spec": {"clusterName": cluster}}

    for machines, expected in (
        ([], []),
        ([machine("one", "target")], ["one"]),
        ([machine("pool-a", "target"), machine("pool-b", "target")], ["pool-a", "pool-b"]),
        ([machine("one", "target"), machine("other", "unrelated")], ["one"]),
        ([machine("annotated", "target")], ["annotated"]),
    ):
        result = selected(**{f"cluster_machines_{phase}": {"resources": machines}}, hosted_cluster_name="target")
        assert [item["metadata"]["name"] for item in result] == expected


def test_skip_drain_preserves_teardown_and_is_delete_only() -> None:
    tasks = deletion_tasks_by_name()
    assert tasks["Set hosted control-plane namespace"][1]["ansible.builtin.set_fact"] == {
        "hosted_control_plane_namespace": "{{ hosted_cluster_namespace }}-{{ hosted_cluster_name }}"
    }
    assert (
        tasks["Skip drain for cluster Machines before NodePool deletion"][0]
        < tasks["Delete NodePool resources"][0]
        < tasks["List cluster Machines after NodePool deletion"][0]
        < tasks["Skip drain for cluster Machines after NodePool deletion"][0]
        < tasks["Wait for NodePool resources to finish deleting"][0]
        < tasks["Wait for baremetalworker to finish deleting workers"][0]
        < tasks["Delete HostedCluster resource"][0]
    )
    for phase in ("before", "after"):
        patch = tasks[f"Skip drain for cluster Machines {phase} NodePool deletion"][1]
        assert patch.get("when") == f"'{DRAIN_ANNOTATION}' not in (item.metadata.annotations | default({{}}, true))"
        should_patch = Environment(undefined=StrictUndefined).compile_expression(patch["when"])
        for annotations, expected in (({}, True), (None, True), ({DRAIN_ANNOTATION: ""}, False)):
            assert should_patch(item={"metadata": {"annotations": annotations}}) is expected
        assert should_patch(item={"metadata": {}}) is True
        assert "finalizers" not in str(patch)
