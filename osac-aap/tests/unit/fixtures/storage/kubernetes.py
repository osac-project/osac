"""Recording Kubernetes boundary with an immutable StorageClass API contract."""

import base64
import json
import os
from pathlib import Path

from ansible.module_utils.basic import AnsibleModule


module = AnsibleModule(argument_spec={
    "kubeconfig": {"type": "str"}, "context": {"type": "str"},
    "host": {"type": "str"}, "validate_certs": {"type": "bool", "default": True},
    "api_version": {"type": "str"}, "kind": {"type": "str"},
    "name": {"type": "str"}, "namespace": {"type": "str"},
    "label_selectors": {"type": "list", "elements": "str"},
    "state": {"type": "str"}, "definition": {"type": "dict"},
    "merge_type": {"type": "raw"}, "wait": {"type": "bool"},
    "wait_condition": {"type": "dict"}, "wait_timeout": {"type": "int"},
    "apply": {"type": "bool"}, "server_side_apply": {"type": "dict"},
})
params = module.params
store_path = Path(os.environ["OSAC_STORAGE_STORE"])
objects = json.loads(store_path.read_text())
definition = params["definition"] or {}
kind = definition.get("kind", params["kind"])
name = definition.get("metadata", {}).get("name", params["name"])
resources = [entry for entry in objects if entry.get("kind") == kind]
if name:
    resources = [entry for entry in resources if entry["metadata"]["name"] == name]
if kind == "CSIDriver":
    resources = [{"metadata": {"name": name}}]
elif kind == "CustomResourceDefinition" and name == "tridentbackendconfigs.trident.netapp.io":
    resources = [{"metadata": {"name": name}}]
elif kind == "Secret" and params["state"] is None and not resources:
    values = {
        "vast_tenant_id": "1", "tenant_manager_username": "fixture-manager",
        "tenant_manager_password": "fixture-only", "vip_pool_name": "fixture-pool",
        "vast_endpoint": "fixture.invalid", "tenant_uid_hash": "12345678",
    }
    resources = [{
        "kind": "Secret",
        "metadata": {"name": name or "fixture-secret", "labels": {
            "osac.openshift.io/tenant": "fstype-test",
            "osac.openshift.io/storage-provider": "vast",
            "osac.openshift.io/storage-backend-id": "fixture",
        }},
        "data": {key: base64.b64encode(value.encode()).decode() for key, value in values.items()},
    }]
if params["state"] == "present" and kind == "StorageClass" and resources:
    if definition.get("parameters", {}) != resources[0].get("parameters", {}):
        module.fail_json(msg="StorageClass.parameters is immutable")
if params["state"] in ("present", "patched") and definition and name:
    objects = [entry for entry in objects if (entry.get("kind"), entry["metadata"]["name"]) != (kind, name)]
    if kind == "TridentBackendConfig":
        definition["metadata"]["resourceVersion"] = "1"
        definition["status"] = {"phase": "Bound", "lastOperationStatus": "Success", "backendInfo": {"backendName": name}}
    if kind == "Secret" and "stringData" in definition:
        definition["data"] = {key: base64.b64encode(value.encode()).decode() for key, value in definition["stringData"].items()}
    objects.append(definition)
    if kind == "StorageClass":
        deleted_name = json.loads(os.environ.get("OSAC_STORAGE_DELETE_ON_CREATE", "{}")).get(name)
        objects = [entry for entry in objects
                   if (entry.get("kind"), entry["metadata"]["name"]) != ("StorageClass", deleted_name)]
elif params["state"] == "absent":
    objects = [entry for entry in objects if (entry.get("kind"), entry["metadata"]["name"]) != (kind, name)]
store_path.write_text(json.dumps(objects))
with open(os.environ["OSAC_STORAGE_CAPTURE"], "a") as capture:
    capture.write(json.dumps({"module": Path(__file__).stem, "resources": resources,
                             "objects": objects, **params}) + "\n")
module.exit_json(changed=False, resources=resources, result=definition)
