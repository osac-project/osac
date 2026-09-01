# API design guidelines

This document describes the conventions and rules that govern the design of the fulfillment service
API. Read it before adding or modifying any `.proto` file, service implementation, or REST
transcoding annotation.

The API is defined using gRPC and protocol buffers. Refer to the API in terms of gRPC services,
methods, and protobuf messages. Avoid describing it as a set of HTTP+JSON endpoints, except when
REST transcoding is the specific topic.

Throughout this document and the codebase, the term "object" refers to the entities managed by the
API. Do not use "resource" for this purpose.

## Public and private APIs

The API has two variants, each defined in its own directory tree:

- `proto/public/osac/public/v1/` contains the public API, intended for regular users.
- `proto/private/osac/private/v1/` contains the private API, reserved for system administrators and
  controllers.

The public API must always be a strict subset of the private API. The private API may contain
services, methods, messages, and fields that do not appear in the public API, but the reverse is
never allowed. Public protos must never import private protos, and vice versa.

Both APIs must be documented with documentation comments in the `.proto` files. The documentation in
both should be identical for the parts they share. The private API has not been documented
consistently in the past, but all new additions must include documentation. The reason is that in the
future the public API will be automatically generated from the private API, and any missing
documentation in the private API will be lost in the process.

## File organization

Each object type is defined across two files:

- `<type>_type.proto` contains the message definition of the object itself, along with its `Spec`,
  `Status`, conditions, enums, and any other messages that are specific to that type.
- `<type>s_service.proto` (plural) contains the gRPC service definition and the request and response
  messages for each method.

For example, the `Cluster` type is defined in `cluster_type.proto` and its service in
`clusters_service.proto`.

Shared types that are used across multiple object types live in their own files. For example,
`metadata_type.proto` defines the common `Metadata` message and `condition_status_type.proto`
defines the shared `ConditionStatus` enum.

## Object structure

All object types follow a standard top-level structure:

```protobuf
message Thing {
  string id = 1;
  Metadata metadata = 2;
  ThingSpec spec = 3;
  ThingStatus status = 4;
}
```

The `id` field is a unique identifier assigned by the system. The `metadata` field contains data
common to all objects. Most objects then have `spec` and `status` fields, described below.

Some objects that represent static configuration or catalog data (for example `HostType` or
`ClusterTemplate`) may omit `spec` and `status` and instead use flat domain-specific fields. This
is acceptable when the object does not have user-modifiable desired state or system-reported
observed state.

### Metadata

The `Metadata` message is shared by all object types and contains the following fields:

| Field                  | Type                         | Description                                      |
|------------------------|------------------------------|--------------------------------------------------|
| `creation_timestamp`   | `google.protobuf.Timestamp`  | Time the object was created.                     |
| `deletion_timestamp`   | `google.protobuf.Timestamp`  | Time the object was marked for deletion.         |
| `creator`              | `string`                     | Identity that created the object.                |
| `name`                 | `string`                     | Required, immutable identifier (RFC 1123 DNS label format). Unique within its scope and immutable after creation. |
| `tenant`               | `string`                     | Tenant that owns the object.                     |
| `labels`               | `map<string, string>`        | Indexed key-value pairs for organizing objects.  |
| `annotations`          | `map<string, string>`        | Arbitrary user-controlled metadata.              |
| `version`              | `int32`                      | Auto-incremented on every change.                |
| `display_name`         | `string`                     | Optional friendly label (max 63, not unique, not DNS-constrained). |
| `description`          | `string`                     | Optional description (max 256, not unique).      |

The private API adds a `finalizers` field (`repeated string`) that is not exposed in the public API.

### Resource names

Every object must have a `metadata.name`. The name is **mandatory** at creation time, **immutable**
after creation, and **unique** within its scope.

#### Format

Names follow the RFC 1123 DNS label format:

- Lowercase letters (`a`–`z`), digits (`0`–`9`), and hyphens (`-`) only
- Must start and end with an alphanumeric character
- 1 to 63 characters long

The proto validation rule is:

```
min_len: 1, max_len: 63, pattern: "^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$"
```

Valid names: `my-cluster`, `prod-net-01`, `a`, `web-3`

Invalid names: `My-Cluster` (uppercase), `-starts-with-hyphen` (leading hyphen),
`ends-with-hyphen-` (trailing hyphen), `has_underscores` (underscores),
`name-that-is-way-too-long-and-exceeds-the-sixty-three-character-maximum-allowed` (too long),
`""` (empty)

#### Uniqueness

Names are unique per (tenant, project, resource type). Two different resource types may have objects
with the same name within the same tenant and project, but two objects of the same type cannot.
A name remains reserved while the object exists, including while deletion is pending (between
`deletion_timestamp` being set and archival completing).

#### Immutability

Once an object is created, its `metadata.name` cannot be changed. Updates that include
`metadata.name` in the field mask are accepted only if the value is identical to the existing name.

#### Error responses

| Scenario | gRPC code | Example message |
|---|---|---|
| Name missing or empty on create | `InvalidArgument` | Validation error on `metadata.name` (protovalidate) |
| Name fails RFC 1123 format | `InvalidArgument` | Validation error on `metadata.name` (protovalidate) |
| Name already taken in scope | `AlreadyExists` | `virtual network 'prod-net' already exists` |
| Update attempts to change name | `InvalidArgument` | `field 'metadata.name' is immutable` |
| Singleton or single-default invariant (for example a second non-deleted `NetworkClass`) | `FailedPrecondition` | `only one NetworkClass per deployment is allowed (existing NetworkClass id '...')` |

Not every unique-index violation is `AlreadyExists`. Ordinary per-object name uniqueness stays
`AlreadyExists`. Unique partial indexes that enforce a singleton or "single default" invariant
(for example one `NetworkClass` per deployment, or at most one default `NetworkClass`) map to
`FailedPrecondition`: the slot is occupied, and a retry may succeed once the conflicting object
is gone. Consumers must match on the gRPC status code rather than assuming every uniqueness
conflict is `AlreadyExists`.

### Spec and status ownership

The `spec` and `status` fields have strict ownership rules:

- **`spec`** is exclusively user-controlled. It represents the desired state declared by the user.
  The system must never modify the `spec` of an object. When a user creates or updates an object,
  they write to `spec`.

- **`status`** is exclusively system-controlled. It represents the observed state as reported by
  the system. Users do not have permission to modify `status`. The system updates `status` to
  reflect the current state of the object, which may differ from what the user requested in `spec`
  while changes are being applied.

### Annotations vs spec/status fields

Annotations (`metadata.annotations`) are intended for arbitrary, user-controlled metadata. They are
not part of the system's data model and must not be used to represent relationships, configuration,
or state that the system depends on.

When an object has a relationship to another object, or when the system needs to store operational
data, use a field in `spec` or `status` instead. Spec fields are typed, validated, documented in
the schema, and visible in generated clients, whereas annotations are opaque strings with no schema
enforcement.

In summary:

- **Spec fields** are for user-declared desired state, including references to related objects.
- **Status fields** are for system-managed observed state.
- **Annotations** are for users to attach their own unstructured metadata. The system should never
  read annotations to make decisions.

Some existing proto files document parent relationships via annotations. That is legacy guidance and
should not be followed when designing new objects or refactoring existing ones.

## Validation constraints

All proto fields that have constraints (required, min/max length, format, etc.) must be annotated with
`buf.validate` rules. The protovalidate library enforces these constraints at runtime, rejecting
invalid requests with `InvalidArgument` errors that include field-level violation details.

### Validation flow

- **Create requests**: Validated by protovalidate interceptor before reaching server handlers
- **Update requests**: Server validates the merged object after applying `update_mask`
  - Interceptor skips validation to avoid false errors on partial objects
  - Server merges request fields (per mask) with database object
  - Server validates the complete merged result with protovalidate

This ensures validation always runs on the actual final state, not partial input.

### Standard constraints

Common validation patterns:

- **Required string fields**: `[(buf.validate.field).string.min_len = 1]`
- **DNS labels** (like `metadata.name`): `min_len: 1`, `max_len: 63`, `pattern: "^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$"`
- **Enum fields**: `[(buf.validate.field).enum.defined_only = true]` to reject unknown values
- **Numeric ranges**: `[(buf.validate.field).int32.gte = 0]`
- **Map constraints**: Use `[(buf.validate.field).map.keys...]` and `[(buf.validate.field).map.values...]`

### CEL expressions

For complex validation logic, use CEL (Common Expression Language):

**Field-level CEL** - validates a single field:
```protobuf
string name = 1 [(buf.validate.field).cel = {
  id: "name_check"
  message: "name must start with 'prod-'"
  expression: "this.startsWith('prod-')"
}];
```

**Message-level CEL** - validates across fields or with resource-specific logic:
```protobuf
message Project {
  option (buf.validate.message).cel = {
    id: "hierarchical_name"
    expression: "this.metadata.name.split('.').all(segment, segment.matches('^[a-z0-9]...'))"
  };

  Metadata metadata = 2;
}
```

### Overriding embedded validation

To skip standard validation on an embedded message and apply resource-specific rules:
1. Use `ignore: IGNORE_ALWAYS` on the field to skip its embedded validation
2. Add message-level CEL to validate with custom logic

Example (Projects allow dots in names, other resources don't):
```protobuf
message Project {
  option (buf.validate.message).cel = {
    expression: "this.metadata.name.split('.').all(segment, segment.matches(...))"
  };

  Metadata metadata = 2 [(buf.validate.field).ignore = IGNORE_ALWAYS];
}
```

Refer to the [protovalidate documentation](https://github.com/bufbuild/protovalidate) for full CEL syntax.

### When not to use protovalidate

Constraints that require external state (database lookups, existence checks, uniqueness) cannot be
expressed in proto annotations and must be implemented in server logic. Examples:

- Resource name uniqueness (requires database query)
- Foreign key validation (requires checking if referenced object exists)
- Quota enforcement (requires tenant-level state)
- Custom business rules that depend on multiple objects or system state

For these cases, implement validation in the server's `Create` or `Update` methods and return
appropriate gRPC errors with descriptive messages:

- `AlreadyExists` for ordinary per-object uniqueness (the same name already exists in scope)
- `FailedPrecondition` for singleton or "single default" invariants (another object already
  occupies the unique slot; a retry may succeed once that object is gone)
- `InvalidArgument` for other constraint failures

Match on the gRPC status code, not on message text. Do not assume every uniqueness violation is
`AlreadyExists`.

## Declarative, intent-based design

The API is declarative. Users express their intent by setting fields in `spec`, and the system works
to reconcile the actual state (reported in `status`) with that intent. There must be no imperative
methods such as `Start`, `Stop`, `Reboot`, or similar action verbs.

When behavior that feels imperative is needed, model it as a declarative spec field:

- **State toggles**: use an enum field in `spec`. For example, if a compute instance needs to be
  powered on or off, define a `spec.power_state` field with values like `POWER_STATE_ON` and
  `POWER_STATE_OFF`. The user sets the desired power state and the system drives the instance toward
  it.

- **One-shot triggers**: use a trigger field that the user updates to request an action. For
  example, a `spec.reboot_trigger` field that the user changes (e.g. increments or sets to a new
  timestamp) to request a reboot. The system detects the change and performs the reboot.

In both cases the system reports progress and outcome through `status` fields and conditions.

## Naming conventions

### Types and messages

Type names use `CamelCase`: `Cluster`, `ComputeInstance`, `ExternalIPAttachment`.

Nested spec and status messages follow the pattern `<Type>Spec` and `<Type>Status`:
`ClusterSpec`, `ClusterStatus`.

### Fields

Field names use `snake_case` in both the `.proto` definitions and the JSON representation used by
the REST gateway.

### Enums

Enum type names use `CamelCase`: `ClusterState`, `ConditionStatus`.

Enum values are prefixed with the enum type name in `UPPER_SNAKE_CASE`, followed by the value name:

```protobuf
enum ClusterState {
  CLUSTER_STATE_UNSPECIFIED = 0;
  CLUSTER_STATE_PROGRESSING = 1;
  CLUSTER_STATE_READY = 2;
  CLUSTER_STATE_FAILED = 3;
}
```

Every enum must have an `_UNSPECIFIED = 0` value as its first entry. This represents the default or
unknown state and must always be present.

Prefer enums over magic strings. If a field can only take a known set of values, define an enum
for it.

## Services

For each object type there is a corresponding service, named as the plural of the type without a
`Service` suffix. For example, the service for the `Cluster` type is `Clusters`, and the service
for `BareMetalInstance` is `BareMetalInstances`.

### Standard methods

Services of the public API must always declare the following five methods, even if some of them are
not yet implemented in the backend (in which case the documentation should explain this). The
reason is that the CLI currently depends on all these methods being declared. This restriction may
be lifted in the future.

| Method   | Purpose                            |
|----------|------------------------------------|
| `Create` | Creates a new object.              |
| `List`   | Returns a filtered list of objects. |
| `Get`    | Returns a single object by `id`.   |
| `Update` | Partially updates an object.       |
| `Delete` | Deletes an object by `id`.         |

Services of the private API must additionally declare a `Signal` method. This method must never
appear in the public API.

| Method   | Purpose                                                          |
|----------|------------------------------------------------------------------|
| `Signal` | Notifies the controller that the object may need reconciliation. |

In general, services should not have any additional ad-hoc methods beyond those listed above. There
are some existing exceptions, and the project will work to remove them over time.

## Request and response messages

Request and response messages are named after the service, following the pattern
`{ServiceName}{Method}Request` and `{ServiceName}{Method}Response`. For example, the messages for
the `Clusters` service are `ClustersCreateRequest`, `ClustersCreateResponse`,
`ClustersListRequest`, and so on.

In general, request and response messages should not contain any fields other than those described
below. If there is a need to add a new field, it should be carefully considered and added in a
generic way so that all types and services can adopt it consistently.

### Create

The request contains a single `object` field of the corresponding object type. The response
contains a single `object` field with the created object (including any fields set by the system,
such as `id` and `metadata`).

```protobuf
message ThingsCreateRequest {
  Thing object = 1;
}

message ThingsCreateResponse {
  Thing object = 1;
}
```

### List

The request contains `offset`, `limit`, `filter`, and `order` fields. The response contains `size`,
`total`, and `items`.

```protobuf
message ThingsListRequest {
  optional int32 offset = 1;
  optional int32 limit = 2;
  optional string filter = 3;
  optional string order = 4;
}

message ThingsListResponse {
  int32 size = 1;
  int32 total = 2;
  repeated Thing items = 3;
}
```

- `offset` is the zero-based index of the first result to return. Defaults to zero.
- `limit` is the maximum number of results. When omitted, the server applies a default limit. When
  set to zero, the server returns only the total count with an empty items list (useful for clients
  that only need to know how many items match a filter). Negative values are rejected with an error.
- `filter` is a [CEL](https://cel.dev) expression evaluated against each candidate object. See
  [docs/FILTER.md](FILTER.md) for the full details of the supported CEL subset.
- `order` specifies the sort order using a syntax similar to SQL `ORDER BY`, for example
  `api_url desc`. This field is defined in the proto files but is not yet implemented. When omitted,
  the order of results is undefined.
- `size` is the number of items actually returned (may be less than `limit`).
- `total` is the total number of matching objects, regardless of `offset` and `limit`.

### Get

The request contains only the `id` of the object. The response contains a single `object` field.

```protobuf
message ThingsGetRequest {
  string id = 1;
}

message ThingsGetResponse {
  Thing object = 1;
}
```

### Update

The request contains `object`, `update_mask`, and `lock`. The response contains the updated
`object`.

```protobuf
message ThingsUpdateRequest {
  Thing object = 1;
  google.protobuf.FieldMask update_mask = 2;
  bool lock = 3;
}

message ThingsUpdateResponse {
  Thing object = 1;
}
```

- `update_mask` specifies which fields to update. In the REST transcoding the gateway automatically
  populates this from the fields present in the JSON request body.
- `lock` enables optimistic locking. When set to `true`, the server rejects the update if the
  current `metadata.version` of the stored object does not match the version in the submitted
  object.

### Delete

The request contains only the `id`. The response is empty.

```protobuf
message ThingsDeleteRequest {
  string id = 1;
}

message ThingsDeleteResponse {}
```

### Signal (private API only)

The request contains only the `id`. The response is empty. This method has no REST transcoding
annotation because it is only used internally via gRPC.

```protobuf
message ThingsSignalRequest {
  string id = 1;
}

message ThingsSignalResponse {}
```

## REST transcoding

All methods of all services must have `google.api.http` options that define the REST transcoding.

### URL prefixes

- Public API: `/api/fulfillment/v1/`
- Private API: `/api/private/v1/`

### URL paths

For each object type there is a URL path derived from the type name, converted to `snake_case` and
pluralized. For example, the `BareMetalInstance` type maps to `baremetal_instances`.

The full URL for the public collection is `/api/fulfillment/v1/baremetal_instances`. Individual
objects are identified by appending the `id`:
`/api/fulfillment/v1/baremetal_instances/{id}`.

### Flat URL space

The URL space must never be deeper than a collection and its members. For example, if a `Subnet`
belongs to a `VirtualNetwork`, do not define a nested URL like
`/api/fulfillment/v1/virtual_networks/123/subnets`. Instead, `Subnet` has its own top-level
collection at `/api/fulfillment/v1/subnets`, and users find the subnets of a virtual network using
the filter capability with a CEL expression like `this.spec.virtual_network.name == "my-vnet"`.

### HTTP verb mapping

| Method   | HTTP verb | URL pattern                        | Body            | Response body |
|----------|-----------|------------------------------------|-----------------|---------------|
| `List`   | `GET`     | `/api/.../things`                  | --              | --            |
| `Get`    | `GET`     | `/api/.../things/{id}`             | --              | `object`      |
| `Create` | `POST`    | `/api/.../things`                  | `object`        | `object`      |
| `Update` | `PATCH`   | `/api/.../things/{object.id}`      | `object`        | `object`      |
| `Delete` | `DELETE`  | `/api/.../things/{id}`             | --              | --            |

For `Get`, `Create`, and `Update`, the `response_body` option is set to `"object"` so the REST
gateway unwraps the response and returns the object directly. For `List`, the full response
(including `size`, `total`, and `items`) is returned as-is.

## Enums

Every enum must start with an `_UNSPECIFIED = 0` value.

Enum values are prefixed with the enum type name to avoid collisions in the generated code. For
example, the values of `SubnetState` are `SUBNET_STATE_UNSPECIFIED`, `SUBNET_STATE_PENDING`,
`SUBNET_STATE_READY`, and `SUBNET_STATE_FAILED`.

Shared enums that are used across multiple object types live in their own files. For example,
`ConditionStatus` is defined in `condition_status_type.proto` with values
`CONDITION_STATUS_UNSPECIFIED`, `CONDITION_STATUS_TRUE`, and `CONDITION_STATUS_FALSE`.

## Conditions

Objects that have a lifecycle typically report detailed status through a list of conditions. The
pattern uses a `{Type}Condition` message and a `{Type}ConditionType` enum:

```protobuf
message ThingCondition {
  ThingConditionType type = 1;
  ConditionStatus status = 2;
  google.protobuf.Timestamp last_transition_time = 3;
  optional string reason = 4;
  optional string message = 5;
}

enum ThingConditionType {
  THING_CONDITION_TYPE_UNSPECIFIED = 0;
  THING_CONDITION_TYPE_PROGRESSING = 1;
  THING_CONDITION_TYPE_READY = 2;
  THING_CONDITION_TYPE_FAILED = 3;
}
```

- `type` identifies the condition.
- `status` uses the shared `ConditionStatus` enum (`TRUE`, `FALSE`, or `UNSPECIFIED`).
- `last_transition_time` records when the condition last changed.
- `reason` is a machine-readable string for programmatic use (optional).
- `message` is a human-readable description for debugging (optional).

The `status` field in the object then contains `repeated ThingCondition conditions` alongside the
top-level `state` enum.

## Object references

When an object needs to reference another object, the API uses typed reference messages instead of
plain string fields. Each referenced type has its own message, giving the schema full type safety
and enabling automatic server-side validation and resolution.

There are two kinds of references:

### Full references

Full references can point to objects in a different project or in the shared tenant. They have
four fields:

```protobuf
message ClusterTemplateReference {
  string id = 1;
  string name = 2;
  string project = 3;
  bool shared = 4;
}
```

| Field     | Description |
|-----------|-------------|
| `id`      | Unique identifier of the referenced object. |
| `name`    | Human-readable name of the referenced object. |
| `project` | Project where the referenced object lives. Defaults to the caller's project when omitted. |
| `shared`  | When `true`, the lookup targets the `shared` tenant (overrides the caller's tenant). |

Callers may supply `id`, `name`, or both. When both are provided they must refer to the same
object; otherwise the server returns `InvalidArgument`. The server auto-populates whichever field
is missing after a successful lookup.

Full references are used when the target object may live outside the caller's project — for
example, cluster templates, catalog items, host types, instance types, IP pools, roles, and users.

### Local references

Local references are scoped to the same tenant and project as the parent object. They have only
two fields:

```protobuf
message VirtualNetworkLocalReference {
  string id = 1;
  string name = 2;
}
```

As with full references, callers may supply `id`, `name`, or both, and the server auto-populates
the other.

Local references are used when the target is always co-located — for example, a `Subnet`
referencing its parent `VirtualNetwork`, or a `ComputeNetworkAttachment` referencing a `Subnet` and
`SecurityGroup`.

### Naming convention

Each reference type is named `<TargetType>Reference` or `<TargetType>LocalReference` and is
defined in the target type's `*_type.proto` file. The spec field that uses it is named after the
relationship (e.g., `template`, `virtual_network`, `role`), not after the reference type itself.

### JSON representation

In the JSON representation used by the REST gateway, reference fields are nested objects:

```json
{
  "spec": {
    "template": { "name": "sandbox" },
    "version": { "name": "4-17-0" }
  }
}
```

For local references the format is identical but without `project` and `shared`:

```json
{
  "spec": {
    "virtual_network": { "name": "my-vnet" }
  }
}
```

Repeated references (e.g., security groups in a network attachment) are arrays of objects:

```json
{
  "subnet": { "name": "my-subnet" },
  "security_groups": [
    { "name": "sg-web" },
    { "name": "sg-ssh" }
  ]
}
```

### Cross-project references

To reference an object in a different project within the same tenant, set the `project` field:

```json
{
  "spec": {
    "template": { "name": "shared-tpl", "project": "templates" }
  }
}
```

To reference an object owned by the shared tenant (e.g., a globally available template), set
`shared` to `true`:

```json
{
  "spec": {
    "template": { "name": "sandbox", "shared": true }
  }
}
```

### Server-side validation and resolution

The server validates references automatically via a gRPC interceptor on `Create` and `Update`
requests. For each reference field the interceptor:

1. Determines the lookup scope (caller's tenant/project for local references; explicit
   `project`/`shared` overrides for full references).
2. Looks up the referenced object by `id`, `name`, or both.
3. If both `id` and `name` are provided, verifies they refer to the same object.
4. Auto-populates whichever of `id` or `name` was not provided by the caller.

Invalid references produce an `InvalidArgument` error with `google.rpc.BadRequest` details
containing one `FieldViolation` per invalid reference. The `field` value is the dot-separated
path to the reference field (e.g., `object.spec.template`, `object.spec.network_attachments[0].subnet`).

## Documentation

All elements of the public API (services, methods, messages, fields, enums, and enum values) must
have documentation comments in the `.proto` files. The private API must also be documented for all
new additions, because in the future the public API will be generated from the private API.

Documentation should be written for users of the API, not for developers of the system. It should
not contain implementation details that are irrelevant to API consumers.

## Additional rules

### No ad-hoc authentication or authorization

The API must not introduce ad-hoc authentication or authorization mechanisms. Authentication is
handled by the gRPC interceptor chain (JWT validation), and authorization is handled by OPA
policies.

### No structured strings

String fields must not carry embedded structure or internal formats. For example, do not define a
string field documented as containing `"key=value"` pairs or `"host:port"` syntax. When structured
data is needed, define a proper protobuf message with typed fields instead.

Well-known formats like IP addresses, CIDRs, and URLs are acceptable as plain strings because
they have universally understood semantics.

### No extra request or response fields

Request and response messages should contain only the standard fields described in this document.
If there is a genuine need for a new field, it should be designed generically so that all services
can adopt it consistently, and the decision should be discussed before implementation.
