import pytest

from tests.e2e import conftest as e2e_fixtures
from tests.e2e.caas import conftest as caas_fixtures
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


def test_pull_secret_fixture_creates_typed_secret_before_use_and_deletes_after(monkeypatch: pytest.MonkeyPatch) -> None:
    commands: list[tuple[str, ...]] = []

    def record_command(_self: OsacCLI, *command: str) -> str:
        commands.append(command)
        return "Created secret 'secret-id'"

    monkeypatch.setattr(OsacCLI, "_run", record_command)
    cli = object.__new__(OsacCLI)
    fixture = e2e_fixtures.pull_secret_name.__wrapped__(cli, "/tmp/pull-secret.json")
    name = next(fixture)
    assert commands == [
        (
            "create",
            "secret",
            "--name",
            name,
            "--type",
            "pull-secret",
            "--from-file",
            ".dockerconfigjson=/tmp/pull-secret.json",
        )
    ]
    with pytest.raises(StopIteration):
        next(fixture)
    assert commands[-1] == ("delete", "secret", name)


def test_create_cluster_uses_secret_name_and_ssh_key_file(monkeypatch: pytest.MonkeyPatch) -> None:
    commands: list[tuple[str, ...]] = []

    def record_command(_self: OsacCLI, *command: str) -> str:
        commands.append(command)
        return "Created cluster 'cluster-id'"

    monkeypatch.setattr(OsacCLI, "_run", record_command)
    cli = object.__new__(OsacCLI)
    assert (
        cli.create_cluster(template="sandbox", pull_secret="secret-name", ssh_public_key_file="/tmp/key.pub")
        == "cluster-id"
    )
    assert commands == [
        (
            "create",
            "cluster",
            "--template",
            "sandbox",
            "--pull-secret",
            "secret-name",
            "--ssh-public-key-file",
            "/tmp/key.pub",
        )
    ]


def test_ssh_key_fixture_creates_typed_secret_before_use_and_deletes_after(monkeypatch: pytest.MonkeyPatch) -> None:
    commands: list[tuple[str, ...]] = []

    def record_command(_self: OsacCLI, *command: str) -> str:
        commands.append(command)
        return "Created secret 'secret-id'"

    monkeypatch.setattr(OsacCLI, "_run", record_command)
    cli = object.__new__(OsacCLI)
    fixture = caas_fixtures.ssh_key_name.__wrapped__(cli, "/tmp/key.pub")
    name = next(fixture)
    assert commands == [
        ("create", "secret", "--name", name, "--type", "ssh-public-key", "--from-file", "public_key=/tmp/key.pub")
    ]
    with pytest.raises(StopIteration):
        next(fixture)
    assert commands[-1] == ("delete", "secret", name)


@pytest.mark.parametrize("source_flag,source", [("--template", "sandbox"), ("--catalog-item", "catalog")])
def test_create_cluster_uses_registered_ssh_key(monkeypatch: pytest.MonkeyPatch, source_flag: str, source: str) -> None:
    commands: list[tuple[str, ...]] = []

    def record_command(_self: OsacCLI, *command: str) -> str:
        commands.append(command)
        return "Created cluster 'cluster-id'"

    monkeypatch.setattr(OsacCLI, "_run", record_command)
    cli = object.__new__(OsacCLI)
    if source_flag == "--template":
        cluster_id = cli.create_cluster(template=source, ssh_key="registered-key")
    else:
        cluster_id = cli.create_cluster_with_catalog_item(catalog_item=source, name="cluster", ssh_key="registered-key")

    assert cluster_id == "cluster-id"
    assert commands == [
        (
            "create",
            "cluster",
            source_flag,
            source,
            *(("--name", "cluster") if source_flag == "--catalog-item" else ()),
            "--ssh-key",
            "registered-key",
        )
    ]
