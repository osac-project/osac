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
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"
)

var _ = DescribeMigration("Add instance type catalog deletion protection", func() {
	BeforeEach(func(ctx context.Context) {
		err := tool.Migrate(ctx, 114)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx, `
			insert into tenants (id, name, tenant, creator, data)
			values ('test-tenant', 'test-tenant', 'test-tenant', 'system', '{}')
			on conflict do nothing`)
		Expect(err).ToNot(HaveOccurred())
	})

	insertInstanceType := func(ctx context.Context, id string) {
		_, err := conn.Exec(ctx, `
			insert into instance_types (id, name, tenant, data)
			values ($1, $1, 'shared', '{}')`, id)
		Expect(err).ToNot(HaveOccurred())
	}

	softDeleteInstanceType := func(ctx context.Context, id string) error {
		_, err := conn.Exec(ctx, `update instance_types set deletion_timestamp = now() where id = $1`, id)
		return err
	}

	expectInUse := func(err error, id string) {
		Expect(err).To(HaveOccurred())
		var pgErr *pgconn.PgError
		Expect(errors.As(err, &pgErr)).To(BeTrue())
		Expect(pgErr.Code).To(Equal("Z0003"))
		Expect(pgErr.Message).To(ContainSubstring(id))
	}

	expectInvalidReference := func(err error, id string) {
		Expect(err).To(HaveOccurred())
		var pgErr *pgconn.PgError
		Expect(errors.As(err, &pgErr)).To(BeTrue())
		Expect(pgErr.Code).To(Equal("Z0002"))
		Expect(pgErr.Message).To(ContainSubstring(id))
	}

	rollbackTx := func(ctx context.Context, tx pgx.Tx) {
		err := tx.Rollback(ctx)
		// A committed transaction is already closed when cleanup runs; all other rollback errors are unexpected.
		if !errors.Is(err, pgx.ErrTxClosed) {
			Expect(err).ToNot(HaveOccurred())
		}
	}

	It("creates a partial GIN index for active catalog-item field definitions", func(ctx context.Context) {
		var indexDefinition string
		err := conn.QueryRow(ctx, `
			select indexdef
			from pg_indexes
			where schemaname = current_schema()
			  and indexname = 'compute_instance_catalog_items_instance_type'`).Scan(&indexDefinition)
		Expect(err).ToNot(HaveOccurred())
		Expect(indexDefinition).To(ContainSubstring("USING gin"))
		Expect(indexDefinition).To(ContainSubstring("jsonb_path_ops"))
		Expect(indexDefinition).To(ContainSubstring("deletion_timestamp"))
	})

	It("prevents soft-deleting an instance type referenced by an active catalog item", func(ctx context.Context) {
		insertInstanceType(ctx, "referenced-type")

		// Compute instance catalog items serialize field_definitions at the top level. Instance type defaults are bare
		// strings containing the instance type name, rather than reference objects.
		_, err := conn.Exec(ctx, `
			insert into compute_instance_catalog_items (id, name, tenant, data)
			values ($1, $1, 'test-tenant', $2::jsonb)`,
			"catalog-item", `{"field_definitions":[{"path":"spec.instance_type","default":"referenced-type"}]}`)
		Expect(err).ToNot(HaveOccurred())

		expectInUse(softDeleteInstanceType(ctx, "referenced-type"), "referenced-type")
	})

	It("rejects active catalog items that reference a missing or deleted instance type", func(ctx context.Context) {
		_, err := conn.Exec(ctx, `
			insert into compute_instance_catalog_items (id, name, tenant, data)
			values ($1, $1, 'test-tenant', $2::jsonb)`,
			"missing-type-catalog-item", `{"field_definitions":[{"path":"spec.instance_type","default":"missing-type"}]}`)
		expectInvalidReference(err, "missing-type")

		insertInstanceType(ctx, "deleted-type")
		Expect(softDeleteInstanceType(ctx, "deleted-type")).To(Succeed())
		_, err = conn.Exec(ctx, `
			insert into compute_instance_catalog_items (id, name, tenant, data)
			values ($1, $1, 'test-tenant', $2::jsonb)`,
			"deleted-type-catalog-item", `{"field_definitions":[{"path":"spec.instance_type","default":"deleted-type"}]}`)
		expectInvalidReference(err, "deleted-type")
	})

	It("rejects a catalog-item update that introduces an invalid instance type reference", func(ctx context.Context) {
		_, err := conn.Exec(ctx, `
			insert into compute_instance_catalog_items (id, name, tenant, data)
			values ('updated-catalog-item', 'updated-catalog-item', 'test-tenant', '{}')`)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx, `
			update compute_instance_catalog_items
			set data = $1::jsonb
			where id = 'updated-catalog-item'`,
			`{"field_definitions":[{"path":"spec.instance_type","default":"missing-type"}]}`)
		expectInvalidReference(err, "missing-type")
	})

	It("serializes instance type deletion before concurrent catalog-item creation", func(ctx context.Context) {
		insertInstanceType(ctx, "delete-first-type")

		deleteConn, err := db.Connection(ctx)
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(deleteConn.Close)
		createConn, err := db.Connection(ctx)
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(createConn.Close)

		deleteTx, err := deleteConn.Begin(ctx)
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func(ctx context.Context) { rollbackTx(ctx, deleteTx) })
		_, err = deleteTx.Exec(ctx, `
			update instance_types set deletion_timestamp = now() where id = 'delete-first-type'`)
		Expect(err).ToNot(HaveOccurred())

		createResult := make(chan error, 1)
		go func() {
			createCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_, createErr := createConn.Exec(createCtx, `
				insert into compute_instance_catalog_items (id, name, tenant, data)
				values ('concurrent-catalog-item', 'concurrent-catalog-item', 'test-tenant',
				'{"field_definitions":[{"path":"spec.instance_type","default":"delete-first-type"}]}')`)
			createResult <- createErr
		}()

		Consistently(createResult, 200*time.Millisecond).ShouldNot(Receive(),
			"catalog-item creation should wait for the instance type deletion's row lock")
		Expect(deleteTx.Commit(ctx)).To(Succeed())

		Eventually(createResult, 5*time.Second).Should(Receive(MatchError(func(err error) bool {
			var pgErr *pgconn.PgError
			return errors.As(err, &pgErr) && pgErr.Code == "Z0002"
		}, "a missing-or-deleted instance type database error")))

		var catalogItemCount int
		err = conn.QueryRow(ctx, `
			select count(*) from compute_instance_catalog_items where id = 'concurrent-catalog-item'`).
			Scan(&catalogItemCount)
		Expect(err).ToNot(HaveOccurred())
		Expect(catalogItemCount).To(BeZero())
	})

	It("serializes catalog-item creation before concurrent instance type deletion", func(ctx context.Context) {
		insertInstanceType(ctx, "create-first-type")

		createConn, err := db.Connection(ctx)
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(createConn.Close)
		deleteConn, err := db.Connection(ctx)
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(deleteConn.Close)

		createTx, err := createConn.Begin(ctx)
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func(ctx context.Context) { rollbackTx(ctx, createTx) })
		_, err = createTx.Exec(ctx, `
			insert into compute_instance_catalog_items (id, name, tenant, data)
			values ('locking-catalog-item', 'locking-catalog-item', 'test-tenant',
			'{"field_definitions":[{"path":"spec.instance_type","default":"create-first-type"}]}')`)
		Expect(err).ToNot(HaveOccurred())

		deleteResult := make(chan error, 1)
		go func() {
			deleteCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_, deleteErr := deleteConn.Exec(deleteCtx, `
				update instance_types set deletion_timestamp = now() where id = 'create-first-type'`)
			deleteResult <- deleteErr
		}()

		Consistently(deleteResult, 200*time.Millisecond).ShouldNot(Receive(),
			"instance type deletion should wait for the catalog item's row lock")
		Expect(createTx.Commit(ctx)).To(Succeed())

		Eventually(deleteResult, 5*time.Second).Should(Receive(MatchError(func(err error) bool {
			var pgErr *pgconn.PgError
			return errors.As(err, &pgErr) && pgErr.Code == "Z0003"
		}, "an instance type in-use database error")))

		var active bool
		err = conn.QueryRow(ctx, `
			select deletion_timestamp = 'epoch' from instance_types where id = 'create-first-type'`).Scan(&active)
		Expect(err).ToNot(HaveOccurred())
		Expect(active).To(BeTrue())
	})

	It("allows soft-deleting an unreferenced instance type", func(ctx context.Context) {
		insertInstanceType(ctx, "unreferenced-type")

		Expect(softDeleteInstanceType(ctx, "unreferenced-type")).To(Succeed())
	})

	It("does not treat a different catalog default as a reference", func(ctx context.Context) {
		insertInstanceType(ctx, "referenced-type")
		insertInstanceType(ctx, "other-type")

		_, err := conn.Exec(ctx, `
			insert into compute_instance_catalog_items (id, name, tenant, data)
			values ($1, $1, 'test-tenant', $2::jsonb)`,
			"catalog-item", `{"field_definitions":[{"path":"spec.instance_type","default":"referenced-type"}]}`)
		Expect(err).ToNot(HaveOccurred())

		Expect(softDeleteInstanceType(ctx, "other-type")).To(Succeed())
	})

	It("ignores references from soft-deleted catalog items", func(ctx context.Context) {
		insertInstanceType(ctx, "referenced-type")

		_, err := conn.Exec(ctx, `
			insert into compute_instance_catalog_items (id, name, tenant, data, deletion_timestamp)
			values ($1, $1, 'test-tenant', $2::jsonb, now())`,
			"deleted-catalog-item", `{"field_definitions":[{"path":"spec.instance_type","default":"referenced-type"}]}`)
		Expect(err).ToNot(HaveOccurred())

		Expect(softDeleteInstanceType(ctx, "referenced-type")).To(Succeed())
	})

	It("handles missing or non-array field definitions", func(ctx context.Context) {
		insertInstanceType(ctx, "unreferenced-type")

		for _, test := range []struct {
			id   string
			data string
		}{
			{"missing-field-definitions", `{}`},
			{"null-field-definitions", `{"field_definitions":null}`},
			{"object-field-definitions", `{"field_definitions":{}}`},
		} {
			_, err := conn.Exec(ctx, `
				insert into compute_instance_catalog_items (id, name, tenant, data)
				values ($1, $1, 'test-tenant', $2::jsonb)`, test.id, test.data)
			Expect(err).ToNot(HaveOccurred())
		}

		Expect(softDeleteInstanceType(ctx, "unreferenced-type")).To(Succeed())
	})

	It("preserves compute instance deletion protection", func(ctx context.Context) {
		insertInstanceType(ctx, "instance-type")

		_, err := conn.Exec(ctx, `
			insert into compute_instances (id, name, tenant, data)
			values ($1, $1, 'test-tenant', $2::jsonb)`,
			"compute-instance", `{"spec":{"instance_type":{"id":"instance-type"}}}`)
		Expect(err).ToNot(HaveOccurred())

		expectInUse(softDeleteInstanceType(ctx, "instance-type"), "instance-type")
	})

	It("preserves compute instance template deletion protection", func(ctx context.Context) {
		insertInstanceType(ctx, "instance-type")

		_, err := conn.Exec(ctx, `
			insert into compute_instance_templates (id, name, tenant, data)
			values ($1, $1, 'test-tenant', $2::jsonb)`,
			"compute-instance-template", `{"spec_defaults":{"instance_type":{"id":"instance-type"}}}`)
		Expect(err).ToNot(HaveOccurred())

		expectInUse(softDeleteInstanceType(ctx, "instance-type"), "instance-type")
	})
})
