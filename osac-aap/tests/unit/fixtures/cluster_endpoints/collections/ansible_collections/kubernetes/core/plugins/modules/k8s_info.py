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
                "clusters": [{"name": "test", "cluster": {"server": "https://127.0.0.1"}}],
                "contexts": [{"name": "test", "context": {"cluster": "test", "user": "test"}}],
                "current-context": "test",
                "users": [{"name": "fixture", "user": {"token": fixture_token}}],
            }
        ).encode()
        resources = [{"data": {"kubeconfig": base64.b64encode(kubeconfig).decode()}}]
    elif kind == "Pod":
        resources = [{"status": {"phase": "Running"}}]
    elif kind == "Service":
        address = os.environ["OSAC_TEST_INGRESS_ADDRESS"]
        resources = [{"status": {"loadBalancer": {"ingress": [{"ip": address}]}}}]
    else:
        module.fail_json(msg=f"unexpected test Kubernetes read for kind {kind!r}")

    module.exit_json(changed=False, resources=resources)


if __name__ == "__main__":
    main()
