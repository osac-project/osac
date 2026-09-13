# fullsend Review Guide for OSAC

This document provides fullsend-specific context to improve review quality and reduce false positives.

**Related documents**:
- `.claude/rules/architecture-patterns.md` - Architectural invariants and common bug patterns
- `COMPONENT_MAP.md` - Cross-file relationships and where to look for related changes

**How to use this guide**:
1. Start with COMPONENT_MAP.md to identify which files are related to the PR changes
2. Use architecture-patterns.md to check architectural invariants and common bug patterns
3. Use this file (REVIEW_GUIDE.md) to avoid hallucinations and false positives

## Architecture Facts (to prevent hallucinations)

### Protocol Buffers & Code Generation
- **FACT**: OSAC is a mono-repo. All components import proto-generated Go code via Go module paths (`github.com/osac-project/osac/fulfillment-service/pkg/api/...`), NOT through a Buf Schema Registry or any external registry.
- **FACT**: There is NO BSR version pinning, registry pulls, or cross-repo proto dependencies. Do not flag missing version pins or registry configuration.
- **FACT**: Proto files are local to this repo and generated via `make generate` in each component.

### Validation Patterns
- **buf.validate usage**: Pre-existing proto files in this codebase do NOT use buf.validate annotations. Only flag missing buf.validate when:
  1. The PR explicitly adds buf.validate to OTHER fields in the same file, OR
  2. The file already has 3+ buf.validate annotations on similar fields
- Do NOT flag a single new field for missing buf.validate in a file with zero existing validation annotations.

### Kubernetes CRD Patterns
- **+optional vs +kubebuilder:validation:Optional**: Check consistency within EACH file. If a file uses `+kubebuilder:validation:Optional` for 10+ fields, flag `+optional` as inconsistent. If both patterns exist roughly equally, it's intentional migration state—do NOT flag.
- **omitempty semantics**: `string` fields with `omitempty` JSON tag in CRD Go structs are standard for optional fields. `HasFieldName()` helper methods work correctly. This is NOT a bug.
- **metadata.annotations for references**: OSAC uses annotations for owner references (`osac.openshift.io/owner-reference`) and tenant scoping (`osac.openshift.io/tenant`), NOT separate fields. This is by design per `architecture-patterns.md`.

### Ansible Patterns
- **module_defaults propagation**: Ansible's `module_defaults` block at playbook level propagates to ALL tasks in that playbook AND included roles/tasks. Do NOT flag missing `kubeconfig:`, `validate_certs:`, `context:` in individual tasks when `module_defaults.group/kubernetes.core.kubeconfig` is set at playbook level.
- **wait conditions**: Not all Kubernetes resources have failure states. StorageClass, Namespace, ConfigMap reconcile to success or stay pending—there is no "failed" condition to check. Do NOT require `failed_when:` on wait tasks for these resources.
- **Service role patterns**: See `osac-aap/.claude/rules/playbook-patterns.md` for the distinction between template roles (project_src-based rendering) and service roles (cluster_instance.yaml driven).

## Review Scope Boundaries

### What TO Review
1. **Cross-file consistency**: Documentation (README, AUTH.md, architecture-patterns.md, CATALOG_ITEMS.md) vs implementation
2. **PR description accuracy**: Does the description match what was actually implemented?
3. **Architectural risks**: Blocking calls in health endpoints, synchronous external API calls in critical paths, Kafka topic mismatches
4. **Pattern violations**: Inconsistent use of validation markers, RBAC annotation patterns, error handling
5. **Missing coverage**: File types ignored in earlier rounds (proto, Helm, SQL migrations, CLI commands)
6. **Cross-PR dependencies**: Field added but reconciler/mapper in separate PR? Flag it.
7. **Test quality**: Do tests exercise the edge cases claimed in PR description? Empty-string vs absent for optional fields?

### What NOT to Review (avoid noise)
1. **Generic security hardening** not relevant at this stage: image SHA pinning, scratch-based containers, non-root users (unless PR is explicitly about security hardening)
2. **Pre-existing patterns** the PR doesn't touch: If the PR adds one field and 20 other fields already lack validation, don't flag the new field—flag the pattern once at file level if at all
3. **Out-of-scope documentation** unless PR description claims doc updates: CATALOG_ITEMS.md, osac-aap role READMEs belong to separate stories
4. **Intentional design decisions** documented in component AGENTS.md: Read `<component>/AGENTS.md` before flagging architectural choices
5. **Round 2+ noise**: If you already flagged an issue in Round 1 and it wasn't fixed, the author has made a decision. Do NOT re-flag unless new evidence changes the severity.

## File Type Coverage Checklist

For EVERY PR, verify you reviewed:
- [ ] Proto definitions (`.proto` files) - check field validation, breaking changes, naming consistency
- [ ] Helm charts (`Chart.yaml`, `values.yaml`, templates/) - check RBAC, annotations, version bumps
- [ ] SQL migrations (`.sql` files) - check DDL safety, index creation, FK constraints
- [ ] CLI commands (Cobra cmd files) - check flag validation, error messages, help text
- [ ] Integration tests - do they exercise the feature's edge cases or just happy path?
- [ ] CRD RBAC markers (`// +kubebuilder:rbac:`) - do they match what the controller actually needs?

## Deep Logic Checks

These are HARD but high-value. Spend time on them:

1. **Regex validation**: Does the regex actually match the documented format? Test boundary cases mentally. Example: `\\.(ts|tsx)$` should be `\\.(tsx|ts)$` when using leftmost-first matching engines (longest alternative must come first to avoid `.tsx` matching only `.ts`).

2. **Retry/wait loops**:
   - Is there a `failed_when:` or error exit for non-retryable failures?
   - Will the loop exhaust all retries on a permanently broken resource?
   - Is `retries * delay` reasonable for the operation?

3. **Cross-resource dependencies**:
   - PR adds `storage_tier` field to ComputeInstanceDisk CRD
   - Does the reconciler in osac-operator actually map it to Ansible extra_vars?
   - Is that in THIS PR or a separate one? If separate, flag the gap.

4. **Immutability claims**:
   - PR says "immutability inherited from existing XValidation"
   - Does adding an optional field to an immutable struct preserve semantics?
   - Check the CEL expression, don't assume.

5. **UpdateMask bugs**:
   - When a field is added, is it in the UpdateMask allow-list? (Check `updateIncludesField` in `fulfillment-service/internal/servers/field_mask.go`)
   - Are there tests that update ONLY that field to verify masking works?

## Framework-Specific Knowledge

### grpc-gateway
- `DefaultHeaderMatcher` only forwards permanent HTTP headers + `Grpc-Metadata-*` prefix
- Custom HTTP headers (e.g., `X-Feature-Flag`) are silently dropped unless gateway config explicitly allows them
- Check `fulfillment-service/internal/cmd/service/start/restgateway/start_rest_gateway_cmd.go` when a feature relies on custom headers

### Kubernetes controller-runtime
- Owner reference annotations are OSAC's pattern, not `metadata.ownerReferences[]`
- Tenant isolation uses annotations, not labels (labels are mutable by workload)
- `// +kubebuilder:rbac:` markers in controller files must match Helm ClusterRole in `osac-operator/charts/operator/templates/`

### OPA Policies
- Located in `fulfillment-service/internal/auth/policies/authz.rego` (single file, not per-resource)
- When a new tenant-scoped resource is added, check if OPA policies grant access
- `fulfillment-service/docs/AUTH.md` must document the RBAC verbs (get/list/create/update/delete)

### Ansible Collections
- `osac.workflows` vs `osac.config_as_code` - see `osac-aap/AGENTS.md` for distinction
- Playbooks under `osac-aap/playbook_osac_*.yml` (top-level) are ENTRY POINTS called by osac-operator
- Roles under `osac-aap/collections/ansible_collections/osac/workflows/roles/` and `osac-aap/collections/ansible_collections/osac/config_as_code/roles/` are IMPLEMENTATION called by workflow playbooks

## Hallucination Self-Check

Before submitting a finding, ask:
1. **Does this thing actually exist in the repo?** Grep for it. If you can't find evidence, don't claim it.
2. **Is this a standard pattern in the framework?** Check official docs, don't invent requirements.
3. **Did I read the component's AGENTS.md?** Design decisions documented there are intentional, not bugs.
4. **Is this issue present in 10+ other files?** If so, it's a pre-existing codebase pattern, not a regression in this PR.

## Example High-Quality Findings

### Good: Specific, Verifiable, Actionable
```text
[medium/stale-doc] AUTH.md missing DiskImages RBAC entry

File: fulfillment-service/docs/AUTH.md:47

The PR adds DiskImages CRD with create/read/update/delete verbs (internal/auth/policies/authz.rego:15-23),
but AUTH.md's tenant permissions table doesn't list DiskImages.

Remediation: Add row to AUTH.md table:
| DiskImages | disk_images | create, read, update, delete | Disk image sources for ComputeInstances |
```

### Bad: Vague, Unverifiable, Noisy
```text
[low/missing-validation] storage_tier field lacks buf.validate constraints

The new storage_tier field should have buf.validate.string.min_len = 1 to prevent empty strings.

[Why this is bad: The entire compute_instance.proto file has ZERO buf.validate annotations.
Flagging one new field is inconsistent and adds noise.]
```

## Round 2+ Strategy

After Round 1 fixes are applied:
1. **Re-verify your Round 1 findings**: Were they actually fixed, or did the author decide not to? If not fixed, assume intentional—don't re-flag.
2. **Check for NEW issues introduced by the fixes**: Did the fix break something else?
3. **Focus on file types you skipped in Round 1**: Proto, Helm, SQL, CLI, tests.
4. **Stop if you have <3 findings**: Low signal-to-noise on later rounds is worse than no review.

Quality over quantity. One real architectural bug > 10 stylistic nits.
