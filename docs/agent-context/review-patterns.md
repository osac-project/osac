# OSAC Review Patterns

Common review feedback patterns from past OSAC PRD and EP submissions. Both
`/prd:draft` and `/design:draft` should anticipate these expectations when
producing documents.

## PRD vs Design: What Goes Where

| PRD (`prd.md`) | Design EP (`design.md`) |
|----------------|------------------------|
| User stories per persona | CRD fields, conditions, finalizers |
| Observable outcomes | Controller reconcile logic |
| Non-goals, assumptions, risks | Playbook names, API schemas |
| High-level affected surfaces | Helm/installer implementation |

**Litmus test:** could a persona observe or experience this directly? If yes,
it belongs in the PRD. If no, it belongs in the design.

## Reviewer Expectations

### PRD Expectations
- User stories should cover all relevant OSAC personas (see [feature dimensions](osac-dimensions.md))
- Requirements describe user-observable outcomes, not implementation
- No API fields, controller names, playbook names, or env vars
- Acceptance criteria are PM-verifiable scenarios, not engineering checklists
- Optional sections are omitted (not filled with placeholders)
- The Problem Statement says what a persona can already do end to end in this
  capability domain — or that nothing does it yet. Reviewers size the scope
  against that sentence.
- A PRD scopes one deliverable step past that current state, not the finished
  version of the capability. Later steps become separate Features under the
  same Outcome. When the current state is nothing, the step is a walking
  skeleton: the thinnest end-to-end path a persona can use, plus the ability
  to see whether it worked. Judge by capability, not component — a component
  with many merged EPs can still be entering a new domain, and an API that
  lets a user declare a property is not prior art for changing it.

### Design EP Expectations
- All template sections must be present, even if marked "TBD" or "N/A"
- Implementation details should cover the affected contracts and lifecycle; scale depth to the change
- Test plans should describe strategy, not just "tests will be added"

### Clarity
- Technical terms should be defined upfront (see Networking EP's Terminology section)
- Relationships between resources should be explicit (parent-child, ownership, scope)
- Workflows should enumerate steps with actor roles clearly defined

### Consistency with OSAC Patterns
- New APIs should be declarative, following [Kubernetes API conventions](https://github.com/kubernetes/community/blob/master/contributors/devel/sig-architecture/api-conventions.md) where possible
- Resources should include tenant isolation metadata (annotations for tenant-id, owner-reference)
- Controller patterns should align with osac-operator conventions
- Integration with osac-aap should be described for provisioning workflows
- Pluggable architectures (like NetworkClass) are preferred over hardcoded implementations

## Frequent Feedback Themes

| Theme | Anti-Pattern | Better Approach |
|-------|-------------|-----------------|
| Missing alternatives | No "Alternatives" section or only strawman options | Explain other approaches considered and why they were rejected |
| Vague non-goals | "Advanced features are out of scope" | "Auto-scaling and multi-region placement are out of scope — addressed in a separate proposal" |
| Implementation-focused user stories | "As a tenant, I want the VirtualNetwork CRD to have a CIDR field" | "As a tenant, I want to define an isolated network with my own IP address space so I can control my network topology" |
| Design leakage in PRD | PRD names controllers, CRD fields, playbooks, env vars, or finalizers | PRD states user-observable outcomes; design doc specifies implementation |
| Acceptance criteria repeat requirements | AC checkboxes restate each FR as "FR-N is implemented" | AC describes end-to-end scenarios a PM can verify by using the product |
| Placeholder test plans | "Unit and integration tests will be added" | Name boundary assertions, environment prerequisites, and the tier/owner for each case using [Integration testing](../INTEGRATION-TESTING.md) |
| Generic risks | "Risk: Implementation might have bugs" | "Risk: IPv6 dual-stack adds testing complexity. Mitigation: Make IPv6 optional, support IPv4-only mode" |
| Inconsistent terminology | "Floating IP" / "PublicIP" / "External IP" used interchangeably | Define terms in a Terminology section and use consistently |
| Workflow gaps | Jumps from creation to deletion | Include all lifecycle operations (create, read, update, delete, start/stop) |
| Several increments in one PRD | One PRD covers discovery, execution, cancellation, history, and warnings — the finished version of the capability rather than the next step past today | Scope one deliverable step (when nothing ships today, the walking skeleton: one usable path plus success/failure visibility) and name the deferred increments in Out of Scope |
| Designing around a stated limitation | Inventing user-facing surface to soften a platform constraint the document itself records as an assumption (e.g. a pending state added to make an uncancellable operation cancellable) | Defer the capability along with the constraint; revisit once the base capability ships |

**Ordering note:** "Workflow gaps" and "Several increments in one PRD" pull in
opposite directions, and which applies depends on the current state. Full
lifecycle coverage is the expectation once the domain's core operation ships —
before that, a partial lifecycle is the point, and the gaps belong in Out of
Scope rather than being filled in.

## Historical EP Reference Library

These examples illustrate document structure and past reviewer feedback, not
current implementation or networking support. Verify the latest accepted design
and source code before adopting an example. In particular, historical IPv6 and
dual-stack examples do not override [networking decisions](networking-decisions.md).

Existing EPs as quality benchmarks:

| Slug | Description | Lines | Notable Patterns |
|------|-------------|-------|------------------|
| `networking` | Networking API with VirtualNetwork, Subnet, SecurityGroup, PublicIP | 818 | Terminology section, dual-stack IPv4/IPv6, NetworkClass pluggable architecture |
| `bare-metal-fulfillment` | Bare metal provisioning with HostPool, Host, HostClass | ~400 | ESI integration, serial console, network attachment at interface level |
| `vmaas` | VM as a Service with ComputeInstance and ComputeInstanceTemplate | ~300 | Template-based provisioning, GPU support, live migration |
| `organizations` | Multi-tenancy organization model | ~300 | Tenant isolation, RBAC patterns |
| `tenant-specific-storageclasses` | Tenant-scoped storage class management | ~200 | Provider/tenant resource split pattern |
| `computeinstance-phase-condition-expansion` | ComputeInstance status and lifecycle updates | ~200 | API evolution pattern for existing resources |

Key takeaways:
- All EPs follow the template structure exactly (no skipped sections)
- Successful EPs define terminology upfront and use it consistently

## Review Process

- Address each comment explicitly (update the proposal or explain why not)
- Don't resolve comments yourself — let the reviewer resolve after confirming
- Update `last-updated` frontmatter field when making changes
- If architectural disagreement stalls the PR, escalate to synchronous discussion
