from __future__ import annotations

import inspect
import subprocess
from unittest.mock import Mock

import pytest

from tests.e2e.caas.sanity import test_cluster_delete_feedback_light as deletion
from tests.e2e.core.grpc_client import GRPCClient
from tests.e2e.references import test_cluster_baremetal_references as references


def test_reference_fixture_does_not_select_first_unbacked_version(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.delenv("OSAC_CLUSTER_VERSION", raising=False)
    monkeypatch.setenv("OSAC_TEST_RELEASE_IMAGE", "release-image")
    monkeypatch.setenv("OSAC_RHCOS_BMI_IMAGE", "oci://rhcos-image")
    grpc = Mock()
    grpc.call.return_value = {"items": [{"metadata": {"name": "4-20-0"}, "spec": {}}]}
    private = Mock()
    private.ensure_disk_image.return_value = "disk-id"
    private.ensure_cluster_version.return_value = {"name": "4-22-0-e2e-references", "id": "version-id"}

    fixture = references.cluster_version.__wrapped__
    arguments = {"private_grpc": private}
    if "grpc" in inspect.signature(fixture).parameters:
        arguments["grpc"] = grpc
    result = fixture(**arguments)
    selected = next(result) if inspect.isgenerator(result) else result

    assert selected == "4-22-0-e2e-references"
    private.ensure_disk_image.assert_called_once_with(name="rhcos-4-22", source_ref="oci://rhcos-image")
    private.ensure_cluster_version.assert_called_once_with(
        version="4.22.0-e2e-references", image="release-image", disk_image="disk-id"
    )


def test_light_deletion_create_supplies_backed_version(monkeypatch: pytest.MonkeyPatch) -> None:
    cli = Mock()
    cli.create_cluster.return_value = "cluster-id"
    private = Mock()

    class StopAfterCreate(Exception):
        pass

    monkeypatch.setattr(deletion, "wait_for_cluster_order_cr", Mock(side_effect=StopAfterCreate))
    params = dict(
        cli=cli,
        grpc=Mock(),
        private_grpc=private,
        k8s_hub_client=Mock(),
        cluster_template="template",
        pull_secret_name="test-pull-secret",
        ssh_public_key_path="/tmp/id.pub",
    )
    scenario = deletion.test_cluster_delete_reports_deleting_state_without_provisioning
    if "caas_disk_image_version" in inspect.signature(scenario).parameters:
        params["caas_disk_image_version"] = "4-22-0-e2e-references"
    with pytest.raises(StopAfterCreate):
        deletion.test_cluster_delete_reports_deleting_state_without_provisioning(**params)
    assert cli.create_cluster.call_args.kwargs["version"] == "4-22-0-e2e-references"
    assert cli.create_cluster.call_args.kwargs["pull_secret"] == "test-pull-secret"
    assert cli.create_cluster.call_args.kwargs["ssh_public_key_file"] == "/tmp/id.pub"


def test_configured_version_without_disk_image_fails_early(monkeypatch: pytest.MonkeyPatch) -> None:
    from tests.e2e.core.caas_versions import ensure_caas_disk_image_version

    monkeypatch.setenv("OSAC_CLUSTER_VERSION", "4-20-0")
    private = Mock(spec=GRPCClient)
    private.get_cluster_version.return_value = {"object": {"metadata": {"name": "4-20-0"}, "spec": {}}}

    with pytest.raises(ValueError, match=r"OSAC_CLUSTER_VERSION.*DiskImage"):
        ensure_caas_disk_image_version(private)
    private.ensure_cluster_version.assert_not_called()


def test_configured_backed_version_is_used_without_creating_another(monkeypatch: pytest.MonkeyPatch) -> None:
    from tests.e2e.core.caas_versions import ensure_caas_disk_image_version

    monkeypatch.setenv("OSAC_CLUSTER_VERSION", "4-22-0-existing")
    private = Mock(spec=GRPCClient)
    private.get_cluster_version.return_value = {
        "object": {"metadata": {"name": "4-22-0-existing"}, "spec": {"diskImage": {"id": "disk-id"}}}
    }
    assert ensure_caas_disk_image_version(private) == "4-22-0-existing"
    private.ensure_disk_image.assert_not_called()
    private.ensure_cluster_version.assert_not_called()


def test_existing_same_version_without_disk_image_is_upgraded() -> None:
    private = Mock(spec=GRPCClient)
    private.create_cluster_version.side_effect = subprocess.CalledProcessError(
        1, ["grpcurl"], stderr="Code: AlreadyExists"
    )
    private.call.return_value = {
        "items": [
            {
                "id": "existing-id",
                "metadata": {"name": "4-22-0-e2e-references"},
                "spec": {"version": "4.22.0-e2e-references"},
            }
        ]
    }
    resolved = GRPCClient.ensure_cluster_version(
        private, version="4.22.0-e2e-references", image="release-image", disk_image="disk-id"
    )
    assert resolved == {"id": "existing-id", "name": "4-22-0-e2e-references"}
    private.update_cluster_version.assert_called_once_with(version_id="existing-id", disk_image={"id": "disk-id"})
