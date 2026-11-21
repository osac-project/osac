"""Deterministic VMS module boundary for real provider task execution."""

from ansible.module_utils.basic import AnsibleModule


module = AnsibleModule(argument_spec={
    "vms": {"type": "dict"}, "name": {"type": "str"},
    "flavor": {"type": "str"}, "tenant_id": {"type": "str"},
    "state": {"type": "str"}, "path": {"type": "str"},
    "create_dir": {"type": "bool"}, "policy_id": {"type": "str"},
    "protocols": {"type": "list", "elements": "str"},
    "force_remove": {"type": "bool"},
})
module.exit_json(changed=False, viewpolicies={"id": 1})
