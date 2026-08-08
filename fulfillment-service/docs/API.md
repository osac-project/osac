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
| `name`                 | `string`                     | Human-friendly name (DNS label rules, optional). |
| `tenant`               | `string`                     | Tenant that owns the object.                     |
| `labels`               | `map<string, string>`        | Indexed key-value pairs for organizing objects.  |
| `annotations`          | `map<string, string>`        | Arbitrary user-controlled metadata.              |
| `version`              | `int32`                      | Auto-incremented on every change.                |
| `display_name`         | `string`                     | Optional friendly label (max 63, not unique, not DNS-constrained). |
| `description`          | `string`                     | Optional description (max 256, not unique).      |

The private API adds a `finalizers` field (`repeated string`) that is not exposed in the public API.

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
- **DNS labels** (like `metadata.name`): `max_len: 63`, `pattern: "^([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?)?$"`
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
`InvalidArgument` errors with descriptive messages.

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
- `limit` is the maximum number of results. When omitted, the server returns all matching objects
  (though it may cap the result set for performance reasons).
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
the filter capability with a CEL expression like `this.spec.virtual_network == "123"`.

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

When an object needs to reference another object, define a `string` field named after the
relationship or the type of the referenced object. The value of the field is the unique identifier
of the referenced object.

For example, when a `Cluster` references a template:

```protobuf
message ClusterSpec {
  string template = 1;
}
```

Note that the field name is `template`, not `template_id`. The convention is to name the field
after the relationship, without an `_id` suffix.

Other examples from the codebase:

- `SubnetSpec.virtual_network` references a `VirtualNetwork` by its `id`.
- `RoleBindingSpec.role` references a `Role` by its `id`.
- `ProjectSpec.parent` references a parent `Project` by its `id`.

In the future the project plans to introduce a typed `Reference` message to make these references
type-safe. Until then, references are plain `string` fields and the relationship must be documented
in the proto comments.

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
