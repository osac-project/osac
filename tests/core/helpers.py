from __future__ import annotations

from tests.core.grpc_client import GRPCClient
from tests.core.k8s_client import K8sClient
from tests.core.runner import poll_until


def wait_for_cr(*, k8s: K8sClient, uuid: str) -> str:
    return poll_until(
        fn=lambda: k8s.get_compute_instance_name(uuid=uuid, checked=False),
        until=lambda v: v != "",
        retries=30,
        delay=2,
        description=f"CR for {uuid}",
    )


def wait_for_provision(*, k8s: K8sClient, name: str) -> None:
    poll_until(
        fn=lambda: k8s.get_compute_instance_latest_job_state(name=name, job_type="provision", checked=False),
        until=lambda v: v == "Succeeded",
        retries=120,
        delay=5,
        description=f"provision Succeeded for {name}",
    )


def wait_for_running(*, k8s: K8sClient, name: str) -> None:
    poll_until(
        fn=lambda: k8s.get_compute_instance_phase(name=name, checked=False),
        until=lambda v: v == "Running",
        retries=90,
        delay=10,
        description=f"{name} Running",
    )


def wait_for_restart(*, k8s: K8sClient, name: str, initial: str, restart_ts: str) -> None:
    poll_until(
        fn=lambda: k8s.get_compute_instance_last_restarted_at(name=name),
        until=lambda v: v != "" and v != initial and v >= restart_ts,
        retries=30,
        delay=10,
        description=f"{name} lastRestartedAt update",
    )


def wait_for_deletion(*, k8s: K8sClient, name: str) -> None:
    poll_until(
        fn=lambda: not k8s.is_present(resource="computeinstance", name=name),
        until=lambda v: v is True,
        retries=120,
        delay=5,
        description=f"{name} deletion",
    )


def wait_for_grpc_removal(*, grpc: GRPCClient, uuid: str) -> None:
    poll_until(
        fn=lambda: uuid not in grpc.list_compute_instance_ids(),
        until=lambda v: v is True,
        retries=30,
        delay=2,
        description=f"{uuid} removed from gRPC list",
    )


def wait_for_virtual_network_cr(*, k8s: K8sClient, uuid: str) -> str:
    return poll_until(
        fn=lambda: k8s.get_virtual_network_name(uuid=uuid, checked=False),
        until=lambda v: v != "",
        retries=30,
        delay=2,
        description=f"VirtualNetwork CR for {uuid}",
    )


def wait_for_virtual_network_ready(*, k8s: K8sClient, name: str) -> None:
    poll_until(
        fn=lambda: k8s.get_virtual_network_phase(name=name, checked=False),
        until=lambda v: v == "Ready",
        retries=60,
        delay=5,
        description=f"{name} VirtualNetwork Ready",
    )


def wait_for_virtual_network_deletion(*, k8s: K8sClient, name: str) -> None:
    poll_until(
        fn=lambda: not k8s.is_present(resource="virtualnetwork", name=name),
        until=lambda v: v is True,
        retries=120,
        delay=5,
        description=f"{name} VirtualNetwork deletion",
    )


def wait_for_subnet_cr(*, k8s: K8sClient, uuid: str) -> str:
    return poll_until(
        fn=lambda: k8s.get_subnet_name(uuid=uuid, checked=False),
        until=lambda v: v != "",
        retries=30,
        delay=2,
        description=f"Subnet CR for {uuid}",
    )


def wait_for_subnet_ready(*, k8s: K8sClient, name: str) -> None:
    poll_until(
        fn=lambda: k8s.get_subnet_phase(name=name, checked=False),
        until=lambda v: v == "Ready",
        retries=60,
        delay=5,
        description=f"{name} Subnet Ready",
    )


def wait_for_subnet_deletion(*, k8s: K8sClient, name: str) -> None:
    poll_until(
        fn=lambda: not k8s.is_present(resource="subnet", name=name),
        until=lambda v: v is True,
        retries=120,
        delay=5,
        description=f"{name} Subnet deletion",
    )


def wait_for_cluster_order_cr(*, k8s: K8sClient, uuid: str) -> str:
    return poll_until(
        fn=lambda: k8s.get_cluster_order_name(uuid=uuid, checked=False),
        until=lambda v: v != "",
        retries=30,
        delay=2,
        description=f"ClusterOrder CR for {uuid}",
    )


def wait_for_cluster_ready(*, k8s: K8sClient, name: str) -> None:
    def _check_phase() -> str:
        phase: str = k8s.get_cluster_order_phase(name=name, checked=False)
        assert phase != "Failed", f"{name} ClusterOrder entered Failed phase"
        return phase

    poll_until(
        fn=_check_phase, until=lambda v: v == "Ready", retries=120, delay=15, description=f"{name} ClusterOrder Ready"
    )


def wait_for_cluster_deletion(*, k8s: K8sClient, name: str) -> None:
    poll_until(
        fn=lambda: not k8s.is_present(resource="clusterorder", name=name),
        until=lambda v: v is True,
        retries=120,
        delay=10,
        description=f"{name} ClusterOrder deletion",
    )


def wait_for_cluster_grpc_removal(*, grpc: GRPCClient, uuid: str) -> None:
    poll_until(
        fn=lambda: uuid not in grpc.list_cluster_ids(),
        until=lambda v: v is True,
        retries=60,
        delay=5,
        description=f"{uuid} removed from gRPC cluster list",
    )


def wait_for_security_group_cr(*, k8s: K8sClient, uuid: str) -> str:
    return poll_until(
        fn=lambda: k8s.get_security_group_name(uuid=uuid, checked=False),
        until=lambda v: v != "",
        retries=30,
        delay=2,
        description=f"SecurityGroup CR for {uuid}",
    )


def wait_for_security_group_ready(*, k8s: K8sClient, name: str) -> None:
    poll_until(
        fn=lambda: k8s.get_security_group_phase(name=name, checked=False),
        until=lambda v: v == "Ready",
        retries=60,
        delay=5,
        description=f"{name} SecurityGroup Ready",
    )


def wait_for_security_group_deletion(*, k8s: K8sClient, name: str) -> None:
    poll_until(
        fn=lambda: not k8s.is_present(resource="securitygroup", name=name),
        until=lambda v: v is True,
        retries=120,
        delay=5,
        description=f"{name} SecurityGroup deletion",
    )
