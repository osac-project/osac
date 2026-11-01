"""Parallel setup helpers of the VMaaS Netris suite: no cluster, fakes that record call order."""

from __future__ import annotations

import itertools
import threading
import time
from collections.abc import Callable
from typing import Any

import pytest

from tests.e2e.networking.vmaas import stack

WAIT = 5


def test_run_parallel_returns_results_in_submit_order() -> None:
    def slow() -> str:
        time.sleep(0.2)
        return "first"

    assert stack.run_parallel([slow, lambda: "second"]) == ["first", "second"]


def test_run_parallel_with_no_calls_returns_nothing() -> None:
    assert stack.run_parallel([]) == []


def test_run_parallel_reraises_pytest_fail() -> None:
    def fails() -> None:
        pytest.fail("a wait timed out")

    with pytest.raises(pytest.fail.Exception, match="a wait timed out"):
        stack.run_parallel([lambda: None, fails])


def test_run_parallel_waits_for_every_worker_before_it_raises() -> None:
    finished: list[str] = []

    def fails_fast() -> None:
        raise RuntimeError("boom")

    def slow() -> None:
        time.sleep(0.3)
        finished.append("slow")

    with pytest.raises(RuntimeError, match="boom"):
        stack.run_parallel([fails_fast, slow])

    assert finished == ["slow"]


def test_run_parallel_raises_the_first_failure_in_submit_order() -> None:
    def slow_failure() -> None:
        time.sleep(0.3)
        raise ValueError("first in submit order")

    def fast_failure() -> None:
        raise KeyError("finished first")

    with pytest.raises(ValueError, match="first in submit order"):
        stack.run_parallel([slow_failure, fast_failure])


class _Recorder:
    """Records the order of events from several threads."""

    def __init__(self) -> None:
        self.events: list[str] = []
        self._lock = threading.Lock()

    def __call__(self, event: str) -> None:
        with self._lock:
            self.events.append(event)


class _NetClient:
    def __init__(self, record: _Recorder, barrier: threading.Barrier | None = None) -> None:
        self.record = record
        self.barrier = barrier

    def create_virtual_network(self, *, name: str, ipv4_cidr: str) -> str:
        self.record("create-vn")
        return "vn-id"

    def create_subnet(self, *, name: str, virtual_network: str, ipv4_cidr: str) -> str:
        self.record("create-subnet")
        if self.barrier is not None:
            self.barrier.wait(WAIT)  # fails unless the SecurityGroup call runs at the same time
        return "sub-id"

    def create_security_group_with_rules(self, **_kwargs: object) -> str:
        self.record("create-sg")
        if self.barrier is not None:
            self.barrier.wait(WAIT)
        return "sg-id"


def _stub_waits(monkeypatch: pytest.MonkeyPatch, record: _Recorder, *, fail: str | None = None) -> None:
    def wait(name: str, result: str = "") -> Callable[..., str]:
        def fn(**_kwargs: object) -> str:
            record(name)
            if fail == name:
                raise TimeoutError(f"{name} never finished")
            return result

        return fn

    monkeypatch.setattr(stack, "wait_for_virtual_network_cr", wait("vn-cr", "vn-cr"))
    monkeypatch.setattr(stack, "wait_for_virtual_network_ready", wait("vn-ready"))
    monkeypatch.setattr(stack, "wait_for_grpc_virtual_network_ready", wait("vn-api"))
    monkeypatch.setattr(stack, "wait_for_subnet_cr", wait("subnet-cr", "subnet-cr"))
    monkeypatch.setattr(stack, "wait_for_subnet_ready", wait("subnet-ready"))
    monkeypatch.setattr(stack, "wait_for_grpc_subnet_ready", wait("subnet-api"))
    monkeypatch.setattr(stack, "wait_for_security_group_cr", wait("sg-cr", "sg-cr"))
    monkeypatch.setattr(stack, "wait_for_security_group_ready", wait("sg-ready"))
    monkeypatch.setattr(stack, "wait_for_grpc_security_group_ready", wait("sg-api"))


def _create(client: _NetClient, networks: list[dict[str, str]]) -> dict[str, str]:
    return stack.create_network(
        client=client,  # type: ignore[arg-type]
        k8s_hub_client=None,  # type: ignore[arg-type]
        networks=networks,
        label="a",
        vn_cidr="172.16.0.0/16",
        subnet_cidr="172.16.1.0/24",
        run_id="r",
        ingress=[],
        egress=[],
    )


def test_create_network_starts_children_only_after_the_virtual_network_waits(monkeypatch: pytest.MonkeyPatch) -> None:
    record = _Recorder()
    _stub_waits(monkeypatch, record)
    network = _create(_NetClient(record), [])

    events = record.events
    vn_done = max(events.index("vn-cr"), events.index("vn-ready"), events.index("vn-api"))
    assert events.index("create-subnet") > vn_done
    assert events.index("create-sg") > vn_done
    assert network["sub_a_id"] == "sub-id" and network["sg_id"] == "sg-id"
    assert network["subnet_cr"] == "subnet-cr" and network["sg_cr"] == "sg-cr"


def test_create_network_waits_for_the_virtual_network_in_the_api_before_any_child(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    record = _Recorder()
    _stub_waits(monkeypatch, record)
    release = threading.Event()
    returned: list[str] = []

    def slow_api_wait(**_kwargs: object) -> None:
        release.wait(0.3)  # the CR is ready, the API is not yet
        returned.append("vn-api")

    monkeypatch.setattr(stack, "wait_for_grpc_virtual_network_ready", slow_api_wait)
    client = _NetClient(record)
    original = client.create_subnet

    def create_subnet(**kwargs: object) -> str:
        assert returned == ["vn-api"], "the Subnet was created before the VirtualNetwork was READY in the API"
        return original(**kwargs)

    client.create_subnet = create_subnet  # type: ignore[method-assign]

    _create(client, [])


def test_create_network_runs_the_subnet_and_the_security_group_at_the_same_time(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    record = _Recorder()
    _stub_waits(monkeypatch, record)

    # Each create waits for the other at the barrier, so a serial create times out.
    _create(_NetClient(record, threading.Barrier(2)), [])

    assert {"subnet-api", "sg-api"} <= set(record.events)


@pytest.mark.parametrize("failing_wait", ["vn-ready", "vn-api"])
def test_create_network_creates_no_child_when_the_virtual_network_fails(
    monkeypatch: pytest.MonkeyPatch, failing_wait: str
) -> None:
    record = _Recorder()
    _stub_waits(monkeypatch, record, fail=failing_wait)
    networks: list[dict[str, str]] = []

    with pytest.raises(TimeoutError, match=failing_wait):
        _create(_NetClient(record), networks)

    assert "create-subnet" not in record.events and "create-sg" not in record.events
    assert networks[0]["vnet_id"] == "vn-id"


def test_create_network_keeps_the_ids_for_cleanup_when_the_subnet_fails(monkeypatch: pytest.MonkeyPatch) -> None:
    record = _Recorder()
    _stub_waits(monkeypatch, record, fail="subnet-ready")
    networks: list[dict[str, str]] = []

    with pytest.raises(TimeoutError, match="subnet-ready"):
        _create(_NetClient(record), networks)

    assert len(networks) == 1
    assert (networks[0]["vnet_id"], networks[0]["sub_a_id"], networks[0]["sg_id"]) == ("vn-id", "sub-id", "sg-id")


# The orchestration of the test module: networks, VMs, waits, finalizers and the merged teardown.

from collections import defaultdict  # noqa: E402
from types import SimpleNamespace  # noqa: E402

from tests.e2e.core.failures import E2EFailure, Kind  # noqa: E402
from tests.e2e.networking.vmaas import test_vmaas_networking as module  # noqa: E402


def _no_sleep(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setattr("tests.e2e.core.runner.time.sleep", lambda _delay: None)


def test_networks_are_taken_in_submit_order_whatever_finishes_first(monkeypatch: pytest.MonkeyPatch) -> None:
    finished: list[str] = []
    restricted: list[tuple[str, list[str]]] = []

    def create_network(*, networks: list[dict[str, str]], label: str, **_kwargs: object) -> dict[str, str]:
        time.sleep({"a": 0.3, "b": 0.15, "t2": 0.0}[label])
        network = {"label": label, "vnet_id": f"vn-{label}"}
        networks.append(network)
        finished.append(label)
        return network

    def create_security_group(*, network: dict[str, str], key: str, **_kwargs: object) -> None:
        restricted.append((network["label"], list(finished)))
        network[f"{key}_sg_id"] = "restricted"

    monkeypatch.setattr(stack, "create_network", create_network)
    monkeypatch.setattr(stack, "create_security_group", create_security_group)
    networks: list[dict[str, str]] = []
    t2_networks: list[dict[str, str]] = []

    a, b, t2 = module._create_networks(
        grpc=None,
        tenant2_grpc=None,
        k8s_hub_client=None,
        run_id="r",
        vn_cidrs=("172.16.0.0/16", "172.17.0.0/16", "172.18.0.0/16"),
        networks=networks,
        tenant2_networks=t2_networks,
    )

    assert finished == ["t2", "b", "a"]
    assert (a["label"], b["label"], t2["label"]) == ("a", "b", "t2")
    assert [n["label"] for n in networks] == ["b", "a"]  # record order is not result order
    assert a["restricted_sg_id"] == "restricted" and "restricted_sg_id" not in b
    # The restricted group is created on network a only after network a itself was READY.
    assert restricted == [("a", ["t2", "b", "a"])]


def test_restricted_group_is_not_created_when_network_a_fails(monkeypatch: pytest.MonkeyPatch) -> None:
    created: list[str] = []

    def create_network(*, label: str, **_kwargs: object) -> dict[str, str]:
        if label == "a":
            raise TimeoutError("network a never READY")
        return {"label": label}

    monkeypatch.setattr(stack, "create_network", create_network)
    monkeypatch.setattr(stack, "create_security_group", lambda **_k: created.append("sg"))

    with pytest.raises(TimeoutError, match="network a"):
        module._create_networks(
            grpc=None,
            tenant2_grpc=None,
            k8s_hub_client=None,
            run_id="r",
            vn_cidrs=("a", "b", "c"),
            networks=[],
            tenant2_networks=[],
        )

    assert created == []


def test_vm_creates_are_serial_within_a_tenant_and_parallel_across_tenants(monkeypatch: pytest.MonkeyPatch) -> None:
    lock = threading.Lock()
    active: defaultdict[str, int] = defaultdict(int)
    peak: defaultdict[str, int] = defaultdict(int)
    seen: set[str] = set()
    barrier = threading.Barrier(2)

    def create_vm(*, cli: str, instances: list[dict[str, str]], name: str, **_kwargs: object) -> dict[str, str]:
        with lock:
            active[cli] += 1
            peak[cli] = max(peak[cli], active[cli])
            first = cli not in seen
            seen.add(cli)
        if first:
            barrier.wait(WAIT)  # breaks unless the other tenant creates its first VM at the same time
        time.sleep(0.02)
        with lock:
            active[cli] -= 1
        instance = {"name": name}
        instances.append(instance)
        return instance

    monkeypatch.setattr(stack, "create_vm", create_vm)
    common = {"k8s_hub_client": None, "vm_template": "t", "instance_type": "i", "storage_tier": "s", "run_id": "r"}
    t1_instances: list[dict[str, str]] = []
    t2_instances: list[dict[str, str]] = []
    net_a, net_b, net_t2 = {"sg_id": "sa", "restricted_sg_id": "sr"}, {"sg_id": "sb"}, {"sg_id": "st"}

    vms_t1, vms_t2 = module._create_vms(
        t1=dict(cli="cli1", instances=t1_instances, key_secret="k1", disk_image="d1", **common),
        t2=dict(cli="cli2", instances=t2_instances, key_secret="k2", disk_image="d2", **common),
        network_a=net_a,
        network_b=net_b,
        network_t2=net_t2,
    )

    assert dict(peak) == {"cli1": 1, "cli2": 1}
    assert list(vms_t1) == ["a1", "a2", "b1", "r1"] and list(vms_t2) == ["t2a"]
    assert [i["name"] for i in t1_instances] == [f"netris-vm-{k}-r" for k in ("a1", "a2", "b1", "r1")]


def test_all_five_vms_are_waited_on_at_the_same_time(monkeypatch: pytest.MonkeyPatch) -> None:
    barrier = threading.Barrier(5)
    monkeypatch.setattr(stack, "wait_for_vm", lambda **_k: barrier.wait(WAIT))

    module._wait_for_vms(
        k8s_hub_client=None, k8s_virt_client=None, instances=[{"name": str(n)} for n in range(5)], identity_file="key"
    )


def test_a_failed_vm_wait_is_raised_after_all_waits_finished(monkeypatch: pytest.MonkeyPatch) -> None:
    done: list[str] = []

    def wait_for_vm(*, instance: dict[str, str], **_kwargs: object) -> None:
        if instance["name"] == "bad":
            raise TimeoutError("bad never ran")
        time.sleep(0.2)
        done.append(instance["name"])

    monkeypatch.setattr(stack, "wait_for_vm", wait_for_vm)

    with pytest.raises(TimeoutError, match="bad never ran"):
        module._wait_for_vms(
            k8s_hub_client=None, k8s_virt_client=None, instances=[{"name": "bad"}, {"name": "ok"}], identity_file="key"
        )

    assert done == ["ok"]


# The routed pool as a fixture check


def _pool_client(status: dict[str, Any]) -> SimpleNamespace:
    return SimpleNamespace(get_external_ip_pool=lambda **_k: {"object": {"status": status}})


def test_pool_check_prepares_the_set_of_suite_addresses() -> None:
    pool: dict[str, Any] = {"id": "p"}

    stack.check_pool_ready(
        _pool_client({"state": "EXTERNAL_IP_POOL_STATE_READY", "available": 5, "allocated": 1}),
        pool,
        cidr="198.51.100.24/29",
        needed=3,
    )

    assert pool["suite_eip_ids"] == set()
    assert "allocated_start" not in pool


def test_pool_check_fails_when_the_pool_is_not_ready() -> None:
    with pytest.raises(AssertionError, match="not READY"):
        stack.check_pool_ready(
            _pool_client({"state": "EXTERNAL_IP_POOL_STATE_PENDING", "available": 6}), {"id": "p"}, cidr="c", needed=3
        )


def test_pool_check_fails_when_fewer_than_the_peak_are_free() -> None:
    with pytest.raises(AssertionError, match="needs 3"):
        stack.check_pool_ready(
            _pool_client({"state": "EXTERNAL_IP_POOL_STATE_READY", "available": 2}), {"id": "p"}, cidr="c", needed=3
        )


# Teardown stages


class _World:
    """Fakes for every leaf of the teardown. `log` holds ("start"|"end", resource) in the order they happened."""

    def __init__(self, monkeypatch: pytest.MonkeyPatch, *, fail: str | None = None) -> None:
        self.log: list[tuple[str, str]] = []
        self.lock = threading.Lock()
        self.fail = fail
        self.listed_pools = ["pool-1"]
        self.eips: list[dict[str, Any]] = []
        self.allocated: int | None = None  # None: as many as the live ExternalIPs on the pool
        _no_sleep(monkeypatch)
        monkeypatch.setattr(module, "_teardown_ingress_record", self._ingress)
        monkeypatch.setattr(stack, "delete_vm", self._vm)
        monkeypatch.setattr(module, "_drop_nat", self._nat)
        monkeypatch.setattr(module, "_drop_nat_eip", self._nat_eip)
        monkeypatch.setattr(stack, "delete_network", self._network)
        monkeypatch.setattr(module, "_teardown_pool", self._pool)
        monkeypatch.setattr(module, "nat_gateway_ids", lambda _client: [])
        self.t1 = self.tenant("t1")
        self.t2 = self.tenant("t2")
        self.t1.update(secret_live=True, key_secret="s1")
        self.t2.update(secret_live=True, key_secret="s2", disk_image_id="img")
        self.external: dict[str, Any] = {
            "pool": {"id": "pool-1", "cr": "pool-cr", "owned": True, "suite_eip_ids": {"suite-1", "suite-2"}},
            "ingress": [{"label": "i1"}, {"label": "i2"}],
            "t1": self.t1["network_a"],
            "t2": self.t2["network"],
        }
        self.k8s_virt = SimpleNamespace(is_absent=lambda **_k: True)

    def tenant(self, name: str) -> dict[str, Any]:
        client = SimpleNamespace(
            name=name, delete_disk_image=lambda **_k: self.record("image"), list_compute_instance_ids=lambda: []
        )
        cli = SimpleNamespace(delete_secret=lambda **_k: self.record(f"secret:{name}"))
        vms = [{"id": f"{name}-vm{n}", "name": f"{name}-vm{n}", "vm_namespace": f"ns-{name}"} for n in range(2)]
        network = {"vnet_id": f"vn-{name}", "vnet_name": f"vn-{name}", "nat_id": f"nat-{name}", "nat_eip_id": "e"}
        state: dict[str, Any] = {
            "cli": cli,
            "grpc": client,
            "instances": vms,
            "networks": [network],
            "disk_image_id": "",
            "network": network,
            "vm_t2a": vms[0],
        }
        if name == "t1":
            other = {"vnet_id": "vn-t1b", "vnet_name": "vn-t1b"}
            state["networks"].append(other)
            state.update(network_a=network, network_b=other, vm_a1=vms[0], vm_b1={**vms[1], "vm_namespace": "ns-t1b"})
        return state

    def record(self, resource: str) -> None:
        self.log.append(("end", resource))

    def _run(self, resource: str) -> None:
        with self.lock:
            self.log.append(("start", resource))
        threading.Event().wait(0.03)  # time.sleep is patched out of these tests
        if self.fail == resource:
            raise RuntimeError(f"{resource} failed")
        with self.lock:
            self.log.append(("end", resource))

    def _ingress(self, record: dict[str, Any], _k8s: object) -> None:
        self._run(f"ingress:{record['label']}")

    def _vm(self, *, instance: dict[str, str], **_kwargs: object) -> None:
        self._run(f"vm:{instance['id']}")

    def _nat(self, _client: object, _k8s: object, network: dict[str, str]) -> None:
        self._run(f"nat:{network['vnet_id']}")

    def _nat_eip(self, _client: object, _k8s: object, network: dict[str, str]) -> None:
        self._run(f"natip:{network['vnet_id']}")

    def _network(self, *, network: dict[str, str], **_kwargs: object) -> None:
        self._run(f"net:{network['vnet_id']}")

    def _pool(self, pool: dict[str, Any], *_args: object) -> None:
        self._run("pool")
        self.listed_pools.remove(pool["id"])

    def holder(self, name: str, *, default: bool, pool: str = "pool-1") -> dict[str, Any]:
        labels = {"osac.openshift.io/default": "true"} if default else {}
        item = {"id": f"id-{name}", "metadata": {"name": name, "tenant": "t", "labels": labels}}
        item["spec"] = {"pool": {"id": pool}}
        self.eips.append(item)
        return item

    def private_grpc(self) -> SimpleNamespace:
        def allocated() -> int:
            on_pool = [i for i in self.eips if i["spec"]["pool"]["id"] == "pool-1"]
            return len(on_pool) if self.allocated is None else self.allocated

        return SimpleNamespace(
            get_external_ip_pool=lambda **_k: {"object": {"status": {"allocated": allocated()}}},
            list_external_ip_pool_ids=lambda: list(self.listed_pools),
            list_private_external_ips=lambda: list(self.eips),
        )

    def teardown(self) -> None:
        module._teardown_leaves_nothing(
            tenants={"t1": self.t1, "t2": self.t2},
            external=self.external,
            cli=self.t1["cli"],
            grpc=self.t1["grpc"],
            private_grpc=self.private_grpc(),
            k8s_hub_client=None,
            k8s_virt_client=self.k8s_virt,
        )

    def finalize(self) -> list[str]:
        errors: list[str] = []
        module._cleanup_external_path(
            self.external,
            grpc=self.t1["grpc"],
            tenant2_grpc=self.t2["grpc"],
            private_grpc=self.private_grpc(),
            k8s_hub_client=None,
            errors=errors,
        )
        module._cleanup_tenants(
            {"t1": self.t1, "t2": self.t2}, cli=self.t1["cli"], grpc=self.t1["grpc"], k8s_hub_client=None, errors=errors
        )
        return errors

    def stage_span(self, prefix: str) -> tuple[int, int]:
        """Index of the first start and of the last end among the log entries of one resource kind."""
        starts = [i for i, (kind, r) in enumerate(self.log) if kind == "start" and r.startswith(prefix)]
        ends = [i for i, (kind, r) in enumerate(self.log) if kind == "end" and r.startswith(prefix)]
        return min(starts), max(ends)


def test_teardown_runs_each_stage_to_its_end_before_the_next_starts(monkeypatch: pytest.MonkeyPatch) -> None:
    world = _World(monkeypatch)

    world.teardown()

    spans = [world.stage_span(p) for p in ("ingress:", "vm:", "nat:", "natip:", "net:", "pool")]
    for (_, previous_end), (next_start, _) in itertools.pairwise(spans):
        assert previous_end < next_start
    secrets = [i for i, (_, r) in enumerate(world.log) if r.startswith("secret:") or r == "image"]
    assert max(i for i, (_, r) in enumerate(world.log) if r.startswith("net:")) < min(secrets)
    assert max(secrets) < world.stage_span("pool")[0]


def test_teardown_deletes_inside_a_stage_at_the_same_time(monkeypatch: pytest.MonkeyPatch) -> None:
    world = _World(monkeypatch)

    world.teardown()

    vm_events = [(kind, r) for kind, r in world.log if r.startswith("vm:")]
    assert [kind for kind, _ in vm_events[:4]] == ["start"] * 4  # four VMs overlap, none waits for another


def test_teardown_checks_the_vm_namespace_after_each_network(monkeypatch: pytest.MonkeyPatch) -> None:
    world = _World(monkeypatch)
    checked: list[str] = []
    world.k8s_virt = SimpleNamespace(is_absent=lambda **kw: checked.append(kw["name"]) or True)

    world.teardown()

    assert sorted(checked) == ["ns-t1", "ns-t1b", "ns-t2"]


def test_teardown_empties_the_state_lists_and_forgets_the_deleted(monkeypatch: pytest.MonkeyPatch) -> None:
    world = _World(monkeypatch)

    world.teardown()

    assert world.t1["instances"] == [] and world.t2["instances"] == []
    assert world.t1["networks"] == [] and world.t2["networks"] == []
    assert world.external["ingress"] == [] and world.external["pool"] is None
    assert world.t1["secret_live"] is False and world.t2["secret_live"] is False
    assert world.t2["disk_image_id"] == ""
    assert world.finalize() == []
    assert not [r for kind, r in world.log if kind == "start" and r.startswith("vm:")][4:]


def test_teardown_fails_when_the_pool_count_does_not_match_its_live_addresses(monkeypatch: pytest.MonkeyPatch) -> None:
    world = _World(monkeypatch)
    world.allocated = 2  # two addresses held, no ExternalIP lists them

    with pytest.raises(AssertionError, match="allocated"):
        world.teardown()

    assert ("start", "pool") not in world.log


def test_teardown_fails_when_a_suite_external_ip_is_still_listed(monkeypatch: pytest.MonkeyPatch) -> None:
    world = _World(monkeypatch)
    world.eips.append({"id": "suite-2", "metadata": {"name": "x"}, "spec": {"pool": {"id": "pool-1"}}})

    with pytest.raises(AssertionError, match="suite-2"):
        world.teardown()


def test_default_external_ips_arriving_after_the_start_do_not_fail_the_teardown(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    world = _World(monkeypatch)
    world.holder("default-nat-a", default=True)
    world.holder("default-nat-b", default=True)

    world.teardown()

    # The pool stays, because the defaults hold it; nothing tried to delete it.
    assert ("start", "pool") not in world.log
    assert world.listed_pools == ["pool-1"]
    assert world.external["pool"] is None


def test_teardown_fails_when_a_non_default_external_ip_holds_the_owned_pool(monkeypatch: pytest.MonkeyPatch) -> None:
    world = _World(monkeypatch)
    world.holder("default-nat", default=True)
    world.holder("stranger", default=False)

    with pytest.raises(AssertionError, match="stranger"):
        world.teardown()

    assert ("start", "pool") not in world.log


def test_default_holders_on_another_pool_do_not_block_the_owned_pool(monkeypatch: pytest.MonkeyPatch) -> None:
    world = _World(monkeypatch)
    world.holder("elsewhere", default=False, pool="other-pool")

    world.teardown()

    assert ("start", "pool") in world.log and world.listed_pools == []


def test_teardown_checks_the_adopted_pool_and_leaves_it_alone(monkeypatch: pytest.MonkeyPatch) -> None:
    world = _World(monkeypatch)
    world.external["pool"].update(owned=False)
    world.holder("default-nat", default=True)

    world.teardown()

    assert ("start", "pool") not in world.log
    assert world.listed_pools == ["pool-1"]

    world = _World(monkeypatch)
    world.external["pool"].update(owned=False)
    world.allocated = 3
    with pytest.raises(AssertionError, match="allocated"):
        world.teardown()


def test_teardown_fails_when_the_adopted_pool_was_removed(monkeypatch: pytest.MonkeyPatch) -> None:
    world = _World(monkeypatch)
    world.external["pool"].update(owned=False)
    world.listed_pools = []

    with pytest.raises(AssertionError, match="adopted"):
        world.teardown()


def test_finalizer_skips_a_pool_held_by_defaults_and_flags_any_other_holder(monkeypatch: pytest.MonkeyPatch) -> None:
    world = _World(monkeypatch)
    world.holder("default-nat", default=True)
    assert world.finalize() == []
    assert ("start", "pool") not in world.log

    world = _World(monkeypatch)
    world.holder("stranger", default=False)
    assert world.finalize() == ["ExternalIPPool pool-1: AssertionError"]


def test_a_stage_that_fails_partway_forgets_exactly_what_it_deleted(monkeypatch: pytest.MonkeyPatch) -> None:
    world = _World(monkeypatch)
    world.fail = "image"
    deleted: list[str] = []
    world.t1["cli"].delete_secret = lambda **_k: deleted.append("secret:t1")
    world.t2["cli"].delete_secret = lambda **_k: deleted.append("secret:t2")

    def delete_image(**_kwargs: object) -> None:
        raise RuntimeError("image delete failed")

    world.t2["grpc"].delete_disk_image = delete_image

    with pytest.raises(RuntimeError, match="image delete failed"):
        module._delete_secrets_and_images({"t1": world.t1, "t2": world.t2}, cli=world.t1["cli"])

    assert sorted(deleted) == ["secret:t1", "secret:t2"]
    assert world.t1["secret_live"] is False and world.t2["secret_live"] is False
    assert world.t2["disk_image_id"] == "img"

    # The finalizer rerun deletes only the DiskImage: no false error for the Secrets already gone.
    errors: list[str] = []
    world.t1["cli"].delete_secret = lambda **_k: (_ for _ in ()).throw(RuntimeError("already gone"))
    world.t2["cli"].delete_secret = world.t1["cli"].delete_secret
    world.t2["grpc"].delete_disk_image = lambda **_k: deleted.append("image")
    module._delete_secrets_and_images({"t1": world.t1, "t2": world.t2}, cli=world.t1["cli"], errors=errors)

    assert errors == [] and deleted[-1] == "image" and world.t2["disk_image_id"] == ""


def test_finalizer_after_a_partial_teardown_deletes_only_what_is_left(monkeypatch: pytest.MonkeyPatch) -> None:
    world = _World(monkeypatch, fail="nat:vn-t2")

    with pytest.raises(RuntimeError, match="nat:vn-t2 failed"):
        world.teardown()
    starts_before = [r for kind, r in world.log if kind == "start"]
    world.fail = None

    errors = world.finalize()

    assert errors == []
    after = [r for kind, r in world.log if kind == "start"][len(starts_before) :]
    # Ingress and VMs are gone from the lists, so they are not deleted again; the NAT that failed is.
    assert not [r for r in after if r.startswith(("ingress:", "vm:"))]
    assert "nat:vn-t2" in after and "pool" in after


def test_finalizer_collects_errors_and_still_runs_every_stage(monkeypatch: pytest.MonkeyPatch) -> None:
    world = _World(monkeypatch, fail="vm:t1-vm0")

    errors = world.finalize()

    assert errors == ["ComputeInstance t1-vm0: RuntimeError"]
    assert {r for kind, r in world.log if kind == "start"} >= {"vm:t1-vm1", "net:vn-t1", "net:vn-t2", "pool"}
    assert [i["id"] for i in world.t1["instances"]] == ["t1-vm0"]


@pytest.mark.parametrize(
    ("value", "kept"),
    [(None, False), ("", False), ("false", False), ("0", False), ("true", True), ("TRUE", True), (" true ", True)],
)
def test_keep_resources_needs_the_flag_set_to_true(
    monkeypatch: pytest.MonkeyPatch, value: str | None, kept: bool
) -> None:
    monkeypatch.delenv(stack.KEEP_RESOURCES_ENV, raising=False)
    if value is not None:
        monkeypatch.setenv(stack.KEEP_RESOURCES_ENV, value)

    assert stack.keep_resources() is kept


def test_keep_resources_leaves_everything_to_the_person_debugging(monkeypatch: pytest.MonkeyPatch) -> None:
    world = _World(monkeypatch)
    monkeypatch.setenv(stack.KEEP_RESOURCES_ENV, "true")

    assert world.finalize() == []
    with pytest.raises(pytest.skip.Exception, match=stack.KEEP_RESOURCES_ENV):
        world.teardown()

    assert world.log == []


def test_finalizers_delete_when_keep_resources_is_off(monkeypatch: pytest.MonkeyPatch) -> None:
    world = _World(monkeypatch)
    monkeypatch.setenv(stack.KEEP_RESOURCES_ENV, "false")

    assert world.finalize() == []

    assert world.log
