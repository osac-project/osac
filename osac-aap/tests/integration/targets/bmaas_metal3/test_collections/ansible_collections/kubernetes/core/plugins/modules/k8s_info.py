#!/usr/bin/python

import json
import os

from ansible.module_utils.basic import AnsibleModule


def resource_key(api_version, kind, namespace, name):
    return "|".join([api_version or "", kind or "", namespace or "", name or ""])


def main():
    module = AnsibleModule(
        argument_spec={
            "api_version": {"type": "str", "required": True},
            "kind": {"type": "str", "required": True},
            "namespace": {"type": "str"},
            "name": {"type": "str"},
            "kubeconfig": {"type": "raw"},
        },
        supports_check_mode=True,
    )
    state_path = os.environ.get("OSAC_BM_HOST_PROVISIONING_STATE")
    if not state_path:
        module.fail_json(msg="OSAC_BM_HOST_PROVISIONING_STATE is required")

    with open(state_path, encoding="utf-8") as state_file:
        state = json.load(state_file)

    key = resource_key(
        module.params["api_version"],
        module.params["kind"],
        module.params.get("namespace"),
        module.params.get("name"),
    )
    resource = state.get("resources", {}).get(key)
    resources = [resource] if resource is not None else []
    module.exit_json(changed=False, resources=resources, api_found=True)


if __name__ == "__main__":
    main()
