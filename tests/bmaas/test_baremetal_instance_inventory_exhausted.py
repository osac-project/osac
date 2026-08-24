from __future__ import annotations

import re
import subprocess

import pytest

from tests.core.grpc_client import GRPCClient
from tests.core.helpers import (
    assert_bmi_does_not_become_running,
    assert_bmi_lifecycle_on_running,
    wait_for_bmi_cr,
    wait_for_bmi_deletion,
    wait_for_bmi_grpc_removal,
    wait_for_bmi_running,
    wait_for_bmi_running_after_recovery,
)
from tests.core.k8s_client import K8sClient
from tests.core.osac_cli import OsacCLI
from tests.core.runner import poll_until

_AVAILABLE_BMH_STATES = {"available", "ready"}
_NOT_FOUND_RE = re.compile(r"Code:\s*NotFound|baremetalinstance\b.*\bnot found", re.IGNORECASE)


def _is_not_found(exc: subprocess.CalledProcessError) -> bool:
    """Return True when delete failed because the BareMetalInstance is already gone."""
    combined = (exc.stderr or "") + (exc.stdout or "")
    return bool(_NOT_FOUND_RE.search(combined))


def _cleanup_bmi(*, cli: OsacCLI, grpc: GRPCClient, k8s: K8sClient, bmi_id: str) -> None:
    """Delete a BareMetalInstance and wait for CR/gRPC removal.

    Only a NotFound response from delete is treated as success (already gone).
    Deletion/removal timeouts and other errors are raised.
    """
    bmi_cr: str = k8s.get_baremetal_instance_name(uuid=bmi_id, checked=False)
    if not bmi_cr and bmi_id not in grpc.list_baremetal_instance_ids():
        return

    try:
        cli.delete_baremetal_instance(uuid=bmi_id)
    except subprocess.CalledProcessError as exc:
        if not _is_not_found(exc):
            raise

    if bmi_cr and k8s.is_present(resource="baremetalinstance", name=bmi_cr):
        wait_for_bmi_deletion(k8s=k8s, name=bmi_cr)
    wait_for_bmi_grpc_removal(grpc=grpc, uuid=bmi_id)


@pytest.mark.regression
def test_baremetal_instance_inventory_exhausted(
    cli: OsacCLI,
    grpc: GRPCClient,
    k8s_hub_client: K8sClient,
    catalog_item: str,
    bmh_namespace: str,
    test_run_id: str,
    ssh_public_key: str,
) -> None:
    """Exhaust BMH inventory, assert overflow stalls/fails, then recover after free capacity.

    Portable across labs/CI (e.g. 2 virtual BMHs → BMI #1, #2, then #3 overflow):
    1. Create N BMIs up front so cluster-side provisioning can overlap
    2. Wait for all N to reach RUNNING, then run full lifecycle checks on BMI #1
    3. Create BMI N+1 and assert it does not reach RUNNING (no free BMH)
    4. Delete one claimed BMI to free a BMH
    5. Assert the overflow BMI recovers to RUNNING
    6. Cleanup remaining instances
    """
    available_count: int = k8s_hub_client.count_bmhs_by_provisioning_state(
        bmh_namespace=bmh_namespace, states=_AVAILABLE_BMH_STATES
    )
    assert available_count >= 1, (
        f"Need at least 1 available BMH to exercise inventory exhaustion; found {available_count}"
    )

    bmi_ids: list[str] = []
    try:
        # Kick off all claim BMIs first so provisioning can proceed in parallel.
        for idx in range(1, available_count + 1):
            bmi_id = cli.create_baremetal_instance(
                name=f"e2e-bmi-inv-{test_run_id}-{idx}", catalog_item=catalog_item, ssh_key=ssh_public_key
            )
            bmi_ids.append(bmi_id)

        for bmi_id in bmi_ids:
            wait_for_bmi_cr(k8s=k8s_hub_client, uuid=bmi_id)
            wait_for_bmi_running(grpc=grpc, bmi_id=bmi_id)

        # Lifecycle checks after all claim BMIs are up (not interleaved with creates).
        assert_bmi_lifecycle_on_running(
            grpc=grpc,
            k8s=k8s_hub_client,
            bmi_id=bmi_ids[0],
            bmh_namespace=bmh_namespace,
        )

        available_after_claim: int = k8s_hub_client.count_bmhs_by_provisioning_state(
            bmh_namespace=bmh_namespace, states=_AVAILABLE_BMH_STATES
        )
        assert available_after_claim == 0, (
            f"Expected 0 available BMHs after claiming {available_count} hosts; found {available_after_claim}"
        )

        overflow_idx = available_count + 1
        overflow_id: str = cli.create_baremetal_instance(
            name=f"e2e-bmi-inv-{test_run_id}-{overflow_idx}", catalog_item=catalog_item, ssh_key=ssh_public_key
        )
        bmi_ids.append(overflow_id)
        assert overflow_id in grpc.list_baremetal_instance_ids()
        wait_for_bmi_cr(k8s=k8s_hub_client, uuid=overflow_id)

        overflow_state: str = assert_bmi_does_not_become_running(grpc=grpc, bmi_id=overflow_id)
        assert overflow_state != "BARE_METAL_INSTANCE_STATE_RUNNING"

        available_during_overflow: int = k8s_hub_client.count_bmhs_by_provisioning_state(
            bmh_namespace=bmh_namespace, states=_AVAILABLE_BMH_STATES
        )
        assert available_during_overflow == 0, (
            f"Overflow BMI must not claim a BMH; available count={available_during_overflow}"
        )

        # Free one BMH so the overflow instance can be scheduled.
        released_id: str = bmi_ids[0]
        _cleanup_bmi(cli=cli, grpc=grpc, k8s=k8s_hub_client, bmi_id=released_id)
        bmi_ids.remove(released_id)

        poll_until(
            fn=lambda: k8s_hub_client.count_bmhs_by_provisioning_state(
                bmh_namespace=bmh_namespace, states=_AVAILABLE_BMH_STATES
            ),
            until=lambda v: v >= 1,
            retries=120,
            delay=10,
            description="at least one BMH available after release",
        )

        wait_for_bmi_running_after_recovery(grpc=grpc, bmi_id=overflow_id)
    finally:
        for bmi_id in reversed(bmi_ids):
            _cleanup_bmi(cli=cli, grpc=grpc, k8s=k8s_hub_client, bmi_id=bmi_id)
