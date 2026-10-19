from pathlib import Path

import yaml


PLAYBOOK = Path(__file__).resolve().parents[2] / "playbook_osac_move_network_attachment.yml"


def test_move_playbook_resolves_subnets_with_the_worker_service_account():
    play = yaml.safe_load(PLAYBOOK.read_text(encoding="utf-8"))[0]
    lookup_task = next(
        task
        for task in play["tasks"]
        if task.get("name") == "Resolve tenant network segment name for each attachment"
    )
    lookup = lookup_task["kubernetes.core.k8s_info"]

    assert lookup["api_version"] == "osac.openshift.io/v1alpha1"
    assert lookup["kind"] == "Subnet"
    assert lookup["namespace"] == "{{ resource.metadata.namespace }}"
    assert lookup["label_selectors"] == [
        "osac.openshift.io/subnet-uuid={{ item.subnetRef }}"
    ]
    assert lookup["host"] == "https://kubernetes.default.svc"
    assert "/var/run/secrets/kubernetes.io/serviceaccount/token" in lookup["api_key"]
    assert lookup["ca_cert"] == "/var/run/secrets/kubernetes.io/serviceaccount/ca.crt"
    assert lookup["validate_certs"] is True
    assert lookup_task["no_log"] is True
