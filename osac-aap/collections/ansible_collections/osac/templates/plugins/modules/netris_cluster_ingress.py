#!/usr/bin/python
"""Reserve or release the Netris IPAM /32 for a ClusterOrder ingress VIP."""

from __future__ import annotations

from ansible.module_utils.basic import AnsibleModule

from ansible_collections.osac.templates.plugins.module_utils import (
    netris_cluster_ingress as core,
    netris_external_ip,
)


DOCUMENTATION = r"""
---
module: netris_cluster_ingress
short_description: Reserve or release a ClusterOrder ingress VIP
version_added: "1.0.0"
author:
  - OSAC project
description:
  - Allocates one IPv4 /32 from the Subnet's recorded lb_vip_ipv4_cidrs and
    stores it in the Netris VPC owned by the controller-supplied
    VirtualNetwork API ID.
  - Reuses the reservation named by the Cluster API ID on retry.
  - Releases only a reservation proven to match the ClusterOrder, site, tenant,
    and one of the Subnet's recorded LB VIP ranges. Release does not require the
    VPC to exist because Netris deletes the IPAM record by its provider ID.
options:
  operation:
    description: ClusterOrder ingress operation.
    type: str
    required: true
    choices:
      - cluster_ingress.allocate
      - cluster_ingress.release
  resource:
    description: ClusterOrder snapshot that owns the reservation.
    type: dict
    required: true
  cluster_id:
    description: Cluster API ID that owns the reservation.
    type: str
    required: true
  subnet:
    description: Subnet snapshot selected by the ClusterOrder.
    type: dict
    required: true
  virtual_network:
    description: VirtualNetwork snapshot that owns the Subnet's Netris VPC.
    type: dict
    required: true
  vpc_id:
    description:
      - Netris VPC ID resolved from the VirtualNetwork API ID.
      - Required for allocation; release can delete by IPAM record ID.
    type: int
    required: false
  ipam:
    description: Recorded Subnet IPAM layout.
    type: dict
    required: true
  reservation_name:
    description: Name derived from the Cluster API ID.
    type: str
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
    "operation": {"type": "str", "required": True, "choices": list(core.OPERATIONS)},
    "resource": {"type": "dict", "required": True},
    "cluster_id": {"type": "str", "required": True},
    "virtual_network": {"type": "dict", "required": True},
    "subnet": {"type": "dict", "required": True},
    "vpc_id": {"type": "int", "required": False, "default": None},
    "ipam": {"type": "dict", "required": True},
    "reservation_name": {"type": "str", "required": True},
    "controller_url": {"type": "str", "required": True, "no_log": True},
    "session_cookie": {"type": "str", "required": True, "no_log": True},
    "site_id": {"type": "int", "required": True},
    "tenant_id": {"type": "int", "required": True},
    "timeout": {"type": "int", "default": 30},
    "validate_certs": {"type": "bool", "default": True},
    "ca_path": {"type": "path", "required": False, "default": None},
}

_REDACTED_PROVIDER_ARGS = (
    "resource", "virtual_network", "subnet", "ipam", "cluster_id",
    "controller_url", "session_cookie"
)
_REDACTED = "VALUE_SPECIFIED_IN_NO_LOG_PARAMETER"


def _redacted_invocation(params: dict) -> dict:
    """Return invocation metadata without resource data or authentication."""
    return {
        "module_args": {
            key: (_REDACTED if key in _REDACTED_PROVIDER_ARGS else value)
            for key, value in params.items()
        }
    }


def main() -> None:
    """Run one idempotent Netris ingress reservation operation."""
    module = AnsibleModule(
        argument_spec=ARGUMENT_SPEC,
        supports_check_mode=True,
        no_log=True,
    )
    params = module.params
    try:
        request = core.validate_request(
            params["resource"], params["virtual_network"], params["subnet"],
            params["ipam"], params["operation"], params["reservation_name"],
            params["cluster_id"], params["vpc_id"],
        )
        if module.check_mode:
            module.exit_json(
                changed=False, address="", invocation=_redacted_invocation(params)
            )
            return
        client = netris_external_ip.NetrisSubnetClient(
            module,
            params["controller_url"],
            params["session_cookie"],
            timeout=params["timeout"],
            validate_certs=params["validate_certs"],
            ca_path=params["ca_path"],
        )
        result = core.reconcile(
            request, client, params["site_id"], params["tenant_id"]
        )
    except ValueError as error:
        category = getattr(error, "category", "invalid_request")
        status = getattr(error, "http_status", None)
        suffix = f", HTTP {status}" if status is not None else ""
        module.fail_json(
            msg=f"Netris ClusterOrder ingress operation failed ({category}{suffix})",
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
