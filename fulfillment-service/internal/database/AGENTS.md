# AGENTS.md — `internal/database/`

Rules for the database layer and its `migrations/` subdirectory. The component
root [`../../AGENTS.md`](../../AGENTS.md) still applies (migrations are immutable;
add a new numbered migration instead of editing an applied one).

## Keep the data-model doc in sync

[`../../docs/DATA_MODEL.md`](../../docs/DATA_MODEL.md) is the authoritative map of
OSAC object relations (both applicative and database-enforced). A migration that
changes a **cross-resource** relation must update it **in the same PR**:

- Adding/dropping a **foreign key, insert-trigger, or delete-guard** between
  resources → update the affected edge's line style, its `field · type` label, and
  the `DB enforcement` cell (with the new `file:line`) in the relations table.
- Adding a **new resource table** → add a node and table row(s).

Follow the conventions and the "Maintaining this document" rules in that file. In
particular: describe what the migration *actually* enforces by reading the
trigger-function body, not the comment next to it; and because guards are
redefined via `create or replace` and can be dropped later, cite the **net
(current)** definition.

## Migration file hash

See [`README.md`](README.md): after adding a migration, run
`uv run dev.py update hashes` and commit the updated `migrations.sha256`.
