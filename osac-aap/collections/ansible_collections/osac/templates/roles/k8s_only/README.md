# k8s_only

Composite dispatch role for a k8s-only NetworkClass — one that has a `k8sManager`
but no `fabricManager` (no separate physical fabric to bridge into).

## Why this role exists

The osac-operator dispatcher resolves every networking resource kind (VirtualNetwork,
Subnet, SecurityGroup, ExternalIPPool, ExternalIP, ExternalIPAttachment) to a single
manager name per NetworkClass, then triggers the matching AAP job template, which in
turn calls `osac.templates.{{ implementation_strategy }}` for that one role. This means
whichever role a k8sManager resolves to must expose an entrypoint for every resource
kind it needs to handle — even when the actual implementation already exists elsewhere.

`cudn_net` already does this for its own SecurityGroup entrypoints, delegating to the
standalone `network_policy` role rather than re-implementing NetworkPolicy handling
itself. `k8s_only` generalizes that same idea across every resource kind, so a
k8s-only deployment doesn't need its own from-scratch implementation of anything —
it composes the existing, independently-tested k8s-native roles:

- **VirtualNetwork / Subnet** → `cudn_net` (ClusterUserDefinedNetwork)
- **SecurityGroup** → `network_policy` (Kubernetes NetworkPolicy)
- **ExternalIPPool / ExternalIP / ExternalIPAttachment** → `metallb_l2` (MetalLB L2 advertisement)

## Task Files

Every task file is a single `ansible.builtin.include_role` delegation — no logic of
its own. Vars set by the calling playbook (`virtual_network`, `subnet`,
`security_group`, `external_ip_pool`, `external_ip`, `external_ip_attachment`, and
their `*_name` companions) flow through automatically since `include_role` inherits
the calling scope.

| Task file | Delegates to |
|---|---|
| `create_virtual_network.yaml` / `delete_virtual_network.yaml` | `osac.templates.cudn_net` |
| `create_subnet.yaml` / `delete_subnet.yaml` | `osac.templates.cudn_net` |
| `create_security_group.yaml` / `delete_security_group.yaml` | `osac.templates.network_policy` |
| `create_external_ip_pool.yaml` / `delete_external_ip_pool.yaml` | `osac.templates.metallb_l2` |
| `create_external_ip.yaml` / `delete_external_ip.yaml` | `osac.templates.metallb_l2` |
| `attach_external_ip.yaml` / `detach_external_ip.yaml` | `osac.templates.metallb_l2` |

## Not yet implemented

**NATGateway** (`create_nat_gateway`/`delete_nat_gateway`) is not implemented by this
role yet — it will be added by a follow-up ticket (OSAC-4265) once NATGateway support
via an OVN EgressIP CR is validated.

## Registration

This role deliberately has no `meta/osac.yaml` — it is not independently selectable
or auto-published as its own NetworkClass the way `cudn_net`/`network_policy`/
`metallb_l2` are. Registering it as the `k8s-only` k8sManager (creating the
NetworkClass and the labeled ConfigMap the osac-operator dispatcher reads to resolve
`implementation_strategy: k8s_only`) happens outside this role — in E2E test
environments and in production installs.
