#!/usr/bin/python

import copy
import json
import os

from ansible.module_utils.basic import AnsibleModule


def resource_key(api_version, kind, namespace, name):
    return "|".join([api_version or "", kind or "", namespace or "", name or ""])


def merge(target, patch):
    for key, value in patch.items():
        if isinstance(value, dict) and isinstance(target.get(key), dict):
            merge(target[key], value)
        else:
            target[key] = copy.deepcopy(value)


def main():
    module = AnsibleModule(
        argument_spec={
            "api_version": {"type": "str"},
            "kind": {"type": "str"},
            "namespace": {"type": "str"},
            "name": {"type": "str"},
            "state": {"type": "str", "default": "present"},
            "definition": {"type": "dict"},
            "kubeconfig": {"type": "raw"},
        },
        supports_check_mode=True,
    )
    state_path = os.environ.get("OSAC_BM_HOST_PROVISIONING_STATE")
    if not state_path:
        module.fail_json(msg="OSAC_BM_HOST_PROVISIONING_STATE is required")

    with open(state_path, encoding="utf-8") as state_file:
        state = json.load(state_file)

    definition = copy.deepcopy(module.params.get("definition") or {})
    metadata = definition.setdefault("metadata", {})
    api_version = module.params.get("api_version") or definition.get("apiVersion")
    kind = module.params.get("kind") or definition.get("kind")
    namespace = module.params.get("namespace") or metadata.get("namespace")
    name = module.params.get("name") or metadata.get("name")
    definition.setdefault("apiVersion", api_version)
    definition.setdefault("kind", kind)
    if namespace:
        metadata.setdefault("namespace", namespace)
    if name:
        metadata.setdefault("name", name)

    operation = {
        "module": "k8s",
        "state": module.params["state"],
        "apiVersion": api_version,
        "kind": kind,
        "namespace": namespace,
        "name": name,
        "definition": definition,
    }
    state.setdefault("operations", []).append(operation)
    resources = state.setdefault("resources", {})
    key = resource_key(api_version, kind, namespace, name)

    if module.params["state"] == "absent":
        resources.pop(key, None)
        result = {}
    else:
        result = copy.deepcopy(resources.get(key, {}))
        merge(result, definition)
        if kind == "BareMetalHost":
            image = definition.get("spec", {}).get("image", "unchanged")
            if image != "unchanged":
                provisioning = result.setdefault("status", {}).setdefault("provisioning", {})
                if image is None:
                    provisioning["state"] = "available"
                    provisioning.pop("image", None)
                else:
                    provisioning["state"] = "provisioned"
                    provisioning["image"] = copy.deepcopy(image)
        resources[key] = result

    with open(state_path, "w", encoding="utf-8") as state_file:
        json.dump(state, state_file)
    module.exit_json(changed=True, result=result)


if __name__ == "__main__":
    main()
