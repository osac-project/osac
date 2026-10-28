#!/usr/bin/python
"""Reserve or release a UID-owned Netris IPAM /32 for an ExternalIP."""

from __future__ import annotations

from ansible.module_utils.basic import AnsibleModule

from ansible_collections.osac.templates.plugins.module_utils import (
    netris_external_ip as core,
)


DOCUMENTATION = r"""
---
module: netris_external_ip
short_description: Reserve or release a Netris ExternalIP /32 reservation
version_added: "1.0.0"
author:
  - OSAC project
description:
  - Allocates a UID-owned canonical IPv4 /32 nat reservation inside the pool CIDR.
  - Reuses the one valid UID reservation on allocation retry.
  - Releases the UID reservation and confirms its absence before success.
options:
  operation:
    description: ExternalIP operation to perform.
    type: str
    required: true
    choices:
      - external_ip.allocate
      - external_ip.release
  resource:
    description: Supplied ExternalIP snapshot.
    type: dict
    required: true
  pool:
    description: Supplied external_ip_pool snapshot.
    type: dict
    required: true
  controller_url:
    description: Netris controller URL.
    type: str
    required: true
  session_cookie:
    description: Netris authentication session cookie.
    type: str
    required: true
  site_id:
    description: Configured Netris provider site ID.
    type: int
    required: true
  tenant_id:
    description: Configured Netris provider tenant ID.
    type: int
    required: true
  timeout:
    description: Netris API request timeout in seconds.
    type: int
    default: 30
  validate_certs:
    description: Verify the Netris controller TLS certificate.
    type: bool
    default: true
  ca_path:
    description: Optional CA bundle path for TLS verification.
    type: path
    required: false
"""

EXAMPLES = r"""
- name: Allocate the ExternalIP reservation
  osac.templates.netris_external_ip:
    operation: external_ip.allocate
    resource: "{{ external_ip }}"
    pool: "{{ osac_job_vars.external_ip_pool }}"
    controller_url: "{{ netris_controller_url }}"
    session_cookie: "{{ netris_session_cookie }}"
    site_id: "{{ netris_site_id }}"
    tenant_id: "{{ netris_tenant_id }}"
"""

RETURN = r"""
changed:
  description: Whether the provider reservation changed.
  type: bool
  returned: always
address:
  description: The canonical IPv4 reservation address, empty for release.
  type: str
  returned: always
"""

ARGUMENT_SPEC = {
    "operation": {
        "type": "str",
        "required": True,
        "choices": list(core.OPERATIONS),
    },
    "resource": {"type": "dict", "required": True},
    "pool": {"type": "dict", "required": True},
    "controller_url": {"type": "str", "required": True, "no_log": True},
    "session_cookie": {"type": "str", "required": True, "no_log": True},
    "site_id": {"type": "int", "required": True},
    "tenant_id": {"type": "int", "required": True},
    "timeout": {"type": "int", "default": 30},
    "validate_certs": {"type": "bool", "default": True},
    "ca_path": {"type": "path", "required": False, "default": None},
}

# Provider inputs that must never appear in the returned invocation. The
# dict snapshots cannot use argument-spec no_log: ansible-core adds every
# leaf value of a no_log'd dict to the module censor set, which would
# also censor the returned reservation address whenever a leaf value
# collided with it.
_REDACTED_PROVIDER_ARGS = ("resource", "pool", "controller_url", "session_cookie")
_REDACTED = "VALUE_SPECIFIED_IN_NO_LOG_PARAMETER"


def _redacted_invocation(params: dict) -> dict:
    """Return an invocation whose provider inputs are redacted."""
    module_args = {
        key: (_REDACTED if key in _REDACTED_PROVIDER_ARGS else value)
        for key, value in params.items()
    }
    return {"module_args": module_args}


def main() -> None:
    """Run one ExternalIP v1 provider operation with sanitized failures."""
    module = AnsibleModule(
        argument_spec=ARGUMENT_SPEC,
        supports_check_mode=True,
        no_log=True,
    )
    params = module.params
    try:
        request = core.validate_request(
            params["resource"], params["pool"], params["operation"]
        )
        if module.check_mode:
            module.exit_json(
                changed=False, address="", invocation=_redacted_invocation(params)
            )
            return
        client = core.NetrisSubnetClient(
            module,
            params["controller_url"],
            params["session_cookie"],
            timeout=params["timeout"],
            validate_certs=params["validate_certs"],
            ca_path=params["ca_path"],
        )
        result = core.reconcile(request, client, params["site_id"], params["tenant_id"])
    except ValueError as error:
        category = getattr(error, "category", "invalid_request")
        status = getattr(error, "http_status", None)
        suffix = f", HTTP {status}" if status is not None else ""
        module.fail_json(
            msg=f"Netris ExternalIP {params['operation']} failed ({category}{suffix})",
            invocation=_redacted_invocation(params),
        )
        return
    module.exit_json(
        changed=result["changed"],
        address=result["address"],
        invocation=_redacted_invocation(params),
    )


if __name__ == "__main__":
    main()
