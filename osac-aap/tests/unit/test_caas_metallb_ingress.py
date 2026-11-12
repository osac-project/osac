"""Verify the CaaS guest MetalLB ingress contract.

The CaaS external-access flow must publish API bootstrap DNS before waiting for
guest readiness, reserve one provider address per ClusterOrder, configure the
guest router through the production-shared MetalLB role, and only then publish
wildcard apps DNS and endpoint feedback from the confirmed Service address.
"""

from pathlib import Path

import pytest
import yaml
from ansible.parsing.dataloader import DataLoader
from ansible.template import Templar

AAP_ROOT = Path(__file__).resolve().parents[2]
COLLECTIONS = AAP_ROOT / "collections" / "ansible_collections"
EXTERNAL_ACCESS = COLLECTIONS / "osac" / "service" / "roles" / "external_access"
TASKS = EXTERNAL_ACCESS / "tasks"
WAIT_FOR = COLLECTIONS / "osac" / "service" / "roles" / "wait_for"
OCP_CI_SMALL = COLLECTIONS / "osac" / "templates" / "roles" / "ocp_ci_small"
CI_EXTERNAL_ACCESS = COLLECTIONS / "ci" / "steps" / "roles" / "external_access"


def load_tasks(path: Path) -> list[dict]:
    loaded = yaml.safe_load(path.read_text())
    assert isinstance(loaded, list)
    return loaded


def task_names(tasks: list[dict]) -> list[str]:
    return [str(task.get("name", "")) for task in tasks]


def included_tasks(tasks: list[dict]) -> list[str]:
    return [
        str(task["ansible.builtin.include_tasks"])
        for task in tasks
        if "ansible.builtin.include_tasks" in task
    ]


def index_of(tasks: list[dict], needle: str) -> int:
    for index, task in enumerate(tasks):
        if needle in yaml.safe_dump(task):
            return index
    raise AssertionError(f"{needle} not found in {task_names(tasks)}")


def test_caas_create_sequences_api_dns_ingress_then_feedback() -> None:
    tasks = load_tasks(TASKS / "caas_create.yaml")

    assert included_tasks(tasks) == [
        "caas_api_dns_create.yaml",
        "caas_ingress_create.yaml",
        "publish.yaml",
    ]


def test_api_bootstrap_dns_precedes_every_guest_readiness_wait() -> None:
    api_tasks = load_tasks(TASKS / "caas_api_dns_create.yaml")
    api_text = yaml.safe_dump(api_tasks)

    assert "kube-apiserver" in api_text
    assert "caas_dns_publish.yaml" in included_tasks(api_tasks)
    assert "external_access_api_domain" in api_text
    assert "external_access_api_int_domain" in api_text
    assert "*.apps." not in api_text
    for guest_only in (
        "retrieve_kubeconfig",
        "wait_for_nodes",
        "wait_for_network_cluster_operator",
        "metallb_ingress",
    ):
        assert guest_only not in api_text


def test_caas_flow_no_longer_derives_ingress_from_agent_inventory() -> None:
    for name in (
        "caas_create.yaml",
        "caas_api_dns_create.yaml",
        "caas_ingress_create.yaml",
        "caas_dns_publish.yaml",
        "caas_vip_reserve.yaml",
        "caas_vip_release.yaml",
    ):
        text = (TASKS / name).read_text()
        assert "kind: Agent" not in text, name
        assert "external_access_ci_agents" not in text, name
        assert "external_access_ingress_ip" not in text, name

    assert not (TASKS / "caas_dns_create.yaml").exists()


def test_ingress_uses_the_reserved_address_and_the_shared_metallb_role() -> None:
    tasks = load_tasks(TASKS / "caas_ingress_create.yaml")
    text = yaml.safe_dump(tasks)

    assert index_of(tasks, "caas_vip_reserve.yaml") < index_of(tasks, "metallb_ingress")
    metallb = next(
        task
        for task in tasks
        if task.get("ansible.builtin.include_role", {}).get("name") == "osac.service.metallb_ingress"
    )
    assert metallb["ansible.builtin.include_role"]["tasks_from"] == "configure_metallb_ingress"
    assert metallb["vars"]["metallb_ingress_admin_kubeconfig"] == "{{ admin_kubeconfig }}"
    assert metallb["vars"]["metallb_ingress_ip"] == "{{ external_access_metallb_ingress_ip }}"

    # L2 mode for CI: no BGP parameters may leak into the guest MetalLB role.
    assert "worker_bgp_params" not in text
    assert "local_asn" not in text


def test_guest_service_address_must_equal_the_reserved_address() -> None:
    tasks = load_tasks(TASKS / "caas_ingress_create.yaml")
    service_wait = next(
        task
        for task in tasks
        if task.get("kubernetes.core.k8s_info", {}).get("kind") == "Service"
        and task["kubernetes.core.k8s_info"].get("name") == "external-ingress"
    )

    assert service_wait["kubernetes.core.k8s_info"]["namespace"] == "openshift-ingress"
    assert service_wait["kubernetes.core.k8s_info"]["kubeconfig"] == "{{ admin_kubeconfig }}"
    until = str(service_wait["until"])
    assert "status.loadBalancer.ingress" in until
    assert "external_access_metallb_ingress_ip" in until
    assert index_of(tasks, "metallb_ingress") < index_of(tasks, "kind: Service")
    assert index_of(tasks, "kind: Service") < index_of(tasks, "caas_dns_publish.yaml")


def test_wildcard_apps_dns_is_published_from_the_confirmed_service_address() -> None:
    tasks = load_tasks(TASKS / "caas_ingress_create.yaml")
    publish = next(task for task in tasks if "caas_dns_publish.yaml" in yaml.safe_dump(task))

    records = publish["vars"]["external_access_dns_records"]
    assert len(records) == 1
    assert records[0]["name"] == "*.{{ external_access_ingress_domain }}"
    assert records[0]["addr"] == "{{ external_access_metallb_ingress_ip }}"
    assert index_of(tasks, "kind: EndpointSlice") < index_of(tasks, "caas_dns_publish.yaml")


def test_guest_ingress_waits_for_node_capacity_before_metallb() -> None:
    tasks = load_tasks(TASKS / "caas_ingress_create.yaml")

    assert index_of(tasks, "retrieve_kubeconfig") < index_of(tasks, "metallb_ingress")
    node_wait = next(
        task for task in tasks
        if task.get("ansible.builtin.include_role", {}).get("name") == "osac.service.wait_for"
        and task["ansible.builtin.include_role"].get("tasks_from") == "wait_for_nodes.yaml"
    )
    assert node_wait["vars"]["wait_for_kubeconfig"] == "{{ admin_kubeconfig }}"
    assert "nodeRequests" in str(node_wait["vars"]["wait_for_nodes_expected_count"])
    assert index_of(tasks, "wait_for_nodes.yaml") < index_of(tasks, "metallb_ingress")


def test_vip_reservation_requires_a_provider_supplied_range() -> None:
    tasks = load_tasks(TASKS / "caas_vip_reserve.yaml")
    text = yaml.safe_dump(tasks)

    claim = next(task for task in tasks if "osac.service.ingress_vip_claim" in yaml.safe_dump(task))
    assert claim["osac.service.ingress_vip_claim"]["state"] == "present"
    assert claim["osac.service.ingress_vip_claim"]["owner_uid"] == "{{ cluster_order.metadata.uid }}"
    assert claim["osac.service.ingress_vip_claim"]["ip_range"] == (
        "{{ external_access_metallb_ingress_range }}"
    )
    assert "CI_GUEST_INGRESS_IP_RANGE" in text
    assert index_of(tasks, "CI_GUEST_INGRESS_IP_RANGE") < index_of(tasks, "ingress_vip_claim")
    assert "external_access_metallb_ingress_ip" in text


def test_reserved_address_is_published_as_the_cluster_ingress_endpoint() -> None:
    publish = (TASKS / "publish.yaml").read_text()

    assert "external_access_metallb_ingress_ip | default(external_access_ingress_ip, true)" in publish


def test_vip_release_happens_after_dns_removal_and_teardown_proof() -> None:
    tasks = load_tasks(TASKS / "caas_delete.yaml")
    text = yaml.safe_dump(tasks)

    assert included_tasks(tasks) == [
        "caas_dns_delete.yaml",
        "clear.yaml",
        "caas_vip_release.yaml",
    ]
    assert "caas_vip_release.yaml" not in (TASKS / "caas_api_dns_create.yaml").read_text()

    release_tasks = load_tasks(TASKS / "caas_vip_release.yaml")
    release_text = yaml.safe_dump(release_tasks)
    assert "baremetalworker-finalizer" in release_text
    assert "HostedCluster" in release_text
    assert index_of(release_tasks, "baremetalworker-finalizer") < index_of(
        release_tasks, "osac.service.ingress_vip_claim"
    )
    release = next(
        task for task in release_tasks if "osac.service.ingress_vip_claim" in yaml.safe_dump(task)
    )
    assert release["osac.service.ingress_vip_claim"]["state"] == "absent"
    guard = next(task for task in release_tasks if "ansible.builtin.assert" in task)
    assert "external_access_vip_release_safe | bool" in guard["ansible.builtin.assert"]["that"]
    assert index_of(release_tasks, "ansible.builtin.assert") < index_of(
        release_tasks, "osac.service.ingress_vip_claim"
    )
    assert text


def test_dns_publication_helper_waits_for_the_expected_answer() -> None:
    publish = load_tasks(TASKS / "caas_dns_publish.yaml")
    text = yaml.safe_dump(publish)

    assert "dns_class" in text
    assert "wait_for_dns" in text
    wait = next(task for task in publish if "osac.service.wait_for" in str(task))
    assert wait["vars"]["wait_for_dns_expected_value"] == "{{ item.addr }}"

    wait_text = (WAIT_FOR / "tasks" / "wait_for_dns.yaml").read_text()
    assert "wait_for_dns_expected_value" in wait_text
    assert "stdout | length > 0" in wait_text


def test_dns_name_variables_are_defined_in_one_place() -> None:
    names = load_tasks(TASKS / "caas_dns_names.yaml")
    text = yaml.safe_dump(names)

    for variable in (
        "external_access_api_domain",
        "external_access_api_int_domain",
        "external_access_ingress_domain",
    ):
        assert variable in text

    for name in ("caas_api_dns_create.yaml", "caas_ingress_create.yaml"):
        assert included_tasks(load_tasks(TASKS / name))[0] == "caas_dns_names.yaml"


def test_legacy_ci_wrapper_runs_the_whole_flow() -> None:
    create = load_tasks(CI_EXTERNAL_ACCESS / "tasks" / "create.yaml")
    delete = load_tasks(CI_EXTERNAL_ACCESS / "tasks" / "delete.yaml")

    assert create[0]["ansible.builtin.include_role"] == {
        "name": "osac.service.external_access",
        "tasks_from": "caas_create.yaml",
    }
    assert delete[0]["ansible.builtin.include_role"] == {
        "name": "osac.service.external_access",
        "tasks_from": "caas_delete.yaml",
    }


def render(expression: str, variables: dict):
    return Templar(loader=DataLoader(), variables=variables).template(expression)


@pytest.mark.parametrize(
    ("counts", "expected"),
    [([1], 1), ([1, 1], 2), ([2, 1], 3)],
)
def test_worker_wait_counts_every_requested_node_set(counts, expected) -> None:
    tasks = load_tasks(TASKS / "caas_ingress_create.yaml")
    wait = next(task for task in tasks if "wait_for_nodes.yaml" in str(task))
    variables = {
        "cluster_order": {"spec": {"nodeRequests": [
            {"numberOfNodes": count} for count in counts
        ]}}
    }
    assert int(render(wait["vars"]["wait_for_nodes_expected_count"], variables)) == expected


@pytest.mark.parametrize(
    ("slices", "ready"),
    [
        ([], False),
        ([{"endpoints": []}], False),
        ([{"endpoints": [{"conditions": {"ready": False}}]}], False),
        ([{"endpoints": [{"conditions": {"ready": True}}]}], True),
    ],
)
def test_dns_publication_waits_for_a_ready_backend(slices, ready) -> None:
    tasks = load_tasks(TASKS / "caas_ingress_create.yaml")
    wait = next(
        task for task in tasks
        if task.get("kubernetes.core.k8s_info", {}).get("kind") == "EndpointSlice"
    )
    assert wait["kubernetes.core.k8s_info"]["label_selectors"] == [
        "kubernetes.io/service-name=external-ingress"
    ]
    result = render("{{ " + wait["until"] + " }}", {
        "external_access_guest_ingress_endpoints": {"resources": slices}
    })
    assert bool(result) is ready


@pytest.mark.parametrize(
    ("orders", "clusters", "safe"),
    [
        ([], [], True),
        ([{"metadata": {"uid": "order-uid", "finalizers": []}}], [], True),
        ([{"metadata": {"uid": "order-uid", "finalizers": [
            "osac.openshift.io/baremetalworker-finalizer"
        ]}}], [], False),
        ([], [{}], False),
        ([{"metadata": {"uid": "order-uid", "finalizers": []}}], [{}], False),
        ([{"metadata": {"uid": "replacement-uid", "finalizers": []}}], [], False),
    ],
)
def test_vip_release_requires_worker_and_hosted_cluster_teardown(orders, clusters, safe) -> None:
    tasks = load_tasks(TASKS / "caas_vip_release.yaml")
    proof = next(task for task in tasks if "ansible.builtin.set_fact" in task)
    result = render(proof["ansible.builtin.set_fact"]["external_access_vip_release_safe"], {
        "external_access_release_cluster_order": {"resources": orders},
        "external_access_release_hosted_cluster": {"resources": clusters},
        "cluster_order": {"metadata": {"uid": "order-uid"}},
    })
    assert bool(result) is safe


@pytest.mark.parametrize(
    ("order_api", "cluster_api", "domains", "safe"),
    [(True, True, ["example.test"], True),
     (False, True, ["example.test"], False),
     (True, False, ["example.test"], False),
     (True, True, [], False)],
)
def test_release_fails_closed_when_teardown_or_dns_cannot_be_verified(
    order_api, cluster_api, domains, safe
) -> None:
    tasks = load_tasks(TASKS / "caas_vip_release.yaml")
    guard = next(task for task in tasks if "ansible.builtin.assert" in task)
    variables = {
        "external_access_release_cluster_order": {"api_found": order_api},
        "external_access_release_hosted_cluster": {"api_found": cluster_api},
        "external_access_vip_release_safe": True,
        "external_access_base_domain": "example.test",
        "external_access_supported_base_domains": domains,
    }
    passed = all(
        bool(render("{{ " + condition + " }}", variables))
        for condition in guard["ansible.builtin.assert"]["that"]
    )
    assert passed is safe


@pytest.mark.parametrize("ip_range", ["", "192.0.2.124-192.0.2.127"])
def test_empty_vip_range_fails_before_claiming(ip_range) -> None:
    tasks = load_tasks(TASKS / "caas_vip_reserve.yaml")
    guard = next(task for task in tasks if "ansible.builtin.assert" in task)
    variables = {
        "external_access_metallb_ingress_range": ip_range,
        "external_access_vip_registry_namespace": "aap",
        "cluster_order": {"metadata": {"uid": "order-uid"}},
    }
    passed = all(
        bool(render("{{ " + condition + " }}", variables))
        for condition in guard["ansible.builtin.assert"]["that"]
    )
    assert passed is bool(ip_range)
    assert index_of(tasks, "ansible.builtin.assert") < index_of(tasks, "osac.service.ingress_vip_claim")


def test_caas_template_still_uses_the_service_owned_entry_points() -> None:
    install = load_tasks(OCP_CI_SMALL / "tasks" / "install.yaml")
    delete = load_tasks(OCP_CI_SMALL / "tasks" / "delete.yaml")

    assert install[0]["ansible.builtin.set_fact"]["install_step_external_access_default"] == {
        "name": "osac.service.external_access",
        "tasks_from": "caas_create.yaml",
    }
    assert delete[0]["ansible.builtin.set_fact"]["delete_step_external_access_default"] == {
        "name": "osac.service.external_access",
        "tasks_from": "caas_delete.yaml",
    }
