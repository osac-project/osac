# Networking Implementation: cudn_net

The `cudn_net` role implements networking via OpenShift's ClusterUserDefinedNetwork (CUDN).

## Architecture

```text
VirtualNetwork (logical grouping)
  └── Subnet (Namespace + CUDN + IPAM)
        └── SecurityGroup (NetworkPolicy)
```

**VirtualNetwork:** Logical grouping only (no K8s resources created). Stores CIDR blocks and implementation strategy.

**Subnet:** Creates **Namespace** with labels for OVN primary UDN, plus **ClusterUserDefinedNetwork** with namespaceSelector. Provisions Layer-2 network with persistent IPAM.

**SecurityGroup:** Creates **NetworkPolicy** in target namespace. Translates ingress/egress rules to K8s NetworkPolicy. Supports TCP, UDP, ICMP, port ranges, CIDR sources.

## Key Files

- `collections/ansible_collections/osac/templates/roles/cudn_net/tasks/create_subnet.yaml` — Creates Namespace with `k8s.ovn.org/primary-user-defined-network: ""` label, creates CUDN
- `collections/ansible_collections/osac/templates/roles/cudn_net/tasks/create_security_group.yaml` — Translates SecurityGroup CR to NetworkPolicy

## Namespace Label Syntax (Critical)

Label value must be empty string, not missing:

```yaml
# Correct:
labels:
  k8s.ovn.org/primary-user-defined-network: ""

# Incorrect (will NOT work):
labels:
  k8s.ovn.org/primary-user-defined-network:
```

## Conditional CIDR Handling

```yaml
- name: Build CUDN subnets array
  ansible.builtin.set_fact:
    cudn_subnets: >-
      {{
        ((subnet_ipv4_cidr | length > 0) | ternary([subnet_ipv4_cidr], []))
        + ((subnet_ipv6_cidr | length > 0) | ternary([subnet_ipv6_cidr], []))
      }}
```
