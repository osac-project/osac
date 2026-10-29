# Prepare the AAP Git Source

This is Doc 2a for OSAC **0.0.24**: [prereqs.md](prereqs.md) → **aap-git-source.md** → [operators.md](operators.md). The OSAC persona supplies the release source identity; the Git administrator publishes it and returns the internal Git URL. This guide does not install operators or create the AAP instance.

The source is the **full `osac-project/osac` monorepo**, including `osac-aap/`. A mirrored execution-environment image does not deliver the Git project. Use the [canonical installation guide](../customer-install-guide.md) for configuration semantics, and the site's normal Git hosting procedure for server administration.

## 1. OSAC persona → Git administrator

Read the unchanged defaults of the actual downloaded `0.0.24` chart from prerequisites:

```bash
helm show values "$OSAC_CHART_DIR" | yq -er '.aap.configAsCode.projectGitBranch'
```

Expected: one full, lowercase 40-character commit. Reject a branch name, short SHA or missing value; never derive the source pin from the EE tag or umbrella version.

| Input | Required contract |
| --- | --- |
| `OSAC_CHART_DIR` | Exact protected prerequisite-handoff path to the downloaded/extracted `0.0.24` chart; consume unchanged in the chart-default check |
| Release / source | `0.0.24`; `https://github.com/osac-project/osac` |
| `AAP_COMMIT` | Exact full commit from `aap.configAsCode.projectGitBranch` in the actual chart's defaults |
| Required tree | Full repository at that commit with `osac-aap/` present as a tree, not a subtree-only replacement repository |
| `AAP_RELEASE_REF` | `refs/heads/osac-aap-0.0.24-<first-12-commit-characters>`; advertised ref must resolve to `AAP_COMMIT` |
| Provenance | Approved chart identity/defaults binding and independently authenticated transfer provenance/checksum for the complete source bundle |
| Probe context | Approved existing cluster namespace/egress path and exact mirrored EE containing Git; do not depend on a not-yet-created AAP instance or operator namespace |

Release-source cross-check only: `osac/v0.0.24` source values pin `01d1adad87b1e1f0b7bad24de6c7c7006ddc6dbe`. The actual downloaded chart remains authoritative; an unexpected difference stops for release-owner review, not substitution of this example.

## 2. Git administrator publication contract

| Bastion reference parameter | Required value / constraint |
| --- | --- |
| Bare repository | `/srv/git/osac.git`; serve only this reviewed repository, not all of `/srv/git` |
| Service identity | Dedicated `osacgit`; read-only service access, no receive-pack or unaudited writer |
| Service name | `osac-git-daemon.service`; internal listener only; retain SELinux enforcement |
| Protocol / returned URL | `git://<bastion-internal-ip>:9418/osac.git` for the site-approved anonymous reference |
| Network scope | TCP `9418` from approved actual cluster egress sources only; never a globally open port |
| Existing deployment | Review repository, ownership, listener and unit before adoption; no blind replacement or restart |

`git://` has **no transport authentication or encryption**. Explicit site approval, independent trusted provenance and restricted ingress are mandatory; a colocated checksum is not authentication. If site policy rejects this reference, stop; never embed credentials in a URL.

> **0.0.24 chart constraint:** for Git, the closed `aap.configAsCode` schema (`additionalProperties=false`) exposes only `projectGitUri`/`projectGitBranch`, not SCM credential, SSH key, token or CA-trust fields; this guide supports unauthenticated delivery only.
> Authenticated alternatives require out-of-band Controller credential/trust configuration, outside this guide and the supported Helm contract.

Serialize publishers across administration hosts using the Git service owner's authority. Stage and verify a complete repository before first publication; create a missing release ref with an atomic absent-ref precondition. An existing ref is acceptable only if it already equals the full expected commit. Never force-update it, replace a repository being read, or remove refs used by another installation. Service restarts and firewall changes require the Git administrator's normal approval.

## 3. Git administrator → OSAC persona

| Returned field | Consumer contract |
| --- | --- |
| `projectGitUri` | Exact approved internal Git URL, reachable from the disconnected cluster execution context |
| `projectGitBranch` | Full `AAP_COMMIT`, **not** the advertised branch name or its 12-character suffix |
| Authentication / trust | Explicit `none` plus approval for the supported unauthenticated reference; no credential bytes |
| Network / publication confirmation | Approved egress path, repository identity, immutable ref → full commit, complete-tree/provenance verification and owner confirmation |
| Private handoff | `aap-git-handoff.txt`, bound to the same run, chart identity, source commit, endpoint and validation time; retain protected runtime evidence, export only allowlisted outcomes |

The OSAC persona inserts the returned URL into `aap.configAsCode.projectGitUri` and the unchanged full commit into `aap.configAsCode.projectGitBranch` in protected `values-osac.yaml`. Review the diff, fresh render and image/Secret contracts; checkpoint the intentional values change without overwriting the original prerequisite digest. The AAP instance/license/TLS configuration remains in [operators.md](operators.md).

## 4. Delivery gate and failure flow (WIP — toolkit unpublished)

> **WIP:** The proposed delivery gate is unavailable until the companion toolkit identity/version/checksum and protected `AAP_GIT_INPUTS` path, schema and producer/handoff contract are approved and available. Do not execute the TBF invocation below until then; accept the handoff only after the gate passes.

The gate validates only; it never configures the Git service/firewall or changes the source ref. Any temporary cluster probe requires explicit approval and UID-scoped cleanup.

<!-- TBF: requires unpublished companion toolkit -->

```bash
"$OSAC_VALIDATE" aap-git-source check git-delivery --inputs "$AAP_GIT_INPUTS"
```

Expected: exit `0`, fixed `PASS git-delivery`, only after chart/provenance, complete tree and cluster-workload fetch/advertised-ref verification agree on the exact commit. A bastion-only check or raw log line is insufficient. Nonzero exit emits fixed `FAIL git-delivery`; no passed handoff is published. Keep endpoints, credentials and raw logs private; retain fixed failure class/duration/run correlation for diagnosis.

Worked flow: the persona supplies the chart-derived commit/ref; the Git administrator stages the full repository and returns the approved URL plus auth/network confirmation. The persona validates delivery, updates the two values and checkpoints, then starts `operators.md`; that guide rechecks source readiness at entry/final acceptance. Actual AAP project synchronization and synchronized revision remain Phase 2 acceptance, not this preflight result.

Failure flow: a timeout returns to DNS/firewall/actual-egress ownership; a SHA mismatch returns to the Git publisher; missing `osac-aap/` or failed provenance returns to source preparation. Stop before accepting the handoff; do not fall back to GitHub, a public probe image or a floating branch. Revalidate only after owner correction; retain the last valid handoff or none, never a partially written success record.

## 5. Day 2 and alternative servers

For the next OSAC release, repeat chart-derived selection and publish a **new** release-specific immutable ref under the same serialization/preconditions. Preserve old refs and active readers, then revalidate the endpoint and approve a new values checkpoint. Never repoint an old release ref or compatibility alias to make an upgrade pass.

Existing approved Gitea, GitLab or other internal Git servers with unauthenticated read access may satisfy the same full-repository/commit/reachability contract. The bastion path, service identity/unit and TCP 9418 apply only to the anonymous reference, not those alternative services. A server migration requires a new endpoint/trust review and delivery check even when the commit is unchanged.
