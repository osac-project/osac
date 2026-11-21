"""Record the Helm boundary without installing a guest CSI driver."""

import json
import os

from ansible.module_utils.basic import AnsibleModule


module = AnsibleModule(argument_spec={
    "kubeconfig": {"type": "str"}, "validate_certs": {"type": "bool"},
    "release_name": {"type": "str"}, "chart_ref": {"type": "str"},
    "chart_version": {"type": "str"}, "release_namespace": {"type": "str"},
    "create_namespace": {"type": "bool"}, "wait": {"type": "bool"},
    "wait_timeout": {"type": "str"}, "values": {"type": "dict"},
    "state": {"type": "str"},
})
with open(os.environ["OSAC_STORAGE_CAPTURE"], "a") as capture:
    capture.write(json.dumps({"module": "helm", **module.params}) + "\n")
module.exit_json(changed=False)
