/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

	http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package migrations

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5/pgconn"
	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"
)

var _ = DescribeMigration("Add storage tier referential integrity triggers", func() {
	insertBackend := func(ctx context.Context, id string) {
		_, err := conn.Exec(ctx,
			`insert into storage_backends (id, name, tenant, data)
			 values ($1, $1, 'system', '{}')`, id)
		Expect(err).ToNot(HaveOccurred())
	}

	It("Creates the storage_tier_backends helper table", func(ctx context.Context) {
		err := tool.Migrate(ctx, 76)
		Expect(err).ToNot(HaveOccurred())

		var count int
		err = conn.QueryRow(ctx, `
			select count(*)
			from pg_catalog.pg_class c
			join pg_catalog.pg_namespace n on n.oid = c.relnamespace
			where n.nspname = 'public'
			  and c.relkind = 'r'
			  and c.relname = 'storage_tier_backends'
		`).Scan(&count)
		Expect(err).ToNot(HaveOccurred())
		Expect(count).To(Equal(1))
	})

	It("Creates the 'materialize_storage_tier_backends' function", func(ctx context.Context) {
		err := tool.Migrate(ctx, 76)
		Expect(err).ToNot(HaveOccurred())

		var count int
		err = conn.QueryRow(ctx, `
			select count(*)
			from information_schema.routines
			where routine_name = 'materialize_storage_tier_backends'
			  and routine_type = 'FUNCTION'
		`).Scan(&count)
		Expect(err).ToNot(HaveOccurred())
		Expect(count).To(Equal(1))
	})

	It("Creates the 'check_storage_tier_backend_refs' function", func(ctx context.Context) {
		err := tool.Migrate(ctx, 76)
		Expect(err).ToNot(HaveOccurred())

		var count int
		err = conn.QueryRow(ctx, `
			select count(*)
			from information_schema.routines
			where routine_name = 'check_storage_tier_backend_refs'
			  and routine_type = 'FUNCTION'
		`).Scan(&count)
		Expect(err).ToNot(HaveOccurred())
		Expect(count).To(Equal(1))
	})

	It("Creates the 'check_storage_backend_not_in_use_by_tier' function", func(ctx context.Context) {
		err := tool.Migrate(ctx, 76)
		Expect(err).ToNot(HaveOccurred())

		var count int
		err = conn.QueryRow(ctx, `
			select count(*)
			from information_schema.routines
			where routine_name = 'check_storage_backend_not_in_use_by_tier'
			  and routine_type = 'FUNCTION'
		`).Scan(&count)
		Expect(err).ToNot(HaveOccurred())
		Expect(count).To(Equal(1))
	})

	It("Adds the materialization trigger to the storage_tiers table", func(ctx context.Context) {
		err := tool.Migrate(ctx, 76)
		Expect(err).ToNot(HaveOccurred())

		var count int
		err = conn.QueryRow(ctx, `
			select count(*)
			from information_schema.triggers
			where trigger_name = 'materialize_storage_tier_backends'
			  and event_object_table = 'storage_tiers'
			  and action_timing = 'AFTER'
		`).Scan(&count)
		Expect(err).ToNot(HaveOccurred())
		Expect(count).To(BeNumerically(">=", 1))
	})

	It("Adds the backend validation trigger to the storage_tiers table", func(ctx context.Context) {
		err := tool.Migrate(ctx, 76)
		Expect(err).ToNot(HaveOccurred())

		var count int
		err = conn.QueryRow(ctx, `
			select count(*)
			from information_schema.triggers
			where trigger_name = 'check_storage_tier_backend_refs'
			  and event_object_table = 'storage_tiers'
			  and action_timing = 'BEFORE'
		`).Scan(&count)
		Expect(err).ToNot(HaveOccurred())
		Expect(count).To(BeNumerically(">=", 1))
	})

	It("Adds the deletion protection trigger to the storage_backends table", func(ctx context.Context) {
		err := tool.Migrate(ctx, 76)
		Expect(err).ToNot(HaveOccurred())

		var count int
		err = conn.QueryRow(ctx, `
			select count(*)
			from information_schema.triggers
			where trigger_name = 'check_storage_backend_not_in_use_by_tier'
			  and event_object_table = 'storage_backends'
			  and action_timing = 'BEFORE'
			  and event_manipulation = 'UPDATE'
		`).Scan(&count)
		Expect(err).ToNot(HaveOccurred())
		Expect(count).To(Equal(1))
	})

	It("Materializes backend references into the helper table on insert", func(ctx context.Context) {
		err := tool.Migrate(ctx, 76)
		Expect(err).ToNot(HaveOccurred())
		insertBackend(ctx, "sb-1")
		insertBackend(ctx, "sb-2")

		_, err = conn.Exec(ctx,
			`insert into storage_tiers (id, name, tenant, data)
			 values ('tier-1', 'tier-1', 'system', $1::jsonb)`,
			`{"backends":[{"backend_id":"sb-1"},{"backend_id":"sb-2"}]}`)
		Expect(err).ToNot(HaveOccurred())

		var count int
		err = conn.QueryRow(ctx, `
			select count(*)
			from storage_tier_backends
			where storage_tier_id = 'tier-1'
		`).Scan(&count)
		Expect(err).ToNot(HaveOccurred())
		Expect(count).To(Equal(2))
	})

	It("Prevents creating a storage tier referencing a non-existent backend", func(ctx context.Context) {
		err := tool.Migrate(ctx, 76)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx,
			`insert into storage_tiers (id, name, tenant, data)
			 values ('tier-bad', 'tier-bad', 'system', $1::jsonb)`,
			`{"backends":[{"backend_id":"no-such-backend"}]}`)
		Expect(err).To(HaveOccurred())
		var pgErr *pgconn.PgError
		Expect(errors.As(err, &pgErr)).To(BeTrue())
		Expect(pgErr.Code).To(Equal("Z0002"))
		Expect(pgErr.Message).To(ContainSubstring("no-such-backend"))
	})

	It("Prevents creating a storage tier referencing a soft-deleted backend", func(ctx context.Context) {
		err := tool.Migrate(ctx, 76)
		Expect(err).ToNot(HaveOccurred())
		insertBackend(ctx, "sb-deleted")

		_, err = conn.Exec(ctx,
			`update storage_backends set deletion_timestamp = now() where id = 'sb-deleted'`)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx,
			`insert into storage_tiers (id, name, tenant, data)
			 values ('tier-bad-2', 'tier-bad-2', 'system', $1::jsonb)`,
			`{"backends":[{"backend_id":"sb-deleted"}]}`)
		Expect(err).To(HaveOccurred())
		var pgErr *pgconn.PgError
		Expect(errors.As(err, &pgErr)).To(BeTrue())
		Expect(pgErr.Code).To(Equal("Z0002"))
		Expect(pgErr.Message).To(ContainSubstring("sb-deleted"))
	})

	It("Prevents soft-deleting a backend referenced by an active storage tier", func(ctx context.Context) {
		err := tool.Migrate(ctx, 76)
		Expect(err).ToNot(HaveOccurred())
		insertBackend(ctx, "sb-protected")

		_, err = conn.Exec(ctx,
			`insert into storage_tiers (id, name, tenant, data)
			 values ('tier-ref', 'tier-ref', 'system', $1::jsonb)`,
			`{"backends":[{"backend_id":"sb-protected"}]}`)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx,
			`update storage_backends set deletion_timestamp = now() where id = 'sb-protected'`)
		Expect(err).To(HaveOccurred())
		var pgErr *pgconn.PgError
		Expect(errors.As(err, &pgErr)).To(BeTrue())
		Expect(pgErr.Code).To(Equal("Z0003"))
		Expect(pgErr.Message).To(ContainSubstring("sb-protected"))
		Expect(pgErr.Message).To(ContainSubstring("StorageTier"))
	})

	It("Reports the count of referencing storage tiers", func(ctx context.Context) {
		err := tool.Migrate(ctx, 76)
		Expect(err).ToNot(HaveOccurred())
		insertBackend(ctx, "sb-shared")

		for _, tierID := range []string{"tier-a", "tier-b", "tier-c"} {
			_, err = conn.Exec(ctx,
				`insert into storage_tiers (id, name, tenant, data)
				 values ($1, $1, 'system', $2::jsonb)`,
				tierID,
				`{"backends":[{"backend_id":"sb-shared"}]}`)
			Expect(err).ToNot(HaveOccurred())
		}

		_, err = conn.Exec(ctx,
			`update storage_backends set deletion_timestamp = now() where id = 'sb-shared'`)
		Expect(err).To(HaveOccurred())
		var pgErr *pgconn.PgError
		Expect(errors.As(err, &pgErr)).To(BeTrue())
		Expect(pgErr.Code).To(Equal("Z0003"))
		Expect(pgErr.Message).To(ContainSubstring("3 StorageTier"))
	})

	It("Allows soft-deleting a backend not referenced by any tier", func(ctx context.Context) {
		err := tool.Migrate(ctx, 76)
		Expect(err).ToNot(HaveOccurred())
		insertBackend(ctx, "sb-unused")

		_, err = conn.Exec(ctx,
			`update storage_backends set deletion_timestamp = now() where id = 'sb-unused'`)
		Expect(err).ToNot(HaveOccurred())
	})

	It("Allows soft-deleting a backend when referencing tiers are already deleted", func(ctx context.Context) {
		err := tool.Migrate(ctx, 76)
		Expect(err).ToNot(HaveOccurred())
		insertBackend(ctx, "sb-was-ref")

		_, err = conn.Exec(ctx,
			`insert into storage_tiers (id, name, tenant, data)
			 values ('tier-gone', 'tier-gone', 'system', $1::jsonb)`,
			`{"backends":[{"backend_id":"sb-was-ref"}]}`)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx,
			`update storage_tiers set deletion_timestamp = now() where id = 'tier-gone'`)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx,
			`update storage_backends set deletion_timestamp = now() where id = 'sb-was-ref'`)
		Expect(err).ToNot(HaveOccurred())
	})
})
