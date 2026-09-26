import pytest

from tests.e2e.core.osac_cli import OsacCLI


def test_create_cluster_serializes_node_sets_as_cli_assignments(monkeypatch: pytest.MonkeyPatch) -> None:
    args: list[str] = []

    def record_command(_self: OsacCLI, *command: str) -> str:
        args.extend(command)
        return "Created cluster 'cluster-id'"

    monkeypatch.setattr(OsacCLI, "_run", record_command)
    cli = object.__new__(OsacCLI)

    assert (
        cli.create_cluster(
            template="sandbox",
            node_sets={
                "compute": {"size": 2, "baremetal_instance_type": {"name": "ci-worker-bm"}},
                "gpu": {"size": 1, "baremetal_instance_type": {"name": "ci-worker-bm-gpu"}},
            },
        )
        == "cluster-id"
    )
    assert args == [
        "create",
        "cluster",
        "--template",
        "sandbox",
        "--node-set",
        "name=compute,size=2,baremetal-instance-type=ci-worker-bm",
        "--node-set",
        "name=gpu,size=1,baremetal-instance-type=ci-worker-bm-gpu",
    ]
