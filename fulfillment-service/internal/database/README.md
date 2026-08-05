# Database

This directory contains the database layer of the service. The `migrations` subdirectory contains
the numbered `.up.sql` files that are applied sequentially to evolve the database schema.

## Migration file hash

The `migrations.sha256` file contains a SHA-256 digest of the sorted list of `.up.sql` filenames.
Its purpose is to prevent two pull requests from accidentally introducing migrations with the same
number: since both PRs would write a different hash, git will flag a merge conflict forcing the
second author to renumber their migration before merging.

If you add a new migration, run `uv run dev.py update hashes` and commit the updated
`migrations.sha256` file alongside your new migration file. If you forget, the unit test that
verifies the hash will fail and indicate the mismatch.
