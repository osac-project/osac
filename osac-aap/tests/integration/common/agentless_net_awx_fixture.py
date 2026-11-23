"""Per-run AgentlessNet state and settings for AWX job profiles."""

from __future__ import annotations

import argparse
import json
import posixpath
import re
import sqlite3
import sys
from dataclasses import dataclass
from pathlib import Path, PurePosixPath
from typing import Mapping

COLLECTIONS = Path(__file__).resolve().parents[3] / "collections"
sys.path.insert(0, str(COLLECTIONS))

from ansible_collections.osac.templates.plugins.module_utils.agentless_net_state import (  # noqa: E402
    StateStore,
)

CONTRACT_VIRTUAL_NETWORK_ID = "a0000000-0000-4000-8000-000000000001"
CONTRACT_SUBNET_ID = "a0000000-0000-4000-8000-000000000002"
FIXTURE_MARKER = ".agentless-net-awx-fixture"
FIXTURE_MARKER_VERSION = 1


@dataclass(frozen=True)
class AgentlessNetAWXFixture:
    workspace: Path
    job_environment: Mapping[str, str]
    inventory: Mapping[str, object]


def _mapped_path(value: str, name: str) -> PurePosixPath:
    if not value or "\0" in value or not PurePosixPath(value).is_absolute():
        raise ValueError(f"{name} must be an absolute job-visible path")
    return PurePosixPath(posixpath.normpath(value))


def _validate_fixture_name(fixture_name: str) -> str:
    if not re.fullmatch(r"[A-Za-z0-9][A-Za-z0-9_.-]*", fixture_name):
        raise ValueError("fixture_name must contain only letters, digits, '.', '_' or '-'")
    if fixture_name in {".", ".."}:
        raise ValueError("fixture_name must identify a fixture directory")
    return fixture_name


def _inventory() -> dict[str, object]:
    return {
        "all": {
            "vars": {
                "agentless_net_provisioning_network": {
                    "name": "provisioning",
                    "vlan_id": 30,
                },
                "agentless_net_host_ports": {
                    "bmi-01.example.test": {
                        "eno1": {"switch": "leaf-1", "port": "swp1"},
                        "eno2": {"switch": "leaf-1", "port": "swp2"},
                    }
                },
            },
            "children": {
                "net_nodes": {
                    "hosts": {
                        "network-node": {
                            "ansible_host": "127.0.0.1",
                            "ansible_user": "contract-test",
                        }
                    }
                },
                "switches": {
                    "hosts": {
                        "leaf-1": {
                            "ansible_host": "127.0.0.1",
                            "ansible_user": "contract-test",
                            "ansible_network_os": "cumulus",
                            "trunk_ports": ["swp100"],
                        }
                    }
                },
            },
        }
    }


def prepare_agentless_net_contract_state(
    state_path: Path,
    tenant: str,
) -> None:
    """Seed the deterministic NetworkAttachment state used by task tests."""
    store = StateStore(state_path)
    store._ensure_virtual_network(
        CONTRACT_VIRTUAL_NETWORK_ID,
        "10.20.0.0/16",
        tenant,
        reconcile=False,
    )
    subnet, _ = store.reserve_subnet(
        CONTRACT_SUBNET_ID,
        CONTRACT_VIRTUAL_NETWORK_ID,
        tenant,
        "10.20.1.0/28",
        "eth1",
        {
            "gateway_ipv4": "10.20.1.1",
            "overlay_ipv4_cidrs": [],
            "physical_ipv4_cidrs": ["10.20.1.0/28"],
            "lb_vip_ipv4_cidrs": [],
        },
        observed_vlan_ids=set(range(1, 100)),
    )
    ready = {**subnet, "phase": "ready"}
    with sqlite3.connect(state_path) as connection:
        connection.execute(
            "UPDATE subnets SET phase = ?, payload = ? WHERE uid = ?",
            (
                "ready",
                json.dumps(ready, sort_keys=True, separators=(",", ":")),
                CONTRACT_SUBNET_ID,
            ),
        )


def _record_owned_files(workspace: Path) -> None:
    files = {
        path.name for path in workspace.iterdir() if path.name != FIXTURE_MARKER
    }
    files.update(
        {
            "network.sqlite3-journal",
            "network.sqlite3-shm",
            "network.sqlite3-wal",
            "switch-failed-once",
        }
    )
    (workspace / FIXTURE_MARKER).write_text(
        json.dumps(
            {"version": FIXTURE_MARKER_VERSION, "files": sorted(files)}
        ),
        encoding="utf-8",
    )


def prepare_agentless_net_awx_fixture(
    workspace: Path,
    *,
    job_workspace: str,
    project_root_in_job: str,
    tenant: str,
    fixture_name: str,
) -> AgentlessNetAWXFixture:
    """Create isolated AgentlessNet state and return the AWX job settings."""
    fixture_name = _validate_fixture_name(fixture_name)
    job_root = _mapped_path(job_workspace, "job_workspace")
    project_root = _mapped_path(project_root_in_job, "project_root_in_job")
    workspace_root = Path(workspace).resolve()
    workspace_root.mkdir(parents=True, exist_ok=True)
    fixture_workspace = workspace_root / fixture_name
    fixture_workspace.mkdir(mode=0o700)

    job_fixture_workspace = job_root / fixture_name
    job_state_file = job_fixture_workspace / "network.sqlite3"
    job_switch_state = job_fixture_workspace / "switch.json"
    job_fail_marker = job_fixture_workspace / "switch-failed-once"
    fake_ssh_directory = project_root / "tests/integration/fixtures/agentless_switch"
    inventory = _inventory()
    job_environment = {
        "AGENTLESS_NET_VN_INVENTORY": json.dumps(
            inventory, sort_keys=True, separators=(",", ":")
        ),
        "AGENTLESS_NET_STATE_FILE": str(job_state_file),
        "AGENTLESS_NET_SSH_PRIVATE_KEY": "",
        "ANSIBLE_SSH_EXECUTABLE": str(fake_ssh_directory / "ssh"),
        "OSAC_AGENTLESS_SWITCH_STATE_FILE": str(job_switch_state),
        "OSAC_AGENTLESS_SWITCH_FAIL_MARKER": str(job_fail_marker),
    }
    fixture = AgentlessNetAWXFixture(
        workspace=fixture_workspace,
        job_environment=job_environment,
        inventory=inventory,
    )

    try:
        (fixture_workspace / FIXTURE_MARKER).write_text(
            json.dumps({"version": FIXTURE_MARKER_VERSION, "files": []}),
            encoding="utf-8",
        )
        prepare_agentless_net_contract_state(
            fixture_workspace / "network.sqlite3", tenant
        )
        (fixture_workspace / "switch.json").write_text(
            json.dumps(
                {
                    "ports": {"swp1": [30], "swp2": [30], "swp3": [200]},
                    "tombstones": [],
                },
                sort_keys=True,
            ),
            encoding="utf-8",
        )
        _record_owned_files(fixture_workspace)
    except BaseException:
        _record_owned_files(fixture_workspace)
        cleanup_agentless_net_awx_fixture(fixture)
        raise

    return fixture


def cleanup_agentless_net_awx_fixture(
    fixture: AgentlessNetAWXFixture,
) -> None:
    """Remove fixture-owned state and leave caller-owned workspace files alone."""
    workspace = Path(fixture.workspace)
    if not workspace.exists():
        return
    if workspace.is_symlink() or not workspace.is_dir():
        raise ValueError("fixture workspace must be the original fixture directory")
    marker = workspace / FIXTURE_MARKER
    if not marker.is_file():
        raise ValueError("fixture workspace is missing its ownership marker")
    try:
        marker_data = json.loads(marker.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as error:
        raise ValueError("fixture ownership marker is invalid") from error
    if not isinstance(marker_data, dict):
        raise ValueError("fixture ownership marker is invalid")
    owned_files = (
        marker_data.get("files")
        if marker_data.get("version") == FIXTURE_MARKER_VERSION
        else None
    )
    if not isinstance(owned_files, list) or any(
        not isinstance(name, str)
        or not name
        or Path(name).name != name
        or name == FIXTURE_MARKER
        for name in owned_files
    ):
        raise ValueError("fixture ownership marker is invalid")

    for name in [*owned_files, FIXTURE_MARKER]:
        path = workspace / name
        if path.is_file() or path.is_symlink():
            path.unlink()
        elif path.is_dir():
            try:
                path.rmdir()
            except OSError:
                pass
    try:
        workspace.rmdir()
    except OSError:
        pass


def _main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--workspace", type=Path)
    parser.add_argument("--job-workspace")
    parser.add_argument("--project-root-in-job")
    parser.add_argument("--tenant")
    parser.add_argument("--fixture-name")
    parser.add_argument("--cleanup-workspace", type=Path)
    args = parser.parse_args()

    if args.cleanup_workspace is not None:
        if any(
            value is not None
            for value in (
                args.workspace,
                args.job_workspace,
                args.project_root_in_job,
                args.tenant,
                args.fixture_name,
            )
        ):
            parser.error("--cleanup-workspace cannot be combined with prepare options")
        cleanup_agentless_net_awx_fixture(
            AgentlessNetAWXFixture(
                workspace=args.cleanup_workspace,
                job_environment={},
                inventory={},
            )
        )
        return

    if any(
        value is None
        for value in (
            args.workspace,
            args.job_workspace,
            args.project_root_in_job,
            args.tenant,
            args.fixture_name,
        )
    ):
        parser.error("prepare requires --workspace, --job-workspace, --project-root-in-job, --tenant, and --fixture-name")

    fixture = prepare_agentless_net_awx_fixture(
        args.workspace,
        job_workspace=args.job_workspace,
        project_root_in_job=args.project_root_in_job,
        tenant=args.tenant,
        fixture_name=args.fixture_name,
    )
    print(
        json.dumps(
            {
                "workspace": str(fixture.workspace),
                "job_environment": dict(fixture.job_environment),
                "inventory": fixture.inventory,
            },
            sort_keys=True,
        )
    )


if __name__ == "__main__":
    _main()
