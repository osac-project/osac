# MCP resource tools

The Fulfillment MCP server exposes bounded, typed resource discovery and
supported writes. It does not provide a generic RPC proxy, method-name
dispatch, reflection, or direct Kubernetes access. Every request is
authenticated as the MCP caller and the public Fulfillment API remains the
authorization boundary.

## Supported resources

`list_resources` supports the baseline compute and networking prerequisites,
`project`, and `secret_reference`. `get_resource` supports only resources with
a registered public Get adapter. Secret references are list-only: MCP returns
the reference's ID, name, and type, and never Secret bytes, credentials,
tokens, provider data, or the underlying Secret object.

List requests use the explicit `filter` field and bounded pagination. The
default page size is 50, the maximum is 100, and negative offsets or invalid
page sizes are rejected before a public API call.

List and Get results use the same narrow resource summary projection. A
summary can contain only an ID, DNS-safe name, version, deterministic state,
and, where applicable, a typed reference kind. It never returns free-text
titles or descriptions, annotations, status messages, credentials, provider
data, or raw protobuf JSON.

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

Omit the `get` function when the public API has no safe Get path. The shared
handler then returns the `invalid_request` safe-error envelope without calling
Fulfillment. The tool schema intentionally accepts all registered types so
this rejection reaches the shared handler rather than host-side schema
validation.

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
