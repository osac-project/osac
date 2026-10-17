# MCP resource discovery

The Fulfillment MCP server exposes bounded, typed resource discovery. It does
not provide a generic RPC proxy, method-name dispatch, reflection, or direct
Kubernetes access. Every request is authenticated as the MCP caller and the
public Fulfillment API remains the authorization boundary.

## Supported resources

`list_resources` supports the baseline compute and networking prerequisites,
`project`, and `secret_reference`. `get_resource` supports only resources with
a registered public Get adapter. Secret references are list-only: MCP returns
the reference's ID, name, and type, and never Secret bytes, credentials,
tokens, provider data, or the underlying Secret object.

List requests use the explicit `filter` field and bounded pagination. The
default page size is 50, the maximum is 100, and negative offsets or invalid
page sizes are rejected before a public API call.

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
