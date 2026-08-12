from __future__ import annotations

from tests.core.grpc_client import GRPCClient
from tests.core.helpers import (
    wait_for_bmh_available,
    wait_for_bmh_provisioned,
    wait_for_bmi_cr,
    wait_for_bmi_deletion,
    wait_for_bmi_grpc_removal,
    wait_for_bmi_running,
)
from tests.core.k8s_client import K8sClient
from tests.core.osac_cli import OsacCLI
from tests.core.runner import poll_until


def test_baremetal_instance_lifecycle(
    cli: OsacCLI,
    grpc: GRPCClient,
    k8s_hub_client: K8sClient,
    catalog_item: str,
    bmh_namespace: str,
    test_run_id: str,
    ssh_public_key: str,
) -> None:
    name = f"e2e-bmi-{test_run_id}"
    bmi_id: str = cli.create_baremetal_instance(name=name, catalog_item=catalog_item, ssh_key=ssh_public_key)

    try:
        assert bmi_id in grpc.list_baremetal_instance_ids()

        bmi_cr_name: str = wait_for_bmi_cr(k8s=k8s_hub_client, uuid=bmi_id)
        wait_for_bmi_running(grpc=grpc, bmi_id=bmi_id)

        external_host_id: str = k8s_hub_client.get_baremetal_instance_external_host_id(name=bmi_cr_name)
        assert "/" in external_host_id, f"Expected namespace/name format, got: {external_host_id}"
        bmh_ns, bmh_name = external_host_id.split("/", 1)
        assert bmh_ns == bmh_namespace, f"BMH landed in {bmh_ns}, expected {bmh_namespace}"

        # Verify provisioning
        wait_for_bmh_provisioned(k8s=k8s_hub_client, name=bmh_name, bmh_namespace=bmh_ns)

        image_url: str = k8s_hub_client.get_bmh_image_url(name=bmh_name, bmh_namespace=bmh_ns)
        assert image_url != "", f"BMH {bmh_name} has no image URL after provisioning"

        consumer_ref: str = k8s_hub_client.get_bmh_consumer_ref(name=bmh_name, bmh_namespace=bmh_ns)
        assert consumer_ref != "", f"BMH {bmh_name} has no consumerRef after allocation"

        online: str = k8s_hub_client.get_bmh_online(name=bmh_name, bmh_namespace=bmh_ns)
        assert online == "true", f"BMH {bmh_name} should be online after provisioning, got: {online}"

        # Power off
        halted = "BARE_METAL_INSTANCE_RUN_STRATEGY_HALTED"
        grpc.update_baremetal_instance_run_strategy(bmi_id=bmi_id, run_strategy=halted)

        poll_until(
            fn=lambda: k8s_hub_client.get_bmh_powered_on(name=bmh_name, bmh_namespace=bmh_ns),
            until=lambda v: v == "false",
            retries=60,
            delay=5,
            description=f"{bmh_name} powered off",
        )

        # Power on
        grpc.update_baremetal_instance_run_strategy(
            bmi_id=bmi_id, run_strategy="BARE_METAL_INSTANCE_RUN_STRATEGY_ALWAYS"
        )

        poll_until(
            fn=lambda: k8s_hub_client.get_bmh_powered_on(name=bmh_name, bmh_namespace=bmh_ns),
            until=lambda v: v == "true",
            retries=60,
            delay=5,
            description=f"{bmh_name} powered on",
        )

        # Deprovision
        cli.delete_baremetal_instance(uuid=bmi_id)
        wait_for_bmi_deletion(k8s=k8s_hub_client, name=bmi_cr_name)
        wait_for_bmi_grpc_removal(grpc=grpc, uuid=bmi_id)

        wait_for_bmh_available(k8s=k8s_hub_client, name=bmh_name, bmh_namespace=bmh_ns)

        image_url_after: str = k8s_hub_client.get_bmh_image_url(name=bmh_name, bmh_namespace=bmh_ns)
        assert image_url_after == "", f"BMH {bmh_name} image not cleared after deprovision: {image_url_after}"

        consumer_ref_after: str = k8s_hub_client.get_bmh_consumer_ref(name=bmh_name, bmh_namespace=bmh_ns)
        assert consumer_ref_after == "", f"BMH {bmh_name} consumerRef not cleared: {consumer_ref_after}"
    except BaseException:
        bmi_cr: str = k8s_hub_client.get_baremetal_instance_name(uuid=bmi_id, checked=False)
        if bmi_cr:
            try:
                cli.delete_baremetal_instance(uuid=bmi_id)
                wait_for_bmi_deletion(k8s=k8s_hub_client, name=bmi_cr)
                wait_for_bmi_grpc_removal(grpc=grpc, uuid=bmi_id)
            except Exception:
                pass
        raise
