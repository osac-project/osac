"""A tenant boots a ComputeInstance and sees its first-boot configuration inside the guest."""

from __future__ import annotations

import json
import os
import time
from collections.abc import Callable
from contextlib import closing
from pathlib import Path

import pytest

from tests.e2e.core import failure_snapshot
from tests.e2e.core.failures import E2EFailure, Kind
from tests.e2e.core.grpc_client import GRPCClient
from tests.e2e.core.helpers import unique_name, wait_for_cr, wait_for_deletion, wait_for_grpc_removal, wait_for_running
from tests.e2e.core.k8s_client import K8sClient
from tests.e2e.core.osac_cli import OsacCLI
from tests.e2e.core.runner import poll_until
from tests.e2e.core.wait_kinds import WaitResourceKind
from tests.e2e.vmaas.sanity.test_console import _create_ticket, _ws_connect, _ws_recv, _ws_url

pytestmark = pytest.mark.sanity


def cloud_config(banner: str) -> str:
    return f"#cloud-config\nwrite_files:\n  - path: /etc/issue\n    content: |\n      {banner}\n"


def ignition_config(banner: str) -> str:
    issue = {"path": "/etc/issue", "overwrite": True, "mode": 0o644, "contents": {"source": f"data:,{banner}%0A"}}
    return json.dumps({"ignition": {"version": "3.4.0"}, "storage": {"files": [issue]}})


def _wait_for_first_boot_banner(
    *,
    grpc: GRPCClient,
    fulfillment_address: str,
    instance: str,
    expected_banner: str,
    source_ref: str,
    request: pytest.FixtureRequest,
) -> None:
    """Wait for the guest to print the banner supplied in its first-boot data."""
    tail = "\n"
    failure_snapshot.SOURCES.register(
        instance, lambda directory, _window: failure_snapshot.write_text(directory, "guest-console.txt", tail)
    )
    request.addfinalizer(lambda: failure_snapshot.SOURCES.unregister(instance))
    url = _ws_url(fulfillment_address)
    with closing(_ws_connect(url, _create_ticket(grpc, instance)["ticket"])) as console:
        next_enter = time.monotonic()
        deadline = next_enter + 300
        while (remaining := deadline - time.monotonic()) > 0:
            # Redisplay /etc/issue if it was printed before we connected.
            if time.monotonic() >= next_enter:
                console.send_binary(b"\r")
                next_enter = time.monotonic() + 15
            chunk = _ws_recv(console, min(5, remaining))
            if chunk == "" or not console.connected:
                break
            if chunk is not None:
                tail += chunk.replace("\r", "")
                if f"\n{expected_banner}\n" in tail:
                    return
                tail = tail[-8192:]
    raise E2EFailure(
        Kind.STUCK_STATE,
        f"{source_ref}: guest did not display the supplied login banner",
        ids={"compute_instance": instance},
    )


# Pin linux/amd64 manifests: an upstream tag update must not silently change first-boot behavior.
IMAGES = [
    pytest.param(
        "quay.io/containerdisks/ubuntu@sha256:2b5ff51129962719239c359bf721bfe99757cb49884447f1b49b2e585d9e6ddf",
        cloud_config,
        id="ubuntu-24.04-cloud-init",
    ),
    pytest.param(
        "quay.io/containerdisks/debian@sha256:bce71a5b1cc75b62fafccbcf901dc0a56fb07ae0cfaad69abf5051d24124b163",
        cloud_config,
        id="debian-12-cloud-init",
    ),
    pytest.param(
        "quay.io/containerdisks/almalinux@sha256:aed917ae2e03541ce7bcdfc214fc7992f5cc1549e8cd81e41d034da385f7e50d",
        cloud_config,
        id="almalinux-9-cloud-init",
    ),
    pytest.param(
        "quay.io/containerdisks/opensuse-leap@sha256:245bcd7fb1cd50f722cb6c07bb6d91c59093e57fa536c635142c9161290ae76c",
        cloud_config,
        id="opensuse-leap-16-cloud-init",
    ),
    pytest.param(
        "quay.io/fedora/fedora-coreos-kubevirt@sha256:21c2597f6d11e62696ee7f6013d1bc0d4cd1b4307fa2a5c9cc4aa44216927e30",
        ignition_config,
        id="fedora-coreos-ignition",
    ),
]
if rhcos := os.environ.get("OSAC_E2E_RHCOS_IMAGE"):
    IMAGES.append(pytest.param(rhcos, ignition_config, id="rhcos-ignition"))


@pytest.mark.parametrize(("source_ref", "config"), IMAGES)
def test_compute_instance_applies_user_data(
    source_ref: str,
    config: Callable[[str], str],
    cli: OsacCLI,
    grpc: GRPCClient,
    k8s_hub_client: K8sClient,
    vm_template: str,
    default_network_attachment: dict[str, object],
    fulfillment_address: str,
    tmp_path: Path,
    request: pytest.FixtureRequest,
) -> None:
    image = unique_name("e2e-image")
    image_id = grpc.create_disk_image(name=image, source_ref=source_ref)
    request.addfinalizer(lambda: grpc.delete_disk_image(disk_image_id=image_id))

    banner = unique_name("osac-first-boot")
    userdata = tmp_path / "userdata"
    userdata.write_text(config(banner))
    secret = unique_name("e2e-userdata")
    cli.create_secret(name=secret, secret_type="user-data", from_files={"userdata": str(userdata)})
    request.addfinalizer(lambda: cli.delete_secret(name=secret))

    instance = cli.create_compute_instance(
        name=unique_name("e2e-ci"),
        template=vm_template,
        disk_image=image,
        user_data_secret_ref=secret,
        network_attachments=[default_network_attachment],
    )
    cr: str | None = None

    def delete_instance() -> None:
        cli.delete_compute_instance(uuid=instance)
        if cr is not None:
            wait_for_deletion(k8s=k8s_hub_client, name=cr)
        wait_for_grpc_removal(grpc=grpc, uuid=instance)

    request.addfinalizer(delete_instance)
    cr = wait_for_cr(k8s=k8s_hub_client, uuid=instance)
    wait_for_running(k8s=k8s_hub_client, name=cr)
    # The console resolver reads Fulfillment state, which can lag the hub CR.
    poll_until(
        fn=lambda: grpc.get_compute_instance(ci_id=instance)["object"].get("status", {}).get("state"),
        until=lambda state: state == "COMPUTE_INSTANCE_STATE_RUNNING",
        description=f"ComputeInstance {instance} Running in Fulfillment for console access",
        resource_kind=WaitResourceKind.COMPUTE_INSTANCE,
        resource_id=instance,
    )

    _wait_for_first_boot_banner(
        grpc=grpc,
        fulfillment_address=fulfillment_address,
        instance=instance,
        expected_banner=banner,
        source_ref=source_ref,
        request=request,
    )
