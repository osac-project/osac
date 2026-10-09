from pathlib import Path

import yaml


TASK_FILE = (
    Path(__file__).parents[2]
    / "collections/ansible_collections/osac/templates/roles/bm_host_provisioning/tasks/delete_metal3.yaml"
)


def _task(tasks: list[dict], name: str) -> dict:
    return next(task for task in tasks if task.get("name") == name)


def _find_task(tasks: list[dict], name: str) -> dict:
    for task in tasks:
        if task.get("name") == name:
            return task
        if "block" in task:
            try:
                return _find_task(task["block"], name)
            except StopIteration:
                pass
    raise StopIteration(name)


def test_bmh_availability_wait_is_not_conditional_on_image() -> None:
    tasks = yaml.safe_load(TASK_FILE.read_text())
    deprovision_task = _task(tasks, "Deprovision bare metal host")
    bmh_task = _task(deprovision_task["block"], "Deprovision BMH")
    image_task = _task(bmh_task["block"], "Remove image and networkData from BMH")
    wait_task = _find_task(tasks, "Wait for BMH to reach available or ready state")

    assert image_task["when"] == "bmh_has_image | bool"
    assert wait_task not in image_task["block"]
    assert wait_task["until"].endswith('in ["available", "ready"]')
    assert wait_task["retries"] == 120
    assert wait_task["delay"] == 30
    assert wait_task.get("ignore_errors") is not True
