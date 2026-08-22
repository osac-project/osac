# Conventions — Cross-Repo Dependencies

This is a hand-trimmed excerpt covering only the cross-repo dependency table
that has no equivalent in any single component's own `AGENTS.md`. See
[README.md](README.md) for provenance and maintenance notes.
Component-specific coding conventions (naming, style, imports, error
handling, logging, comments, function/module design, proto naming) are
authoritative in `fulfillment-service/AGENTS.md` and `osac-operator/AGENTS.md`.

## Cross-Repo Dependencies

When changing one repo, check all dependent repos in this table before submitting:

| Change in | Also check | Why |
|-----------|-----------|-----|
| `osac-operator` CRD types | `fulfillment-service` reconciler registration | New CRD types must be registered in the fulfillment-service reconciler (in-repo change, same PR) |
| `osac-operator` CRD spec changes | `osac-aap` roles that read CRD fields | Adding a field to `ClusterOrderSpec` requires the AAP playbook to extract and use it |
| `fulfillment-service` CLI flag changes | `tests/e2e/` test helpers | Adding `--pull-secret-file` required updating `OsacCLI.create_cluster` in `tests/e2e/` (in-repo change, same PR) |

Evidence: MGMT-24226 eval scored 3/5 because the agent fixed `fulfillment-service` and `osac-aap` but missed updating `osac-installer`'s pinned image version — this was pre-mono-repo-merge (OSAC-1739), when these were still separate repos and image tags were pinned per-commit. That specific mechanism (a separate pinned tag needing a manual re-sync via `sync-image-tags.sh`) no longer exists post-mono-repo — `osac/osac-installer/scripts/sync-image-tags.sh` was removed upstream (`OSAC-3367`) and `osac/osac-installer/values/*/values.yaml` now leaves mono-repo-resident components' image tags unpinned. See the CRD-registration and CLI-flag rows above for what still needs a cross-file check today.
