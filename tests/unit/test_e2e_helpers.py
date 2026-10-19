from __future__ import annotations

import ipaddress
import subprocess
from unittest.mock import Mock, call

import pytest

from tests.e2e.core import helpers
from tests.e2e.core.grpc_client import GRPCClient
from tests.e2e.core.k8s_client import K8sClient
from tests.e2e.references import conftest as references_conftest
from tests.e2e.references import test_compute_references as compute_references
from tests.e2e.references import test_ip_management_references as ip_management_references


@pytest.mark.parametrize("cleanup_fails", [False, True])
@pytest.mark.parametrize(
    "test_name",
    [
        "test_compute_instance_full_chain_by_name",
        "test_compute_instance_reaches_running_with_name_refs",
        "test_cross_tenant_template_reference",
        "test_compute_instance_user_data_secret_by_name",
    ],
)
def test_compute_references_wait_for_deletion_before_releasing_dependencies(
    monkeypatch: pytest.MonkeyPatch, test_name: str, cleanup_fails: bool
) -> None:
    events: list[str] = []
    grpc = Mock(spec=GRPCClient)
    private_grpc = Mock(spec=GRPCClient)
    k8s = Mock(spec=K8sClient)
    grpc.get_compute_instance_catalog_item.return_value = {"object": {"metadata": {"name": "catalog"}}}
    private_grpc.create_compute_instance_catalog_item.return_value = "catalog-id"
    private_grpc.delete_compute_instance_catalog_item.side_effect = lambda **_: events.append("catalog-deleted")

    def api_call(*, service: str, data: dict) -> dict:
        if service.endswith("Secrets/Create"):
            return {"object": {"id": "secret-id"}}
        if service.endswith("Secrets/Delete"):
            events.append("secret-deleted")
            return {}
        spec = data["object"]["spec"]
        return {
            "object": {
                "id": "ci-id",
                "spec": {
                    **spec,
                    "catalog_item": {**spec["catalog_item"], "id": "catalog-id"},
                    "network_attachments": [
                        {
                            "subnet": {"name": "subnet", "id": "subnet-id"},
                            "security_groups": [{"name": "sg", "id": "sg-id"}],
                        }
                    ],
                    "user_data_secret": {"name": "ref-user-data-test-tag", "id": "secret-id"},
                },
            }
        }

    grpc.call.side_effect = api_call
    grpc.delete_compute_instance.side_effect = lambda **_: events.append("delete-requested")
    api_results = iter([{}, subprocess.CalledProcessError(1, ["grpcurl"], stderr="ERROR:\n  Code: NotFound\n")])

    def get_instance(**_: object) -> dict:
        events.append("api-checked")
        result = next(api_results)
        if isinstance(result, Exception):
            raise result
        return result

    grpc.get_compute_instance.side_effect = get_instance
    cr_results = iter([False, True])
    k8s.is_absent.side_effect = lambda **_: events.append("cr-checked") or next(cr_results)
    k8s.get_compute_instance_phase.return_value = "Running"
    monkeypatch.setattr(compute_references, "uuid4", lambda: Mock(hex="test-tag"))
    monkeypatch.setattr(compute_references, "wait_for_cr", lambda **_: events.append("cr-resolved") or "vm-test")
    monkeypatch.setattr(compute_references, "wait_for_provision", lambda **_: None)
    monkeypatch.setattr(compute_references, "wait_for_running", lambda **_: None)
    monkeypatch.setattr("tests.e2e.core.runner.time.sleep", lambda _: None)
    kwargs = {
        "k8s_hub_client": k8s,
        "ref_subnet": {"id": "subnet-id", "name": "subnet"},
        "ref_security_group": {"id": "sg-id", "name": "sg"},
        "ref_instance_type": "instance-type",
        "ref_disk_image": "disk-image",
        "default_storage_tier": "local",
    }
    if test_name == "test_cross_tenant_template_reference":
        kwargs.update(jwt_grpc_tenant1=grpc, private_grpc=private_grpc, compute_template="template")
    else:
        kwargs.update(grpc=grpc, ref_ci_catalog_item="catalog-id")

    if cleanup_fails:
        k8s.is_absent.side_effect = subprocess.CalledProcessError(1, ["kubectl"], stderr="Forbidden")
        with pytest.raises(RuntimeError, match="cleanup request failed"):
            getattr(compute_references.TestComputeReferences(), test_name)(**kwargs)
        assert "secret-deleted" not in events
        assert "catalog-deleted" not in events
        grpc.get_compute_instance.assert_not_called()
        return

    getattr(compute_references.TestComputeReferences(), test_name)(**kwargs)

    expected = ["cr-resolved", "delete-requested", "cr-checked", "cr-checked", "api-checked", "api-checked"]
    expected.extend(
        {
            "test_cross_tenant_template_reference": ["catalog-deleted"],
            "test_compute_instance_user_data_secret_by_name": ["secret-deleted"],
        }.get(test_name, [])
    )
    assert events == expected
    assert k8s.is_absent.call_args_list == [call(resource="computeinstances.osac.openshift.io", name="vm-test")] * 2


@pytest.fixture
def compute_cleanup_clients(monkeypatch: pytest.MonkeyPatch) -> tuple[Mock, Mock]:
    grpc = Mock(spec=GRPCClient)
    grpc.get_compute_instance.side_effect = subprocess.CalledProcessError(
        1, ["grpcurl"], stderr="ERROR:\n  Code: NotFound\n  Message: instance gone"
    )
    k8s = Mock(spec=K8sClient)
    k8s.is_absent.return_value = True
    monkeypatch.setattr("tests.e2e.core.runner.time.sleep", lambda _: None)
    return grpc, k8s


def test_compute_cleanup_still_requests_delete_when_cr_discovery_times_out(
    monkeypatch: pytest.MonkeyPatch, compute_cleanup_clients: tuple[Mock, Mock]
) -> None:
    grpc, k8s = compute_cleanup_clients
    monkeypatch.setattr(compute_references, "wait_for_cr", Mock(side_effect=TimeoutError("CR discovery timed out")))

    with pytest.raises(TimeoutError, match="CR discovery timed out"):
        compute_references._delete_compute_instance_and_wait(grpc, k8s, "ci-id")

    grpc.delete_compute_instance.assert_called_once_with(ci_id="ci-id")
    k8s.is_absent.assert_not_called()


def test_compute_cleanup_tolerates_already_requested_deletion(compute_cleanup_clients: tuple[Mock, Mock]) -> None:
    grpc, k8s = compute_cleanup_clients
    grpc.delete_compute_instance.side_effect = subprocess.CalledProcessError(
        1, ["grpcurl"], stderr="ERROR:\n  Code: NotFound\n"
    )

    compute_references._delete_compute_instance_and_wait(grpc, k8s, "ci-id", "vm-test")

    k8s.is_absent.assert_called_once_with(resource="computeinstances.osac.openshift.io", name="vm-test")
    grpc.get_compute_instance.assert_called_once_with(ci_id="ci-id")


@pytest.mark.parametrize("stage", ["delete", "cr", "api"])
def test_compute_cleanup_propagates_command_errors_without_credentials(
    compute_cleanup_clients: tuple[Mock, Mock], stage: str
) -> None:
    grpc, k8s = compute_cleanup_clients
    error = subprocess.CalledProcessError(
        1, ["grpcurl", "-H", "Authorization: Bearer test-token"], stderr="ERROR:\n  Code: PermissionDenied\n"
    )
    if stage == "delete":
        grpc.delete_compute_instance.side_effect = error
    elif stage == "cr":
        k8s.is_absent.side_effect = error
    else:
        grpc.get_compute_instance.side_effect = error

    with pytest.raises(RuntimeError, match="ci-id cleanup request failed") as exc_info:
        compute_references._delete_compute_instance_and_wait(grpc, k8s, "ci-id", "vm-test")

    assert "test-token" not in str(exc_info.value)
    assert not isinstance(exc_info.value.__cause__, subprocess.CalledProcessError)


def test_compute_cleanup_preserves_pending_non_subprocess_context(
    monkeypatch: pytest.MonkeyPatch, compute_cleanup_clients: tuple[Mock, Mock]
) -> None:
    grpc, k8s = compute_cleanup_clients
    pending = AssertionError("original test failure")
    grpc.delete_compute_instance.side_effect = subprocess.CalledProcessError(
        1, ["grpcurl", "-H", "Authorization: Bearer test-token"]
    )
    monkeypatch.setattr(compute_references, "wait_for_cr", Mock(side_effect=pending))

    with pytest.raises(RuntimeError, match="ci-id cleanup request failed") as exc_info:
        compute_references._delete_compute_instance_and_wait(grpc, k8s, "ci-id")

    assert exc_info.value.__cause__ is pending
    assert "test-token" not in str(exc_info.value)


def test_compute_cleanup_suppresses_pending_subprocess_context(
    monkeypatch: pytest.MonkeyPatch, compute_cleanup_clients: tuple[Mock, Mock]
) -> None:
    grpc, k8s = compute_cleanup_clients
    pending = subprocess.CalledProcessError(1, ["grpcurl", "-H", "Authorization: Bearer test-token"])
    grpc.delete_compute_instance.side_effect = subprocess.CalledProcessError(
        1, ["grpcurl", "-H", "Authorization: Bearer test-token"]
    )
    monkeypatch.setattr(compute_references, "wait_for_cr", Mock(side_effect=pending))

    with pytest.raises(RuntimeError, match="ci-id cleanup request failed") as exc_info:
        compute_references._delete_compute_instance_and_wait(grpc, k8s, "ci-id")

    assert exc_info.value.__cause__ is None
    assert "test-token" not in str(exc_info.value)


@pytest.mark.parametrize("stage", ["cr", "api"])
def test_compute_cleanup_keeps_existing_polling_budgets(
    monkeypatch: pytest.MonkeyPatch, compute_cleanup_clients: tuple[Mock, Mock], stage: str
) -> None:
    grpc, k8s = compute_cleanup_clients
    sleep = Mock()
    monkeypatch.setattr("tests.e2e.core.runner.time.sleep", sleep)
    if stage == "cr":
        k8s.is_absent.return_value = False
    else:
        grpc.get_compute_instance.side_effect = None
        grpc.get_compute_instance.return_value = {"object": {"id": "ci-id"}}

    with pytest.raises(TimeoutError):
        compute_references._delete_compute_instance_and_wait(grpc, k8s, "ci-id", "vm-test")

    if stage == "cr":
        assert k8s.is_absent.call_count == 120
        grpc.get_compute_instance.assert_not_called()
        assert sleep.call_args_list == [call(5)] * 119
    else:
        assert grpc.get_compute_instance.call_count == 30
        assert sleep.call_args_list == [call(2)] * 29


@pytest.mark.parametrize(
    "output,absent",
    [
        ('Error from server (NotFound): computeinstances.osac.openshift.io "vm-test" not found', True),
        ('Error from server (NotFound): computeinstances.osac.openshift.io "another-vm" not found', False),
        ('Error from server (NotFound): secrets "vm-test" not found', False),
        ('Error from server (Forbidden): computeinstances.osac.openshift.io "vm-test" is forbidden', False),
        ("Unable to connect to the server: TLS handshake timeout", False),
    ],
)
def test_compute_cleanup_requires_exact_kubernetes_notfound(
    monkeypatch: pytest.MonkeyPatch, compute_cleanup_clients: tuple[Mock, Mock], output: str, absent: bool
) -> None:
    grpc, _ = compute_cleanup_clients
    k8s = K8sClient(namespace="test-namespace")
    monkeypatch.setattr("tests.e2e.core.k8s_client.run_unchecked", lambda *_: (output, 1))

    if absent:
        compute_references._delete_compute_instance_and_wait(grpc, k8s, "ci-id", "vm-test")
    else:
        with pytest.raises(RuntimeError, match="cleanup request failed"):
            compute_references._delete_compute_instance_and_wait(grpc, k8s, "ci-id", "vm-test")
        grpc.get_compute_instance.assert_not_called()


def test_allocate_worker_subnet_keeps_prefix_ranges_disjoint(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setattr(helpers.allocate_worker_subnet, "_counters", {}, raising=False)

    monkeypatch.setenv("PYTEST_XDIST_WORKER", "gw3")
    pool_subnet = helpers.allocate_worker_subnet(prefix=24)

    monkeypatch.setenv("PYTEST_XDIST_WORKER", "gw0")
    external_ip_subnet = helpers.allocate_worker_subnet(prefix=30)

    assert pool_subnet == ipaddress.IPv4Network("172.27.96.0/24")
    assert external_ip_subnet == ipaddress.IPv4Network("172.27.128.0/30")
    assert not pool_subnet.overlaps(external_ip_subnet)


def test_allocate_worker_subnet_rejects_worker_outside_reserved_24_range(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setattr(helpers.allocate_worker_subnet, "_counters", {}, raising=False)
    monkeypatch.setenv("PYTEST_XDIST_WORKER", "gw4")

    with pytest.raises(RuntimeError, match="outside the reserved /24 address space"):
        helpers.allocate_worker_subnet(prefix=24)


def _patch_ref_eip_pool_setup(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setattr(references_conftest, "allocate_worker_subnet", lambda: ipaddress.IPv4Network("172.27.0.0/24"))
    monkeypatch.setattr(references_conftest, "wait_for_external_ip_pool_cr", lambda **_: "pool-cr")
    for helper_name in ("wait_for_external_ip_pool_ready", "wait_for_external_ip_pool_grpc_ready"):
        monkeypatch.setattr(references_conftest, helper_name, lambda **_: None)


def test_ref_eip_pool_teardown_removes_dependent_ips_before_pool(monkeypatch: pytest.MonkeyPatch) -> None:
    _patch_ref_eip_pool_setup(monkeypatch)
    wait_for_ip_deletion = Mock()
    wait_for_pool_deletion = Mock()
    monkeypatch.setattr(references_conftest, "wait_for_external_ip_deletion", wait_for_ip_deletion)
    monkeypatch.setattr(references_conftest, "wait_for_external_ip_pool_deletion", wait_for_pool_deletion)

    private_grpc = Mock(spec=GRPCClient)
    private_grpc.create_external_ip_pool.return_value = "pool-id"
    private_grpc.call.side_effect = [
        {
            "items": [
                {"id": "unrelated-ip", "spec": {"pool": {"id": "another-pool"}}},
                {"id": "ip-id-one", "spec": {"pool": {"id": "pool-id"}}},
            ],
            "nextPageToken": "next-page",
        },
        {"items": [{"id": "ip-id-two", "spec": {"pool": {"id": "pool-id"}}}]},
    ]
    k8s = Mock()
    k8s.get_external_ip_name.side_effect = ["ip-one-cr", "ip-two-cr"]
    k8s.is_present.return_value = True

    fixture = references_conftest.ref_eip_pool
    generator = getattr(fixture, "__wrapped__", fixture)(private_grpc, k8s, "test-run")
    next(generator)

    with pytest.raises(StopIteration):
        next(generator)

    assert private_grpc.method_calls == [
        call.create_external_ip_pool(name="ref-eip-pool-test-run", cidrs=["172.27.0.0/24"]),
        call.call(service="osac.private.v1.ExternalIPs/List", data=None),
        call.call(service="osac.private.v1.ExternalIPs/List", data={"pageToken": "next-page"}),
        call.delete_external_ip(external_ip_id="ip-id-one"),
        call.delete_external_ip(external_ip_id="ip-id-two"),
        call.delete_external_ip_pool(pool_id="pool-id"),
    ]
    assert wait_for_ip_deletion.call_args_list == [call(k8s=k8s, name="ip-one-cr"), call(k8s=k8s, name="ip-two-cr")]
    wait_for_pool_deletion.assert_called_once_with(k8s=k8s, name="pool-cr")


def test_ref_eip_pool_teardown_propagates_pool_deletion_error(monkeypatch: pytest.MonkeyPatch) -> None:
    _patch_ref_eip_pool_setup(monkeypatch)
    private_grpc = Mock(spec=GRPCClient)
    private_grpc.create_external_ip_pool.return_value = "pool-id"
    private_grpc.call.return_value = {"items": []}
    private_grpc.delete_external_ip_pool.side_effect = subprocess.CalledProcessError(
        1, ["grpcurl"], stderr="Authorization: Bearer secret-token"
    )

    fixture = references_conftest.ref_eip_pool
    generator = getattr(fixture, "__wrapped__", fixture)(private_grpc, Mock(), "test-run")
    next(generator)

    with pytest.raises(subprocess.CalledProcessError, match="grpcurl"):
        next(generator)


def test_ref_eip_pool_teardown_attempts_pool_deletion_after_child_cleanup_failure(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    _patch_ref_eip_pool_setup(monkeypatch)
    cleanup_error = RuntimeError("child cleanup failed")
    pool_error = subprocess.CalledProcessError(1, ["grpcurl"], stderr="Code: Internal")
    monkeypatch.setattr(references_conftest, "_cleanup_ref_pool_external_ips", Mock(side_effect=cleanup_error))

    private_grpc = Mock(spec=GRPCClient)
    private_grpc.create_external_ip_pool.return_value = "pool-id"
    private_grpc.delete_external_ip_pool.side_effect = pool_error

    fixture = references_conftest.ref_eip_pool
    generator = getattr(fixture, "__wrapped__", fixture)(private_grpc, Mock(), "test-run")
    next(generator)

    with pytest.raises(ExceptionGroup) as exc_info:
        next(generator)

    assert exc_info.value.exceptions == (cleanup_error, pool_error)
    private_grpc.delete_external_ip_pool.assert_called_once_with(pool_id="pool-id")


def test_ref_eip_pool_teardown_resolves_cr_name_after_setup_failure(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setattr(references_conftest, "allocate_worker_subnet", lambda: ipaddress.IPv4Network("172.27.0.0/24"))
    monkeypatch.setattr(
        references_conftest, "wait_for_external_ip_pool_cr", Mock(side_effect=TimeoutError("CR not found"))
    )
    monkeypatch.setattr(references_conftest, "_cleanup_ref_pool_external_ips", lambda *_: None)
    wait_for_pool_deletion = Mock()
    monkeypatch.setattr(references_conftest, "wait_for_external_ip_pool_deletion", wait_for_pool_deletion)

    private_grpc = Mock(spec=GRPCClient)
    private_grpc.create_external_ip_pool.return_value = "pool-id"
    k8s = Mock()
    k8s.get_external_ip_pool_name.return_value = "pool-cr"
    k8s.is_present.return_value = True

    fixture = references_conftest.ref_eip_pool
    generator = getattr(fixture, "__wrapped__", fixture)(private_grpc, k8s, "test-run")

    with pytest.raises(TimeoutError, match="CR not found"):
        next(generator)

    private_grpc.delete_external_ip_pool.assert_called_once_with(pool_id="pool-id")
    k8s.get_external_ip_pool_name.assert_called_once_with(uuid="pool-id", checked=False)
    wait_for_pool_deletion.assert_called_once_with(k8s=k8s, name="pool-cr")


def test_nat_gateway_reference_waits_for_external_ip_allocation_and_deletion(monkeypatch: pytest.MonkeyPatch) -> None:
    events: list[str] = []
    k8s = Mock()
    grpc = Mock(spec=GRPCClient)
    grpc.call.side_effect = [
        {"object": {"id": "external-ip-id"}},
        {
            "object": {
                "spec": {
                    "virtualNetwork": {"id": "virtual-network-id", "name": "virtual-network"},
                    "externalIp": {"id": "external-ip-id", "name": "ref-nat-eip-test-tag"},
                }
            }
        },
        {"items": []},
    ]
    grpc.create_nat_gateway.side_effect = lambda **_: events.append("nat-created") or "nat-id"
    grpc.delete_external_ip.side_effect = lambda **_: events.append("external-ip-delete-requested")

    def wait_for_cr(**_: object) -> str:
        events.append("external-ip-cr-created")
        return "external-ip-cr"

    def wait_for_allocation(**_: object) -> None:
        events.append("external-ip-allocated")

    def wait_for_deletion(**_: object) -> None:
        events.append("external-ip-deleted")

    monkeypatch.setattr(ip_management_references, "wait_for_external_ip_cr", wait_for_cr)
    monkeypatch.setattr(ip_management_references, "wait_for_external_ip_allocated", wait_for_allocation)
    monkeypatch.setattr(ip_management_references, "wait_for_external_ip_deletion", wait_for_deletion)
    monkeypatch.setattr(ip_management_references, "uuid4", lambda: Mock(hex="test-tag"))

    ip_management_references.TestIPManagementReferences().test_nat_gateway_by_name(
        grpc=grpc,
        k8s_hub_client=k8s,
        ref_virtual_network={"id": "virtual-network-id", "name": "virtual-network"},
        ref_eip_pool={"id": "pool-id", "name": "pool"},
        ref_test_run_id="test-run",
    )

    assert events == [
        "external-ip-cr-created",
        "external-ip-allocated",
        "nat-created",
        "external-ip-delete-requested",
        "external-ip-deleted",
    ]
