from pathlib import Path

import yaml


PLAYBOOK = Path(__file__).resolve().parents[2] / "playbook_osac_move_network_attachment.yml"


def test_move_playbook_resolves_subnets_with_controller_credentials():
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
    assert "host" not in lookup
    assert "api_key" not in lookup
    assert "ca_cert" not in lookup
    assert lookup_task["no_log"] is True
