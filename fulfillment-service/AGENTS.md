# AGENTS.md

This file provides guidance to AI coding agents when working with code in this repository.

## Where to Find Information

This file contains only frequently-needed commands and non-obvious rules. For detailed information:

- **Setup & local development**: [README.md](README.md)
- **API design conventions**: [docs/API.md](docs/API.md) - Includes public/private API design rules
- **CleanAPI workflow guide**: [docs/CLEANAPI.md](docs/CLEANAPI.md) - How to edit private protos, workflows, troubleshooting
- **Authentication & authorization**: [docs/AUTH.md](docs/AUTH.md)
- **Installation & deployment**: [docs/INSTALL.md](docs/INSTALL.md)
- **Console access architecture**: [docs/VM_CONSOLE.md](docs/VM_CONSOLE.md)
- **Database patterns**: See examples in `internal/database/migrations/*.up.sql` for:
  - Materialized helper tables with triggers (cross-object constraints)
  - Backfill patterns (`update table set data = data`)
  - Custom SQLSTATE error codes (map in `internal/database/dao/*_errors.go`)
- **Server patterns**: See `internal/servers/*_server.go` for:
  - Public/private server delegation
  - Builder pattern for server configuration
- **Testing patterns**: See `*_suite_test.go` files for Ginkgo/Gomega setup
- **CLI design guidelines**: [`internal/cmd/cli/.claude/rules/cli-ux.md`](internal/cmd/cli/.claude/rules/cli-ux.md)
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

# Proto: full build pipeline (public from private, lint, generate)
uv run dev.py build protos

# Proto: incremental - just generate Go code (skip public proto generation)
buf generate

# Proto: just lint
uv run dev.py lint proto

# Run all tests including integration (requires kind cluster)
ginkgo run -r
```

### Integration Tests

```bash
# Create Kind cluster + deploy infrastructure
make -C ../osac-installer install-infra PLATFORM=kind PROFILE=dev NS=osac

# Build image, deploy fulfillment-service via osac chart, run tests
make -C ../osac-installer test PLATFORM=kind PROFILE=dev NS=osac SUITE=fulfillment

# Clean up
make -C ../osac-installer uninstall PLATFORM=kind PROFILE=dev NS=osac
```

Requires `/etc/hosts` entries:
- `127.0.0.1 keycloak.keycloak.svc.cluster.local`
- `127.0.0.1 fulfillment-api.osac.svc.cluster.local`
- `127.0.0.1 fulfillment-internal-api.osac.svc.cluster.local`

### Linting and Code Generation

```bash
# Lint Go code (from fulfillment-service/)
uv run dev.py lint

# Lint proto files (from fulfillment-service/)
uv run dev.py lint proto

# Full proto build pipeline (from fulfillment-service/)
# Generates public from private, lints, and generates Go code
uv run dev.py build protos

# Incremental: just generate Go code from proto (from fulfillment-service/)
# Skips public proto generation
buf generate

# Regenerate mocks (from fulfillment-service/)
go generate ./...

# Python linting (from fulfillment-service/)
uv run ruff check

# Tidy Go modules (from fulfillment-service/)
go mod tidy
```

**CRITICAL**: After any `.proto` file change:
- **ONLY edit files in `proto/private/`** — `proto/public/` is fully generated and will be overwritten
- **From `fulfillment-service/` directory**: Run `uv run dev.py build protos` (generates public API from private API, lints, and generates Go code)
- Or run `buf generate` for incremental builds (skips public proto generation)
- **Commit all three changes**: private protos, generated public protos (`proto/public/`), and generated Go code (`internal/api/`)

**CI Enforcement**: The `check-generated-code.yaml` workflow runs on every PR that touches proto files. To avoid failures:
1. Change to `fulfillment-service/` directory
2. Run `uv run dev.py build protos` locally
3. Review and commit all three changes (private protos, public protos, Go code)

**Proto Workflow**: The public API is generated from private protos using protoc-gen-cleanapi. Fields/messages marked with cleanapi annotations are filtered out. The cleanapi dependency is exported to `.buf/deps/cleanapi` (gitignored).

For detailed guidance on using cleanapi annotations, field numbering best practices, and common workflows, see [docs/CLEANAPI.md](docs/CLEANAPI.md).

**Updating protoc-gen-cleanapi version**:
The cleanapi version is defined once in `dev/tools.py` (`PROTOC_GEN_CLEANAPI.version`). When you update it there, run `uv run dev.py build sync-cleanapi-version` to propagate the version to `buf.yaml` and `buf.gen.yaml`, and automatically sync `buf.lock` via `buf dep update` (which runs unconditionally to handle fresh checkouts and failed prior updates). The proto build command (`uv run dev.py build protos`) does this automatically.

Buf and protoc are installed separately - see the [buf installation guide](https://buf.build/docs/installation) and install protoc with `brew install protobuf` on macOS.

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
- `internal/testing/` - Test utilities (test server, database helpers)
- `it/` - Integration tests
- `charts/` - Helm charts

### Proto Structure

Protos are split into public and private APIs under `proto/`:

```text
proto/private/osac/private/v1/        - EDITABLE: Admin/controller API (source of truth, full CRUD + Signal RPC)
proto/tests/osac/tests/v1/            - EDITABLE: Test-only proto definitions
proto/public/osac/public/v1/          - GENERATED: User-facing API (never edit manually)
```

**IMPORTANT**: Only edit `.proto` files in `proto/private/` and `proto/tests/`. The `proto/public/` directory is fully generated and must never be edited manually.

Each resource has `<resource>_type.proto` (message definitions) and `<resource>s_service.proto` (RPC methods). Generated Go code lands in `internal/api/osac/{public,private}/v1/`.

**Public from Private**: The public API is generated from private API using protoc-gen-cleanapi. Fields/messages marked with `[(cleanapi.field).private = true]` are filtered out. Run `uv run dev.py build protos` to regenerate public protos after changing private protos with cleanapi annotations.

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

- **After proto changes**: See [Linting and Code Generation](#linting-and-code-generation). **From `fulfillment-service/`**, run `uv run dev.py build protos` and commit all generated code changes.
- **After Go module changes**: When `go.mod` is edited, run `go mod tidy`.
- **Before committing**: `buf lint` (via `uv run dev.py lint proto`) and the Go linter (via `uv run dev.py lint go`) run automatically as pre-commit hooks — see the `fulfillment-service-*` hooks in the root-level `.pre-commit-config.yaml` (there is no component-local `.pre-commit-config.yaml` anymore) — so there is no need to remember to run them manually, though you still can with `uv run dev.py lint`.
- **Before creating a PR**: Run `gofmt -s -w .` (auto-formats, then fails if any files changed — commit the fixes first), `uv run dev.py lint proto`, and `ginkgo run -r internal`.
- **On every PR**: CI runs `check-generated-code.yaml` to verify generated code is up to date. If it fails, regenerate code locally (see above) and commit the changes.

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
the full set of conventions and rules for the API, including:
- **Public and private APIs** (field numbering, cleanapi annotations, documentation requirements)
- Object structure, naming, services
- Request/response patterns, REST transcoding
- Enums, conditions, object references

OSAC follows [Kubernetes API conventions](https://github.com/kubernetes/community/blob/main/contributors/devel/sig-architecture/api-conventions.md) adapted for protobuf.

For practical workflows when editing protos, see [docs/CLEANAPI.md](docs/CLEANAPI.md).

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

As with any proto change, run `uv run dev.py lint proto && buf generate` afterward (see [Linting and Code Generation](#linting-and-code-generation)).

## Common Pitfalls

- **Editing `proto/public/` instead of `proto/private/`** — public protos are fully generated and your changes will be overwritten. Always edit `proto/private/` and run `uv run dev.py build protos` to regenerate public protos.
- **Forgetting to commit generated code** — after editing `proto/private/`, you must commit changes to both `proto/public/` and `internal/api/`. The CI check will fail if generated code is missing.
- `SERVICE_SUFFIX` lint rule is intentionally excluded in `buf.yaml`
- Unit tests: run `ginkgo run -r internal` (not `ginkgo run -r`) to avoid triggering integration tests
- CI timeout: 1 hour for unit and integration test runs
- Platform-gated tests (`//go:build darwin`, e.g. `internal/config/config_secret_store_darwin_test.go`) are skipped by `ginkgo run -r internal` on non-macOS machines and in the default Linux CI; they run separately in `.github/workflows/darwin-keychain-tests.yml` on a `macos-latest` runner

See [Linting and Code Generation](#linting-and-code-generation) for the required `uv run dev.py build protos` step, and [Files Requiring Extra Caution](#files-requiring-extra-caution) for generated paths that must never be hand-edited.

## Files Requiring Extra Caution

### Never Edit Manually

- `proto/public/` - fully generated from `proto/private/` by `uv run dev.py build protos`
- `internal/api/` - fully generated by `buf generate` from proto files
- `go.sum` - managed by `go mod tidy`
- `*_mock.go` files - generated by `mockgen` via `//go:generate` directives
- `dist/` - build artifacts from goreleaser (created only during `goreleaser build`, not committed to repository)

### Verify Before Changing

- `charts/` - maintained Helm chart sources, not generated; call out the change explicitly in the PR description so a reviewer from [OWNERS](OWNERS) can confirm it's intentional
- `proto/private/**/*.proto` - changes cascade to generated code in `proto/public/` and `internal/api/` (see [Linting and Code Generation](#linting-and-code-generation))
- `internal/database/migrations/*.up.sql` - existing migrations must never be modified; only add new numbered files
- `.goreleaser.yaml`, `buf.yaml`, `buf.gen.yaml` - infrastructure config; call out the change explicitly in the PR description so a reviewer from [OWNERS](OWNERS) can confirm it's intentional (pre-commit/yamllint config now lives in the root-level `.pre-commit-config.yaml`/`.yamllint.yaml`, not here)

## Common Fix Locations

| Bug pattern | File(s) to check |
|-------------|-----------------|
| Public API missing field (Create/Update not persisting a field) | `internal/servers/*_server.go` — `Create()` and `Update()` methods (shared path is `GenericServer`) |
| Table rendering missing or incorrect column | `internal/rendering/tables/*.yaml` — table definition files |

## OpenShift Deployment

Helm is the install path. Prerequisites are cert-manager, PostgreSQL 18+, and Keycloak
(any install of each, as long as the service is configured to talk to them). The full
OpenShift procedure — including enabling HTTP/2 — is in [`docs/INSTALL.md`](docs/INSTALL.md).
That file is also linked from [Where to Find Information](#where-to-find-information).
