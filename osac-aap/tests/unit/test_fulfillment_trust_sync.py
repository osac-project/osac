"""Exercise the real content assertion without Kubernetes or credentials."""

import copy
import json
import os
import subprocess
import sys
from pathlib import Path

import pytest
import yaml

COMPONENT = Path(__file__).resolve().parents[2]
ROLE = COMPONENT / "collections/ansible_collections/osac/service/roles/fulfillment_trust_sync"


def content_assertion():
    tasks = yaml.safe_load((ROLE / "tasks/main.yaml").read_text())
    return next(task for task in tasks if task["name"] == "Assert the fulfillment CA ConfigMap has only approved content")


def test_assertion_conditions_are_strings():
    for path in (
        ROLE / "tasks/main.yaml",
        COMPONENT / "tests/integration/targets/fulfillment_trust_sync/tasks/baseline.yml",
    ):
        document = yaml.safe_load(path.read_text())
        tasks = document[0]["tasks"] if "hosts" in document[0] else document
        for task in tasks:
            if "ansible.builtin.assert" in task:
                assert all(isinstance(condition, str) for condition in task["ansible.builtin.assert"]["that"]), task["name"]
    assert content_assertion()["no_log"] is True


@pytest.mark.parametrize(
    "case",
    ["matching", "missing", "duplicate", "wrong_bundle", "extra_data", "tenant", "owner", "hash", "extra_annotation"],
)
def test_content_assertion(tmp_path, case):
    values = yaml.safe_load((ROLE / "defaults/main.yaml").read_text())
    annotations = {
        "osac.openshift.io/tenant": "fixture-tenant",
        "osac.openshift.io/owner-reference": "fixture-uid",
        "osac.openshift.io/fulfillment-ca-sha256": "f" * 64,
    }
    config_map = {"data": {"bundle.pem": "public-fixture"}, "metadata": {"annotations": annotations.copy()}}
    resources = [config_map]
    if case == "missing":
        resources = []
    elif case == "duplicate":
        resources.append(copy.deepcopy(config_map))
    elif case == "wrong_bundle":
        config_map["data"]["bundle.pem"] = "different-public-fixture"
    elif case == "extra_data":
        config_map["data"]["unexpected"] = "public-fixture"
    elif case in ("tenant", "owner", "hash"):
        key = {"tenant": "tenant", "owner": "owner-reference", "hash": "fulfillment-ca-sha256"}[case]
        config_map["metadata"]["annotations"][f"osac.openshift.io/{key}"] = "different"
    elif case == "extra_annotation":
        config_map["metadata"]["annotations"]["unexpected"] = "public-fixture"
    values.update(
        osac_job_vars={
            "resource": {"metadata": {"uid": "fixture-uid", "annotations": {"osac.openshift.io/tenant": "fixture-tenant"}}},
            "fulfillment_trust": {"bundle_pem": "public-fixture", "bundle_sha256": "f" * 64},
        },
        _fulfillment_trust_config_map={"resources": resources},
    )
    play = [{"hosts": "localhost", "gather_facts": False, "vars": values, "tasks": [content_assertion()]}]
    playbook = tmp_path / "play.json"
    playbook.write_text(json.dumps(play))
    config = tmp_path / "ansible.cfg"
    config.write_text("[defaults]\nhost_key_checking=True\n")
    result = subprocess.run(
        [sys.executable, "-m", "ansible.cli.playbook", "-i", "localhost,", "-c", "local", str(playbook)],
        env={
            **os.environ,
            "ANSIBLE_CONFIG": str(config),
            "ANSIBLE_ALLOW_BROKEN_CONDITIONALS": "false",
            "ANSIBLE_LOCAL_TEMP": str(tmp_path / "local"),
        },
        capture_output=True,
        text=True,
        timeout=30,
        check=False,
    )
    assert result.returncode == (0 if case == "matching" else 2), result.stdout + result.stderr
