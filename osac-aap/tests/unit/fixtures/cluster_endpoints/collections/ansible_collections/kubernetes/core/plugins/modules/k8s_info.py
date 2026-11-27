#!/usr/bin/python
"""Test double for Kubernetes reads in the ClusterOrder endpoint playbook."""

import base64
import json
import os

from ansible.module_utils.basic import AnsibleModule


def main() -> None:
    module = AnsibleModule(
        argument_spec={
            "api_version": {"type": "str"},
            "kind": {"type": "str", "required": True},
            "namespace": {"type": "str"},
            "name": {"type": "str"},
            "label_selectors": {"type": "list", "elements": "str"},
            "kubeconfig": {"type": "raw"},
        },
        supports_check_mode=True,
    )
    params = module.params
    kind = params["kind"]

    def validate_api_server() -> None:
        expected_server = os.environ.get("OSAC_TEST_EXPECTED_SERVER")
        kubeconfig = params.get("kubeconfig") or {}
        clusters = kubeconfig.get("clusters", []) if isinstance(kubeconfig, dict) else []
        server = clusters[0].get("cluster", {}).get("server") if clusters else None
        if expected_server and server != expected_server:
            module.fail_json(msg="service-network kubeconfig did not use the expected API Service FQDN")

    if kind == "Secret":
        expected_namespace = os.environ["OSAC_TEST_HCP_NAMESPACE"]
        if params["namespace"] != expected_namespace:
            module.fail_json(msg="HostedControlPlane namespace was not bound from osac_job_vars")
        if os.environ.get("OSAC_TEST_KUBECONFIG_MISSING") == "1":
            module.exit_json(changed=False, resources=[])
        fixture_token = os.environ["OSAC_TEST_KUBECONFIG_TOKEN"]
        kubeconfig = json.dumps(
            {
                "apiVersion": "v1",
                "kind": "Config",
                "clusters": [{"name": "test", "cluster": {"server": "https://kube-apiserver:6443"}}],
                "contexts": [{"name": "test", "context": {"cluster": "test", "user": "test"}}],
                "current-context": "test",
                "users": [{"name": "fixture", "user": {"token": fixture_token}}],
            }
        ).encode()
        resources = [{"data": {"kubeconfig": base64.b64encode(kubeconfig).decode()}}]
    elif kind == "Pod":
        validate_api_server()
        resources = [{
            "metadata": {},
            "status": {
                "phase": "Running",
                "conditions": [{"type": "Ready", "status": "True"}],
            },
        }]
    elif kind == "Service":
        validate_api_server()
        address = os.environ["OSAC_TEST_INGRESS_ADDRESS"]
        resources = [{"status": {"loadBalancer": {"ingress": [{"ip": address}]}}}]
    else:
        module.fail_json(msg=f"unexpected test Kubernetes read for kind {kind!r}")

    module.exit_json(changed=False, resources=resources)


if __name__ == "__main__":
    main()
