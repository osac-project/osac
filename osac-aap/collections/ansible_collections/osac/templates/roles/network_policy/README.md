# network_policy

Implements SecurityGroup enforcement via standard Kubernetes NetworkPolicy. This is a standalone backend reusable across any K8s-based NetworkClass (CUDN, Netris, etc.).

## Resources

### SecurityGroup

SecurityGroups translate to Kubernetes NetworkPolicy resources that enforce network traffic rules on pods within Subnet namespaces.

**Key behaviors:**
- One NetworkPolicy per SecurityGroup (named `sg-{security-group-name}`)
- NetworkPolicies are deployed to all Subnet namespaces associated with the SecurityGroup's parent VirtualNetwork
- Pod selection uses label `osac.openshift.io/security-group: {sg-name}`
- Multiple SecurityGroups are additive: pods can have multiple SG labels, and traffic is allowed if ANY NetworkPolicy allows it
- Empty ingress/egress rule arrays result in deny-all for that direction

**Rule translation:**
- Protocol "all" → omit protocol field in NetworkPolicy (allows all protocols). Port fields are not applicable and ignored by the API.
- Protocol "tcp" or "udp" → include protocol field with port/endPort. Port fields (`portFrom`, `portTo`) are required.
- Protocol "icmp" → port fields are not applicable and ignored by the API. **Warning:** Standard Kubernetes NetworkPolicy does not support ICMP protocol filtering. ICMP rules with a source/destination CIDR will allow **all protocols** from that CIDR, not just ICMP. Use protocol "all" explicitly if this is the intended behavior.
- Port ranges: if portFrom == portTo, use single port; if different, use port + endPort
- Source CIDR → ingress.from.ipBlock.cidr
- Destination CIDR → egress.to.ipBlock.cidr

**Namespace targeting:**
- SecurityGroups apply to namespaces labeled with `osac.openshift.io/virtual-network: {vn-name}`
- If no matching namespaces exist when SecurityGroup is created, the task succeeds with a warning
- When new Subnets are created, the osac-operator re-triggers SecurityGroup reconciliation to apply policies

**Multi-SecurityGroup behavior:**
Each SecurityGroup creates its own NetworkPolicy with `podSelector.matchLabels` on `osac.openshift.io/security-group: {sg-name}`. A pod is selected by a single SecurityGroup via this label. Kubernetes applies NetworkPolicies additively — traffic is allowed if ANY matching policy allows it.

For example, a pod labeled with `web-sg`:

```yaml
metadata:
  labels:
    osac.openshift.io/security-group: web-sg
```

will be selected by the `sg-web-sg` NetworkPolicy. To apply a different SecurityGroup, change the label value to the desired SecurityGroup name.

**Known Limitations:**
- ICMP protocol filtering is not supported by standard Kubernetes NetworkPolicy (only TCP, UDP, SCTP are supported). ICMP rules are translated to NetworkPolicy rules without protocol specification, which effectively allows all traffic from the specified CIDR rather than ICMP-only.

## Task Files

- `tasks/create_security_group.yaml` - Creates NetworkPolicy resources from SecurityGroup rules
- `tasks/delete_security_group.yaml` - Removes NetworkPolicy resources

## Usage

```yaml
- name: Create SecurityGroup
  ansible.builtin.include_role:
    name: osac.templates.network_policy
    tasks_from: create_security_group
  vars:
    security_group: "{{ ansible_eda.event.payload }}"
```

```yaml
- name: Delete SecurityGroup
  ansible.builtin.include_role:
    name: osac.templates.network_policy
    tasks_from: delete_security_group
  vars:
    security_group: "{{ ansible_eda.event.payload }}"
```
