from __future__ import annotations

from tests.e2e.core.k8s_client import K8sClient


def job_pod_mounting(k8s: K8sClient, configmap: str, job_id: str) -> dict | None:
    for pod in k8s.list_json(resource="pods", namespace=k8s.namespace)["items"]:
        labels = pod.get("metadata", {}).get("labels", {})
        if labels.get("ansible-awx-job-id") != job_id:
            continue
        for volume in pod.get("spec", {}).get("volumes", []):
            if volume.get("configMap", {}).get("name") == configmap:
                return pod
    return None
