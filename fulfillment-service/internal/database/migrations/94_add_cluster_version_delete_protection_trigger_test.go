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

var _ = DescribeMigration("Add cluster version delete protection trigger", func() {
	BeforeEach(func(ctx context.Context) {
		err := tool.Migrate(ctx, 94)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx,
			`insert into tenants (id, name, tenant, creator, data)
			 values ('test-tenant', 'test-tenant', 'test-tenant', 'system', '{}')
			 on conflict do nothing`)
		Expect(err).ToNot(HaveOccurred())
	})

	insertVersion := func(ctx context.Context, id, name string) {
		_, err := conn.Exec(ctx,
			`insert into cluster_versions (id, name, tenant, data)
			 values ($1, $2, 'shared', $3::jsonb)`,
			id, name, `{"spec":{"version":"4.17.0","image":"quay.io/test:4.17.0"}}`)
		Expect(err).ToNot(HaveOccurred())
	}

	deleteVersion := func(ctx context.Context, id string) error {
		_, err := conn.Exec(ctx,
			`update cluster_versions set deletion_timestamp = now() where id = $1`, id)
		return err
	}

	softDelete := func(ctx context.Context, table, id string) {
		_, err := conn.Exec(ctx,
			`update `+table+` set deletion_timestamp = now() where id = $1`, id)
		Expect(err).ToNot(HaveOccurred())
	}

	expectPgErr := func(err error, code string) *pgconn.PgError {
		Expect(err).To(HaveOccurred())
		var pgErr *pgconn.PgError
		ExpectWithOffset(1, errors.As(err, &pgErr)).To(BeTrue())
		ExpectWithOffset(1, pgErr.Code).To(Equal(code))
		return pgErr
	}

	// Outbound: delete protection.

	It("Prevents deleting a cluster version referenced by an active cluster", func(ctx context.Context) {
		insertVersion(ctx, "cv-1", "4-17-0")

		_, err := conn.Exec(ctx,
			`insert into clusters (id, tenant, data) values ('cluster-1', 'test-tenant', $1::jsonb)`,
			`{"spec":{"version":{"name":"4-17-0"}}}`)
		Expect(err).ToNot(HaveOccurred())

		err = deleteVersion(ctx, "cv-1")
		pgErr := expectPgErr(err, "Z0003")
		Expect(pgErr.Message).To(ContainSubstring("4-17-0"))
		Expect(pgErr.Message).To(ContainSubstring("cluster"))
		Expect(pgErr.Message).ToNot(ContainSubstring("cluster-1"))
	})

	It("Prevents deleting a cluster version referenced by an active cluster template", func(ctx context.Context) {
		insertVersion(ctx, "cv-2", "4-17-1")

		_, err := conn.Exec(ctx,
			`insert into cluster_templates (id, tenant, data) values ('template-1', 'test-tenant', $1::jsonb)`,
			`{"spec_defaults":{"version":{"name":"4-17-1"}}}`)
		Expect(err).ToNot(HaveOccurred())

		err = deleteVersion(ctx, "cv-2")
		pgErr := expectPgErr(err, "Z0003")
		Expect(pgErr.Message).To(ContainSubstring("4-17-1"))
		Expect(pgErr.Message).To(ContainSubstring("cluster template"))
		Expect(pgErr.Message).ToNot(ContainSubstring("template-1"))
	})

	It("Prevents deleting a cluster version referenced by an active cluster catalog item", func(ctx context.Context) {
		insertVersion(ctx, "cv-3", "4-17-2")

		_, err := conn.Exec(ctx,
			`insert into cluster_catalog_items (id, tenant, data) values ('cci-1', 'test-tenant', $1::jsonb)`,
			`{"field_definitions":[{"path":"version","default":{"name":"4-17-2"}}]}`)
		Expect(err).ToNot(HaveOccurred())

		err = deleteVersion(ctx, "cv-3")
		pgErr := expectPgErr(err, "Z0003")
		Expect(pgErr.Message).To(ContainSubstring("4-17-2"))
		Expect(pgErr.Message).To(ContainSubstring("cluster catalog item"))
		Expect(pgErr.Message).ToNot(ContainSubstring("cci-1"))
	})

	It("Allows deleting a cluster version that is not referenced", func(ctx context.Context) {
		insertVersion(ctx, "cv-4", "4-17-3")

		err := deleteVersion(ctx, "cv-4")
		Expect(err).ToNot(HaveOccurred())
	})

	It("Allows deleting a cluster version when the referencing cluster is already soft-deleted", func(ctx context.Context) {
		insertVersion(ctx, "cv-5", "4-17-4")

		_, err := conn.Exec(ctx,
			`insert into clusters (id, tenant, data) values ('cluster-2', 'test-tenant', $1::jsonb)`,
			`{"spec":{"version":{"name":"4-17-4"}}}`)
		Expect(err).ToNot(HaveOccurred())
		softDelete(ctx, "clusters", "cluster-2")

		err = deleteVersion(ctx, "cv-5")
		Expect(err).ToNot(HaveOccurred())
	})

	It("Allows deleting a cluster version when the referencing template is already soft-deleted", func(ctx context.Context) {
		insertVersion(ctx, "cv-6", "4-17-5")

		_, err := conn.Exec(ctx,
			`insert into cluster_templates (id, tenant, data) values ('template-2', 'test-tenant', $1::jsonb)`,
			`{"spec_defaults":{"version":{"name":"4-17-5"}}}`)
		Expect(err).ToNot(HaveOccurred())
		softDelete(ctx, "cluster_templates", "template-2")

		err = deleteVersion(ctx, "cv-6")
		Expect(err).ToNot(HaveOccurred())
	})

	It("Allows deleting a cluster version when the referencing catalog item is already soft-deleted", func(ctx context.Context) {
		insertVersion(ctx, "cv-7", "4-17-6")

		_, err := conn.Exec(ctx,
			`insert into cluster_catalog_items (id, tenant, data) values ('cci-2', 'test-tenant', $1::jsonb)`,
			`{"field_definitions":[{"path":"version","default":{"name":"4-17-6"}}]}`)
		Expect(err).ToNot(HaveOccurred())
		softDelete(ctx, "cluster_catalog_items", "cci-2")

		err = deleteVersion(ctx, "cv-7")
		Expect(err).ToNot(HaveOccurred())
	})
})
