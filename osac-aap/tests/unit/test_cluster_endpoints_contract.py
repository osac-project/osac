"""Verify the ClusterOrder-owned endpoint task and AAP launch contracts."""

import json
import os
import secrets
import subprocess
import sys
from pathlib import Path

import yaml

AAP_ROOT = Path(__file__).resolve().parents[2]
COLLECTIONS = AAP_ROOT / "collections/ansible_collections/osac"
TASKS = COLLECTIONS / "service/roles/cluster_endpoints/tasks"
METALLB_TASKS = COLLECTIONS / "service/roles/metallb_ingress/tasks"
ROUTE53_DNS_TASKS = AAP_ROOT / "collections/ansible_collections/dns/route53/roles/dns/tasks"
PLAYBOOK = AAP_ROOT / "playbook_osac_cluster_endpoints.yml"
TEST_FIXTURES = AAP_ROOT / "tests/unit/fixtures/cluster_endpoints"
ANSIBLE_PLAYBOOK = Path(sys.executable).parent / "ansible-playbook"


def load(path: Path):
    return yaml.safe_load(path.read_text())


def named(tasks: list[dict]) -> dict[str, dict]:
    return {task["name"]: task for task in tasks}


def test_endpoint_playbook_has_a_fixed_task_allowlist_and_rejects_extra_vars() -> None:
    play = load(PLAYBOOK)[0]
    assert "hostedClusterName" in str(play["vars"]["hosted_cluster_name"])
    assert "osac_job_vars.hosted_cluster_name" in str(play["vars"]["hosted_cluster_name"])
    pre_tasks = named(play["pre_tasks"])
    validation = pre_tasks["Check the endpoint task input"]
    allowed = validation["vars"]["cluster_endpoint_tasks"]

    assert allowed == [
        "configure_guest_ingress",
        "publish_public_dns_api",
        "remove_public_dns_api",
        "publish_public_dns_ingress",
        "remove_public_dns_ingress",
    ]
    assert "osac_job_vars.task" in str(validation["ansible.builtin.assert"]["that"])
    extra_vars_guard = pre_tasks["Reject unapproved extra variables"]
    assert "launch_extra_vars" in str(extra_vars_guard)
    assert "osac_job_vars" in str(extra_vars_guard)

    dispatch = play["tasks"][0]["ansible.builtin.include_role"]
    assert dispatch == {
        "name": "osac.service.cluster_endpoints",
        "tasks_from": "main.yaml",
    }
    endpoint_tasks = load(TASKS / "main.yaml")
    publication_preflight = endpoint_tasks[0]
    assert publication_preflight["when"] == (
        "osac_job_vars.task in ['publish_public_dns_api', 'publish_public_dns_ingress']"
    )
    publication_assert = publication_preflight["block"][0]["ansible.builtin.assert"]
    assert "hosted_cluster_base_domain" in str(publication_assert["that"])
    assert "external_access_supported_base_domains" in str(
        publication_assert["that"]
    )
    preflight_error = publication_preflight["rescue"][0]["ansible.builtin.set_stats"]["data"]["osac_error"]
    assert preflight_error["retryable"] is False
    assert "rejected before DNS writes" in preflight_error["message"]
    dispatch = endpoint_tasks[-1]["block"]
    assert dispatch[0]["ansible.builtin.include_tasks"] == (
        "{{ osac_job_vars.task }}.yaml"
    )
    rescue = endpoint_tasks[-1]["rescue"]
    retryable_error = rescue[0]["ansible.builtin.set_stats"]["data"]["osac_error"]
    assert retryable_error["retryable"] is True
    assert "OSAC will retry" in retryable_error["message"]


def test_guest_ingress_uses_the_hosted_control_plane_service_network_kubeconfig() -> None:
    tasks = load(TASKS / "configure_guest_ingress.yaml")
    by_name = named(tasks)

    secret_lookup = by_name["Read the service-network admin kubeconfig"][
        "kubernetes.core.k8s_info"
    ]
    assert secret_lookup["kind"] == "Secret"
    assert secret_lookup["name"] == "service-network-admin-kubeconfig"
    require_secret = by_name["Require the service-network admin kubeconfig Secret"]
    assert require_secret["ansible.builtin.assert"]["that"] == [
        "_cluster_endpoint_kubeconfig_secret.resources | length == 1"
    ]
    assert "no_log" not in require_secret
    fail_msg = require_secret["ansible.builtin.assert"]["fail_msg"]
    assert "was not found" in fail_msg
    assert "hosted_control_plane_namespace" not in fail_msg
    configure = by_name["Configure guest ingress with the reserved address"]
    assert configure["ansible.builtin.include_role"]["name"] == (
        "osac.service.metallb_ingress"
    )
    assert "ingress_allocated_address" in str(configure["vars"])
    wait = by_name["Wait for the guest ingress Service to report the reserved address"]
    assert wait["until"]
    assert "ingress_allocated_address" in str(wait["until"])
    assert wait["no_log"] is True


def test_metallb_tasks_do_not_log_the_admin_kubeconfig() -> None:
    for task_file in ("configure_metallb_ingress.yml", "configure_bgp.yml"):
        for task in load(METALLB_TASKS / task_file):
            module = task.get("kubernetes.core.k8s") or task.get("kubernetes.core.k8s_info")
            if module and "metallb_ingress_admin_kubeconfig" in str(module.get("kubeconfig")):
                assert task.get("no_log") is True, task["name"]


def test_dns_tasks_enforce_supported_domain_and_record_conventions() -> None:
    for task_name, record_name in (
        ("publish_public_dns_api", "api-int."),
        ("remove_public_dns_api", "api-int."),
        ("publish_public_dns_ingress", "*.apps."),
        ("remove_public_dns_ingress", "*.apps."),
    ):
        tasks = load(TASKS / f"{task_name}.yaml")
        text = str(tasks)
        dns_operation = next(
            task
            for task in tasks
            if "ansible.builtin.include_role" in task
            and "{{ dns_class }}" in str(task["ansible.builtin.include_role"])
        )
        assert record_name in text
        assert "hosted_cluster_base_domain" in text
        assert "external_access_supported_base_domains" in text
        assert "lookup('env'" not in text
        if task_name in {"publish_public_dns_api", "remove_public_dns_api"}:
            assert dns_operation["loop_control"]["label"] == "api-dns-record"
            assert "{{" not in dns_operation["loop_control"]["label"]
        if task_name.startswith("remove_"):
            domain_guard = next(
                task
                for task in tasks
                if "ansible.builtin.assert" in task
            )
            assert "_cluster_endpoint_dns_domain in _cluster_endpoint_dns_supported_domains" in str(
                domain_guard["ansible.builtin.assert"]["that"]
            )
            assert "when" not in dns_operation
        else:
            assert "when" in dns_operation
            assert "_cluster_endpoint_dns_domain in _cluster_endpoint_dns_supported_domains" in str(
                dns_operation["when"]
            )

    api_tasks = load(TASKS / "publish_public_dns_api.yaml")
    api_wait = next(task for task in api_tasks if task["name"] == "Wait for API DNS records")
    assert api_wait["loop_control"]["label"] == "api-dns-wait"
    assert api_wait["no_log"] is True


def test_dns_publication_waits_for_records_using_the_configured_authoritative_server() -> None:
    expectations = {
        "publish_public_dns_api": [
            "api.{{ hosted_cluster_name }}.{{ hosted_cluster_base_domain }}",
            "api-int.{{ hosted_cluster_name }}.{{ hosted_cluster_base_domain }}",
        ],
        "publish_public_dns_ingress": [
            "default-router.apps.{{ hosted_cluster_name }}.{{ _cluster_endpoint_dns_domain }}",
        ],
    }

    for task_name, expected_records in expectations.items():
        tasks = load(TASKS / f"{task_name}.yaml")
        wait = next(task for task in tasks if task["name"].startswith("Wait for "))
        lookup = next(
            task
            for task in tasks
            if task["name"].startswith("Resolve authoritative nameserver")
        )

        assert wait["ansible.builtin.include_role"]["name"] == "osac.service.wait_for"
        assert wait["ansible.builtin.include_role"]["tasks_from"] == "wait_for_dns"
        assert wait["vars"]["wait_for_dns_expected_value"] == "{{ external_ip.address }}"
        assert "dns_server" in str(wait["vars"]["wait_for_dns_server"])
        assert "_cluster_endpoint_zone_ns.stdout_lines" in str(
            wait["vars"]["wait_for_dns_server"]
        )
        assert "external_access_dns_wait_use_authoritative" in str(lookup["when"])
        assert "_cluster_endpoint_dns_zone" in str(lookup["ansible.builtin.command"])
        assert "failed_when" in lookup
        assert all(record in str(tasks) for record in expected_records)


def test_cluster_endpoints_job_uses_cluster_fulfillment_without_generic_dispatch() -> None:
    controller = load(
        COLLECTIONS / "config_as_code/roles/aap/vars/controller.yml"
    )
    template = next(
        item
        for item in controller["controller_templates"]
        if item["name"] == "{{ aap_prefix }}-cluster-endpoints"
    )

    assert template["playbook"] == "osac-aap/playbook_osac_cluster_endpoints.yml"
    assert template["inventory"] == "{{ aap_prefix }}-cluster-fulfillment"
    assert template["instance_groups"] == ["{{ aap_prefix }}-cluster-fulfillment-ig"]
    assert template["prevent_instance_group_fallback"] is True
    assert template["ask_labels_on_launch"] is True
    assert template["timeout"] == 3600


def test_route53_dns_task_names_do_not_log_record_names() -> None:
    for action in ("create", "delete"):
        task = load(ROUTE53_DNS_TASKS / f"{action}.yaml")[0]
        assert task["name"] == f"{'Create' if action == 'create' else 'Delete'} DNS record via Route 53"
        assert "{{" not in task["name"]
        assert task["no_log"] is True


def _run_endpoint_playbook(tmp_path: Path, task: str, inputs: dict, env: dict) -> subprocess.CompletedProcess:
    extra_vars = tmp_path / "extra-vars.json"
    extra_vars.write_text(json.dumps({"osac_job_vars": {"task": task, **inputs}}))
    test_collection_path = TEST_FIXTURES / "collections"
    environ = {
        **os.environ,
        "ANSIBLE_COLLECTIONS_PATH": os.pathsep.join(
            (str(test_collection_path), str(AAP_ROOT / "vendor"), str(AAP_ROOT / "collections"))
        ),
        "ANSIBLE_ROLES_PATH": str(TEST_FIXTURES / "roles"),
        "ANSIBLE_LOCALHOST_WARNING": "False",
        "ANSIBLE_INVENTORY_UNPARSED_WARNING": "False",
        "ANSIBLE_NOCOLOR": "1",
        "ANSIBLE_PYTHON_INTERPRETER": sys.executable,
        "ANSIBLE_STDOUT_CALLBACK": "ansible.posix.json",
        "ANSIBLE_SHOW_CUSTOM_STATS": "true",
    }
    environ.update(env)
    for key in ("KUBECONFIG", "K8S_AUTH_KUBECONFIG", "K8S_AUTH_HOST", "K8S_AUTH_API_KEY"):
        environ.pop(key, None)

    return subprocess.run(
        [
            str(ANSIBLE_PLAYBOOK),
            "-i",
            "localhost,",
            "-c",
            "local",
            str(PLAYBOOK),
            "-e",
            f"@{extra_vars}",
        ],
        cwd=AAP_ROOT,
        env=environ,
        capture_output=True,
        text=True,
        timeout=60,
        check=False,
    )


def _custom_stats(proc: subprocess.CompletedProcess) -> dict:
    output = proc.stdout.strip()
    if not output:
        return {}
    return json.loads(output).get("global_custom_stats") or {}


def test_actual_playbook_binds_controller_ingress_inputs_without_cluster_access(tmp_path: Path) -> None:
    address = "192.0.2.81"
    fixture_token = secrets.token_urlsafe(32)
    proc = _run_endpoint_playbook(
        tmp_path,
        "configure_guest_ingress",
        {
            "cluster_order": {
                "metadata": {"name": "order-a", "namespace": "osac-system"},
                "status": {
                    "clusterReference": {
                        "namespace": "cluster-a",
                        "hostedClusterName": "hosted-order-a",
                    }
                },
            },
            "hosted_control_plane_namespace": "cluster-a-hcp",
            "ingress_allocated_address": address,
        },
        {
            "OSAC_TEST_HCP_NAMESPACE": "cluster-a-hcp",
            "OSAC_TEST_INGRESS_ADDRESS": address,
            "OSAC_TEST_KUBECONFIG_TOKEN": fixture_token,
        },
    )

    output = proc.stdout + proc.stderr
    if fixture_token in output:
        raise AssertionError("actual endpoint playbook output exposed the kubeconfig token")
    if proc.returncode != 0:
        safe_stdout = proc.stdout.replace(fixture_token, "[REDACTED]")
        safe_stderr = proc.stderr.replace(fixture_token, "[REDACTED]")
        raise AssertionError(
            f"actual endpoint playbook failed:\n{safe_stdout}\n{safe_stderr}"
        )


def test_actual_playbook_reports_missing_service_network_kubeconfig(tmp_path: Path) -> None:
    proc = _run_endpoint_playbook(
        tmp_path,
        "configure_guest_ingress",
        {
            "cluster_order": {
                "metadata": {"name": "order-a", "namespace": "osac-system"},
                "status": {
                    "clusterReference": {
                        "namespace": "cluster-a",
                        "hostedClusterName": "hosted-order-a",
                    }
                },
            },
            "hosted_control_plane_namespace": "cluster-a-hcp",
            "ingress_allocated_address": "192.0.2.81",
        },
        {
            "OSAC_TEST_HCP_NAMESPACE": "cluster-a-hcp",
            "OSAC_TEST_INGRESS_ADDRESS": "192.0.2.81",
            "OSAC_TEST_KUBECONFIG_MISSING": "1",
        },
    )

    output = proc.stdout + proc.stderr
    assert proc.returncode != 0, "playbook accepted a missing kubeconfig Secret"
    assert "Secret service-network-admin-kubeconfig was not found" in output
    assert "cluster-a-hcp" not in output
    assert _custom_stats(proc)["osac_error"]["retryable"] is True


def test_actual_playbook_binds_controller_api_dns_input_before_provider_access(tmp_path: Path) -> None:
    capture = tmp_path / "dns-input.json"
    address = "192.0.2.82"
    proc = _run_endpoint_playbook(
        tmp_path,
        "publish_public_dns_api",
        {
            "cluster_order": {
                "metadata": {"name": "order-a", "namespace": "osac-system"},
                "status": {
                    "clusterReference": {
                        "namespace": "cluster-a",
                        "hostedClusterName": "replacement-hosted-order-a",
                    }
                },
            },
            "hosted_cluster_name": "published-hosted-order-a",
            "external_ip": {"address": address},
            "hosted_cluster_base_domain": "example.test",
        },
        {
            "EXTERNAL_ACCESS_BASE_DOMAIN": "legacy.example.test",
            "EXTERNAL_ACCESS_SUPPORTED_BASE_DOMAINS": "example.test,legacy.example.test",
            "DNS_CLASS": "test_dns",
            "OSAC_TEST_DNS_CAPTURE": str(capture),
        },
    )

    assert proc.returncode != 0, "test DNS role should stop before provider access"
    assert capture.exists(), f"DNS role was not reached:\n{proc.stdout}\n{proc.stderr}"
    assert json.loads(capture.read_text()) == {
        "record": "api.published-hosted-order-a.example.test",
        "value": address,
    }
    assert _custom_stats(proc)["osac_error"]["retryable"] is True


def test_actual_playbook_rejects_unsupported_dns_publication_and_cleanup(
    tmp_path: Path,
) -> None:
    cluster_order = {
        "metadata": {"name": "order-a", "namespace": "osac-system"},
        "status": {"clusterReference": {"namespace": "cluster-a"}},
    }
    for task in (
        "publish_public_dns_api",
        "publish_public_dns_ingress",
        "remove_public_dns_api",
        "remove_public_dns_ingress",
    ):
        task_dir = tmp_path / task
        task_dir.mkdir()
        capture = task_dir / "dns-input.json"
        proc = _run_endpoint_playbook(
            task_dir,
            task,
            {"cluster_order": cluster_order, "hosted_cluster_base_domain": "example.test"},
            {
                "EXTERNAL_ACCESS_BASE_DOMAIN": "example.test",
                "EXTERNAL_ACCESS_SUPPORTED_BASE_DOMAINS": "other.test",
                "DNS_CLASS": "test_dns",
                "OSAC_TEST_DNS_CAPTURE": str(capture),
            },
        )

        if task.startswith("publish_"):
            assert proc.returncode != 0, f"{task} accepted an unsupported base domain"
            assert "EXTERNAL_ACCESS_SUPPORTED_BASE_DOMAINS" in proc.stdout + proc.stderr
            assert not capture.exists(), f"{task} reached the DNS provider role"
            error = _custom_stats(proc)["osac_error"]
            assert error["retryable"] is False
            assert "rejected before DNS writes" in error["message"]
        else:
            assert proc.returncode != 0, f"{task} accepted an unsupported base domain"
            assert "EXTERNAL_ACCESS_SUPPORTED_BASE_DOMAINS" in proc.stdout + proc.stderr
            assert not capture.exists(), f"{task} reached the DNS provider role"
            assert _custom_stats(proc)["osac_error"]["retryable"] is True
