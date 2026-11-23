import json
import os
import subprocess
import sys
from pathlib import Path

import pytest

INTEGRATION_COMMON = Path(__file__).resolve().parents[1] / "integration" / "common"
sys.path.insert(0, str(INTEGRATION_COMMON))

import agentless_net_awx_fixture as fixture_module  # noqa: E402


def prepare_fixture(tmp_path, name="job-one", job_workspace="/runner/work"):
    return fixture_module.prepare_agentless_net_awx_fixture(
        tmp_path,
        job_workspace=job_workspace,
        project_root_in_job="/runner/project",
        tenant="tenant-a",
        fixture_name=name,
    )


def test_prepare_returns_job_paths_inventory_and_seeded_network_state(
    tmp_path
):
    fixture = prepare_fixture(tmp_path)

    assert fixture.workspace == tmp_path / "job-one"
    assert fixture.job_environment["AGENTLESS_NET_STATE_FILE"] == (
        "/runner/work/job-one/network.sqlite3"
    )
    assert json.loads(fixture.job_environment["AGENTLESS_NET_VN_INVENTORY"]) == (
        fixture.inventory
    )
    assert fixture.job_environment["ANSIBLE_SSH_EXECUTABLE"] == (
        "/runner/project/tests/integration/fixtures/agentless_switch/ssh"
    )
    assert "PATH" not in fixture.job_environment

    store = fixture_module.StateStore(fixture.workspace / "network.sqlite3")
    network = store.get_virtual_network(
        fixture_module.CONTRACT_VIRTUAL_NETWORK_ID, "tenant-a"
    )
    subnet = store.get_subnet(fixture_module.CONTRACT_SUBNET_ID, "tenant-a")
    assert network["virtual_network_cidr"] == "10.20.0.0/16"
    assert subnet["ipv4_cidr"] == "10.20.1.0/28"
    assert subnet["phase"] == "ready"
    assert fixture.inventory["all"]["children"]["switches"]["hosts"]["leaf-1"][
        "ansible_network_os"
    ] == "cumulus"

    state = json.loads((fixture.workspace / "switch.json").read_text())
    assert state == {
        "ports": {"swp1": [30], "swp2": [30], "swp3": [200]},
        "tombstones": [],
    }


def test_fake_ssh_mutates_only_the_selected_fixture_state(tmp_path):
    first = prepare_fixture(tmp_path, "job-one", "/runner/one")
    second = prepare_fixture(tmp_path, "job-two", "/runner/two")
    environment = {**os.environ, **first.job_environment}
    environment["OSAC_AGENTLESS_SWITCH_STATE_FILE"] = str(
        first.workspace / "switch.json"
    )
    environment["OSAC_AGENTLESS_SWITCH_FAIL_MARKER"] = str(
        first.workspace / "switch-failed-once"
    )
    fake_ssh = (
        Path(fixture_module.__file__).resolve().parents[1]
        / "fixtures"
        / "agentless_switch"
        / "ssh"
    )
    command = "flock /tmp/nvue.lock -c 'nv set interface swp9 bridge domain br_default access 100'"

    subprocess.run(
        [str(fake_ssh), "-c", command],
        env=environment,
        check=True,
        capture_output=True,
        text=True,
    )

    first_state = json.loads((first.workspace / "switch.json").read_text())
    second_state = json.loads((second.workspace / "switch.json").read_text())
    assert first_state["ports"]["swp9"] == [100]
    assert second_state["ports"] == {"swp1": [30], "swp2": [30], "swp3": [200]}


def test_cleanup_removes_fixture_files_but_preserves_unowned_files(
    tmp_path
):
    fixture = prepare_fixture(tmp_path)
    sibling = prepare_fixture(tmp_path, "sibling", "/runner/sibling")
    (fixture.workspace / "switch-failed-once").write_text("failed once\n")
    unowned_file = fixture.workspace / "profile.log"
    unowned_file.write_text("keep\n")
    workspace_sentinel = tmp_path / "caller-owned.txt"
    workspace_sentinel.write_text("keep\n")

    fixture_module.cleanup_agentless_net_awx_fixture(fixture)

    assert unowned_file.read_text() == "keep\n"
    assert workspace_sentinel.read_text() == "keep\n"
    assert not (fixture.workspace / "network.sqlite3").exists()
    assert not (fixture.workspace / "switch.json").exists()
    assert not (fixture.workspace / "switch-failed-once").exists()
    assert not (fixture.workspace / fixture_module.FIXTURE_MARKER).exists()
    assert (sibling.workspace / "network.sqlite3").exists()
    assert (sibling.workspace / "switch.json").exists()


def test_cleanup_removes_empty_fixture_directory_and_is_idempotent(
    tmp_path
):
    fixture = prepare_fixture(tmp_path)

    fixture_module.cleanup_agentless_net_awx_fixture(fixture)
    fixture_module.cleanup_agentless_net_awx_fixture(fixture)

    assert not fixture.workspace.exists()
    assert tmp_path.is_dir()


def test_cleanup_refuses_to_remove_a_workspace_without_its_marker(tmp_path):
    unowned_workspace = tmp_path / "unowned"
    unowned_workspace.mkdir()
    sentinel = unowned_workspace / "keep.txt"
    sentinel.write_text("keep\n")
    fixture = fixture_module.AgentlessNetAWXFixture(
        workspace=unowned_workspace,
        job_environment={},
        inventory={},
    )

    with pytest.raises(ValueError, match="ownership marker"):
        fixture_module.cleanup_agentless_net_awx_fixture(fixture)

    assert sentinel.read_text() == "keep\n"


@pytest.mark.parametrize(
    ("job_workspace", "fixture_name"),
    [("relative/path", "job-one"), ("/runner/work", "../outside")],
)
def test_prepare_rejects_paths_that_cannot_be_safely_mapped(
    tmp_path, job_workspace, fixture_name
):
    with pytest.raises(ValueError):
        fixture_module.prepare_agentless_net_awx_fixture(
            tmp_path,
            job_workspace=job_workspace,
            project_root_in_job="/runner/project",
            tenant="tenant-a",
            fixture_name=fixture_name,
        )

    assert list(tmp_path.iterdir()) == []
