from __future__ import annotations

from tests.core.grpc_client import GRPCClient
from tests.core.k8s_client import K8sClient
from tests.core.runner import poll_until, run_unchecked


def wait_for_cr(*, k8s: K8sClient, uuid: str) -> str:
    return poll_until(
        fn=lambda: k8s.get_compute_instance_name(uuid=uuid, checked=False),
        until=lambda v: v != "",
        retries=30,
        delay=2,
        description=f"CR for {uuid}",
    )


def wait_for_provision(*, k8s: K8sClient, name: str) -> None:
    def _check_provisioned() -> str:
        phase: str = k8s.get_compute_instance_phase(name=name, checked=False)
        assert phase != "Failed", f"{name} entered Failed phase before Provisioned=True"
        return k8s.get_compute_instance_condition_status(
            name=name, condition_type="Provisioned", checked=False
        )

    poll_until(
        fn=_check_provisioned,
        until=lambda v: v == "True",
        retries=120,
        delay=5,
        description=f"{name} Provisioned condition",
    )


def wait_for_running(*, k8s: K8sClient, name: str) -> None:
    def _check_phase() -> str:
        phase: str = k8s.get_compute_instance_phase(name=name, checked=False)
        assert phase != "Failed", f"{name} entered Failed phase"
        return phase

    poll_until(
        fn=_check_phase,
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


def wait_for_public_ip_pool_cr(*, k8s: K8sClient, uuid: str) -> str:
    return poll_until(
        fn=lambda: k8s.get_public_ip_pool_name(uuid=uuid, checked=False),
        until=lambda v: v != "",
        retries=30,
        delay=1,
        description=f"PublicIPPool CR for {uuid}",
    )


def wait_for_public_ip_pool_ready(*, k8s: K8sClient, name: str) -> None:
    poll_until(
        fn=lambda: k8s.get_public_ip_pool_phase(name=name, checked=False),
        until=lambda v: v == "Ready",
        retries=60,
        delay=2,
        description=f"{name} PublicIPPool Ready",
    )


def wait_for_public_ip_pool_deletion(*, k8s: K8sClient, name: str) -> None:
    poll_until(
        fn=lambda: not k8s.is_present(resource="publicippool", name=name),
        until=lambda v: v is True,
        retries=120,
        delay=5,
        description=f"{name} PublicIPPool deletion",
    )


def wait_for_public_ip_cr(*, k8s: K8sClient, uuid: str) -> str:
    return poll_until(
        fn=lambda: k8s.get_public_ip_name(uuid=uuid, checked=False),
        until=lambda v: v != "",
        retries=30,
        delay=1,
        description=f"PublicIP CR for {uuid}",
    )


def wait_for_public_ip_allocated(*, k8s: K8sClient, name: str) -> None:
    poll_until(
        fn=lambda: k8s.get_public_ip_state(name=name, checked=False),
        until=lambda v: v == "Allocated",
        retries=60,
        delay=5,
        description=f"{name} PublicIP Allocated",
    )


def wait_for_public_ip_deletion(*, k8s: K8sClient, name: str) -> None:
    poll_until(
        fn=lambda: not k8s.is_present(resource="publicip", name=name),
        until=lambda v: v is True,
        retries=120,
        delay=5,
        description=f"{name} PublicIP deletion",
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
    # HACK: HyperShift has multiple teardown bugs where controllers leave orphaned state
    # that deadlocks HostedCluster deletion. We force-clean on every poll iteration:
    #
    # 1. AgentCluster deprovision finalizer: capi-provider-agent is killed during teardown
    #    before removing its finalizer, blocking namespace termination.
    #    https://github.com/openshift/hypershift/blob/main/hypershift-operator/controllers/hostedcluster/karpenter.go#L88
    #
    # 2. Agent labels: the CAPI provider sometimes fails to clear
    #    clusterdeployment-namespace from agents after HostedCluster deletion. The delete
    #    playbook's detach_and_unlabel skips agents that still have this label set, leaving
    #    the clusterorder label stuck and blocking agent reuse for subsequent tests.
    def _check_deleted() -> bool:
        _force_cleanup_agentcluster_finalizers(k8s=k8s, name=name)
        _force_cleanup_agent_labels(k8s=k8s, name=name)
        return not k8s.is_present(resource="clusterorder", name=name)

    poll_until(
        fn=_check_deleted,
        until=lambda v: v is True,
        retries=120,
        delay=10,
        description=f"{name} ClusterOrder deletion",
    )


def _force_cleanup_agentcluster_finalizers(*, k8s: K8sClient, name: str) -> None:
    # HCP namespace: {osac-ns}-{co-name}-{hc-name}, where hc-name == co-name
    hc_ns = f"{k8s.namespace}-{name}"
    cp_ns = f"{hc_ns}-{name}"
    finalizer = "agentclustercapi-provider.agent-install.openshift.io/deprovision"
    base_args = [*k8s._base(), "--as", "system:admin"]
    output, rc = run_unchecked(
        *base_args, "get", "agentclusters.capi-provider.agent-install.openshift.io",
        "-n", cp_ns, "-o", f"jsonpath={{.items[?(@.metadata.finalizers[*]=='{finalizer}')].metadata.name}}",
    )
    if rc != 0 or not output.strip():
        return
    for ac_name in output.strip().split():
        finalizers_json, rc = run_unchecked(
            *base_args, "get", f"agentclusters.capi-provider.agent-install.openshift.io/{ac_name}",
            "-n", cp_ns, "-o", "jsonpath={.metadata.finalizers}",
        )
        if rc != 0 or finalizer not in finalizers_json:
            continue
        import json
        idx = json.loads(finalizers_json).index(finalizer)
        run_unchecked(
            *base_args, "patch", f"agentclusters.capi-provider.agent-install.openshift.io/{ac_name}",
            "-n", cp_ns, "--type=json",
            f'-p=[{{"op": "remove", "path": "/metadata/finalizers/{idx}"}}]',
        )


def _force_cleanup_agent_labels(*, k8s: K8sClient, name: str) -> None:
    agent_ns = "hardware-inventory"
    clusterorder_label = "osac.openshift.io/clusterorder"
    clusterdeployment_ns_label = "agent-install.openshift.io/clusterdeployment-namespace"
    base_args = [*k8s._base(), "--as", "system:admin"]
    output, rc = run_unchecked(
        *base_args, "get", "agents.agent-install.openshift.io",
        "-n", agent_ns, "-l", f"{clusterorder_label}={name}",
        "-o", "jsonpath={.items[*].metadata.name}",
    )
    if rc != 0 or not output.strip():
        return
    for agent_name in output.strip().split():
        run_unchecked(
            *base_args, "label", f"agents.agent-install.openshift.io/{agent_name}",
            "-n", agent_ns, f"{clusterorder_label}-", f"{clusterdeployment_ns_label}-",
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
