#!/usr/bin/python
"""Reserve or release a CI guest ingress VIP for one ClusterOrder."""

from __future__ import annotations

import yaml
from ansible.module_utils.basic import AnsibleModule

from ansible_collections.osac.service.plugins.module_utils import (
    ingress_vip_claim as core,
)


DOCUMENTATION = r"""
---
module: ingress_vip_claim
short_description: Reserve or release a ClusterOrder guest ingress VIP
version_added: "1.0.0"
author:
  - OSAC project
description:
  - Allocates one address from the provider-reserved CI guest ingress range to a
    ClusterOrder and keeps the claim in a hub ConfigMap, keyed by ClusterOrder UID.
  - Writes carry the read resourceVersion, so concurrent ClusterOrders get a
    retryable conflict instead of overwriting each other's claim.
  - Retries of the same ClusterOrder return the same address. A claim is kept
    until it is explicitly released, so a failed install or cleanup never hands
    a still-advertised address to another cluster.
options:
  state:
    description: Whether the ClusterOrder should own an address.
    type: str
    required: true
    choices: [present, absent]
  ip_range:
    description:
      - Provider-reserved IPv4 range of the form C(first-last), for example
        C(10.10.0.240-10.10.0.243). Required for O(state=present).
    type: str
  owner_uid:
    description: UID of the ClusterOrder that owns the claim.
    type: str
    required: true
  owner_namespace:
    description: Namespace of the owning ClusterOrder.
    type: str
    required: true
  owner_name:
    description: Name of the owning ClusterOrder.
    type: str
    required: true
  registry_namespace:
    description: Namespace of the hub ConfigMap that holds the claims.
    type: str
    required: true
  registry_name:
    description: Name of the hub ConfigMap that holds the claims.
    type: str
    default: osac-ci-guest-ingress-vip-claims
  max_attempts:
    description: Attempts made before a conflicting registry write is reported.
    type: int
    default: 5
  retry_delay:
    description: Seconds to wait between attempts.
    type: float
    default: 1.0
  kubeconfig:
    description:
      - Kubeconfig of the hub, as an inline mapping or a file path.
      - Defaults to the in-cluster configuration, then the local kubeconfig.
    type: raw
"""

RETURN = r"""
---
address:
  description: Address reserved for the ClusterOrder; null when none is held.
  returned: always
  type: str
  sample: 10.10.0.240
changed:
  description: Whether the claim registry was modified.
  returned: always
  type: bool
registry:
  description: ConfigMap holding the claims, as C(namespace/name).
  returned: always
  type: str
"""

ARGUMENT_SPEC = {
    "state": {"type": "str", "required": True, "choices": ["present", "absent"]},
    "ip_range": {"type": "str"},
    "owner_uid": {"type": "str", "required": True},
    "owner_namespace": {"type": "str", "required": True},
    "owner_name": {"type": "str", "required": True},
    "registry_namespace": {"type": "str", "required": True},
    "registry_name": {"type": "str", "default": core.DEFAULT_REGISTRY_NAME},
    "max_attempts": {"type": "int", "default": core.MAX_ATTEMPTS},
    "retry_delay": {"type": "float", "default": core.RETRY_DELAY_SECONDS},
    "kubeconfig": {"type": "raw", "no_log": True},
}


class _PreviewStore:
    """Check-mode store: reads real state and records the intended write."""

    def __init__(self, store):
        self._store = store
        self.pending = None

    def read(self):
        return self._store.read()

    def write(self, data, resource_version):
        self.pending = data


def _api_client(kubeconfig):
    """Return a hub API client from the given kubeconfig or the environment."""
    from kubernetes import client, config

    if kubeconfig:
        if isinstance(kubeconfig, (dict, str)) and (
            isinstance(kubeconfig, dict)
            or kubeconfig.lstrip().startswith(("{", "apiVersion"))
        ):
            document = (
                kubeconfig if isinstance(kubeconfig, dict) else yaml.safe_load(kubeconfig)
            )
            return config.new_client_from_config_dict(document)
        return config.new_client_from_config(config_file=kubeconfig)

    try:
        config.load_incluster_config()
    except config.ConfigException:
        config.load_kube_config()
    return client.ApiClient()


def main() -> None:
    """Run one claim or release against the hub registry."""
    from kubernetes import client

    module = AnsibleModule(
        argument_spec=ARGUMENT_SPEC,
        supports_check_mode=True,
        required_if=[("state", "present", ["ip_range"])],
    )
    params = module.params
    owner = {
        "uid": params["owner_uid"],
        "namespace": params["owner_namespace"],
        "name": params["owner_name"],
    }
    registry = f"{params['registry_namespace']}/{params['registry_name']}"

    try:
        api_client = _api_client(params["kubeconfig"])
        store = core.ConfigMapClaimStore(
            client.CoreV1Api(api_client), params["registry_namespace"], params["registry_name"]
        )
        if module.check_mode:
            store = _PreviewStore(store)

        if params["state"] == "present":
            address, changed = core.claim_address(
                params["ip_range"],
                owner,
                store,
                max_attempts=params["max_attempts"],
                retry_delay=params["retry_delay"],
            )
        else:
            address, changed = core.release_address(
                owner,
                store,
                max_attempts=params["max_attempts"],
                retry_delay=params["retry_delay"],
            )
    except core.ClaimError as error:
        module.fail_json(
            msg=f"guest ingress VIP {params['state']} failed ({error.category}): {error}",
            category=error.category,
            registry=registry,
        )
        return

    module.exit_json(changed=changed, address=address, registry=registry)


if __name__ == "__main__":
    main()
