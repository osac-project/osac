# TODOS

## Installer & Lifecycle Tooling

### Must-gather image scope: single source of truth for collected namespaces/resources

**What:** Before shipping the v1 custom `oc adm must-gather` image (installer/lifecycle design doc), define the collected namespace/resource-type list from a single source of truth instead of a hardcoded list in the image.

**Why:** OSAC has five components today (fulfillment-service, osac-operator, osac-aap, osac-csi-driver, bare-metal-fulfillment-operator) and is still growing. A hardcoded collection list silently drifts as components are added — recreating exactly the kind of undocumented tribal knowledge the installer/lifecycle tooling exists to eliminate.

**Context:** Flagged during `/plan-eng-review` of `~/.gstack/projects/eliorerz-osac/eerez-docs-cross-repo-merge-order-agents-design-20260922-180505.md`. A components manifest (or reuse of an existing enumeration, e.g. something CODEOWNERS-adjacent) that both must-gather image generation and any future component registry could read from would prevent drift. Whoever builds the v1 must-gather image should settle this before hardcoding a list.

**Effort:** S
**Priority:** P2
**Depends on:** Approach A (or B) being implemented for installer/lifecycle tooling

---

### Broaden `--require-secret` coverage beyond the AAP license + primary DB Secrets

**What:** The pre-install-validate hook currently checks `aap.configAsCode.manifestSecret` and every `service.database.connection[].secret.name` entry via `--require-secret`. It doesn't check metering's own DB Secrets (no distinct `metering.database.connection`-shaped value was found in `charts/osac/values.yaml` at implementation time) or the CaaS DNS/Netris credentials (`aap.instanceGroups.*.secret.*`) — those are Secrets the chart *creates from* Helm values at install time, not pre-existing state to discover, so they don't fit the existence-check model the way the AAP license/DB Secrets do.

**Why:** A production metering deployment with a missing DB Secret currently fails partway through `helm install` instead of getting caught by the pre-install gate, same failure mode this whole feature exists to prevent.

**Context:** Surfaced while implementing the data-driven prerequisite matrix (`osac-installer/pkg/install/data/prerequisites.yaml`) and its `SecretRef`/`secretref` plumbing (`osac-installer/pkg/install/engine_secret.go`, `fulfillment-service/internal/cmd/cli/install/secretref/`). Confirm the real metering Secret value path (subchart values, not the umbrella chart's own `values.yaml`) before adding it.

**Effort:** S
**Priority:** P3
**Depends on:** None

---

### Pull secret validity, network reachability, and apps-domain checks

**What:** PR #821's prerequisite matrix (`docs/guides/installation/customer-install-guide.md`) also lists a valid cluster pull secret (`registry.redhat.io`/`quay.io` auth), outbound reachability to `github.com`/`ghcr.io`/`quay.io`/`registry.redhat.io`, and a resolvable apps domain (`oc get ingresses.config/cluster`) as prerequisites. None of these are covered by the new checks matrix.

**Why:** These are exactly the kind of failure that surfaces as a confusing mid-install error (an ImagePullBackOff 20 minutes into `helm install`) instead of a clear pre-flight message.

**Context:** Deliberately deferred when building the data-driven prerequisite matrix (see Completed below) — each needs a genuinely different check engine (a real registry-auth probe, a DNS/HTTP reachability probe), not a variation on `crd-exists`/`csv-succeeded`.

**Effort:** M
**Priority:** P3
**Depends on:** None

---

### Reconcile `osac-installer/prerequisites/<operator>/*.yaml` with the checks matrix

**What:** `osac-installer/prerequisites/{cert-manager,cnv,lvms,mce,metallb}/*.yaml` are raw install manifests (Subscription/OperatorGroup/CR) for the dev-install path, and independently encode the same namespace/channel facts now also captured in `osac-installer/pkg/install/data/prerequisites.yaml`.

**Why:** Two places that can drift apart, even though the current channels/namespaces were cross-checked against `charts/osac-deps/templates/*.yaml` when the matrix was built (2026-09-24) and matched.

**Context:** Flagged, not acted on, while building the checks matrix — different shape (raw K8s manifests vs. structured validation metadata) and different purpose (installing vs. checking), so unifying them is a separate, larger refactor of the dev-install path, not a corollary of this feature.

**Effort:** M
**Priority:** P3
**Depends on:** None

---

## Completed

### Validate → install gap (TOCTOU) between `osac install validate` and `helm install`

**What:** `osac install validate` could report a Hub cluster as install-ready, but nothing re-checked that state immediately before `helm install` actually ran.

**Why:** State can drift between validate and install (another process touches the cluster, a storage class gets removed, a license expires). If it does, Helm fails partway through with the exact kind of confusing partial-install state this feature exists to prevent. This is a classic time-of-check-to-time-of-use gap.

**Context:** Flagged during `/plan-eng-review` of `~/.gstack/projects/eliorerz-osac/eerez-docs-cross-repo-merge-order-agents-design-20260922-180505.md`.

**Effort:** S (mostly reusing existing hook wiring, once `osac install validate` exists)
**Priority:** P2
**Depends on:** Approach A (or B) being implemented for installer/lifecycle tooling

**Completed:** 2026-09-24 — `osac-installer/charts/osac/templates/hooks/pre-install-validate.yaml`'s hand-written `kubectl` shell script was replaced with the exact `osac install validate` command a human runs manually, using the `fulfillment-service` image's already-bundled `osac` binary (`.Values.service.images.service`, not a new image) so there is exactly one implementation of every check, not two. Services/toggles are forwarded as CLI flags computed from values Helm already has (`osac.enabledServices` in `_helpers.tpl`, `--metal3`, `--require-secret`), so the hook never needs its own copy of prerequisite knowledge. See the "Data-driven prerequisite validation matrix" work below for the full scope.

---

### Data-driven prerequisite validation matrix

**What:** Replace the one-Go-file-per-operator checks (`checks_certmanager.go`, `checks_metal3.go`, `checks_storageclass.go`) and the Helm hook's separate, duplicate bash implementation of the same 2 checks with a single declarative matrix consumed by both.

**Why:** `osac install discover`/`validate` only checked 2 things by default; PR #821's audit (`docs/guides/installation/customer-install-guide.md`) documents OSAC actually depending on ~9 platform Operators, an OCP version range, and pre-existing Secrets. Growing the old pattern to that size would mean 9 more Go files *and* 9 more bash blocks per addition.

**Context:** `~/.claude-personal/plans/jaunty-riding-giraffe.md` has the full design (superseded in one respect during implementation: Secret prerequisites turned out to have deployer-configurable names, not fixed ones — see below).

**Effort:** L
**Priority:** P1
**Depends on:** None

**Completed:** 2026-09-24 —
- `osac-installer/pkg/install/data/prerequisites.yaml` (embedded via `go:embed`): one entry per prerequisite — Operator name/namespace/OLM package/minimum version, CRD names, OCP version floor — cross-checked against `osac-installer/charts/osac-deps/templates/*.yaml` and `docs/guides/installation/customer-install-guide.md` Tables 2.1/3.1. `matrix_test.go` self-checks every entry against the closed kind/requiredFor/toggle vocabulary, so a bad entry fails CI, not a live cluster run.
- Five generic engines (`engine_crd.go`, `engine_csv.go`, `engine_resource_field.go`, `engine_storageclass.go`, `engine_ocpversion.go`) replace the three deleted per-operator files; `registry.go` is now a matrix loader/dispatcher, not a hand-written check list. `CheckOptions` generalized from a single `Metal3Enabled bool` to `Services []string` + `Metal3 bool`.
- **Deviation from the approved plan:** the plan's static `secret-exists` matrix entries (hardcoded `osac-db-config`, `config-as-code-manifest-ig`, etc.) turned out to be wrong — `service.database.connection[].secret.name` and `aap.configAsCode.manifestSecret` are deployer-configurable Helm values, not fixed names. Replaced with a runtime-parameterized `SecretChecks([]SecretRef)` (`engine_secret.go`) and a `--require-secret NAMESPACE/NAME[:KEY,...]` CLI flag (parsed by the new `fulfillment-service/internal/cmd/cli/install/secretref` package); the Helm hook fills it in from its own already-rendered values, so the exact configured name is what's checked, not a guess.
- `client.go`: `LoadClients` now falls back to `rest.InClusterConfig()` when no kubeconfig resolves (needed to run unattended inside the hook's Job pod), and distinguishes three failure cases with clear messages instead of one generic wrapped clientcmd error — including a new `ErrNoClusterConfig` sentinel for "no cluster configuration found at all" (added at explicit request after reviewing the initial plan).
- `--services` (default: every service) and `--require-secret` added to both `discover`/`validate`; the Helm hook passes `--services` computed from `global.services.*.enabled`/`metering.enabled` (`osac.enabledServices` in `_helpers.tpl`) so it never false-fails on a service the install never enabled.
- Hook RBAC split by sensitivity: the existing `ClusterRole` widened to a static, unconditional read-only rule set (CRDs, StorageClasses, ClusterServiceVersions, ClusterVersion, Provisioning) so a future matrix entry never needs a chart change; a new namespaced `Role`/`RoleBinding`, get-only and `resourceNames`-scoped to exactly the Secret names the same values compute, handles Secrets separately since they're more sensitive than CRD/CSV status.
- Verified: `make test` (81 specs across `osac-installer/pkg/install` and the CLI's `install/*` packages) and `make lint` (0 issues) both green; full `fulfillment-service` module `go test ./...` (including `it/`) passes; `helm template`/`helm lint` on `charts/osac` with the `vmaas-ci`/`caas-ci`/`bmaas-ci` profiles confirmed the rendered Job args, RBAC `resourceNames`, and the `validation.enabled=false` no-op guard all behave as designed.

### Set up `osac-installer/pkg/install/` for install checks

**What:** Determine which repo should own the `pkg/install/` discovery/validation checks library before implementing Approach A, then build it.

**Why:** `fulfillment-service` owns the CLI; `osac-installer` owns the prerequisite/chart domain knowledge the checks actually need. A nested-Go-module version of this was tried, reverted (evidence showed the cited precedent `osac-operator/api` has no working lint or unit-test CI coverage), then reinstated deliberately — built correctly this time so it doesn't repeat that gap. See History.

**Context:** See Decision ledger R2 (and its full History — 4 passes) in `~/.gstack/projects/eliorerz-osac/eerez-docs-cross-repo-merge-order-agents-design-20260922-180505.md`.

**Effort:** S
**Priority:** P1
**Depends on:** None

**History:** Pass 1 (2026-09-22): deferred as "investigate before deciding." Pass 2 (2026-09-24): proposed as a nested Go module in `osac-installer`, citing `osac-operator/api` as precedent. Pass 3 (2026-09-24, same day): reverted to `fulfillment-service/internal/install/` after finding `osac-operator/api` itself has no working lint (`make -C osac-operator lint-fix` never descends into its separate `go.mod`) or unit-test CI coverage, only a hand-wired `go build` line in `codeql.yml`. Pass 4 (2026-09-24): reinstated as a nested module in `osac-installer` at explicit request to build it "as it should be in the final step" — this time with the CI gap actually closed (see Completed below), not just noted.

**Completed:** v0-unreleased (2026-09-24) — built at `osac-installer/pkg/install/` (checks package: `client.go`, `check.go`, `checks_certmanager.go`, `checks_storageclass.go`, `checks_metal3.go`, `registry.go`, `report.go`, own `go.mod`, own `.golangci.yml`, own `Makefile` using the repo's shared `tools/golangci-lint.mk`) and `fulfillment-service/internal/cmd/cli/install/` (`osac install discover`/`validate` Cobra commands, wired into `root_cmd.go`, plus `AGENTS.md` documenting the deliberate exception to this CLI's tenant-facing principle). Full CI wiring, verified not just written:
- `go.work`: added to the `use` list.
- `fulfillment-service/go.mod`: `require` (pseudo-version, no tagging process) + `replace ../osac-installer/pkg/install` — confirmed both are needed, not `go.work` alone, for the scoped GoReleaser release build.
- `.pre-commit-config.yaml`: new `osac-installer-pkg-install-golangci-lint` hook running `make -C osac-installer/pkg/install lint-fix` — `cd`s into the module's own directory first, unlike `osac-operator-golangci-lint`'s hook, which doesn't. Ran it directly via `pre-commit run` — passed.
- `.github/workflows/codeql.yml`: added a `(cd osac-installer/pkg/install && go build ./...)` line to the manual per-module build-mode list.
- `.github/workflows/unit-tests.yml` + `.github/filters/ci-filters.yml`: added a dedicated `run-osac-installer-pkg-install-tests` job (own path filter, own `ginkgo run -r .`), and added the new path to `unit-tests-fulfillment-service`'s filter too, since fulfillment-service now depends on it.
- `make lint` (real, correctly-pinned golangci-lint via `tools/golangci-lint.mk`, not the too-old local binary that blocked verification in earlier passes): **0 issues**. `make test`: **34/34 specs**. Full workspace build (`go build ./cmd/osac ./internal/...` from `fulfillment-service`) and the CLI wrapper's own 19 specs: all green.
- `go work sync` initially bumped indirect dependency versions across every unrelated workspace module — reverted everywhere except the one unavoidable `golang.org/x/oauth2` bump directly caused by the new dependency in `fulfillment-service/go.mod` itself.

**Not done as part of this item** (tracked separately): porting `pre-install-validate.yaml`'s inline shell into `osac-installer/pkg/install/` (see the TOCTOU-gap TODO above, now unblocked and simpler — both now live in the same repo), `osac install logs`/`upgrade-check` (out of scope per the design doc), and naming a Dev Preview sponsor / first-unattended-installer (still open in the design doc).
