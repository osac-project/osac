# Architecture — Cross-Component

This is a hand-trimmed excerpt covering only cross-component concerns that
span multiple `osac/` components (`fulfillment-service`, `osac-operator`,
`bare-metal-fulfillment-operator`) and have no equivalent in any single
component's own `AGENTS.md`. See [README.md](README.md) for provenance and
maintenance notes. Component-specific architecture (code organization,
layers, patterns internal to one component) is authoritative in that
component's own `AGENTS.md` — see `fulfillment-service/AGENTS.md`'s
`## Architecture` section and `osac-operator/AGENTS.md`'s `## Architecture`
section.

## Data Flow

**Create Resource (Client to Fulfillment):**

1. Client calls gRPC/REST endpoint on public server (e.g., CreateCluster)
2. Public server maps request to private API representation
3. Private server applies tenancy/attribution metadata
4. Generic server validates and creates database record
5. Success response returned to client
6. Notifier broadcasts change event if configured

**Resource Provisioning (Fulfillment to Operator):**

This spans two separate control loops in two separate processes/binaries —
fulfillment-service's internal reconciler projects DB state into a
Kubernetes CR; osac-operator's controller-runtime controller, watching that
CR independently, is what actually drives provisioning:

1. fulfillment-service's own internal `Controller (Fulfillment)` (a
   separate process from the gRPC/REST servers) polls its own private API
   via the generic `Reconciler[O]` framework (`internal/controllers/reconciler.go`)
2. The reconciler calls the resource type's generic `Get`/`List` RPC
   (discovered via protobuf reflection — not a resource-specific method
   like `GetCluster`) with filter/watch expressions
3. Fulfillment service returns the resource with its current DB-backed
   spec/status; the reconciler's per-resource task then creates or patches
   the corresponding Kubernetes custom resource on the hub cluster (e.g.,
   `cluster`'s task builds and applies a `ClusterOrder` CR) — this is the
   boundary between the two control loops
4. Separately, osac-operator's Kubernetes controller (controller-runtime,
   a different binary/process — see Operator Controllers below) watches
   that CR and reconciles spec vs status (checks conditions)
5. If action needed, the operator controller triggers a provisioning provider
6. Provider (AAP) executes Ansible jobs or webhooks

**Provisioning Feedback (Operator to Fulfillment):**

1. Controller receives event from provisioning provider (job completion, status)
2. Feedback controller sends Signal RPC to fulfillment service private API
3. Private server updates resource status, conditions, and finalizers
4. Status change persists to database
5. Public server reflects updated status when queried

**State Management:**

- Desired state: Stored in resource Spec
- Observed state: Stored in resource Status (conditions, phase, last-observed-generation)
- Metadata: creation_timestamp, deletion_timestamp, labels, annotations
- Controller duty: Reconcile observed state toward desired state
- Database as source of truth: Controllers read from DB via gRPC, write back via Signal RPC
- Tenancy enforcement: see Cross-Cutting Concerns → Multi-tenancy, below

## Entry Points

**fulfillment-service binary:**
- Location: `fulfillment-service/cmd/fulfillment-service/main.go` → `internal/cmd/service/Root()`
- Triggers: Service start commands (grpc-server, rest-gateway, controller, console-proxy), dev/probe commands
- Responsibilities: Service initialization, subcommand routing, logging setup

**gRPC Server:**
- Location: `fulfillment-service/internal/cmd/service/start/grpcserver/`
- Triggers: `fulfillment-service start grpc-server` CLI command
- Responsibilities: Listen on gRPC port, register all service implementations, set up interceptors (panic recovery, metrics, logging, auth, transactions)

**REST Gateway:**
- Location: `fulfillment-service/internal/cmd/service/start/restgateway/`
- Triggers: `fulfillment-service start rest-gateway` CLI command
- Responsibilities: Translate HTTP/JSON to gRPC; proxy requests to gRPC server; serve OpenAPI specs

**Controller (Fulfillment):**
- Location: `fulfillment-service/internal/cmd/service/start/controller/`
- Triggers: `fulfillment-service start controller` CLI command
- Responsibilities: Run reconcilers for in-process resource monitoring and feedback
- Controllers: 18 resource-specific controllers in `internal/controllers/` covering baremetalinstance, cluster, computeinstance, externalip, externalipattachment, externalippool, identityprovider, natgateway, onboarding (tenant onboarding tasks — a separate reconciler from `tenant` below), project, projectmembership, role, rolebinding, securitygroup, subnet, tenant, user, virtualnetwork, plus shared finalizer management

**Console Proxy (Fulfillment):**
- Location: `fulfillment-service/internal/cmd/service/start/consoleproxy/`
- Triggers: `fulfillment-service start console-proxy` CLI command
- Responsibilities: Issues and validates console-session tickets, proxies gRPC/WebSocket console traffic toward the management cluster's console proxy (see `fulfillment-service/docs/VM_CONSOLE.md` for the full request path); runs as its own Kubernetes Deployment, separate from the gRPC/REST/controller processes

**osac-operator binary:**
- Location: `osac-operator/cmd/main.go`
- Triggers: Kubernetes operator deployment (helm/kustomize)
- Responsibilities: Initialize multicluster manager, register controllers, set up gRPC client to fulfillment service, start reconciliation loops

**Operator Controllers (osac-operator):**
- Location: `osac-operator/internal/controller/`
- Pattern: Most resource types have a primary controller and a feedback controller, both built on a shared generic `feedback.Bridge[T,R]` abstraction. Most feedback controllers get their own dedicated, per-resource-named file (e.g., `computeinstance_controller.go` + `computeinstance_feedback_controller.go`). `ClusterOrder` also has both — its feedback controller (type `FeedbackReconciler`, registered as `clusterorder-feedback`, sends the real gRPC `Signal` RPC to fulfillment-service) predates that per-resource naming convention and lives in the generically-named `feedback_controller.go` instead of a `clusterorder_feedback_controller.go` file; `clusterorder_controller.go`'s own `Status().Patch` call is a separate, in-cluster Kubernetes status write, not the feedback signal. Notable exceptions to the pairing itself, verified directly against every `Named(...)` controller registration in the package (not necessarily exhaustive if new controllers are added later): BaremetalInstance has a feedback controller only (no primary controller file in this package); Storage, Tenant, and NetworkClassCapabilities each have a standalone primary controller with no feedback pair.
- See `osac-operator/AGENTS.md`'s "Resources Managed" and "Dual-Controller Pattern" sections for the per-resource-type controller list and behavior.
- Triggers: Resource created/updated in Kubernetes or fulfillment service
- Responsibilities: Reconcile spec vs status, trigger provisioning providers, send feedback signals

**CLI binary:**
- Location: `fulfillment-service/cmd/osac/main.go` → `internal/cmd/cli/Root()`
- Triggers: Manual CLI invocation for cluster/host/compute instance management
- Responsibilities: Provide kubectl-like CLI interface, call fulfillment service gRPC APIs

**Console Proxy (Operator):**
- Location: `osac-operator/cmd/console-proxy/main.go`
- Triggers: Deployed as a Kubernetes aggregated API server alongside the operator
- Responsibilities: Proxies KubeVirt VM console/VNC access; see `osac-operator/AGENTS.md`'s "Console Proxy" section for implementation details (auth, discovery, subresource routing, TLS config)

**bare-metal-fulfillment-operator:**
- Location: `bare-metal-fulfillment-operator/`
- CRD types: BareMetalInstance, BareMetalPool (`api/v1alpha1/`)
- Controllers: `baremetalinstance_controller.go`, `baremetalpool_controller.go` (`internal/controller/`)
- Triggers: BareMetalInstance/BareMetalPool resources created/updated in Kubernetes
- Responsibilities: Orchestrates bare-metal host provisioning via Metal3 integration; manages pool-based allocation of bare-metal hosts

## Error Handling

**Strategy:** Hierarchical error wrapping with context preservation; gRPC status codes mapped to domain errors.

**Patterns:**

- DAO errors: Wrapped with table/operation context (e.g., "failed to create cluster in table 'clusters'")
- Server errors: Translated to gRPC status codes (NotFound → Code.NOT_FOUND, AlreadyExists → Code.ALREADY_EXISTS)
- Controller errors: Logged with full context; reconciliation retried with exponential backoff
- Auth errors: Return Unauthenticated or PermissionDenied gRPC codes; tenancy violations logged
- Database transaction errors: Automatically rolled back; client receives clear error message

## Cross-Cutting Concerns

**Logging:** `fulfillment-service` uses `slog` (Go's structured logging), with each layer logging context (request ID, resource ID, tenant ID) and configuration via CLI flags (`--log-level`, `--log-format`); the Kubernetes operators (`osac-operator`, `bare-metal-fulfillment-operator`) and `osac-metering` use `zap` via controller-runtime's/`logr`'s logging interface instead

**Validation:** Protocol Buffer field presence/constraints at message definition; server-side validation before storage (see `fulfillment-service/AGENTS.md`'s Database Layer section for CEL-based query filtering)

**Authentication:** JWT token extraction from gRPC metadata; OAuth2 token file reading for service-to-service auth; token verification delegated to Keycloak

**Multi-tenancy:** Tenant ID stored in resource annotations (`osac.openshift.io/tenant`); all database queries filtered by tenant automatically; OPA policies enforce isolation at authorization layer

**Observability:**
- Metrics: Prometheus instrumentation at gRPC interceptor level; controller reconciliation metrics tracked
- Health checks: gRPC health check protocol; Kubernetes liveness/readiness probes
- Events: Database change events published via Notifier; watch streams propagate changes to controllers

**Transactions:** gRPC interceptor wraps each RPC in database transaction; automatic rollback on error; ensures consistency across CRUD operations
