#!/usr/bin/python
"""Create a direct NetApp StorageClass without patching existing objects."""

from ansible.module_utils.basic import AnsibleModule, missing_required_lib
from ansible_collections.kubernetes.core.plugins.module_utils.k8s.client import get_api_client

try:
    from kubernetes.dynamic.exceptions import ConflictError
except ImportError:
    HAS_KUBERNETES = False
else:
    HAS_KUBERNETES = True


DOCUMENTATION = r"""
---
module: netapp_storage_class_create
short_description: Create a direct NetApp StorageClass with a Kubernetes POST
version_added: "1.0.0"
author:
  - OSAC project
description:
  - Creates a StorageClass on the target cluster using the kubernetes.core client.
  - Reports a name collision without modifying the existing object.
options:
  definition:
    description: StorageClass manifest to create.
    type: dict
    required: true
  kubeconfig:
    description: Target Kubernetes configuration path or dictionary.
    type: raw
  validate_certs:
    description: Verify the target Kubernetes API server certificate.
    type: bool
"""

RETURN = r"""
already_exists:
  description: Whether another writer created the StorageClass before this request.
  type: bool
  returned: when a create request completes
"""


def main():
    module = AnsibleModule(
        argument_spec=dict(
            definition=dict(type="dict", required=True, no_log=True),
            kubeconfig=dict(type="raw", no_log=True),
            validate_certs=dict(type="bool"),
        ),
        supports_check_mode=True,
    )
    if not HAS_KUBERNETES:
        module.fail_json(msg=missing_required_lib("kubernetes"))
    if module.check_mode:
        module.exit_json(changed=True)

    try:
        client = get_api_client(
            kubeconfig=module.params["kubeconfig"],
            validate_certs=module.params["validate_certs"],
        )
        storage_class = client.resource("StorageClass", "storage.k8s.io/v1")
        client.create(storage_class, module.params["definition"])
    except ConflictError:
        module.exit_json(changed=False, already_exists=True)
    except Exception:
        module.fail_json(msg="Failed to create the NetApp StorageClass on the target cluster.")

    module.exit_json(changed=True, already_exists=False)


if __name__ == "__main__":
    main()
