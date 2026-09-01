# CleanAPI Workflow Guide

This document provides practical workflows for using the `cleanapi` proto plugin to maintain separate public and private APIs. For API design principles, field numbering rules, and documentation requirements, see [API.md](API.md#public-and-private-apis).

## Quick Reference

**What to edit**: Only `proto/private/` and `proto/tests/`
**Never edit**: `proto/public/` (fully generated)
**Regenerate with**: `uv run dev.py build protos` (from `fulfillment-service/`)
**Commit**: All three - private protos, public protos, and generated Go code

### Directory Structure

```text
proto/private/osac/private/v1/  - EDITABLE: Source of truth, full API
proto/public/osac/public/v1/    - GENERATED: Public-facing API (DO NOT EDIT)
proto/tests/osac/tests/v1/      - EDITABLE: Test-only definitions
```

## CleanAPI Annotations Reference

For complete design principles and requirements, see [API.md](API.md#public-and-private-apis).

### Field-Level Annotations

```protobuf
import "cleanapi/annotations.proto";

message Cluster {
  string id = 1;
  Metadata metadata = 2;
  ClusterSpec spec = 3;
  ClusterStatus status = 4;

  // This field is only visible in the private API
  string internal_node_id = 5 [(cleanapi.field).private = true];
}
```

### Message-Level Annotations

```protobuf
// This entire message is private
message InternalDebugInfo {
  option (cleanapi.message).private = true;

  string debug_trace_id = 1;
  string internal_cluster_id = 2;
}
```

### Method-Level Annotations (Private-Only RPCs)

Mark individual RPC methods as private-only. This is commonly used for the `Signal` method that controllers use to notify the service of state changes:

```protobuf
service BareMetalInstances {
  rpc List(BareMetalInstancesListRequest) returns (BareMetalInstancesListResponse) {
    option (google.api.http) = {get: "/api/private/v1/baremetal_instances"};
  }

  // This method only appears in the private API
  rpc Signal(BareMetalInstancesSignalRequest) returns (BareMetalInstancesSignalResponse) {
    option (cleanapi.method).private = true;
  }
}

// Request/response messages should also be marked private
message BareMetalInstancesSignalRequest {
  option (cleanapi.message).private = true;
  string id = 1;
}

message BareMetalInstancesSignalResponse {
  option (cleanapi.message).private = true;
}
```

### File-Level Annotations

```protobuf
// Mark entire file as private
option (cleanapi.file).private = true;

// Set package name for public proto
option (cleanapi.file).package = "osac.public.v1";

// Rewrite HTTP routes: /api/private/v1/ → /api/fulfillment/v1/
option (cleanapi.file).http_route_prefix_map = "private:fulfillment";
```

### Key Design Rules

See [API.md](API.md#public-and-private-apis) for full details:

1. **Field numbering**: Private fields must be placed at the end of the message
2. **Comments**: All comments are copied to public protos - write for public API consumers
3. **Private methods**: Use `option (cleanapi.method).private = true` for controller-only RPCs (e.g., Signal)
4. **Private files**: Use `option (cleanapi.file).private = true` for entire proto files that should never be public
5. **Route mapping**: Use `http_route_prefix_map = "private:fulfillment"` to rewrite HTTP routes in the public API

## Common Use Cases

### Adding a New Private Field to an Existing Message

1. Open the message in `proto/private/osac/private/v1/<resource>_type.proto`
2. Add the field at the end
3. Mark it with `[(cleanapi.field).private = true]`
4. Add a comment that makes sense (it won't appear in public proto)

```protobuf
message ComputeInstance {
  string id = 1;
  ObjectMetadata metadata = 2;
  ComputeInstanceSpec spec = 3;
  ComputeInstanceStatus status = 4;

  // NEW: Add private field at the end, this comment will not appear in the public proto
  string internal_vm_id = 5 [(cleanapi.field).private = true];
}
```

5. Run `uv run dev.py build protos` from `fulfillment-service/`
6. Commit all three changes: private proto, public proto, and generated Go code

### Adding a New Public Field

1. Open the message in `proto/private/osac/private/v1/<resource>_type.proto`
2. Add the field in the appropriate logical position
3. **Do not** add the `cleanapi.field` annotation
4. Add a comment suitable for public API consumers

```protobuf
message ComputeInstance {
  string id = 1;
  ObjectMetadata metadata = 2;
  ComputeInstanceSpec spec = 3;
  ComputeInstanceStatus status = 4;

  // NEW: Public field - no annotation needed
  // Human-readable display name for the instance.
  string display_name = 5;
}
```

5. Run `uv run dev.py build protos` from `fulfillment-service/`
6. Commit all changes

### Converting a Public Field to Private

This is a **breaking change** for the public API and should be avoided. If you must do it:

1. Add a new private field with a different number
2. Deprecate the old public field
3. In a later release, migrate data and remove the old field

### Creating a New Private-Only Message

1. Create or edit the proto file in `proto/private/osac/private/v1/`
2. Add `option (cleanapi.message).private = true;` inside the message definition
3. Run `uv run dev.py build protos`
4. Verify the message does not appear in `proto/public/`

### Adding a Private-Only RPC Method

Use this for controller-only methods (e.g., the `Signal` RPC):

1. Add the method to `proto/private/osac/private/v1/<resource>s_service.proto`
2. Mark method with `option (cleanapi.method).private = true`
3. Mark request and response messages with `option (cleanapi.message).private = true`
4. Run `uv run dev.py build protos`
5. Verify the method does not appear in the public service

### Creating a Private-Only Proto File

Use this when an entire resource should never be public (e.g., `volume_type.proto` - managed internally by CSI driver):

1. Create the file in `proto/private/osac/private/v1/<resource>_type.proto`
2. Add `option (cleanapi.file).private = true;` at the top after the package declaration
3. Define your messages, enums, and services normally
4. Run `uv run dev.py build protos`
5. Verify the file does not appear in `proto/public/`

### Setting Up HTTP Route Prefix Mapping

For service files that need routes rewritten (`/api/private/v1/` → `/api/fulfillment/v1/`):

1. Add to the top of `<resource>s_service.proto`:
   ```protobuf
   option (cleanapi.file).package = "osac.public.v1";
   option (cleanapi.file).http_route_prefix_map = "private:fulfillment";
   ```
2. Run `uv run dev.py build protos`
3. Verify routes are rewritten in `proto/public/<resource>s_service.proto`

## Workflow Summary

### Editing Private Protos

```bash
# 1. Edit files in proto/private/
vim proto/private/osac/private/v1/clusters_type.proto

# 2. From fulfillment-service/, regenerate everything
uv run dev.py build protos

# 3. Review the changes
git diff proto/public/
git diff internal/api/

# 4. Commit all three changes
git add proto/private/ proto/public/ internal/api/
git commit -s -m "OSAC-XXXX: Add internal_node_id field to Cluster"
```

### Verifying Your Changes

After running `uv run dev.py build protos`:

1. **Check the public proto** in `proto/public/osac/public/v1/` to verify:
   - Private fields are not present
   - Public fields are present
   - Comments are appropriate for public consumers

2. **Check the generated Go code** in `internal/api/`:
   - Both public and private packages are generated
   - No compilation errors

3. **Run tests**:
   ```bash
   ginkgo run -r internal
   ```

## Troubleshooting

### "cleanapi annotations not found"

If you see errors about missing `cleanapi` imports:

```bash
cd fulfillment-service
uv run dev.py build protos
```

This command fetches the cleanapi dependency and places it in `.buf/deps/cleanapi/`.

### CI Check Failing: "Generated code is out of date"

You forgot to commit the generated public protos or Go code:

```bash
cd fulfillment-service
uv run dev.py build protos
git add proto/public/ internal/api/
cd ../osac-operator
buf generate && make lint
git add internal/api
cd ../osac-metering/metering-service
make generate lint
git add internal/api
```

### Public Proto Still Contains Private Fields

Check that you:
1. Used the correct annotation: `[(cleanapi.field).private = true]`
2. Imported `cleanapi/annotations.proto` at the top of the file
3. Ran `uv run dev.py build protos` (not just `buf generate`)

## Advanced Topics

### Updating the CleanAPI Version

The cleanapi version is defined in `dev/tools.py`:

```python
PROTOC_GEN_CLEANAPI = ToolDefinition(
    name="protoc-gen-cleanapi",
    version="v1.2.3",  # Update this version
    # ...
)
```

After updating:

```bash
cd fulfillment-service
uv run dev.py build sync-cleanapi-version
```

This propagates the version to `buf.yaml`, `buf.gen.yaml`, and updates `buf.lock`.

For the complete cleanapi specification, see the [cleanapi documentation](https://buf.build/cleanapi/cleanapi).

## See Also

- [AGENTS.md](../AGENTS.md) - Build commands and workflow
- [API.md](API.md) - API design conventions
- [protoc-gen-cleanapi documentation](https://buf.build/cleanapi/cleanapi)
- [protoc-gen-cleanapi repo](https://github.com/jhernand/protoc-gen-cleanapi)
