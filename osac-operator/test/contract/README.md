# Operator contract tests

The ordinary package tests check Helm/RBAC contracts without a deployed
operator. Run them from `osac-operator/` with `go test ./test/contract`.

## Provisioning retry against real AAP

`TestProvisionRetryAAPBoundary` exercises the production task engine and AAP
HTTP client against real AAP jobs and Ansible `set_stats` artifacts. Kubernetes
status persistence uses controller-runtime's in-memory client and the retry
clock is controlled. It runs no Fulfillment service, provider, or VM. Envtest
in `internal/controller/` separately proves Kubernetes CRD persistence and
resource-version conflicts.

This test is opt-in. A skip provides no live-AAP evidence and does not satisfy
the required pre-merge gate for OSAC-7860. A passing local unit/Envtest run
cannot substitute for it.

### One-time setup

Use an authorized test AAP organization and a dedicated fixture template. Set
up the following resources yourself; the test reads them and leaves them intact:

1. A Git project pointing to a repository containing
   `osac-operator/test/contract/testdata/provision_retry/playbook.yml`. Pin its
   SCM branch to the full commit ID containing this fixture, synchronize it,
   and use that same ID for `SOURCE_REVISION`. The source must be reachable by
   AAP; a local unpushed commit is insufficient. Disable branch override for
   this template/project. The test checks both project and actual job revisions.
2. An execution environment containing `ansible-core` and the standard AAP
   runner/callback support for job artifacts. Pin the EE image as
   `registry/path/image@sha256:<64 hex digits>`. The test checks the configured
   image and each job's EE. Ansible must preserve the boolean type of the
   `retryable` expression in `set_stats`; a changed artifact shape fails the
   contract rather than being normalized by the test.
3. A localhost-only inventory (`localhost ansible_connection=local`) and a
   job template selecting the pinned project, the playbook path above, that
   inventory and the digest-pinned EE. Enable **Prompt on launch** for both
   **Variables** and **Labels**. Use no machine, provider or other template
   credentials. Keep the inventory and execution settings fixed. The fixture
   targets localhost with a local connection and never contacts a provider.
4. A token allowed to read project/template/EE metadata, read template
   credentials, create labels in the organization, launch/read/cancel/delete
   fixture jobs, and list/delete their labels. Supply trusted TLS roots to the
   host running Go; TLS verification is always enabled. The API base should
   end at `/api/controller` on gateway installations or `/api` on older
   Controllers; the clients append `/v2/`. Use the direct trusted endpoint,
   since the metadata client rejects HTTP redirects.

Validate the playbook with the same EE's `ansible-playbook --syntax-check`
before configuring the template. Do not add provider credentials to make
this fixture pass.

### Configuration and execution

Set these variables in the shell or your approved secret loader. Keep token
values out of terminal transcripts, shell history, reports and committed files.

| Environment variable | Value |
|---|---|
| `OSAC_PROVISION_RETRY_AAP_CONTRACT` | `1` to opt in |
| `OSAC_PROVISION_RETRY_AAP_URL` | Trusted HTTPS API base, without `/v2/` |
| `OSAC_PROVISION_RETRY_AAP_TOKEN` | Token from the approved secret store |
| `OSAC_PROVISION_RETRY_AAP_ORG_ID` | Dedicated test organization's positive numeric ID |
| `OSAC_PROVISION_RETRY_AAP_PROJECT_ID` | Pinned fixture project's positive numeric ID |
| `OSAC_PROVISION_RETRY_AAP_TEMPLATE_ID` | Fixture template's positive numeric ID |
| `OSAC_PROVISION_RETRY_AAP_SOURCE_REVISION` | Full lowercase Git commit ID containing the fixture |
| `OSAC_PROVISION_RETRY_AAP_EE_DIGEST` | `sha256:` followed by 64 lowercase hex digits |
| `OSAC_PROVISION_RETRY_AAP_CLEANUP` | `1` authorizes cancellation/deletion of this run's jobs and labels |

From `osac-operator/`:

```bash
go test ./test/contract -run '^TestProvisionRetryAAPBoundary$' -count=1 -v
```

Absent opt-in produces an explicit skip. Opt-in with incomplete configuration
fails before launch. Allow roughly 35 minutes in the worst case: six scenarios,
up to five minutes per job (two jobs for `fail_once`), plus bounded cleanup.
Go's default timeout is ten minutes; for slow environments append
`-timeout=50m`. Fast healthy environments need much less time.

The `fail_once` scenario sends `fixture_attempt=1`, gets a real failed job with
a retryable `osac_error`, and advances the engine's fake clock through its
normal backoff. The next engine launch receives `fixture_attempt=2` through
the existing `osac_job_vars` envelope and succeeds without a typed result.
The harness asserts persisted operator `Running` state and the original failure
before its next engine poll, even if the AAP job has already completed. It then
observes the real successful result and verifies checkpoint clearing. No sleep,
shared counter or external synchronization service controls the fixture result.

The other scenarios cover nonretryable, malformed/missing `osac_error`, and
malformed/unexpected `osac_result`. The harness requires real terminal status,
pinned job metadata and exact safe checkpoint reason/message/attempt/time.
All six scenarios must pass; no required scenario may skip.

### Cleanup and evidence

Every scenario uses a random `contract-<32 hex digits>` run ID in its inputs
and unique `osac-provision-none-<run ID>-<attempt>` labels. Cleanup is registered
before launch with a separate two-minute context. It finds jobs by those labels,
checks the exact run ID and fixture project/playbook, cancels active jobs, waits
for completion, deletes the run-owned records and verifies label removal. It
does not remove the shared template, project, inventory, organization or EE.
There is no organization-wide cleanup query or deletion.

Some AAP versions remove labels automatically when their last job is deleted;
the empty label query is accepted. If the installed version retains labels,
`DELETE /v2/labels/<id>/` must be supported and permitted. Unsupported deletion,
insufficient permissions, an ownership mismatch or a timeout fails the test;
do not silently ignore it. Inspect only this run's labels/jobs and arrange
authorized manual cleanup if the automatic cleanup reports failure. A client
process killed before cleanup also needs that manual inspection.

Record the exact command, timestamp, tested operator commit, fixture revision,
EE digest, all scenario results and cleanup result. The successful test logs
the revision/digest and disclosed simulated boundaries. Do not save raw API
responses, credentials, hostnames or infrastructure details as evidence. Until
an authorized AAP run succeeds, the live boundary remains unverified.
