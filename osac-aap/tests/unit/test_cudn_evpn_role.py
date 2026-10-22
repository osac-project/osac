import os
import subprocess
from pathlib import Path

import pytest
import yaml
from jinja2 import Environment, StrictUndefined


def _role_metadata_path() -> Path:
    return (
        Path(__file__).resolve().parents[2]
        / "collections"
        / "ansible_collections"
        / "osac"
        / "templates"
        / "roles"
        / "cudn_evpn"
        / "meta"
        / "osac.yaml"
    )


def _role_path() -> Path:
    return _role_metadata_path().parents[1]


def _raw_create_tasks() -> list[dict]:
    tasks_path = _role_path() / "tasks" / "create_subnet.yaml"
    return yaml.safe_load(tasks_path.read_text(encoding="utf-8"))


def _flatten_tasks(tasks: list[dict]) -> list[dict]:
    flattened = []
    for task in tasks:
        flattened.append(task)
        flattened.extend(_flatten_tasks(task.get("block", [])))
        flattened.extend(_flatten_tasks(task.get("always", [])))
    return flattened


def _create_tasks() -> list[dict]:
    return _flatten_tasks(_raw_create_tasks())


def _render_cudn() -> dict:
    template_path = _role_path() / "templates" / "cudn.yaml.j2"
    template = Environment(undefined=StrictUndefined).from_string(template_path.read_text(encoding="utf-8"))
    rendered = template.render(
        tenant_id="tenant-7",
        vnet_name="vnet-7",
        vnet_uuid="vnet-uuid-7",
        subnet_name="test-subnet",
        subnet={"metadata": {"namespace": "tenant-ns"}},
        subnet_cidr="192.0.2.0/24",
        l2_vni="10007",
        l3_vni="20007",
        fabric_reserved_range="192.0.2.1-192.0.2.20",
    )
    return yaml.safe_load(rendered)


def _run_role_with_inputs(tmp_path: Path, input_overrides: dict) -> subprocess.CompletedProcess:
    repo_root = Path(__file__).resolve().parents[2]
    inputs = {
        "subnet": {
            "metadata": {"name": "test-subnet", "annotations": {"osac.openshift.io/tenant": "tenant-7"}},
            "spec": {"virtualNetwork": "vnet-uuid-7", "ipv4Cidr": "192.0.2.0/24"},
        },
        "subnet_name": "test-subnet",
        "template_parameters": {},
        "l2_vni": "10007",
        "l3_vni": "20007",
        "fabric_reserved_range": "192.0.2.1-192.0.2.20",
    }
    inputs.update(input_overrides)
    playbook = [
        {
            "name": "Run EVPN manager input validation",
            "hosts": "localhost",
            "connection": "local",
            "gather_facts": False,
            "tasks": [
                {
                    "name": "Include EVPN role",
                    "ansible.builtin.include_role": {"name": "osac.templates.cudn_evpn", "tasks_from": "create_subnet"},
                    "vars": inputs,
                }
            ],
        }
    ]
    playbook_path = tmp_path / "invalid_inputs.yaml"
    playbook_path.write_text(yaml.safe_dump(playbook), encoding="utf-8")
    local_temp = tmp_path / "ansible-local"
    local_temp.mkdir()
    kubeconfig = tmp_path / "empty-kubeconfig"
    kubeconfig.write_text("apiVersion: v1\nkind: Config\nclusters: []\ncontexts: []\nusers: []\n", encoding="utf-8")
    environment = os.environ.copy()
    environment["ANSIBLE_CONFIG"] = str(repo_root / "ansible.cfg")
    environment["ANSIBLE_LOCAL_TEMP"] = str(local_temp)
    environment["KUBECONFIG"] = str(kubeconfig)
    environment["OSAC_REMOTE_CLUSTER_KUBECONFIG"] = ""

    return subprocess.run(
        ["ansible-playbook", "--inventory", "localhost,", str(playbook_path)],
        cwd=repo_root,
        env=environment,
        capture_output=True,
        text=True,
        check=False,
        timeout=30,
    )


def test_role_metadata_registers_ipv4_k8s_manager() -> None:
    metadata = yaml.safe_load(_role_metadata_path().read_text(encoding="utf-8"))

    assert metadata["template_type"] == "network"
    assert metadata["k8s_manager"] == "cudn_evpn"
    assert "fabric_manager" not in metadata
    assert metadata["capabilities"] == {"supports_ipv4": True, "supports_ipv6": False, "supports_dual_stack": False}


def test_role_arguments_match_the_subnet_dispatch_contract() -> None:
    argspec_path = _role_path() / "meta" / "argument_specs.yaml"
    argspec = yaml.safe_load(argspec_path.read_text(encoding="utf-8"))["argument_specs"]["create_subnet"]["options"]

    assert argspec["subnet"]["required"] is True
    assert argspec["subnet_name"]["required"] is True
    assert argspec["template_parameters"]["required"] is False
    assert argspec["l2_vni"]["required"] is True
    assert argspec["l3_vni"]["required"] is True
    assert argspec["fabric_reserved_range"]["required"] is True


def test_required_fabric_outputs_are_checked_before_kubernetes_writes() -> None:
    tasks = _create_tasks()
    task_names = [task.get("name") for task in tasks]
    assert "Validate EVPN fabric outputs" in task_names

    validation_index = task_names.index("Validate EVPN fabric outputs")
    kubernetes_write_indices = [index for index, task in enumerate(tasks) if "kubernetes.core.k8s" in task]
    assert kubernetes_write_indices
    assert validation_index < min(kubernetes_write_indices)

    validation = tasks[validation_index]["ansible.builtin.assert"]
    assert len(validation["that"]) >= 3
    assert any("l2_vni" in condition for condition in validation["that"])
    assert any("l3_vni" in condition for condition in validation["that"])
    assert any("fabric_reserved_range" in condition for condition in validation["that"])


@pytest.mark.parametrize(
    "input_overrides",
    [
        {"fabric_reserved_range": ""},
        {"fabric_reserved_range": "  "},
        {"l2_vni": "0"},
        {"l2_vni": "not-a-vni"},
        {"l3_vni": "16777216"},
    ],
)
def test_invalid_fabric_inputs_fail_before_namespace_write(tmp_path: Path, input_overrides: dict) -> None:
    result = _run_role_with_inputs(tmp_path, input_overrides)
    output = result.stdout + result.stderr

    assert result.returncode != 0
    assert "No Kubernetes resources were changed" in output
    assert "Create tenant workload namespace" not in output


def test_missing_fabric_range_fails_argument_validation_before_namespace_write(tmp_path: Path) -> None:
    result = _run_role_with_inputs(tmp_path, {"fabric_reserved_range": None})
    output = result.stdout + result.stderr

    assert result.returncode != 0
    assert "fabric_reserved_range" in output
    assert "Create tenant workload namespace" not in output


def test_cudn_template_carries_evpn_identity_and_fabric_range() -> None:
    cudn = _render_cudn()

    assert cudn["kind"] == "ClusterUserDefinedNetwork"
    assert cudn["metadata"]["name"] == "vnet-7"
    assert cudn["metadata"]["labels"] == {"evpn": "true"}
    assert cudn["metadata"]["annotations"] == {
        "osac.openshift.io/tenant": "tenant-7",
        "osac.openshift.io/owner-reference": "VirtualNetwork/vnet-7",
        "osac.openshift.io/virtualnetwork-uuid": "vnet-uuid-7",
        "osac.openshift.io/virtualnetwork-namespace": "tenant-ns",
        "osac.openshift.io/cudn-owner-subnet": "test-subnet",
    }
    assert cudn["spec"]["namespaceSelector"]["matchLabels"] == {
        "tenant": "tenant-7",
        "osac.openshift.io/virtualnetwork-uuid": "vnet-uuid-7",
    }

    network = cudn["spec"]["network"]
    assert network["topology"] == "Layer2"
    assert network["transport"] == "EVPN"
    assert network["layer2"]["role"] == "Primary"
    assert network["layer2"]["subnets"] == ["192.0.2.0/24"]
    assert network["layer2"]["reservedSubnets"] == ["192.0.2.1-192.0.2.20"]
    assert "defaultGatewayIPs" not in network["layer2"]
    assert network["evpn"] == {"vtep": "tenant-vtep", "macVRF": {"vni": 10007}, "ipVRF": {"vni": 20007}}


def test_role_creates_tenant_namespace_and_waits_for_cudn_ready() -> None:
    tasks = _create_tasks()
    task_names = [task.get("name") for task in tasks]
    assert "Create tenant workload namespace" in task_names
    assert "Read existing shared ClusterUserDefinedNetwork" in task_names
    assert "Validate existing CUDN belongs to the parent VirtualNetwork" in task_names
    assert "Read persisted CUDN owner from the shared Lease" in task_names
    assert "Persist CUDN owner on the shared Lease before Kubernetes writes" in task_names
    assert "Apply ClusterUserDefinedNetwork for its current Subnet owner" in task_names
    assert "Wait for current CUDN generation to be Ready" in task_names
    assert "Export CUDN status" in task_names

    namespace_task = tasks[task_names.index("Create tenant workload namespace")]
    namespace = namespace_task["kubernetes.core.k8s"]["definition"]
    assert namespace["kind"] == "Namespace"
    assert namespace["metadata"]["labels"]["k8s.ovn.org/primary-user-defined-network"] == ""
    assert namespace["metadata"]["labels"]["osac.openshift.io/k8s-manager"] == "cudn_evpn"
    assert namespace["metadata"]["labels"]["osac.openshift.io/virtualnetwork-uuid"] == "{{ vnet_uuid }}"
    assert namespace["metadata"]["annotations"]["osac.openshift.io/owner-reference"] == "Subnet/{{ subnet_name }}"

    existing_lookup = tasks[task_names.index("Read existing shared ClusterUserDefinedNetwork")]
    assert existing_lookup["kubernetes.core.k8s_info"]["kind"] == "ClusterUserDefinedNetwork"
    assert existing_lookup["kubernetes.core.k8s_info"]["name"] == "{{ vnet_name }}"

    apply_cudn = tasks[task_names.index("Apply ClusterUserDefinedNetwork for its current Subnet owner")]
    assert apply_cudn["kubernetes.core.k8s"]["state"] == "present"
    assert "when" not in apply_cudn

    persist_owner_index = task_names.index("Persist CUDN owner on the shared Lease before Kubernetes writes")
    persist_owner = tasks[persist_owner_index]["kubernetes.core.k8s"]
    assert persist_owner["state"] == "patched"
    assert persist_owner["definition"]["metadata"]["annotations"] == {
        "osac.openshift.io/cudn-owner-subnet": "{{ _cudn_owner_subnet_name }}"
    }
    assert persist_owner_index < task_names.index("Create tenant workload namespace")

    ready_task = tasks[task_names.index("Wait for current CUDN generation to be Ready")]
    assert ready_task["kubernetes.core.k8s_info"]["kind"] == "ClusterUserDefinedNetwork"
    assert ready_task["retries"] == 30
    assert ready_task["delay"] == 10
    assert "Ready" in ready_task["until"]
    assert "observedGeneration" in ready_task["until"]
    assert "'ge'" in ready_task["until"]

    status_task = tasks[task_names.index("Export CUDN status")]
    stats = status_task["ansible.builtin.set_stats"]["data"]
    assert "cudn_name" in stats
    assert "cudn_namespace" in stats
    assert "vrf_name" in stats


def test_cudn_readiness_wait_rejects_ready_from_an_older_generation() -> None:
    tasks = _create_tasks()
    task = next(task for task in tasks if task.get("name") == "Wait for current CUDN generation to be Ready")
    condition = Environment().compile_expression(task["until"])

    def evaluate(observed_generation: int) -> bool:
        return condition(
            cudn_status={
                "resources": [
                    {
                        "metadata": {"generation": 4},
                        "status": {
                            "conditions": [
                                {"type": "Ready", "status": "True", "observedGeneration": observed_generation}
                            ]
                        },
                    }
                ]
            }
        )

    assert not evaluate(3)
    assert evaluate(4)


def test_shared_cudn_is_parent_owned_and_reconciled_only_by_current_owner() -> None:
    tasks = _create_tasks()
    task_names = [task.get("name") for task in tasks]
    read_index = task_names.index("Read existing shared ClusterUserDefinedNetwork")
    validate_index = task_names.index("Validate existing CUDN belongs to the parent VirtualNetwork")
    namespace_index = task_names.index("Create tenant workload namespace")
    apply_index = task_names.index("Apply ClusterUserDefinedNetwork for its current Subnet owner")

    assert read_index < validate_index < namespace_index < apply_index
    assertion = tasks[validate_index]["ansible.builtin.assert"]
    assert "owner-reference" in assertion["that"][0]
    assert "virtualnetwork-uuid" in assertion["that"][0]
    assert "virtualnetwork-namespace" in assertion["that"][0]
    assert "tenant" in assertion["that"][0]
    assert "Refusing to manage this shared resource" in assertion["fail_msg"]
    assert tasks[apply_index]["kubernetes.core.k8s"]["state"] == "present"
    owner_block = next(
        task for task in tasks if task.get("name") == "Provision CUDN resources only for its persisted Subnet owner"
    )
    assert owner_block["when"][0] == "_cudn_owner_subnet_name == subnet_name"
    owner_block_names = [task.get("name") for task in _flatten_tasks(owner_block["block"])]
    assert "Create tenant workload namespace" in owner_block_names


def test_existing_sibling_namespaces_are_backfilled_before_cudn_selector_migration() -> None:
    tasks = _create_tasks()
    task_names = [task.get("name") for task in tasks]
    validate_cudn_index = task_names.index("Validate existing CUDN belongs to the parent VirtualNetwork")
    read_namespaces_index = task_names.index("Read existing workload namespaces before CUDN selector migration")
    validate_namespaces_index = task_names.index("Validate existing workload namespaces before selector migration")
    backfill_index = task_names.index("Backfill the VirtualNetwork UUID on existing workload namespaces")
    apply_cudn_index = task_names.index("Apply ClusterUserDefinedNetwork for its current Subnet owner")

    assert validate_cudn_index < read_namespaces_index < validate_namespaces_index < backfill_index < apply_cudn_index

    read_namespace_task = tasks[read_namespaces_index]
    read_task = read_namespace_task["kubernetes.core.k8s_info"]
    assert read_task["kind"] == "Namespace"
    assert read_task["name"] == "{{ item.metadata.name }}"
    assert read_task["kubeconfig"] == "{{ remote_cluster_kubeconfig | default(omit) }}"
    assert read_namespace_task["loop"] == "{{ _cudn_namespace_subnets }}"

    namespace_guard = tasks[validate_namespaces_index]["ansible.builtin.assert"]
    assert "virtual-network" in namespace_guard["that"][0]
    assert "owner-reference" in namespace_guard["that"][0]
    assert "virtualnetwork-uuid" in namespace_guard["that"][0]
    assert "Refusing to change the CUDN selector" in namespace_guard["fail_msg"]

    backfill = tasks[backfill_index]["kubernetes.core.k8s"]
    assert backfill["state"] == "patched"
    assert backfill["definition"]["metadata"]["labels"] == {"osac.openshift.io/virtualnetwork-uuid": "{{ vnet_uuid }}"}
    assert "item.resources | default([]) | length > 0" in tasks[backfill_index]["when"]


def test_namespace_selector_migration_only_backfills_matching_tenant_subnets() -> None:
    guard = next(
        task
        for task in _create_tasks()
        if task.get("name") == "Validate existing workload namespaces before selector migration"
    )["ansible.builtin.assert"]["that"][0]
    expression = Environment().compile_expression(guard)
    subnet_item = {"metadata": {"name": "subnet-7"}}
    args = {"tenant_id": "tenant-7", "vnet_name": "vnet-7", "vnet_uuid": "vnet-uuid-7"}

    assert expression(item={"resources": [], "item": subnet_item}, **args) is True

    legacy_namespace = {
        "metadata": {
            "labels": {"tenant": "tenant-7", "virtual-network": "vnet-7"},
            "annotations": {
                "osac.openshift.io/tenant": "tenant-7",
                "osac.openshift.io/owner-reference": "Subnet/subnet-7",
            },
        }
    }
    assert expression(item={"resources": [legacy_namespace], "item": subnet_item}, **args) is True

    current_namespace = {
        "metadata": {
            "labels": {
                "tenant": "tenant-7",
                "virtual-network": "vnet-7",
                "osac.openshift.io/virtualnetwork-uuid": "vnet-uuid-7",
            },
            "annotations": {
                "osac.openshift.io/tenant": "tenant-7",
                "osac.openshift.io/owner-reference": "Subnet/subnet-7",
            },
        }
    }
    assert expression(item={"resources": [current_namespace], "item": subnet_item}, **args) is True

    for key, value in (
        ("tenant", "tenant-other"),
        ("virtual-network", "other-vnet"),
        ("osac.openshift.io/virtualnetwork-uuid", "other-vnet-uuid"),
    ):
        conflicting = {
            **current_namespace,
            "metadata": {
                **current_namespace["metadata"],
                "labels": {**current_namespace["metadata"]["labels"], key: value},
            },
        }
        assert expression(item={"resources": [conflicting], "item": subnet_item}, **args) is False

    conflicting_owner = {
        **legacy_namespace,
        "metadata": {
            **legacy_namespace["metadata"],
            "annotations": {
                **legacy_namespace["metadata"]["annotations"],
                "osac.openshift.io/owner-reference": "Subnet/other",
            },
        },
    }
    assert expression(item={"resources": [conflicting_owner], "item": subnet_item}, **args) is False


def test_existing_cudn_must_match_the_full_virtualnetwork_identity() -> None:
    task = next(
        task
        for task in _create_tasks()
        if task.get("name") == "Validate existing CUDN belongs to the parent VirtualNetwork"
    )
    owner_expression = Environment().compile_expression(task["ansible.builtin.assert"]["that"][0])
    identity = {
        "osac.openshift.io/owner-reference": "VirtualNetwork/vnet-7",
        "osac.openshift.io/tenant": "tenant-7",
        "osac.openshift.io/virtualnetwork-uuid": "vnet-uuid-7",
        "osac.openshift.io/virtualnetwork-namespace": "tenant-ns",
    }
    args = {
        "vnet_name": "vnet-7",
        "tenant_id": "tenant-7",
        "vnet_uuid": "vnet-uuid-7",
        "subnet": {"metadata": {"namespace": "tenant-ns"}},
    }

    assert owner_expression(_existing_cudn={"resources": []}, **args) is True
    assert owner_expression(_existing_cudn={"resources": [{"metadata": {"annotations": identity}}]}, **args) is True
    legacy_identity = {
        "osac.openshift.io/owner-reference": "VirtualNetwork/vnet-7",
        "osac.openshift.io/tenant": "tenant-7",
        "osac.openshift.io/cudn-owner-subnet": "old-owner",
    }
    assert (
        owner_expression(_existing_cudn={"resources": [{"metadata": {"annotations": legacy_identity}}]}, **args) is True
    )

    # A same-named VirtualNetwork in a different tenant or namespace must not
    # adopt or mutate the cluster-scoped CUDN belonging to another parent.
    for key, value in (
        ("osac.openshift.io/tenant", "tenant-other"),
        ("osac.openshift.io/virtualnetwork-uuid", "other-vnet-uuid"),
        ("osac.openshift.io/virtualnetwork-namespace", "other-tenant-ns"),
    ):
        conflicting = {**identity, key: value}
        assert (
            owner_expression(_existing_cudn={"resources": [{"metadata": {"annotations": conflicting}}]}, **args)
            is False
        )
    assert (
        owner_expression(
            _existing_cudn={
                "resources": [
                    {"metadata": {"annotations": {**legacy_identity, "osac.openshift.io/tenant": "tenant-other"}}}
                ]
            },
            **args,
        )
        is False
    )


def test_oldest_active_non_skipped_subnet_owns_cudn_and_ties_break_by_name() -> None:
    tasks = _create_tasks()
    lookup_index = next(
        i for i, task in enumerate(tasks) if task.get("name") == "List Subnets in the parent VirtualNetwork"
    )
    active_task = next(
        task for task in tasks if task.get("name") == "Find active Subnets for the parent VirtualNetwork"
    )
    candidate_task = next(
        task
        for task in tasks
        if task.get("name") == "Add active Subnets that allow Kubernetes provisioning as CUDN owner candidates"
    )
    owner_task = next(
        task for task in tasks if task.get("name") == "Select oldest eligible Subnet as initial CUDN owner"
    )
    owner_template = owner_task["ansible.builtin.set_fact"]["_cudn_owner_subnet"]
    owner_expression = Environment().compile_expression(owner_template.removeprefix("{{").removesuffix("}}").strip())
    active_expression = Environment().compile_expression(
        active_task["ansible.builtin.set_fact"]["_cudn_owner_active_subnets"]
        .removeprefix("{{")
        .removesuffix("}}")
        .strip()
    )
    subnets = [
        {
            "metadata": {
                "name": "older-but-skipped",
                "creationTimestamp": "2024-01-01T00:00:00Z",
                "annotations": {"osac.openshift.io/skip-k8s-manager": "true"},
            },
            "spec": {"virtualNetwork": "vnet-uuid-7"},
        },
        {
            "metadata": {"name": "tie-z", "creationTimestamp": "2026-01-01T00:00:00Z"},
            "spec": {"virtualNetwork": "vnet-uuid-7"},
        },
        {
            "metadata": {
                "name": "older-but-deleting",
                "creationTimestamp": "2025-01-01T00:00:00Z",
                "deletionTimestamp": "2026-01-02T00:00:00Z",
            },
            "spec": {"virtualNetwork": "vnet-uuid-7"},
        },
        {
            "metadata": {"name": "tie-a", "creationTimestamp": "2026-01-01T00:00:00Z"},
            "spec": {"virtualNetwork": "vnet-uuid-7"},
        },
        {
            "metadata": {"name": "other-vnet", "creationTimestamp": "2024-01-01T00:00:00Z"},
            "spec": {"virtualNetwork": "other-uuid"},
        },
    ]

    active_subnets = active_expression(_vnet_subnet_lookup={"resources": subnets}, vnet_uuid="vnet-uuid-7")
    allow_k8s = Environment().compile_expression(candidate_task["when"])
    assert candidate_task["loop"] == "{{ _cudn_owner_active_subnets }}"
    assert allow_k8s(item=subnets[0]) is False
    candidates = [item for item in active_subnets if allow_k8s(item=item)]
    owner = owner_expression(_cudn_owner_candidates=candidates)
    assert owner["metadata"]["name"] == "tie-a"
    assert lookup_index < tasks.index(active_task) < tasks.index(candidate_task) < tasks.index(owner_task)

    owner_block = next(
        task for task in tasks if task.get("name") == "Provision CUDN resources only for its persisted Subnet owner"
    )
    assert owner_block["when"] == [
        "_cudn_owner_subnet_name == subnet_name",
        "(subnet.metadata.annotations | default({})).get('osac.openshift.io/skip-k8s-manager', '') != 'true'",
    ]


def test_persisted_lease_owner_precedes_cudn_annotation_and_initial_oldest_subnet() -> None:
    tasks = _create_tasks()
    owner_parts = next(task for task in tasks if task.get("name") == "Keep the persisted CUDN owner stable")
    effective_owner = next(task for task in tasks if task.get("name") == "Set effective persisted CUDN owner")
    lease_owner = Environment().compile_expression(
        owner_parts["ansible.builtin.set_fact"]["_cudn_owner_from_lease"].removeprefix("{{").removesuffix("}}").strip()
    )
    cudn_owner = Environment().compile_expression(
        owner_parts["ansible.builtin.set_fact"]["_cudn_owner_from_cudn"].removeprefix("{{").removesuffix("}}").strip()
    )
    choose_owner = Environment().compile_expression(
        effective_owner["ansible.builtin.set_fact"]["_cudn_owner_subnet_name"]
        .removeprefix("{{")
        .removesuffix("}}")
        .strip()
    )

    assert (
        lease_owner(
            _cudn_owner_lease={
                "resources": [{"metadata": {"annotations": {"osac.openshift.io/cudn-owner-subnet": "first"}}}]
            }
        )
        == "first"
    )
    assert (
        cudn_owner(
            _existing_cudn={
                "resources": [{"metadata": {"annotations": {"osac.openshift.io/cudn-owner-subnet": "legacy"}}}]
            }
        )
        == "legacy"
    )
    assert (
        choose_owner(
            _cudn_owner_from_lease="first",
            _cudn_owner_from_cudn="legacy",
            _cudn_owner_subnet={"metadata": {"name": "new-oldest"}},
        )
        == "first"
    )
    assert (
        choose_owner(
            _cudn_owner_from_lease="",
            _cudn_owner_from_cudn="legacy",
            _cudn_owner_subnet={"metadata": {"name": "new-oldest"}},
        )
        == "legacy"
    )
    assert (
        choose_owner(
            _cudn_owner_from_lease="",
            _cudn_owner_from_cudn="",
            _cudn_owner_subnet={"metadata": {"name": "first-owner"}},
        )
        == "first-owner"
    )


def test_parent_virtual_network_uuid_resolves_name_for_shared_cudn_identity() -> None:
    tasks = _create_tasks()
    names = [task.get("name") for task in tasks]
    lookup_index = names.index("Look up parent VirtualNetwork by UUID label")
    resolve_index = names.index("Set parent VirtualNetwork name")
    create_namespace_index = names.index("Create tenant workload namespace")

    assert lookup_index < resolve_index < create_namespace_index
    lookup = tasks[lookup_index]["kubernetes.core.k8s_info"]
    assert lookup["kind"] == "VirtualNetwork"
    assert lookup["namespace"] == "{{ subnet.metadata.namespace }}"
    assert lookup["label_selectors"] == ["osac.openshift.io/virtualnetwork-uuid={{ vnet_uuid }}"]
    assert tasks[resolve_index]["ansible.builtin.set_fact"]["vnet_name"] == (
        "{{ _parent_vnet_lookup.resources[0].metadata.name }}"
    )


def test_shared_cudn_create_is_serialized_and_lock_is_released_on_failure() -> None:
    tasks = _raw_create_tasks()
    names = [task.get("name") for task in tasks]
    assert names.index("Ensure shared CUDN lock Lease exists") < names.index("Acquire shared CUDN lock")
    assert names.index("Acquire shared CUDN lock") < names.index(
        "Provision EVPN Subnet resources while holding the VirtualNetwork lock"
    )

    provision = tasks[names.index("Provision EVPN Subnet resources while holding the VirtualNetwork lock")]
    resource_names = [task.get("name") for task in _flatten_tasks(provision.get("block", []))]
    assert resource_names.index("Create tenant workload namespace") < resource_names.index(
        "Apply ClusterUserDefinedNetwork for its current Subnet owner"
    )
    assert resource_names.index("Wait for current CUDN generation to be Ready") < resource_names.index(
        "Export CUDN status"
    )
    assert provision["always"][0]["ansible.builtin.include_role"]["tasks_from"] == "release"


def test_shared_lock_lease_has_tenant_and_parent_metadata() -> None:
    helper_path = _role_path() / "tasks" / "ensure_vnet_lock.yaml"
    task = yaml.safe_load(helper_path.read_text(encoding="utf-8"))[0]
    metadata = task["kubernetes.core.k8s"]["definition"]["metadata"]

    assert metadata["annotations"] == {
        "osac.openshift.io/tenant": "{{ tenant_id }}",
        "osac.openshift.io/owner-reference": "VirtualNetwork/{{ vnet_name }}",
    }
