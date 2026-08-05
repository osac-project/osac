# -*- coding: utf-8 -*-

#
# Copyright (c) 2026 Red Hat Inc.
#
# Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with
# the License. You may obtain a copy of the License at
#
#   http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
# "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
# specific language governing permissions and limitations under the License.
#

import hashlib
import logging
import sys

import click
import humanize

from . import dirs


@click.group()
def update() -> None:
    """
    Updates generated project artifacts.
    """


@update.command(name="hashes")
def hashes() -> None:
    """
    Updates the database migrations hash.
    """
    # Collect and sort migration files:
    migrations_dir = dirs.project() / "internal" / "database" / "migrations"
    migration_files = migrations_dir.glob("*.up.sql")
    migration_files_sorted = sorted(migration_files)

    # We will report errors as we find them, and keep a count, so we can raise an error at the end if there are any:
    error_count = 0

    # Check that all migration files have a numeric prefix, while building an index of migration numbers to file names:
    migration_index: dict[int, list[str]] = {}
    for migration_file in migration_files_sorted:
        migration_parts = migration_file.name.split("_", 1)
        if len(migration_parts) < 2:
            logging.error(f"Migration file '{migration_file.name}' has an invalid prefix")
            error_count += 1
            continue
        migration_prefix = migration_parts[0]
        if not migration_prefix.isdigit():
            logging.error(f"Migration file '{migration_file.name}' has an invalid prefix")
            error_count += 1
            continue
        migration_number = int(migration_prefix)
        migration_index.setdefault(migration_number, []).append(migration_file.name)

    # Check that there are no duplicate migration numbers:
    for migration_number, migration_files in migration_index.items():
        if len(migration_files) <= 1:
            continue
        migration_names = [f"'{migration_file}'" for migration_file in migration_files]
        migration_names.sort()
        migration_list = humanize.natural_list(migration_names)
        logging.error(
            f"Migrations {migration_list} have the same number",
        )
        error_count += 1

    # Raise an error if there were any errors:
    if error_count > 0:
        logging.error("Found errors in migration files, fix them and run the command again")
        sys.exit(1)

    # Compute the hash of the migration files:
    computed_hash_source = "".join(migration_file.name + "\n" for migration_file in migration_files_sorted)
    computed_hash_source_bytes = computed_hash_source.encode()
    computed_hash_bytes = hashlib.sha256(computed_hash_source_bytes).digest()
    computed_hash_text = computed_hash_bytes.hex()

    # Read the current hash from the file:
    hash_file = migrations_dir.parent / "migrations.sha256"
    stored_hash_text = hash_file.read_text().strip() if hash_file.exists() else ""

    # Check if the hash is already up to date:
    if stored_hash_text == computed_hash_text:
        logging.info("Database migrations hash is already up to date")
        return

    # Update the hash file:
    hash_file.write_text(computed_hash_text + "\n")
    logging.info("Database migrations hash updated to '%s'", computed_hash_text)
