# Prepare the AAP Git Source

- [Prepare the AAP Git Source](#prepare-the-aap-git-source)
  - [1. OSAC persona → Git administrator](#1-osac-persona--git-administrator)
  - [2. Git administrator publication contract](#2-git-administrator-publication-contract)
  - [3. Git administrator → OSAC persona](#3-git-administrator--osac-persona)
  - [4. Delivery gate and failure flow](#4-delivery-gate-and-failure-flow)
  - [5. Day 2 and alternative servers](#5-day-2-and-alternative-servers)

This is Doc 2a for OSAC **0.0.62**: [prereqs.md](prereqs.md) → **aap-git-source.md** → [operators.md](operators.md). The OSAC persona supplies the release source identity; the Git administrator publishes it and returns the internal Git URL. This guide does not install operators or create the AAP instance.

The source is the **full `osac-project/osac` monorepo**, including `osac-aap/`. A mirrored execution-environment image does not deliver the Git project. Use the [canonical installation guide](../customer-install-guide.md) for configuration semantics, and the site's normal Git hosting procedure for server administration.

## 1. OSAC persona → Git administrator

Read the unchanged defaults of the actual downloaded `0.0.62` chart from prerequisites:

```bash
helm show values "$OSAC_CHART_DIR" | yq -er '.aap.configAsCode.projectGitBranch'
```

Expected: one full, lowercase 40-character commit. Reject a branch name, short SHA or missing value; never derive the source pin from the EE tag or umbrella version.

| Input | Required contract |
| --- | --- |
| `OSAC_CHART_DIR` | Exact protected prerequisite-handoff path to the downloaded/extracted `0.0.62` chart; consume unchanged in the chart-default check |
| Release / source | `0.0.62`; `https://github.com/osac-project/osac` |
| `AAP_COMMIT` | Exact full commit from `aap.configAsCode.projectGitBranch` in the actual chart's defaults |
| Required tree | Full repository at that commit with `osac-aap/` present as a tree, not a subtree-only replacement repository |
| `AAP_RELEASE_REF` | `refs/heads/osac-aap-0.0.62-e526ec64b1d5`; advertised ref must resolve to `AAP_COMMIT` |
| Provenance | Approved chart identity/defaults binding and independently authenticated transfer provenance/checksum for the complete source bundle |
| Probe context | Approved existing cluster namespace/egress path and exact mirrored EE containing Git; do not depend on a not-yet-created AAP instance or operator namespace |

Release-source cross-check only: `osac/v0.0.62` source values pin `e526ec64b1d54981c13505af3c7142d1b6591239`. The actual downloaded chart remains authoritative; an unexpected difference stops for release-owner review, not substitution of this example.

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

> **0.0.62 chart constraint:** for Git, the closed `aap.configAsCode` schema (`additionalProperties=false`) exposes only `projectGitUri`/`projectGitBranch`, not SCM credential, SSH key, token or CA-trust fields; this guide supports unauthenticated delivery only.
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

## 4. Delivery gate and failure flow

The delivery gate is a manual evidence gate. It validates only; it never configures the Git service/firewall or changes the source ref. Any temporary cluster probe requires explicit approval and UID-scoped cleanup.

Accept the handoff only after chart/provenance, complete-tree, and cluster-workload fetch/advertised-ref verification agree on the exact commit. A bastion-only check or raw log line is insufficient. Keep endpoints, credentials, and raw logs private; retain the fixed failure class, duration, and run correlation for diagnosis.

Worked flow: the persona supplies the chart-derived commit/ref; the Git administrator stages the full repository and returns the approved URL plus auth/network confirmation. The persona validates delivery, updates the two values and checkpoints, then starts `operators.md`; that guide rechecks source readiness at entry/final acceptance. Actual AAP project synchronization and synchronized revision remain Phase 2 acceptance, not this preflight result.

Failure flow: a timeout returns to DNS/firewall/actual-egress ownership; a SHA mismatch returns to the Git publisher; missing `osac-aap/` or failed provenance returns to source preparation. Stop before accepting the handoff; do not fall back to GitHub, a public probe image or a floating branch. Revalidate only after owner correction; retain the last valid handoff or none, never a partially written success record.

## 5. Day 2 and alternative servers

For the next OSAC release, repeat chart-derived selection and publish a **new** release-specific immutable ref under the same serialization/preconditions. Preserve old refs and active readers, then revalidate the endpoint and approve a new values checkpoint. Never repoint an old release ref or compatibility alias to make an upgrade pass.

Existing approved Gitea, GitLab or other internal Git servers with unauthenticated read access may satisfy the same full-repository/commit/reachability contract. The bastion path, service identity/unit and TCP 9418 apply only to the anonymous reference, not those alternative services. A server migration requires a new endpoint/trust review and delivery check even when the commit is unchanged.
