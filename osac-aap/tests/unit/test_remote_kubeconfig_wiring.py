"""The CaaS and BMaaS roles split hub objects from infrastructure.

AAP job pods run inside the run's vcluster, so their in-cluster service
account reaches the hub API only. Every call that touches infrastructure
(HostedClusters, NodePools, Machines, namespaces, Agents, BareMetalHosts and
their Secrets, the retrieved admin kubeconfig) must pass the outer
kubeconfig the way osac.templates.ocp_virt_vm does:
``kubeconfig: "{{ remote_cluster_kubeconfig | default(omit) }}"``. Calls on
hub objects (ClusterOrders, BareMetalInstances and their namespace Secrets)
and calls that target the guest hosted cluster through
K8S_AUTH_KUBECONFIG stay without the parameter, each with a comment saying
why. These tests pin the split per call site, the playbook wiring that feeds
the fact, and the container groups that export the env.
"""

from pathlib import Path

import sys

import yaml

AAP_ROOT = Path(__file__).resolve().parents[2]

sys.path.insert(0, str(Path(__file__).resolve().parent))
from test_config_as_code_vcluster_settings import render_controller  # noqa: E402
COLLECTIONS = AAP_ROOT / "collections" / "ansible_collections" / "osac"
SERVICE = COLLECTIONS / "service" / "roles"
STEPS = COLLECTIONS / "steps" / "roles"
TEMPLATES = COLLECTIONS / "templates" / "roles"
WORKFLOWS = COLLECTIONS / "workflows" / "playbooks"

REMOTE_ARG = "{{ remote_cluster_kubeconfig | default(omit) }}"
FINALIZER_ARG = "{{ finalizer_kubeconfig | default(omit) }}"
K8S_MODULES = ("kubernetes.core.k8s", "kubernetes.core.k8s_info", "kubernetes.core.k8s_json_patch")


def k8s_calls(path: Path) -> list[tuple[str, str, dict]]:
    """Every k8s module call in a tasks file, as (task, module, args)."""
    calls: list[tuple[str, str, dict]] = []

    def walk(tasks: list[dict]) -> None:
        for task in tasks:
            if not isinstance(task, dict) or "name" not in task:
                continue
            for module in K8S_MODULES:
                if module in task:
                    calls.append((task["name"], module, task[module]))
            for block in ("block", "rescue", "always"):
                if block in task:
                    walk(task[block])

    walk(yaml.safe_load(path.read_text()) or [])
    return calls


def assert_kubeconfig(path: Path, hub_exceptions: frozenset[str]) -> None:
    calls = k8s_calls(path)
    assert calls, f"{path} must keep making k8s calls"
    for task, _module, args in calls:
        if task in hub_exceptions:
            assert "kubeconfig" not in args, f"{task} is a hub object and must stay in-cluster"
        else:
            assert args.get("kubeconfig") == REMOTE_ARG, f"{task} must follow the remote kubeconfig"


def test_hosted_cluster_create_calls_all_follow_the_remote_kubeconfig() -> None:
    assert_kubeconfig(
        SERVICE / "hosted_cluster" / "tasks" / "create_hosted_cluster.yaml",
        frozenset(),
    )


def test_hosted_cluster_delete_calls_follow_the_remote_kubeconfig() -> None:
    # The ClusterOrder worker wait is the one hub object in the flow.
    assert_kubeconfig(
        SERVICE / "hosted_cluster" / "tasks" / "delete_hosted_cluster.yaml",
        frozenset({"Wait for baremetalworker to finish deleting workers"}),
    )


def test_manage_agents_calls_all_follow_the_remote_kubeconfig() -> None:
    for task_file in sorted((SERVICE / "manage_agents" / "tasks").glob("*.yml")):
        assert_kubeconfig(task_file, frozenset())


def test_retrieve_kubeconfig_reads_the_outer_cluster() -> None:
    """The HostedCluster and its admin-kubeconfig Secret are infrastructure:
    both calls get the kubeconfig."""
    assert_kubeconfig(SERVICE / "retrieve_kubeconfig" / "tasks" / "main.yaml", frozenset())


def test_cluster_working_namespace_lists_outer_namespaces() -> None:
    assert_kubeconfig(SERVICE / "cluster_working_namespace" / "tasks" / "main.yml", frozenset())


def test_external_access_reads_the_kube_apiserver_service_on_the_outer_cluster() -> None:
    """The kube-apiserver Service sits in the hosted cluster's namespace on the
    outer cluster, which the AAP job pod cannot read through its hub
    service account."""
    assert_kubeconfig(
        SERVICE / "external_access" / "tasks" / "caas_api_dns_create.yaml",
        frozenset(),
    )


def test_cluster_infra_splits_hub_orders_from_outer_agents() -> None:
    """BareMetalPool and BareMetalInstance are OSAC CRs in POD_NAMESPACE
    (hub objects); Agents live in the outer cluster's agent namespace."""
    assert_kubeconfig(
        STEPS / "cluster_infra" / "tasks" / "create.yaml",
        frozenset(
            {
                "Step - Create BareMetalPool",
                "Wait for BareMetalInstances to be allocated",
            }
        ),
    )


def test_bm_host_provisioning_splits_hub_secrets_from_outer_hosts() -> None:
    """BareMetalHosts, their HardwareData and their namespace Secrets are
    infrastructure; the userData Secret referenced by the BareMetalInstance
    lives in the hub namespace next to it."""
    hub_reads = frozenset(
        {
            "Fetch userDataSecret",
            "Read user data from Secret",
        }
    )
    assert_kubeconfig(
        TEMPLATES / "bm_host_provisioning" / "tasks" / "create_metal3.yaml", hub_reads
    )
    assert_kubeconfig(
        TEMPLATES / "bm_host_provisioning" / "tasks" / "delete_metal3.yaml", frozenset()
    )
    assert_kubeconfig(
        TEMPLATES / "bm_host_provisioning" / "tasks" / "create_openstack.yaml", hub_reads
    )


def test_cluster_settings_reads_the_provider_secret_in_cluster() -> None:
    """The provider default Secret is a hub-side deployment object, and the
    post-install flow runs this role against the guest hosted cluster
    through K8S_AUTH_KUBECONFIG, which an explicit kubeconfig would override."""
    calls = k8s_calls(SERVICE / "cluster_settings" / "tasks" / "main.yml")
    assert len(calls) == 1
    assert "kubeconfig" not in calls[0][2]


def test_install_addon_operator_targets_the_guest_through_env() -> None:
    """playbook_osac_install_addon_operator.yml materializes the hosted
    cluster's admin kubeconfig into K8S_AUTH_KUBECONFIG; an explicit
    kubeconfig parameter here would retarget the outer cluster."""
    calls = k8s_calls(SERVICE / "install_addon_operator" / "tasks" / "main.yml")
    assert len(calls) == 10
    for task, _module, args in calls:
        assert "kubeconfig" not in args, task


def test_finalizer_takes_an_opt_in_kubeconfig() -> None:
    """The shared finalizer must not read remote_cluster_kubeconfig itself:
    VMaaS adds hub-side ComputeInstance finalizers while the variable is
    set, so the kubeconfig is an explicit per-call opt-in."""
    calls = k8s_calls(SERVICE / "finalizer" / "tasks" / "main.yaml")
    assert len(calls) == 3
    for task, _module, args in calls:
        assert args.get("kubeconfig") == FINALIZER_ARG, task


def playbook_tasks(path: Path) -> list[dict]:
    return yaml.safe_load(path.read_text())[0]["tasks"]


def include_role_steps(tasks: list[dict]) -> list[dict]:
    return [task for task in tasks if "ansible.builtin.include_role" in task]


CAAS_CREATE = AAP_ROOT / "playbook_osac_create_hosted_cluster.yml"
CAAS_DELETE = AAP_ROOT / "playbook_osac_delete_hosted_cluster.yml"
BMAAS_CREATE = AAP_ROOT / "playbook_osac_create_bare_metal_instance.yml"
BMAAS_DELETE = AAP_ROOT / "playbook_osac_delete_bare_metal_instance.yml"
BMAAS_POOL_CREATE = AAP_ROOT / "playbook_osac_create_bare_metal_pool.yml"
BMAAS_POOL_DELETE = AAP_ROOT / "playbook_osac_delete_bare_metal_pool.yml"


def test_every_caas_and_bmaas_entry_point_loads_the_remote_kubeconfig() -> None:
    for playbook in (
        CAAS_CREATE,
        CAAS_DELETE,
        WORKFLOWS / "cluster" / "create.yml",
        WORKFLOWS / "cluster" / "delete.yml",
        BMAAS_CREATE,
        BMAAS_DELETE,
        BMAAS_POOL_CREATE,
        BMAAS_POOL_DELETE,
    ):
        play = yaml.safe_load(playbook.read_text())[0]
        includes = [
            task["ansible.builtin.include_role"]
            for section in ("pre_tasks", "tasks")
            for task in include_role_steps(play.get(section, []))
            if task.get("name") == "Include get remote cluster kubeconfig"
        ]
        assert includes == [
            {
                "name": "osac.service.common",
                "tasks_from": "get_remote_cluster_kubeconfig",
            }
        ], playbook


def finalizer_steps(tasks: list[dict]) -> dict[str, dict]:
    steps = {}
    for task in include_role_steps(tasks):
        finalizer = task["ansible.builtin.include_role"]
        if finalizer.get("name") == "osac.service.finalizer":
            steps[task["name"]] = task.get("vars", {})
    return steps


def assert_finalizer_split(steps: dict[str, dict], state: str, verb: str) -> None:
    order = steps[f"{verb} infrastructure finalizer to the cluster order" if state == "present" else f"{verb} infrastructure finalizer from the cluster order"]
    namespace = steps[f"{verb} infrastructure finalizer to the working namespace" if state == "present" else f"{verb} infrastructure finalizer from the working namespace"]

    assert order["finalizer_state"] == state
    assert order["finalizer_target"]["kind"] == "ClusterOrder"
    assert "finalizer_kubeconfig" not in order

    assert namespace["finalizer_state"] == state
    assert namespace["finalizer_target"]["kind"] == "Namespace"
    assert namespace["finalizer_kubeconfig"] == REMOTE_ARG


def test_cluster_finalizers_split_hub_order_from_outer_namespace() -> None:
    assert_finalizer_split(finalizer_steps(playbook_tasks(CAAS_CREATE)), "present", "Add")
    assert_finalizer_split(finalizer_steps(playbook_tasks(CAAS_DELETE)), "absent", "Remove")


def test_workflow_cluster_finalizers_split_hub_order_from_outer_namespace() -> None:
    assert_finalizer_split(
        finalizer_steps(playbook_tasks(WORKFLOWS / "cluster" / "create.yml")), "present", "Add"
    )
    assert_finalizer_split(
        finalizer_steps(playbook_tasks(WORKFLOWS / "cluster" / "delete.yml")), "absent", "Remove"
    )


def test_status_reporting_annotates_the_hub_cluster_order() -> None:
    for playbook in (
        AAP_ROOT / "playbook_osac_report_hosted_cluster_status.yml",
        WORKFLOWS / "reporting" / "cluster_status.yml",
    ):
        tasks = yaml.safe_load(playbook.read_text())[0]["tasks"]
        report = next(task for task in tasks if task["name"] == "Report workflow status")
        assert "kubeconfig" not in report["kubernetes.core.k8s"], playbook


# The container groups that run the CaaS and BMaaS provisioning roles, and
# the ones that deliberately never see the outer kubeconfig: the post-install
# and add-on-operator jobs drive the guest hosted cluster through the admin
# kubeconfig the workflow passes them.
INFRA_GROUP_SUFFIXES = ("-cluster-fulfillment-ig", "-bare-metal-fulfillment-ig")
GUEST_GROUP_SUFFIXES = ("-create-hosted-cluster-post-install-ig", "-addon-operators-ig")

REMOTE_SECRET = {"name": "remote-cluster-kubeconfig", "key": "kubeconfig"}
MOUNT_PATH = "/etc/osac"


def _worker(pod: dict) -> dict:
    return pod["spec"]["containers"][0]


def _mounts(pod: dict) -> list[dict]:
    return _worker(pod).get("volumeMounts", [])


def _volumes(pod: dict) -> list[dict]:
    return pod["spec"].get("volumes", [])


def _env(pod: dict) -> dict:
    return {item["name"]: item.get("value") for item in _worker(pod).get("env", [])}


def test_with_the_remote_secret_the_infra_groups_mount_and_export_it(monkeypatch):
    controller = render_controller(
        monkeypatch,
        {
            "NAMESPACE": "osac",
            "AAP_PASSWORD": "not-read-here",
            "REMOTE_CLUSTER_KUBECONFIG_SECRET_NAME": REMOTE_SECRET["name"],
            "REMOTE_CLUSTER_KUBECONFIG_SECRET_KEY": REMOTE_SECRET["key"],
        },
    )

    for suffix in INFRA_GROUP_SUFFIXES:
        group = next(g for g in controller["controller_instance_groups"] if g["name"].endswith(suffix))
        pod = yaml.safe_load(group["pod_spec_override"])
        assert group["pod_spec_override"], suffix

        mounts = [m for m in _mounts(pod) if m["name"] == "remote-cluster-kubeconfig"]
        assert mounts == [
            {"name": "remote-cluster-kubeconfig", "mountPath": MOUNT_PATH, "readOnly": True}
        ], group["name"]
        volumes = [v for v in _volumes(pod) if v["name"] == "remote-cluster-kubeconfig"]
        assert volumes == [
            {
                "name": "remote-cluster-kubeconfig",
                "secret": {
                    "secretName": REMOTE_SECRET["name"],
                    "items": [{"key": REMOTE_SECRET["key"], "path": "remote-cluster-kubeconfig"}],
                    "defaultMode": 420,
                },
            }
        ], group["name"]
        assert _env(pod)["OSAC_REMOTE_CLUSTER_KUBECONFIG"] == f"{MOUNT_PATH}/remote-cluster-kubeconfig"


def test_without_the_remote_secret_the_infra_groups_stay_without_it(monkeypatch):
    controller = render_controller(
        monkeypatch, {"NAMESPACE": "osac", "AAP_PASSWORD": "not-read-here"}
    )

    for group in controller["controller_instance_groups"]:
        if group["name"].endswith(INFRA_GROUP_SUFFIXES):
            assert "remote-cluster-kubeconfig" not in group["pod_spec_override"], group["name"]


def test_the_guest_groups_never_mount_the_remote_kubeconfig(monkeypatch):
    controller = render_controller(
        monkeypatch,
        {
            "NAMESPACE": "osac",
            "AAP_PASSWORD": "not-read-here",
            "REMOTE_CLUSTER_KUBECONFIG_SECRET_NAME": REMOTE_SECRET["name"],
            "REMOTE_CLUSTER_KUBECONFIG_SECRET_KEY": REMOTE_SECRET["key"],
        },
    )

    for group in controller["controller_instance_groups"]:
        if group["name"].endswith(GUEST_GROUP_SUFFIXES):
            spec = group["pod_spec_override"]
            if spec:
                assert "remote-cluster-kubeconfig" not in spec, group["name"]
                assert "OSAC_REMOTE_CLUSTER_KUBECONFIG" not in spec, group["name"]
