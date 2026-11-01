from __future__ import annotations

import ipaddress
import subprocess
from pathlib import Path
from types import SimpleNamespace
from typing import Any

import pytest

from tests.e2e.core.wait_kinds import WaitResourceKind
from tests.e2e.networking import teardown
from tests.e2e.networking.cidrs import TENANT_SECOND_OCTETS, run_offset, subnet_cidr, tenant_vn_cidr
from tests.e2e.networking.vmaas import conftest as vmaas_conftest
from tests.e2e.networking.vmaas import stack
from tests.e2e.networking.vmaas import test_vmaas_networking as test_module

SUITE = Path(vmaas_conftest.__file__).parent


def _item(path: Path, name: str) -> SimpleNamespace:
    markers: list[object] = []
    return SimpleNamespace(path=path, name=name, add_marker=markers.append, markers=markers)


def _collect(monkeypatch: pytest.MonkeyPatch, *, enabled: bool) -> list[Any]:
    monkeypatch.setenv(vmaas_conftest.EXTERNAL_PATH_FLAG, "true" if enabled else "false")
    items = [
        _item(SUITE / "external_ip" / "test_pool.py", "pool"),
        _item(Path("/elsewhere/tests/e2e/vmaas/test_other.py"), "other-before"),
        _item(SUITE / "references" / "test_refs.py", "refs"),
        _item(SUITE / "test_vmaas_networking.py", "main-0"),
        _item(Path("/elsewhere/tests/e2e/vmaas/test_later.py"), "other-after"),
        _item(SUITE / "test_vmaas_networking.py", "main-1"),
    ]
    vmaas_conftest.pytest_collection_modifyitems(config=SimpleNamespace(), items=items)
    return items


def test_main_suite_runs_before_the_external_directories(monkeypatch: pytest.MonkeyPatch) -> None:
    items = _collect(monkeypatch, enabled=True)

    inside = [item.name for item in items if item.path.is_relative_to(SUITE)]
    assert inside == ["main-0", "main-1", "pool", "refs"]


def test_items_outside_the_suite_keep_their_positions(monkeypatch: pytest.MonkeyPatch) -> None:
    items = _collect(monkeypatch, enabled=True)

    assert [(i, item.name) for i, item in enumerate(items) if not item.path.is_relative_to(SUITE)] == [
        (1, "other-before"),
        (4, "other-after"),
    ]


def test_external_directories_are_skipped_only_when_the_flag_is_off(monkeypatch: pytest.MonkeyPatch) -> None:
    off = _collect(monkeypatch, enabled=False)
    on = _collect(monkeypatch, enabled=True)

    assert {item.name: bool(item.markers) for item in off} == {
        "main-0": False,
        "main-1": False,
        "pool": True,
        "refs": True,
        "other-before": False,
        "other-after": False,
    }
    assert not any(item.markers for item in on)


@pytest.mark.parametrize(
    ("worker", "expected"),
    [
        ("gw0", ("172.16.0.0/16", "172.17.0.0/16", "172.18.0.0/16")),
        ("gw1", ("172.19.0.0/16", "172.20.0.0/16", "172.21.0.0/16")),
        ("master", ("172.16.0.0/16", "172.17.0.0/16", "172.18.0.0/16")),
        ("gw3", ("172.25.0.0/16", "172.26.0.0/16", "172.28.0.0/16")),
    ],
)
def test_network_cidrs_reserve_three_per_worker(
    monkeypatch: pytest.MonkeyPatch, worker: str, expected: tuple[str, str, str]
) -> None:
    monkeypatch.setenv("PYTEST_XDIST_WORKER", worker)

    assert stack.network_cidrs() == expected


def test_network_cidrs_avoid_excluded_and_openshift_ranges() -> None:
    for index in range(len(TENANT_SECOND_OCTETS)):
        network = ipaddress.ip_network(tenant_vn_cidr(index))
        assert network.subnet_of(ipaddress.ip_network("172.16.0.0/12"))
        assert not network.overlaps(ipaddress.ip_network("172.27.0.0/16"))
        assert not network.overlaps(ipaddress.ip_network("172.30.0.0/16"))


def test_tenant_vn_cidr_is_unique_for_every_index_and_run_offset() -> None:
    for offset in range(len(TENANT_SECOND_OCTETS)):
        cidrs = [tenant_vn_cidr(index, offset=offset) for index in range(len(TENANT_SECOND_OCTETS))]
        assert len(set(cidrs)) == len(cidrs)


def test_subnet_cidr_is_a_slash_24_of_the_network() -> None:
    assert subnet_cidr("172.20.0.0/16", 2) == "172.20.2.0/24"
    assert run_offset("ff00") < len(TENANT_SECOND_OCTETS)


def test_network_cidrs_rejects_a_worker_beyond_the_reserved_range(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setenv("PYTEST_XDIST_WORKER", "gw4")

    with pytest.raises(RuntimeError, match="no reserved"):
        stack.network_cidrs()


DEFAULT = "default via 192.168.1.1 dev eth0 proto dhcp metric 100"


def test_route_uses_default_when_via_and_dev_match() -> None:
    assert stack.route_uses_default("198.51.100.25 via 192.168.1.1 dev eth0 src 192.168.1.9 uid 0", DEFAULT)


def test_route_does_not_use_default_when_the_next_hop_differs() -> None:
    assert not stack.route_uses_default("198.51.100.25 via 10.9.0.1 dev eth0 src 192.168.1.9 uid 0", DEFAULT)
    assert not stack.route_uses_default("198.51.100.25 via 192.168.1.1 dev wg0 src 10.0.0.2 uid 0", DEFAULT)


def test_on_link_route_is_not_the_default() -> None:
    assert not stack.route_uses_default("198.51.100.25 dev br-pool src 198.51.100.24 uid 0", DEFAULT)


def test_route_with_no_default_route_is_not_the_default() -> None:
    assert not stack.route_uses_default("198.51.100.25 via 192.168.1.1 dev eth0", "")


def test_assert_pool_routed_fails_without_a_specific_route(monkeypatch: pytest.MonkeyPatch) -> None:
    outputs = iter(["198.51.100.25 via 192.168.1.1 dev eth0 uid 0", DEFAULT])
    monkeypatch.setattr(
        stack.subprocess, "run", lambda *_a, **_k: SimpleNamespace(returncode=0, stdout=next(outputs), stderr="")
    )

    with pytest.raises(pytest.fail.Exception, match=r"no route for ExternalIP pool 198\.51\.100\.24/29"):
        stack.assert_pool_routed("198.51.100.24/29")


def test_assert_pool_routed_fails_when_ip_fails(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setattr(
        stack.subprocess, "run", lambda *_a, **_k: SimpleNamespace(returncode=2, stdout="", stderr="RTNETLINK")
    )

    with pytest.raises(pytest.fail.Exception, match="OSAC_EXTERNAL_IP_POOL_CIDR"):
        stack.assert_pool_routed("198.51.100.24/29")


def test_assert_pool_routed_passes_for_a_specific_route(monkeypatch: pytest.MonkeyPatch) -> None:
    commands: list[list[str]] = []
    outputs = iter(["198.51.100.25 via 172.16.0.2 dev isp0 uid 0", DEFAULT])

    def run(command: list[str], **_kwargs: object) -> SimpleNamespace:
        commands.append(command)
        return SimpleNamespace(returncode=0, stdout=next(outputs), stderr="")

    monkeypatch.setattr(stack.subprocess, "run", run)

    stack.assert_pool_routed("198.51.100.24/29")

    assert commands[0] == ["ip", "-o", "route", "get", "198.51.100.25"]


def test_require_host_tools_names_what_is_missing(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setattr(stack.shutil, "which", lambda tool: None if tool == "kubectl" else f"/bin/{tool}")

    with pytest.raises(pytest.fail.Exception, match="lacks kubectl"):
        stack.require_host_tools()


def test_require_host_tools_passes_with_every_tool(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setattr(stack.shutil, "which", lambda tool: f"/bin/{tool}")

    stack.require_host_tools()


def test_poll_or_fail_turns_a_timeout_into_a_diagnosed_failure(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setattr("tests.e2e.core.runner.time.sleep", lambda _delay: None)

    with pytest.raises(pytest.fail.Exception, match=r"thing timed out\. look at the lab"):
        stack.poll_or_fail(
            hint="look at the lab",
            fn=lambda: False,
            until=bool,
            retries=2,
            delay=0,
            description="thing",
            resource_kind=WaitResourceKind.COMPUTE_INSTANCE,
            resource_id="ci-1",
        )


class _FakeClient:
    """A tenant client whose delete raises once the object is already gone."""

    def __init__(self, listed: list[str]) -> None:
        self.listed = listed
        self.deletes = 0

    def list_compute_instance_ids(self) -> list[str]:
        return list(self.listed)

    def delete_compute_instance(self, *, ci_id: str) -> None:
        self.deletes += 1
        if ci_id not in self.listed:
            raise RuntimeError("NotFound")
        self.listed.remove(ci_id)


def _hub(present: bool) -> SimpleNamespace:
    return SimpleNamespace(is_present=lambda **_kwargs: present)


def test_delete_vm_is_idempotent_when_the_object_is_already_gone(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setattr("tests.e2e.core.runner.time.sleep", lambda _delay: None)
    client = _FakeClient(["ci-1"])
    instance = {"id": "ci-1", "name": "vm", "cr": "ci-cr"}

    stack.delete_vm(client=client, k8s_hub_client=_hub(False), instance=instance)
    stack.delete_vm(client=client, k8s_hub_client=_hub(False), instance=instance)

    assert client.listed == []


def test_delete_vm_waits_for_the_custom_resource(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setattr("tests.e2e.core.runner.time.sleep", lambda _delay: None)
    presence = iter([True, True, False])
    hub = SimpleNamespace(is_present=lambda **_kwargs: next(presence))

    stack.delete_vm(
        client=_FakeClient(["ci-1"]), k8s_hub_client=hub, instance={"id": "ci-1", "name": "vm", "cr": "ci-cr"}
    )

    assert next(presence, None) is None


def test_delete_vm_without_a_custom_resource_only_checks_the_api(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setattr("tests.e2e.core.runner.time.sleep", lambda _delay: None)

    stack.delete_vm(
        client=_FakeClient([]),
        k8s_hub_client=SimpleNamespace(is_present=lambda **_kwargs: pytest.fail("no CR to look up")),
        instance={"id": "ci-1", "name": "vm"},
    )


def _no_sleep(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setattr("tests.e2e.core.runner.time.sleep", lambda _delay: None)


def test_wait_owned_gone_names_a_rejected_delete_on_timeout(monkeypatch: pytest.MonkeyPatch) -> None:
    _no_sleep(monkeypatch)

    def delete() -> None:
        raise subprocess.CalledProcessError(
            1, ["grpcurl", "-H", "Authorization: Bearer secret"], stderr="FailedPrecondition: manual attachment"
        )

    with pytest.raises(TimeoutError) as exc_info:
        teardown.wait_owned_gone(
            description="vm gone",
            api_gone=lambda: False,
            cr_gone=lambda: True,
            resource_kind=WaitResourceKind.COMPUTE_INSTANCE,
            resource_id="ci-1",
            delete=delete,
            retries=2,
            delay=0,
        )

    assert "FailedPrecondition: manual attachment" in str(exc_info.value)
    assert "secret" not in str(exc_info.value)


def test_teardown_ingress_record_deletes_attachment_before_eip_and_tolerates_gone(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    _no_sleep(monkeypatch)
    calls: list[str] = []
    attachments, eips = ["att"], ["eip"]

    class Client:
        def list_external_ip_attachment_ids(self) -> list[str]:
            return list(attachments)

        def list_external_ip_ids(self) -> list[str]:
            return list(eips)

        def delete_external_ip_attachment(self, *, attachment_id: str) -> None:
            calls.append("attachment")
            attachments.remove(attachment_id)

        def delete_external_ip(self, *, external_ip_id: str) -> None:
            calls.append("eip")
            eips.remove(external_ip_id)

    hub = SimpleNamespace(get_external_ip_attachment_name=lambda **_k: "", get_external_ip_name=lambda **_k: "")
    record = {"label": "t1", "client": Client(), "attachment_id": "att", "eip_id": "eip"}

    test_module._teardown_ingress_record(record, hub)
    test_module._teardown_ingress_record(record, hub)

    assert calls == ["attachment", "eip"]
    assert record["attachment_id"] == "" and record["eip_id"] == ""


class _PoolClient:
    def __init__(self, items: list[dict[str, Any]]) -> None:
        self.items = items
        self.created: list[tuple[str, list[str]]] = []

    def call(self, *, service: str) -> dict[str, Any]:
        return {"items": self.items}

    def create_external_ip_pool(self, *, name: str, cidrs: list[str]) -> str:
        self.created.append((name, cidrs))
        return "new-pool"

    def get_external_ip_pool(self, *, pool_id: str) -> dict[str, Any]:
        return {"object": {"status": {"state": "EXTERNAL_IP_POOL_STATE_READY"}}}


def _stub_pool_waits(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setattr(stack, "wait_for_external_ip_pool_cr", lambda **_k: "pool-cr")
    monkeypatch.setattr(stack, "wait_for_external_ip_pool_ready", lambda **_k: None)
    monkeypatch.setattr(stack, "wait_for_external_ip_pool_grpc_ready", lambda **_k: None)


def test_resolve_pool_adopts_only_an_exact_cidr_match(monkeypatch: pytest.MonkeyPatch) -> None:
    _stub_pool_waits(monkeypatch)
    client = _PoolClient(
        [
            {"id": "other", "spec": {"cidrs": ["172.27.0.0/24"]}, "status": {"state": "EXTERNAL_IP_POOL_STATE_READY"}},
            {
                "id": "ours",
                "spec": {"cidrs": ["198.51.100.24/29"]},
                "status": {"state": "EXTERNAL_IP_POOL_STATE_READY"},
            },
        ]
    )

    pool = stack.resolve_external_ip_pool(private_grpc=client, k8s_hub_client=None, name="n", cidr="198.51.100.24/29")

    assert (pool["id"], pool["owned"]) == ("ours", False)
    assert client.created == []


def test_resolve_pool_creates_when_only_other_ranges_exist_and_records_early(monkeypatch: pytest.MonkeyPatch) -> None:
    client = _PoolClient([{"id": "other", "spec": {"cidrs": ["172.27.0.0/24"]}}])
    record: dict[str, Any] = {}

    def failing_wait(**_kwargs: object) -> str:
        raise TimeoutError("cr never appeared")

    monkeypatch.setattr(stack, "wait_for_external_ip_pool_cr", failing_wait)

    with pytest.raises(TimeoutError):
        stack.resolve_external_ip_pool(
            private_grpc=client, k8s_hub_client=None, name="n", cidr="198.51.100.24/29", record=record
        )

    assert client.created == [("n", ["198.51.100.24/29"])]
    assert (record["id"], record["owned"]) == ("new-pool", True)


def test_wait_for_attribution_needs_the_instance_and_a_transition_time(monkeypatch: pytest.MonkeyPatch) -> None:
    _no_sleep(monkeypatch)
    states = iter(
        [
            {"attribution": {"computeInstance": {"id": "ci"}}},
            {"attribution": {"computeInstance": {"id": "other"}}, "attachmentTransitionTime": "t"},
            {"attribution": {"computeInstance": {"id": "ci"}}, "attachmentTransitionTime": "t"},
        ]
    )
    client = SimpleNamespace(get_private_external_ip=lambda **_k: {"object": {"status": next(states)}})

    stack.wait_for_attribution(private_grpc=client, eip_id="eip", ci_id="ci")

    assert next(states, None) is None


def test_delete_network_continues_after_a_helper_failure_and_reports_it(monkeypatch: pytest.MonkeyPatch) -> None:
    deleted: list[str] = []

    def remove(_group: str) -> None:
        raise RuntimeError("could not remove the SSH helper")

    monkeypatch.setattr(stack.guest_ssh, "remove_helpers_for", remove)
    monkeypatch.setattr(stack, "teardown_sg", lambda *_a: deleted.append("sg"))
    monkeypatch.setattr(stack, "teardown_subnets", lambda *_a: deleted.append("subnet"))
    monkeypatch.setattr(stack, "teardown_vnet", lambda *_a: deleted.append("vnet"))
    network = {"vnet_id": "vn", "vnet_name": "n"}

    with pytest.raises(RuntimeError, match="could not remove the SSH helper"):
        stack.delete_network(client=None, k8s_hub_client=None, network=network)

    assert deleted == ["sg", "subnet", "vnet"]
