from __future__ import annotations

import subprocess
import tempfile
from pathlib import Path
from typing import Any

import pytest

from tests.e2e.core.helpers import (
    unique_name,
    wait_for_cr,
    wait_for_deletion,
    wait_for_provision,
    wait_for_running,
)
from tests.e2e.core.k8s_client import K8sClient
from tests.e2e.core.osac_cli import OsacCLI

pytestmark = pytest.mark.sanity


def _cleanup_registered_ssh_key(
    *,
    instance_id: str | None,
    instance_name: str | None,
    delete_compute_instance: Any,
    wait_for_compute_instance_deletion: Any,
    delete_secret: Any,
) -> None:
    try:
        if instance_id is not None:
            delete_compute_instance(instance_id)
            if instance_name is not None:
                wait_for_compute_instance_deletion(instance_name)
    finally:
        delete_secret()


def test_registered_ssh_key_cleanup_deletes_instance_without_cr_name() -> None:
    calls: list[tuple[str, str]] = []

    _cleanup_registered_ssh_key(
        instance_id="instance-id",
        instance_name=None,
        delete_compute_instance=lambda instance_id: calls.append(("delete-instance", instance_id)),
        wait_for_compute_instance_deletion=lambda instance_name: calls.append(("wait", instance_name)),
        delete_secret=lambda: calls.append(("delete-key", "key-id")),
    )

    assert calls == [("delete-instance", "instance-id"), ("delete-key", "key-id")]


def test_registered_ssh_key_cleanup_deletes_key_when_instance_cleanup_fails() -> None:
    calls: list[str] = []

    def delete_compute_instance(_instance_id: str) -> None:
        calls.append("delete-instance")
        raise RuntimeError("instance cleanup failed")

    with pytest.raises(RuntimeError, match="instance cleanup failed"):
        _cleanup_registered_ssh_key(
            instance_id="instance-id",
            instance_name="instance-name",
            delete_compute_instance=delete_compute_instance,
            wait_for_compute_instance_deletion=lambda _instance_name: calls.append("wait"),
            delete_secret=lambda: calls.append("delete-key"),
        )

    assert calls == ["delete-instance", "delete-key"]


def test_compute_instance_resolves_registered_ssh_key(
    cli: OsacCLI,
    k8s_hub_client: K8sClient,
    default_subnet: str,
    vm_template: str,
) -> None:
    with tempfile.TemporaryDirectory() as tmpdir:
        private_key = Path(tmpdir) / "compute-instance-key"
        subprocess.run(
            ["ssh-keygen", "-t", "ed25519", "-f", str(private_key), "-N", "", "-C", "osac-vmaas-test"],
            check=True,
            capture_output=True,
            text=True,
        )
        public_key = private_key.with_suffix(".pub").read_text()

        key_name = unique_name("e2e-ssh-key")
        public_key_file = private_key.with_suffix(".pub")
        cli.create_secret(
            name=key_name,
            secret_type="ssh-public-key",
            from_files={"public_key": str(public_key_file)},
        )
        instance_id: str | None = None
        instance_name: str | None = None
        try:
            instance_id = cli.create_compute_instance(
                template=vm_template,
                name=unique_name("e2e-ssh-ci"),
                network_attachments=[{"subnet": default_subnet}],
                ssh_key=key_name,
            )
            instance_name = wait_for_cr(k8s=k8s_hub_client, uuid=instance_id)
            wait_for_provision(k8s=k8s_hub_client, name=instance_name)
            wait_for_running(k8s=k8s_hub_client, name=instance_name)

            cr: dict[str, Any] = k8s_hub_client.get_json(resource="computeinstance", name=instance_name)
            # The CI runner cannot route to tenant subnet IPs, so verify the resolved key at the CR boundary.
            assert cr["spec"]["sshKey"] == public_key
        finally:
            _cleanup_registered_ssh_key(
                instance_id=instance_id,
                instance_name=instance_name,
                delete_compute_instance=lambda uuid: cli.delete_compute_instance(uuid=uuid),
                wait_for_compute_instance_deletion=lambda name: wait_for_deletion(k8s=k8s_hub_client, name=name),
                delete_secret=lambda: cli.delete_secret(name=key_name),
            )
