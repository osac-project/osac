# Integration testing

This guide helps developers and agents select and write integration tests as
part of the ADLC. Read the affected component's touched-area map and follow its
suite guide below for commands, setup, real/faked dependencies and coverage gaps.
Inspect the actual suite and test source before adding tests or claiming coverage.

## Maintaining testing guidance

Keep this file focused on shared policy and links. Component suite guides record
suite roles, prerequisites, dependency boundaries and known gaps. They do not
copy exact command lines or individual test file names: discover commands from
the owning source of truth (`make -C <component> help` or the Makefile `test*`
targets, the component `AGENTS.md`, suite READMEs and fixtures, and the CI jobs
under `.github/workflows/`) and cite that source as evidence. Individual
test scenarios and assertions belong in test source; per-change execution results
belong in the PR or linked task, and feature coverage matrices belong in the
reviewed test plan.

Update the affected suite guide when its suite role, prerequisites, dependency
boundary, coverage scope or known gap changes. Update the component's touched-area
map only when its required validation changes. Routine additions or changes to
tests within an existing suite do not require a documentation inventory update.
Do not add feature-specific sections or scenario lists to suite guides; a
feature adds at most one boundary or gap line, with its Jira URL.
Change this shared file only when shared policy or the guide links change.
Link unresolved gaps to their owning Jira URLs; report missing ownership explicitly.

## Shared tiers and boundaries

Use these tier names consistently:

- **Unit** — one package or function with external dependencies mocked.
- **Envtest** — a real Kubernetes API server and etcd process with the
  component's controllers or providers driven in-process; this is not a Kind
  integration test.
- **Component integration** — the component runs against a real Kind,
  testcontainer, broker, database, or protocol endpoint as documented by the
  component.
- **Contract** — a focused test of a boundary between two components or
  between a component and a provider.
- **E2E** — a cross-component user journey through the deployed OSAC stack.

Component-specific test labels in a matrix are subtiers of one of these canonical
tiers. The matrix must make that mapping explicit; a local label does not add
another tier or satisfy a Contract requirement by itself.

A lower tier does not satisfy a higher-tier requirement. Every component
integration section must disclose which dependencies are real and which are
faked or stubbed. If the required boundary has no qualifying suite, record
the gap and link the owning follow-up task; do not describe a lower-tier or
stub-only test as coverage of that boundary.

Build/package validation checks image assembly and dependencies. It is separate
from the test tiers and does not replace the applicable integration tests.

### Work ownership

Use the test tier to route implementation work. Unit, Envtest,
component-integration, and Contract tests for changed code belong to the owning
`[DEV]` story. Deployed cross-component user journeys belong to `[QE]` stories.
The reviewed test plan must classify each case by tier and owner; do not copy a
component-integration case into a QE story or treat an E2E case as covered by a
lower-tier test.

## Planning evidence

Design test plans must include a coverage matrix derived from the affected
components' touched-area maps and the actual test infrastructure. Use one row
per behavior and required boundary; a cross-component case may need several
rows. Include unit-only changes with their applicable tier rather than requiring
integration tests for every change.

Each row must identify:

- The owning component, touched behavior, and requirement/interface references.
- The required tier and the boundary whose behavior the test proves.
- The test-case IDs and existing suite/file to extend, or a clearly marked
  proposed location for new coverage.
- The execution command, working directory, and environment prerequisites.
- Which services, APIs, databases, providers, and controllers run for real,
  and which are simulated, mocked, or omitted.
- Any unavailable suite or unresolved infrastructure prerequisite, with the
  owning follow-up's Jira URL. If no owner or ticket exists, report that as
  unresolved; do not invent a ticket or claim the boundary is covered.

Verify existing suite paths and commands against the repository. Mark proposed
commands as proposed; where execution is not yet defined, record the gap
instead of supplying a plausible command. Name a specific tier and harness
rather than leaving alternatives such as "envtest or Kind" or "Cypress or
equivalent". Describe the running dependencies: a fixture-based test is not
evidence of a deployed boundary merely because it is labelled integration.

For timing or asynchronous behavior, identify the trigger, observable result,
measurement interval, and pass/fail bound. State how the test isolates the path
being measured from fallback polling, periodic resync, or mocked completion.
Flag contradictory or unspecified source behavior instead of inventing an
expected result.

Decomposition must carry the applicable matrix evidence into each
implementation or QE task's testing approach, including case IDs and
unresolved gaps. A task must remain actionable when ingested independently of
the feature test plan. Keep new suite/infrastructure work explicit in the
decomposition.

Before reporting a plan or decomposition ready, check every touched area against
its required boundary. Distinguish "has a planned test case" from "has an
identified execution path" and from "execution passed". Report missing or
wrong-tier coverage as unresolved even when all requirement and interface IDs
have mappings. A documented provider gap does not require real hardware for
an unrelated status-projection change; scope the test to the changed behavior.

### Evidence checks before completing a phase

Apply these checks during generation and self-review, then correct the artifacts
before reporting the phase complete:

| Claim | Evidence required |
|---|---|
| Integration coverage | Identify the exercised boundary and running dependencies. Lint, typechecking, collection, and schema-generation checks are static/build validation, not integration tests. |
| Envtest coverage | Envtest provides a Kubernetes API server and etcd. It does not provide fulfillment-service, PostgreSQL, or provider controllers; name and start those separately when the test requires them. |
| Cross-component coverage | A fake endpoint proves the caller's handling of that double, not the receiving service's persistence or reconciliation. Separate those cases and choose the harness for each. |
| Runnable test | Cite the repository file defining the command and the suite it runs. Replace vague instructions such as "run focused integration tests" with that command, or record execution as blocked pending an explicitly proposed harness. |
| Source contradiction or missing requirement | Cite the exact source file, section, and passage. Re-read the authoritative PRD/design before declaring a blocker; distinguish stale Jira text from a contradiction in those documents. |

For an existing approved design, retain its explicit assertions (including
negative, exhaustive, lifecycle, and timing assertions) or record why an
assertion cannot be planned. Do not replace them with a generic compatibility
check simply because it shares the same requirement or interface ID.

After decomposition, reconcile the test plan and tasks in both directions. If a
task restores a missing assertion or corrects a boundary, update the
corresponding test case and coverage row in the same phase. Do not report a
complete mapping while leaving the plan and tasks with different expected
behavior. Preserve earlier versions separately when conducting an evaluation.

Report behavioral coverage, execution readiness, and test execution results
separately. A case with a proposed harness or unresolved command is planned but
not execution-ready; static checks passing does not change that status.

## Implementation evidence

Every change must classify its touched behaviors against the tier boundaries
above and, when the affected component has one, its touched-area map. Changes
that do not alter runtime behavior need no integration coverage; record that
classification explicitly. Classify behavior-changing generated config against
the touched-area map.

Before opening a pull request:

- Every required integration row must have an executed, passing test through
  the named boundary. Unit tests and mocks do not satisfy a real component
  boundary; a documented provider gap does not make a lower-tier test
  coverage of that boundary.
- Cite the executed command and working directory per row. Lint, typecheck,
  collection, and schema-generation checks are build validation, not
  integration evidence.
- Missing or wrong-tier coverage is reported as a gap, never as covered. Link
  it to the owning follow-up ticket using its Jira URL when one exists. If no
  owner or ticket exists, report the gap as unresolved; do not invent a ticket.
- The PR description or review report must state, per touched boundary: the
  exercised boundary, running dependencies, executed command, and result — or
  report the gap as unresolved with its owning ticket URL when one exists. If
  there is no owner or ticket, state that explicitly; never claim the boundary
  is covered.

## Component suite guides

The sections below retain the entry points used by component instructions and
shared reviewers. Follow the linked guide for its test tiers, commands, coverage
notes and gaps; these details are maintained per component.

## Agentic CI sandbox

Read the [Agentic CI sandbox suite guide](testing/agentic-ci.md).

## Release automation

Read the [Release automation suite guide](testing/release-automation.md).

## fulfillment-service

Read the [fulfillment-service suite guide](testing/fulfillment-service.md).

## osac-installer

Read the [osac-installer suite guide](testing/osac-installer.md).

## osac-operator

Read the [osac-operator suite guide](testing/osac-operator.md).

## bare-metal-fulfillment-operator

Read the [bare-metal-fulfillment-operator suite guide](testing/bare-metal-fulfillment-operator.md).

## osac-aap

Read the [osac-aap suite guide](testing/osac-aap.md).

## osac-csi-driver

Read the [osac-csi-driver suite guide](testing/osac-csi-driver.md).

## osac-metering

Read the [osac-metering suite guide](testing/osac-metering.md).

## tests/e2e

Read the [tests/e2e suite guide](testing/e2e.md).

## Specific suite entry points

### Install preflight (OSAC-6561)

See [installer preflight setup and boundaries](testing/osac-installer.md#install-preflight-osac-6561).

### NET-CLEAN-07 deployed OSAC cleanup

See [operator cleanup setup and boundaries](testing/osac-operator.md#networking-cleanup-lifecycle-coverage-net-clean-07).

### NET-CLEAN-08 deployed BMF ownership boundary

See [BMF cleanup setup and boundaries](testing/bare-metal-fulfillment-operator.md#networking-cleanup-ownership-net-clean-08).

### R03 unified worker cleanup implementation checkpoint

See [worker cleanup boundaries](testing/osac-operator.md#r03-unified-worker-cleanup-implementation-checkpoint).

### ComputeInstance disk handoff (OSAC-6685)

See [Fulfillment disk handoff setup and boundaries](testing/fulfillment-service.md#computeinstance-disk-handoff-osac-6685).

## Multi-branch test isolation

See [Multi-branch test isolation](MULTI-BRANCH-ISOLATION.md) for cluster names,
namespaces, ports, database/Kafka separation and cleanup across checkouts.
