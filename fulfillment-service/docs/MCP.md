# MCP tools

The Fulfillment MCP server exposes bounded, typed resource discovery and
supported writes. It does not provide a generic RPC proxy, method-name
dispatch, reflection, or direct Kubernetes access. Every request is
authenticated as the MCP caller and the public Fulfillment API remains the
authorization boundary.

## Supported resources

`list_resources` supports the baseline compute and networking prerequisites,
public exposure resources, the provider-managed `external_ip_pool`, `project`,
and `secret_reference`.

`get_resource` accepts only:

- `compute_instance_catalog_item`
- `compute_instance_template`
- `instance_type`
- `disk_image`
- `storage_tier`
- `project`
- `virtual_network`
- `subnet`
- `security_group`
- `external_ip`
- `external_ip_pool`
- `external_ip_attachment`
- `nat_gateway`
- `compute_instance`

Secret references are list-only: MCP returns the reference's ID, name, and
type, and never Secret bytes, credentials, tokens, provider data, or the
underlying Secret object.

When Fulfillment returns the missing-Secret validation error recognized by the
MCP mapper, typed write tools return the shared `not_found` safe-error
envelope. The result names the missing Secret reference and directs the caller
to the existing `/secrets/create` wizard. Other missing-Secret failures retain
their public API error mapping. MCP never accepts or returns Secret bytes, and
the current typed write schemas do not consume a Secret reference. Create the
Secret in the wizard, update the owning configuration outside MCP when it must
point to that new Secret, and then retry the original typed write. A direct
reference-consuming write boundary is owned by OSAC-5861.

List requests use the explicit `filter` field and bounded pagination. The
default page size is 50, the maximum is 100, and negative offsets or invalid
page sizes are rejected before a public API call.

List and Get results use the same narrow resource summary projection. A
summary can contain only an ID, DNS-safe name, version, deterministic state,
and, where applicable, a typed reference kind. It never returns free-text
titles or descriptions, annotations, status messages, credentials, provider
data, or raw protobuf JSON.

## Supported network writes

`create_network_resource` creates exactly one `virtual_network`, `subnet`, or
`security_group` through the existing public Create API as the MCP caller. It
accepts only the typed fields for the selected resource, requires canonical
IPv4 CIDRs, and rejects edit-shaped input containing an existing resource ID
with `unsupported_in_place` before any public mutation. Subnet and
SecurityGroup creation passes the named parent VirtualNetwork to the public
Create call. A missing or inaccessible parent is rejected there and returns a
named prerequisite error without creating a dependent resource. NetworkClass
remains provider-managed and is never exposed
as a tenant write. SecurityGroup creation accepts at most 100 rules in total
across `ingress_rules` and `egress_rules`, with the same per-direction schema
limit.

`delete_network_resource` deletes exactly one explicitly named eligible
network resource: VirtualNetwork, Subnet, SecurityGroup, ExternalIP,
ExternalIPAttachment, or NATGateway. It does not cascade: a parent with active
dependents returns a conflict, and callers must delete dependents first.

`expose_network_resource` creates exactly one `external_ip`,
`external_ip_attachment`, or `nat_gateway` through the existing public Create
API as the MCP caller. ExternalIP allocation selects an existing
provider-managed ExternalIPPool discovered with the read-only public List/Get
API; MCP does not create or edit pools. An
ExternalIPAttachment is inbound DNAT and requires one ComputeInstance, Cluster,
or BareMetalInstance target, with `api` or `ingress` required for Cluster
targets. A NATGateway is outbound SNAT and requires a VirtualNetwork and an
ExternalIP. The typed input rejects edit-shaped IDs, ambiguous targets, and
fields from another resource variant before dispatch. Missing or inaccessible
dependencies return a named safe prerequisite error, and the public API remains
the authorization and dependency boundary.

## Registry extension contract

A resource-family task extends the registry in
`internal/cmd/service/start/mcpserver/resource_registry.go` by adding:

1. A typed `ResourceType` constant.
2. A registry entry that references only explicitly supported public List or
   Get clients.
3. A projection that returns the minimum caller-safe fields. Do not serialize
   a generic object when a summary or field redaction is required.
4. Unit and component-integration coverage for caller authorization, explicit
   filters, pagination bounds, supported and unsupported methods, and no
   mutation on rejection.

Omit the `get` function when the public API has no safe Get path. The
`get_resource` input schema lists only resource types with a registered Get
adapter, so clients reject list-only types during schema validation. The shared
handler still returns the `invalid_request` safe-error envelope without calling
Fulfillment if a request for an unsupported type reaches it.

Registry entries must not add arbitrary method names, reflection, arbitrary
request messages, in-place edits, or unbounded pagination. If a family lacks
the required public List or Get path, keep it out of the registry and document
the missing contract.

`check_connection` gets `caller.tenants` from the public `Tenants.List` API with
the caller's forwarded API token. It includes up to ten assignable tenant names
and excludes the reserved shared tenant; `permission_summary_truncated` is true
when the list or its method checks are incomplete. The method-specific
`permission_summary` comes from public SelfSubjectAccessReviews for each
included tenant. Tenant visibility does not grant those methods, and the summary
is a point-in-time check rather than a guarantee for later requests.

## Project write tools

`create_project` creates a Project through the public Projects/Create API as
the signed-in caller. It accepts the hierarchical `name`, optional `title`,
and optional `description`; tenant, creator, identity, hierarchy, and
membership attribution remain controlled by Fulfillment. Project results expose
only the agent-safe identity fields (`id`, `name`, `version`, and `state`);
free-text fields are accepted as input but are not echoed into model context.

`update_project` accepts only `title` and `description`. Each supplied field is
sent with an exact `spec.title` or `spec.description` mask, and all identity,
tenant, hierarchy, membership, status, and unselected fields remain unchanged.
The public Projects Update wrapper does not provide an atomic stale-version
precondition, so the tool does not set `lock` or accept a version. Concurrent
or later updates to the same field use last-write-wins behavior. A Project
manager or tenant administrator can update within the existing public API
permissions; an ordinary member or caller from another tenant receives the
safe authorization error.
