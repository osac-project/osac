from __future__ import annotations

import subprocess
from uuid import uuid4

from tests.core.grpc_client import GRPCClient, PRIVATE_API
from tests.core.osac_cli import OsacCLI

TEST_CORES: int = 4
TEST_MEMORY_GIB: int = 8


def _assert_state_transition(
    private_grpc: GRPCClient, it_name: str, target_state: str,
) -> None:
    """Update an InstanceType to *target_state* and verify the transition."""
    private_grpc.update_instance_type(name=it_name, state=target_state)
    response = private_grpc.get_instance_type(name=it_name)
    actual = response["object"]["spec"]["state"]
    assert actual == target_state, (
        f"state transition to {target_state}: {actual} != {target_state}"
    )


def test_instance_type_lifecycle(cli: OsacCLI, private_grpc: GRPCClient) -> None:
    it_name: str = f"e2e-lifecycle-{uuid4().hex[:8]}"

    try:
        # 1. CREATE via private gRPC (admin operation)
        private_grpc.create_instance_type(
            name=it_name,
            cores=TEST_CORES,
            memory_gib=TEST_MEMORY_GIB,
            description="Lifecycle test type",
        )
        names: list[str] = private_grpc.list_instance_type_names()
        assert it_name in names, f"InstanceType {it_name} not found in list after create: {names}"

        # 2. GET via gRPC (verify API fields)
        response: dict = private_grpc.get_instance_type(name=it_name)
        obj: dict = response["object"]
        assert obj["spec"]["cores"] == TEST_CORES, (
            f"spec.cores mismatch: {obj['spec']['cores']} != {TEST_CORES}"
        )
        assert obj["spec"]["memoryGib"] == TEST_MEMORY_GIB, (
            f"spec.memoryGib mismatch: {obj['spec']['memoryGib']} != {TEST_MEMORY_GIB}"
        )
        assert obj["spec"]["state"] == "INSTANCE_TYPE_STATE_ACTIVE", (
            f"spec.state mismatch: {obj['spec']['state']} != INSTANCE_TYPE_STATE_ACTIVE"
        )
        assert obj["metadata"]["name"] == it_name, (
            f"metadata.name mismatch: {obj['metadata']['name']} != {it_name}"
        )

        # Verify CLI describe works
        cli_output = cli.describe_instance_type(name=it_name)
        assert it_name in cli_output, f"CLI describe should show {it_name}: {cli_output}"

        # 3. STATE TRANSITION: ACTIVE -> DEPRECATED
        _assert_state_transition(private_grpc, it_name, "INSTANCE_TYPE_STATE_DEPRECATED")

        # 4. STATE TRANSITION: DEPRECATED -> OBSOLETE
        _assert_state_transition(private_grpc, it_name, "INSTANCE_TYPE_STATE_OBSOLETE")

        # 5. STATE TRANSITION: OBSOLETE -> ACTIVE (reactivation)
        _assert_state_transition(private_grpc, it_name, "INSTANCE_TYPE_STATE_ACTIVE")

        # 6. DELETE via private gRPC (admin operation)
        private_grpc.delete_instance_type(name=it_name)
        names = private_grpc.list_instance_type_names()
        assert it_name not in names, f"InstanceType {it_name} still in list after delete: {names}"

        # 7. NEGATIVE: get after delete should fail
        output, rc = private_grpc.call_unchecked(
            service=f"{PRIVATE_API}.InstanceTypes/Get", data={"id": it_name},
        )
        assert rc != 0, f"get after delete should fail, but rc={rc}, output: {output}"
        error_lower = output.lower()
        assert any(term in error_lower for term in [
            "not found", "404", "notfound",
        ]), f"Expected not-found error after delete, got: {output}"

    finally:
        try:
            private_grpc.delete_instance_type(name=it_name)
        except subprocess.CalledProcessError as e:
            output = ((e.stdout or "") + (e.stderr or "")).lower()
            if "not found" not in output:
                raise
