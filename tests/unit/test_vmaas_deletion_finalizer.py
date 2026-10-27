from __future__ import annotations

import subprocess
from unittest.mock import Mock

import pytest

from tests.e2e.core.grpc_client import GRPCClient
from tests.e2e.vmaas.regression import test_compute_instance_disk_image as disk_image


@pytest.fixture
def private_grpc(monkeypatch: pytest.MonkeyPatch) -> Mock:
    monkeypatch.setattr("tests.e2e.core.runner.time.sleep", lambda _: None)
    return Mock(spec=GRPCClient)


@pytest.mark.parametrize("present", [False, True])
@pytest.mark.parametrize("output_stream", ["stderr", "output"])
def test_finalizer_conflict_rereads_version_and_preserves_concurrent_finalizers(
    private_grpc: Mock, present: bool, output_stream: str
) -> None:
    finalizers = ["controller", disk_image.DELETION_TEST_FINALIZER] if not present else ["controller"]
    private_grpc.call.side_effect = [
        {"object": {"metadata": {"version": 9, "finalizers": finalizers}}},
        subprocess.CalledProcessError(74, ["grpcurl"], **{output_stream: "ERROR:\n  Code: Aborted\n"}),
        {"object": {"metadata": {"version": 10, "finalizers": [*finalizers, "concurrent-finalizer"]}}},
        {},
    ]

    disk_image._set_deletion_test_finalizer(private_grpc, "ci-id", present=present)

    assert private_grpc.call.call_count == 4
    updates = [call.kwargs["data"] for call in private_grpc.call.call_args_list[1::2]]
    for update, version in zip(updates, [9, 10], strict=True):
        assert update["lock"] is True
        assert update["updateMask"] == {"paths": ["metadata.finalizers"]}
        assert update["object"]["id"] == "ci-id"
        assert update["object"]["metadata"]["version"] == version
    expected = ["controller", "concurrent-finalizer"]
    if present:
        expected.append(disk_image.DELETION_TEST_FINALIZER)
    assert updates[1]["object"]["metadata"]["finalizers"] == expected


@pytest.mark.parametrize("present", [False, True])
def test_finalizer_update_is_idempotent(private_grpc: Mock, present: bool) -> None:
    finalizers = ["controller", disk_image.DELETION_TEST_FINALIZER] if present else ["controller"]
    private_grpc.call.return_value = {"object": {"metadata": {"version": 9, "finalizers": finalizers}}}

    disk_image._set_deletion_test_finalizer(private_grpc, "ci-id", present=present)

    assert private_grpc.call.call_count == 1


@pytest.mark.parametrize("code", ["PermissionDenied", "InvalidArgument", "NotFound", "Unavailable", "AbortedExtra"])
def test_finalizer_update_does_not_retry_other_errors(private_grpc: Mock, code: str) -> None:
    error = subprocess.CalledProcessError(1, ["grpcurl"], stderr=f"ERROR:\n  Code: {code}\n  Message: Aborted\n")
    private_grpc.call.side_effect = [{"object": {"metadata": {"version": 9}}}, error]

    with pytest.raises(subprocess.CalledProcessError) as exc_info:
        disk_image._set_deletion_test_finalizer(private_grpc, "ci-id", present=True)

    assert exc_info.value is error
    assert private_grpc.call.call_count == 2


def test_finalizer_conflict_retries_are_bounded(private_grpc: Mock) -> None:
    def conflict(*, service: str, data: dict) -> dict:
        if service.endswith("/Get"):
            return {"object": {"metadata": {"version": 9}}}
        raise subprocess.CalledProcessError(74, ["grpcurl"], stderr="ERROR:\n  Code: Aborted\n")

    private_grpc.call.side_effect = conflict

    with pytest.raises(TimeoutError):
        disk_image._set_deletion_test_finalizer(private_grpc, "ci-id", present=True)

    assert private_grpc.call.call_count == 20
