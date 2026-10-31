"""Guard ComputeInstance delete conditionals against non-boolean results.

ansible-core 2.19+ (the execution environment runs 2.20) fails a task whose
``when`` evaluates to a string or None. The unit tests run ansible-core 2.18,
which accepts both, so the result type is asserted here.
"""

from pathlib import Path

import pytest
import yaml
from jinja2 import Environment, StrictUndefined

DELETE_RESOURCES = (
    Path(__file__).resolve().parents[2]
    / "collections/ansible_collections/osac/templates/roles/ocp_virt_vm/tasks/delete_resources.yaml"
)
LABEL = "osac.openshift.io/compute-instance-id"
TASK_NAME = "Find the DataVolumes and Secrets of the ComputeInstance by its API ID label"


def _evaluate(variables: dict) -> object:
    tasks = yaml.safe_load(DELETE_RESOURCES.read_text())
    task = next(task for task in tasks if task["name"] == TASK_NAME)
    return Environment(undefined=StrictUndefined).compile_expression(task["when"])(**variables)


@pytest.mark.parametrize(
    ("variables", "expected"),
    [
        ({"vm_network_labels": {LABEL: "0b9c1f7e"}}, True),
        ({"vm_network_labels": {LABEL: ""}}, False),
        ({"vm_network_labels": {"other": "x"}}, False),
        ({"vm_network_labels": {}}, False),
        ({}, False),
    ],
    ids=["label-present", "label-empty", "label-absent", "no-labels", "undefined"],
)
def test_label_lookup_condition_is_a_bool(variables: dict, expected: bool) -> None:
    result = _evaluate(variables)
    assert isinstance(result, bool), f"ansible-core 2.19+ rejects {type(result).__name__} conditionals"
    assert result is expected
