# AGENTS.md

This file provides guidance to AI coding agents when working with code in this repository.

## Where to Find Information

This file contains only frequently-needed commands and non-obvious rules. For detailed information:

- **Setup & local development**: [README.md](README.md)
- **API design conventions**: [docs/API.md](docs/API.md)
- **Authentication & authorization**: [docs/AUTH.md](docs/AUTH.md)
- **Installation & deployment**: [docs/INSTALL.md](docs/INSTALL.md)
- **Database patterns**: See examples in `internal/database/migrations/*.up.sql` for:
  - Materialized helper tables with triggers (cross-object constraints)
  - Backfill patterns (`update table set data = data`)
  - Custom SQLSTATE error codes (map in `internal/database/dao/*_errors.go`)
- **Server patterns**: See `internal/servers/*_server.go` for:
  - Public/private server delegation
  - Builder pattern for server configuration
- **Testing patterns**: See `*_suite_test.go` files for Ginkgo/Gomega setup
- **Dev tooling**: [dev/README.md](dev/README.md) for extending `dev.py`
- **Linter configuration**:
  - Go: `.golangci.yml`
  - Python: `pyproject.toml`
  - YAML: root-level `.yamllint.yaml` (no component-local copy anymore)

**Before planning or implementing any change, read every document listed above that is
relevant to the area you are working in.** Do not rely solely on existing source code as a
reference -- the documents above describe design intent and conventions that are not always
obvious from the code alone. Skipping them leads to subtle bugs and convention violations.

## Overview

The fulfillment-service is a gRPC server with REST gateway for managing infrastructure resources
(clusters, hosts, compute instances, networking). It uses PostgreSQL for storage, OPA for
authorization, and supports Kubernetes deployment via Helm.

## Build and Test Commands

```bash
# Build binaries
go build ./cmd/fulfillment-service
go build ./cmd/osac

# Run unit tests only (excludes integration tests in it/)
ginkgo run -r internal

# Run a specific package's tests
ginkgo run internal/servers

# Run tests matching a name pattern
ginkgo run -r internal --focus="CreateCluster"

# Run tests with verbose output
ginkgo run -v internal/servers

# Skip tests matching a pattern
ginkgo run -r internal --skip="database"

# Lint
uv run dev.py lint

# Proto: lint and generate
buf lint
buf generate

# Run all tests including integration (requires kind cluster)
ginkgo run -r
```

### Integration Tests

```bash
# Run integration tests (creates a kind cluster)
ginkgo run it

# Preserve cluster for debugging
IT_KEEP_KIND=true ginkgo run it

# Run only setup (create cluster without tests)
IT_KEEP_KIND=true ginkgo run --label-filter=setup it

# Clean up preserved cluster
kind delete cluster --name fulfillment-service-it
```

Requires `/etc/hosts` entries:
- `127.0.0.1 keycloak.keycloak.svc.cluster.local`
- `127.0.0.1 fulfillment-api.osac.svc.cluster.local`
- `127.0.0.1 fulfillment-internal-api.osac.svc.cluster.local`

### Linting and Code Generation

```bash
# Lint Go code
uv run dev.py lint

# Lint proto files
buf lint

# Generate Go code from proto
buf generate

# Regenerate mocks
go generate ./...

# Python linting
uv run ruff check

# Tidy Go modules
go mod tidy
```

**CRITICAL**: Always run `buf lint && buf generate` after any `.proto` file change. Generated code lands in `internal/api/` (never edit manually). Buf is installed via `buf-action` in CI (see the root-level `.github/workflows/check-pull-request.yaml`); for local use, install buf separately following the [official installation guide](https://buf.build/docs/installation).

For extending `dev.py` with new commands, see [dev/README.md](dev/README.md).

### Running Locally

See [README.md](README.md) for instructions on running the service locally, including PostgreSQL setup and starting the gRPC server and REST gateway.

## Development Tooling

Development and build tasks are automated through the `dev.py` script, which is run with `uv run
dev.py`. When a new task needs to be automated (for example building, formatting, generating code,
running tests with specific options, or installing a tool), refer to [dev/README.md](dev/README.md).

## Architecture

### Code Organization

- `cmd/fulfillment-service/` - Service binary entry point (calls `internal/cmd/service.Root()`)
- `cmd/osac/` - CLI binary entry point (calls `internal/cmd/cli.Root()`)
- `internal/cmd/service/start/` - Server startup commands (grpcserver, restgateway, controller)
- `internal/servers/` - gRPC service implementations (one `*_server.go` per resource)
- `proto/` - Protocol Buffer definitions (public/private/tests)
- `internal/api/` - Generated Go code from protobuf (see [Files Requiring Extra Caution](#files-requiring-extra-caution))
- `internal/database/` - PostgreSQL access layer with generic DAO
- `internal/database/dao/` - Generic type-safe DAO (`GenericDAO[O Object]`)
- `internal/database/migrations/` - SQL migration files
- `internal/auth/` - Authentication, tenancy, and attribution logic
- `internal/controllers/` - Kubernetes controllers
- `internal/testing/` - Test utilities (test server, database helpers, kind helpers)
- `it/` - Integration tests
- `charts/` - Helm charts

### Proto Structure

Protos are split into public and private APIs under `proto/`:

```text
proto/public/osac/public/v1/   - User-facing API (read-heavy, limited write)
proto/private/osac/private/v1/ - Admin/controller API (full CRUD + Signal RPC)
proto/tests/osac/tests/v1/     - Test-only proto definitions
```

Each resource has `<resource>_type.proto` (message definitions) and `<resource>s_service.proto` (RPC methods). Generated Go code lands in `internal/api/osac/{public,private}/v1/`.

### Server Implementation Pattern

Public servers delegate to private servers and add tenant/auth logic:
- `ClustersServer` (public) wraps `PrivateClustersServer` (private)
- Builder pattern: `ClustersServerBuilder` configures dependencies
- Both server files live in `internal/servers/` (e.g., `clusters_server.go` + `private_clusters_server.go`)

### Database Layer

Uses `pgx/v5` with a generic DAO pattern:
- `GenericDAO[O Object]` provides type-safe CRUD for any protobuf message
- Resources stored as JSON-serialized protobuf in a `data` column
- Standard columns: `id`, `name`, `creation_timestamp`, `deletion_timestamp`, `finalizers`, `creator`, `tenant`, `labels`, `annotations`, `data`
- CEL filter expressions translated to SQL WHERE clauses via `FilterTranslator`
- Migrations in `internal/database/migrations/` (numbered `*.up.sql` files)

### gRPC Interceptor Chain

The gRPC server uses chained interceptors (configured in `internal/cmd/service/start/grpcserver/`):
1. Panic recovery
2. Prometheus metrics
3. Structured logging (slog)
4. Authentication (JWT validation)
5. Database transaction management

### Mock Generation

Uses `go.uber.org/mock` (uber-go/mock). Mocks are generated with `//go:generate mockgen` directives and live alongside source files (e.g., `attribution_logic_mock.go`).

### Testing Pattern

Tests use Ginkgo v2 + Gomega. Typical suite setup in `*_suite_test.go`:
- `BeforeSuite` initializes logger, auth logic, database
- `DeferCleanup` for teardown
- `dao.CreateTables[T]()` dynamically creates test schemas

## Automated Hooks

The following automated checks are configured and should be run at the appropriate times:

- **After proto changes**: See [Linting and Code Generation](#linting-and-code-generation).
- **After Go module changes**: When `go.mod` is edited, run `go mod tidy`.
- **Before committing**: `buf lint` (via `uv run dev.py lint proto`) and the Go linter (via `uv run dev.py lint go`) run automatically as pre-commit hooks — see the `fulfillment-service-*` hooks in the root-level `.pre-commit-config.yaml` (there is no component-local `.pre-commit-config.yaml` anymore) — so there is no need to remember to run them manually, though you still can with `uv run dev.py lint`.
- **Before creating a PR**: Run `gofmt -s -w .` (auto-formats, then fails if any files changed — commit the fixes first), `uv run dev.py lint proto`, and `ginkgo run -r internal`.

`buf lint` includes a custom plugin rule, `OSAC_OBJECT_SHAPE` (implemented in `cmd/buf-plugin-osac-lint/`), which checks that the base message of every resource — the message returned by `Get` and accepted by `Create` — has the standard `id`/`metadata`/`spec`/`status` shape described above. Messages that intentionally deviate from this shape must be marked with a `// buf:lint:ignore OSAC_OBJECT_SHAPE` comment directly above the message declaration.

## CLI Command Help Text

When adding or modifying CLI commands, write help text (both `Short` and `Long` descriptions, as
well as flag help strings) using Markdown. The help system renders Markdown at display time, so the
source strings should use Markdown syntax for emphasis, inline code, code blocks, and similar
formatting.

Because raw backticks would conflict with Go string syntax, use the `{{ bt }}` template function for
inline code and `{{ bt 3 }}` for fenced code blocks.

For flag help, start with a short type hint in italics (e.g. `_[BOOLEAN]_`, `_URL_`,
`_FILE|DIRECTORY_`) followed by a dash and the description.

Do not end `shortHelp` strings with a trailing period — the help template does not append one.

Refer to existing commands such as `internal/cmd/cli/login/login_cmd.go` for style and examples of
how help text is structured.

### Private-API Subcommands

Subcommands that use the private API (`privatev1`) must be annotated so they are hidden from
`--help` when private mode (--private) is disabled or configuration is unavailable. Wrap the `AddCommand` call with
`help.MarkPrivateAPI`:

```go
result.AddCommand(help.MarkPrivateAPI(mysubcommand.Cmd()))
```

Add the subcommand name to the `privateNames` map in the corresponding `*_cmd_test.go` annotation
test (e.g. `create_cmd_test.go`, `describe_cmd_test.go`).

## API Design Guidelines

Before making any API design or implementation decision (adding or modifying `.proto` files,
services, messages, or REST transcoding), read [docs/API.md](docs/API.md). That document contains
the full set of conventions and rules for the API, including object structure, naming, services,
request/response patterns, REST transcoding, enums, conditions, object references, and
documentation requirements.

## Validation Constraints

When adding new proto fields, always include `buf.validate` annotations for any constraints on the field:

- **Required fields**: `[(buf.validate.field).string.min_len = 1]` or `[(buf.validate.field).repeated.min_items = 1]`
- **Format validation**: `pattern` for regex, `email`, `uuid`, etc.
- **Range constraints**: `gte`, `lte`, `gt`, `lt` for numeric fields
- **Map validation**: Use `.map.keys` and `.map.values` for key/value constraints
- **CEL expressions**: Use `[(buf.validate.field).cel = {...}]` for complex field validation
- **Message-level CEL**: Use `option (buf.validate.message).cel = {...}` for cross-field or resource-specific constraints

### Validation Flow

- **Create requests**: Validated by protovalidate interceptor before reaching server handlers
- **Update requests**: Interceptor skips validation; server validates the merged object after applying `update_mask`
  - This prevents false validation errors when clients send partial objects for update
  - Server merges request fields (per mask) with DB object, then validates the complete result

### Resource-Specific Validation

To override embedded message validation (e.g., Projects allowing dots in names while Metadata doesn't):
1. Use `[(buf.validate.field).ignore = IGNORE_ALWAYS]` on the embedded field to skip its standard validation
2. Add message-level CEL to validate the field with resource-specific rules:
   ```protobuf
   option (buf.validate.message).cel = {
     expression: "this.metadata.name == '' || this.metadata.name.split('.').all(...)"
   };
   ```

Do not implement validation in Go code that can be expressed declaratively in proto.

As with any proto change, run `buf lint && buf generate` afterward (see [Linting and Code Generation](#linting-and-code-generation)).

## Common Pitfalls

- `SERVICE_SUFFIX` lint rule is intentionally excluded in `buf.yaml`
- Unit tests: run `ginkgo run -r internal` (not `ginkgo run -r`) to avoid triggering integration tests
- CI timeout: 1 hour for unit and integration test runs
- Integration test logs uploaded as `logs-helm` and `logs-kustomize` artifacts (always, even on failure)

See [Linting and Code Generation](#linting-and-code-generation) for the required `buf lint && buf generate` step, and [Files Requiring Extra Caution](#files-requiring-extra-caution) for generated paths that must never be hand-edited.

## Files Requiring Extra Caution

### Never Edit Manually

- `internal/api/` - fully generated by `buf generate` from proto files
- `go.sum` - managed by `go mod tidy`
- `*_mock.go` files - generated by `mockgen` via `//go:generate` directives
- `dist/` - build artifacts from goreleaser (created only during `goreleaser build`, not committed to repository)

### Verify Before Changing

- `charts/` and `it/charts/` - maintained Helm chart sources, not generated; call out the change explicitly in the PR description so a reviewer from [OWNERS](OWNERS) can confirm it's intentional
- `proto/**/*.proto` - changes cascade to generated code (see [Linting and Code Generation](#linting-and-code-generation))
- `internal/database/migrations/*.up.sql` - existing migrations must never be modified; only add new numbered files
- `.goreleaser.yaml`, `buf.yaml`, `buf.gen.yaml` - infrastructure config; call out the change explicitly in the PR description so a reviewer from [OWNERS](OWNERS) can confirm it's intentional (pre-commit/yamllint config now lives in the root-level `.pre-commit-config.yaml`/`.yamllint.yaml`, not here)
