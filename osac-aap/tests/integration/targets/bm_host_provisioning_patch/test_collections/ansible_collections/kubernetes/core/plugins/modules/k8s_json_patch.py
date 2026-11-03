#!/usr/bin/python

import copy
import json
import os

from ansible.module_utils.basic import AnsibleModule


def resource_key(api_version, kind, namespace, name):
    return "|".join([api_version or "", kind or "", namespace or "", name or ""])


def unescape(token):
    return token.replace("~1", "/").replace("~0", "~")


def resolve_parent(resource, pointer):
    tokens = [unescape(t) for t in pointer.strip("/").split("/") if t != ""]
    node = resource
    for token in tokens[:-1]:
        if isinstance(node, list):
            node = node[int(token)]
        else:
            node = node[token]
    return node, tokens[-1] if tokens else None


def apply_patch(resource, ops):
    for op in ops:
        parent, key = resolve_parent(resource, op["path"])
        if op["op"] == "test":
            expected = op.get("value")
            actual = parent.get(key) if isinstance(parent, dict) else parent[int(key)]
            if actual != expected:
                raise ValueError("test op failed at %s" % op["path"])
        elif op["op"] == "remove":
            if isinstance(parent, dict):
                del parent[key]
            else:
                del parent[int(key)]
        else:
            raise ValueError("unsupported op %s" % op["op"])


def main():
    module = AnsibleModule(
        argument_spec={
            "api_version": {"type": "str"},
            "kind": {"type": "str"},
            "namespace": {"type": "str"},
            "name": {"type": "str"},
            "patch": {"type": "list", "elements": "dict"},
            "kubeconfig": {"type": "raw"},
        },
        supports_check_mode=True,
    )
    state_path = os.environ.get("OSAC_BM_HOST_PROVISIONING_STATE")
    if not state_path:
        module.fail_json(msg="OSAC_BM_HOST_PROVISIONING_STATE is required")

    with open(state_path, encoding="utf-8") as state_file:
        state = json.load(state_file)

    api_version = module.params.get("api_version")
    kind = module.params.get("kind")
    namespace = module.params.get("namespace")
    name = module.params.get("name")
    patch = module.params.get("patch") or []

    operation = {
        "module": "k8s_json_patch",
        "apiVersion": api_version,
        "kind": kind,
        "namespace": namespace,
        "name": name,
        "patch": copy.deepcopy(patch),
    }
    state.setdefault("operations", []).append(operation)

    key = resource_key(api_version, kind, namespace, name)
    resource = state.get("resources", {}).get(key)
    if resource is None:
        module.fail_json(msg="%s %s/%s not found in test state" % (kind, namespace, name))

    try:
        apply_patch(resource, patch)
    except (KeyError, IndexError, ValueError) as exc:
        module.fail_json(msg="json patch failed: %s" % exc)

    with open(state_path, "w", encoding="utf-8") as state_file:
        json.dump(state, state_file)
    module.exit_json(changed=True, result=copy.deepcopy(resource))


if __name__ == "__main__":
    main()
