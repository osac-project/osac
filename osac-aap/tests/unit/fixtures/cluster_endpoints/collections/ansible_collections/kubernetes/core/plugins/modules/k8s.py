#!/usr/bin/python
"""Test double for Kubernetes writes in the ClusterOrder endpoint playbook."""

import os

from ansible.module_utils.basic import AnsibleModule


def main() -> None:
    module = AnsibleModule(
        argument_spec={
            "state": {"type": "str", "required": True},
            "definition": {"type": "dict", "required": True},
            "kubeconfig": {"type": "raw"},
        },
        supports_check_mode=True,
    )
    definition = module.params["definition"]
    if definition.get("kind") == "IPAddressPool":
        expected_address = os.environ["OSAC_TEST_INGRESS_ADDRESS"]
        addresses = definition.get("spec", {}).get("addresses", [])
        if addresses != [f"{expected_address}-{expected_address}"]:
            module.fail_json(msg="ingress address was not bound from osac_job_vars")
    module.exit_json(changed=False, result=definition)


if __name__ == "__main__":
    main()
