from __future__ import annotations

from unittest.mock import Mock

import pytest

from tests.e2e.caas.regression import test_cluster_version_disk_image as scenario
from tests.e2e.core.grpc_client import GRPCClient

VERSION = {"id": "version-id", "name": "4-22-0-e2e-no-disk-image"}
VALID_ERROR = (
    "ERROR:\n  Code: FailedPrecondition\n"
    f"  Message: cluster version '{VERSION['name']}' does not have a disk image attached"
)


def _run_scenario(output: str, returncode: int) -> tuple[Mock, Mock]:
    public = Mock(spec=GRPCClient)
    public.call_unchecked.return_value = (output, returncode)
    private = Mock(spec=GRPCClient)
    private.ensure_cluster_version.return_value = VERSION
    scenario.test_cluster_create_rejected_without_disk_image_for_baremetal_workers(
        grpc=public, private_grpc=private, cluster_template="template-a"
    )
    return public, private


def test_missing_disk_image_scenario_accepts_shared_validation_error() -> None:
    public, private = _run_scenario(VALID_ERROR, 1)

    # Keep the intentionally unbacked fixture: adding an image would bypass
    # the negative API contract rather than fix its obsolete error assertion.
    version_args = private.ensure_cluster_version.call_args.kwargs
    assert "disk_image" not in version_args
    assert version_args["version"].startswith("4.22.0-e2e-no-disk-image")
    assert public.call_unchecked.call_args.kwargs["data"]["object"]["spec"]["version"] == {
        "name": VERSION["name"],
        "shared": True,
    }
    private.call_unchecked.assert_called_once_with(
        service="osac.private.v1.ClusterVersions/Delete", data={"id": VERSION["id"]}
    )


@pytest.mark.parametrize(
    ("output", "returncode"),
    [
        (VALID_ERROR, 0),
        (VALID_ERROR.replace("FailedPrecondition", "InvalidArgument"), 1),
        (VALID_ERROR.replace(VERSION["name"], "other-version"), 1),
        (VALID_ERROR.replace("does not have a disk image attached", "disk image registry is unreachable"), 1),
    ],
)
def test_missing_disk_image_scenario_rejects_success_or_other_errors(output: str, returncode: int) -> None:
    with pytest.raises(AssertionError):
        _run_scenario(output, returncode)
