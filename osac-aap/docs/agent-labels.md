# CaaS Agent binding labels

CaaS worker Agents are discovered on demand when BMaaS provisions the worker
BareMetalInstance. The OSAC bare-metal worker controller correlates each Agent
with its tenant-owned BMI using the host MAC address, then approves and labels
the Agent for its ClusterOrder and NodePool. There is no AAP-maintained pool of
pre-booted Agents or periodic BMH/BCM Agent importer.

| Label | Owner | Purpose |
|---|---|---|
| `osac.openshift.io/instance_type` | Bare-metal worker controller | Selected BMIT name, also recorded on NodePool metadata. |
| `osac.openshift.io/clusterorder` | Bare-metal worker controller | Correlates the Agent to its ClusterOrder. |
| `osac.openshift.io/node-set` | Bare-metal worker controller | Selects the Agent for its logical NodeSet's NodePool. |
| `osac.openshift.io/worker-name` | Bare-metal worker controller | Prevents a worker Agent from being bound twice. |

NodePool Agent selectors match `clusterorder` plus `node-set`, not
`instance_type`. Distinct NodeSets may select the same BMIT and must still
have isolated Agent pools and independent replica counts.

Do not pre-label CaaS worker Agents with a ClusterOrder or manually approve
them: the controller must first verify the authoritative tenant, BMI ownership,
and MAC correlation. BMaaS inventory and BMIT setup are separate prerequisites;
see the [Metal3 backend](../../docs/guides/admin/metal3-backend.md) or
[BCM backend](../../docs/guides/admin/bcm-backend.md) guides for registering
hosts. Other AAP/provider flows may still use their own Agent labeling and
networking conventions; removing the old importers does not change those
provider contracts.

To inspect a worker Agent, check its labels and binding in its cluster namespace:

```bash
oc get agent <agent-name> -n <cluster-namespace> -o json | \
  jq '{labels: .metadata.labels, clusterDeployment: .spec.clusterDeploymentName, approved: .spec.approved}'
```
