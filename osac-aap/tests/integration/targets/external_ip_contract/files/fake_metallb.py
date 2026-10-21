"""Assign a deterministic IPv4 address to test ExternalIP Services in Kind."""

import os
import time

from kubernetes import client, config
from kubernetes.client.exceptions import ApiException


def main():
    config.load_kube_config(config_file=os.environ["KUBECONFIG"])
    core = client.CoreV1Api()
    custom = client.CustomObjectsApi()
    assigned_ip = "198.51.100.6"
    status_namespace = os.environ["OSAC_EIP_STATUS_MUTATION_NAMESPACE"]
    status_name = os.environ["OSAC_EIP_STATUS_MUTATION_NAME"]
    status_update_applied = False
    while True:
        try:
            external_ip = custom.get_namespaced_custom_object(
                "osac.openshift.io",
                "v1alpha1",
                status_namespace,
                "externalips",
                status_name,
            )
        except ApiException as error:
            if error.status != 404:
                raise
            time.sleep(0.2)
            continue
        external_ip_uid = external_ip["metadata"]["uid"]
        for service in core.list_service_for_all_namespaces(
            label_selector=(
                "osac.openshift.io/managed-by=osac-fulfillment,"
                f"osac.openshift.io/externalip-uid={external_ip_uid}"
            )
        ).items:
            if service.spec.type != "LoadBalancer":
                continue
            ingress = []
            if service.status and service.status.load_balancer:
                ingress = service.status.load_balancer.ingress or []
            if ingress:
                continue
            if not status_update_applied:
                custom.patch_namespaced_custom_object_status(
                    "osac.openshift.io",
                    "v1alpha1",
                    status_namespace,
                    "externalips",
                    status_name,
                    {"status": {"phase": "Progressing"}},
                )
                status_update_applied = True
            core.patch_namespaced_service_status(
                service.metadata.name,
                service.metadata.namespace,
                {"status": {"loadBalancer": {"ingress": [{"ip": assigned_ip}]}}},
            )
        time.sleep(0.2)


if __name__ == "__main__":
    main()
