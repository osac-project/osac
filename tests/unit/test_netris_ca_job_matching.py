from types import SimpleNamespace

from tests.e2e.core.aap_job_pods import job_pod_mounting


def test_job_pod_mounting_matches_awx_job_id_label():
    pod = {
        "metadata": {
            "name": "automation-job-42",
            "labels": {"ansible-awx": "install-uuid", "ansible-awx-job-id": "42", "ansible_job": ""},
        },
        "spec": {"volumes": [{"name": "netris-ca-bundle", "configMap": {"name": "customer-netris-ca"}}]},
    }
    k8s = SimpleNamespace(namespace="osac", list_json=lambda **_: {"items": [pod]})

    assert job_pod_mounting(k8s, "customer-netris-ca", "42") is pod
    assert job_pod_mounting(k8s, "customer-netris-ca", "43") is None
    assert job_pod_mounting(k8s, "other-configmap", "42") is None
