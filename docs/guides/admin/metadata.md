# Labels and annotations

This guide explains how OSAC platform administrators and tenant users work with labels and
annotations, including keys reserved for system use. It describes the Fulfillment API and CLI
behavior; it does not grant additional permissions. For the API field definitions, see
[fulfillment-service API.md](../../../fulfillment-service/docs/API.md#administrator-only-metadata).
For CEL filters, see [Filter expressions](../../../fulfillment-service/docs/FILTER.md).

## Reserved namespace

A label or annotation key is reserved when its domain, before the slash, is exactly
`admin.osac.openshift.io` or ends in `.admin.osac.openshift.io`. Matching is case-sensitive.
Examples:

| Key | Reserved by this rule? |
|-----|------------------------|
| `admin.osac.openshift.io/managed` | Yes |
| `controller.admin.osac.openshift.io/source` | Yes |
| `fakeadmin.osac.openshift.io/managed` | No |
| `admin.osac.openshift.io.example/managed` | No |
| `admin.osac.openshift.io` (no slash) | No |
| `osac.openshift.io/tenant` | No |
| `osac.openshift.io/owner-reference` | No |

The last two keys are outside this reserved namespace rule, but retain their existing
resource-specific ownership and validation behavior. This privacy rule does not make them safe to
edit as ordinary application metadata.

## Private and public API behavior

The private API exposes List, Get, Create, Update, and Delete for all 35 persistent resource types
below, plus a `Signal` RPC on each service. A platform administrator authorized by the existing
`is_admin` policy can use the existing private methods. Other private identities remain limited by
their existing authorization policy. This feature does not add methods, roles, or permissions.

The public API keeps its existing resource method surface:

| Public methods | Resource types |
|----------------|----------------|
| Create, List, Get, Update, Delete (23) | `BareMetalInstanceCatalogItem`, `BareMetalInstanceTemplate`, `BareMetalInstance`, `ClusterCatalogItem`, `ClusterTemplate`, `ClusterVersion`, `Cluster`, `ComputeInstanceCatalogItem`, `ComputeInstanceTemplate`, `ComputeInstance`, `DiskImage`, `FabricDomain`, `IdentityProvider`, `InstanceType`, `ProjectMembership`, `Project`, `RoleBinding`, `Role`, `Secret`, `SecurityGroup`, `Tenant`, `User`, `Volume` |
| Create, List, Get, Delete; no public Update (5) | `ExternalIPAttachment`, `ExternalIP`, `NATGateway`, `Subnet`, `VirtualNetwork` |
| List, Get only (4) | `AddOnOperator`, `BareMetalInstanceType`, `ExternalIPPool`, `StorageTier` |
| Private API only (3) | `Hub`, `NetworkClass`, `StorageBackend` |

Public method availability does not mean every tenant caller is authorized for every method. The
reserved-metadata rules apply to every public caller, including callers with tenant administrator
roles: public object responses and event payloads omit reserved labels and annotations, and public
Create or Update requests cannot set them. Caller and resource authorization remains unchanged.

### Private operations and existing service limits

Use `osac login --private` with existing platform administrator credentials, then manage reserved
keys with the normal CLI `key=value` and `key-` syntax:

```shell
osac label clusters prod-cluster admin.osac.openshift.io/managed=true
osac annotate clusters prod-cluster controller.admin.osac.openshift.io/source=capacity-controller
osac label clusters prod-cluster admin.osac.openshift.io/managed-
```

The final command removes that label from the private object. The same CLI syntax can edit ordinary
labels and annotations through a public session, subject to the resource's existing public Update
method and authorization.

The API has service-specific method limits in addition to these metadata rules:

- Public `VirtualNetworks`, `Subnets`, `ExternalIPs`, `ExternalIPAttachments`, and `NATGateways`
  have no Update method. Their private services retain Update under existing authorization.
- Public `ExternalIPPools` supports only List and Get. Its private service retains the existing
  methods.
- Private Update supports metadata maps on `NetworkClasses`, `VirtualNetworks`, `Subnets`,
  `SecurityGroups`, `ExternalIPPools`, `ExternalIPs`, `ExternalIPAttachments`, `NATGateways`,
  and `FabricDomains`.
  Updates to `ExternalIPs`, `ExternalIPAttachments`, and `NATGateways` require a non-empty existing
  update mask; include the metadata map path explicitly.
- Public `SecurityGroups.Update` replaces only ingress and egress rule lists. It cannot update
  metadata and preserves the object's metadata.
- Public `FabricDomains` retains Create, List, Get, Update, and Delete.
- The CSI identity can use private `Volumes.Create`, `Get`, `List`, and `Delete`. It has no private
  `Volumes.Update` or `Signal` permission. The current shared CSI identity has universal tenant
  scope; tenant information is carried in request metadata rather than isolated by identity. The
  metadata feature does not widen this method set or change that existing scope.

The private method surface is not a promise that every private service identity can call every
method. In particular, use the caller's existing policy and service contract when deciding whether
an operation is available.

## Replacing maps, masks, and locks

An Update mask that selects `metadata.labels` or `metadata.annotations` replaces that complete map.
In a private request, keys omitted from the selected map are removed; an empty or omitted selected
map clears all keys. A key with an empty string value is still present. A map not selected by the
mask is unchanged.

On the public API, a selected map replaces ordinary entries while OSAC restores the currently
stored reserved entries. Omitting a reserved key therefore does not remove it. An empty or omitted
selected map clears ordinary entries and retains reserved entries. Omitting the map from the update
mask leaves it unchanged. Public updates preserve the reserved portion on the existing unmasked
paths too, while ordinary entries keep each resource's current behavior.

Use an explicit map path, such as `metadata.labels` or `metadata.annotations`, when the request must
make replacement or clearing intent clear and the service supports that path. REST requests
normally get an update mask from fields present in the JSON body. Behavior when an update mask is
omitted remains resource-specific; for example, `ComputeInstances` infers supplied fields, while
clearing an empty map requires an explicit mask. Do not assume one unmasked-update rule applies to
every resource.

Optimistic locking is optional. With `lock=true`, submit the current `metadata.version`; if the
stored version has changed, the server returns `Aborted`. Read the latest object, reapply the
intended change, and retry. With locking disabled or omitted, the existing update behavior is
unchanged.

## Public input errors and retry guidance

A public Create or Update request containing a reserved label or annotation fails with
`InvalidArgument` and the generic message:

```text
Label or annotation is reserved for use by the system.
```

The server rejects the whole request before applying other fields, and the error does not echo the
reserved key or value. This applies if the reserved entry is unchanged or is outside the selected
update mask. Do not retry the same public payload. Remove the reserved entry from the public
request; if the operation needs to change that entry, use an authorized private API caller instead.

The CLI removal form `key-` is an omission-only operation. Since public reads hide reserved keys,
removing a known reserved key through a public session cannot delete its stored value and may
complete without changing that entry. Use the private API to remove it.

## Filters, counts, and events

Public List and `Events.Watch` filters reject literal references to reserved metadata keys, whether
the expression indexes a map or tests key membership. List and Watch setup return `InvalidArgument`;
Watch's error may include the submitted filter expression. Neither response reports whether the key
exists or returns its stored value. This applies to count-only List requests (`limit=0`) as well.
Ordinary metadata keys remain filterable. For example, a List filter can use
`this.metadata.labels['team'] == 'platform'`, and an event filter can use
`event.project.metadata.labels['team'] == 'platform'`.

Public event payloads omit reserved entries. See [Filter expressions](../../../fulfillment-service/docs/FILTER.md)
for syntax and examples.

## Compatibility, rollback, and support

The privacy behavior requires a fulfillment-service server version that includes this feature.
Clients do not need a new CLI syntax. A server version predating the feature keeps its previous
visibility and input-validation behavior, so do not rely on reserved metadata being hidden while
using an older server.

Before rolling back to a server version without this behavior, use the feature-capable server and
private API to remove reserved keys from resources that the older server exposes publicly. Cleanup
is manual; downgrade does not automatically remove the keys.

The optional `--log-bodies` setting is unchanged and remains disabled by default. Current flag help
says it records request/response body size, not the complete body. The metadata feature does not
add a blanket label/annotation redaction guarantee. When opening a support case, include the
resource type and ID, API method, status code, update-mask paths, lock/version state, and server
version. Redact sensitive literals from any filter expression before sharing it. Do not attach
protected metadata values or unreviewed request dumps; share key names only when needed and approved.
