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
	"fmt"

	"github.com/jackc/pgx/v5/pgconn"
	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/ginkgo/v2/dsl/table"
	. "github.com/onsi/gomega"
)

var _ = DescribeMigration("Add-on operator catalog item deletion protection", func() {
	BeforeEach(func(ctx context.Context) {
		Expect(tool.Migrate(ctx, 140)).To(Succeed())
	})

	DescribeTable("protects active catalog references",
		func(ctx context.Context, operatorID, catalogID, policy string) {
			_, err := conn.Exec(ctx, `
				insert into add_on_operators (id, name, tenant, data)
				values ($1, $2, 'shared', '{}'::jsonb)`, operatorID, operatorID)
			Expect(err).ToNot(HaveOccurred())

			data := fmt.Sprintf(`{"published":true,"fields":{"add_on_operators":%s}}`, policy)
			_, err = conn.Exec(ctx, `
				insert into cluster_catalog_items (id, name, tenant, data)
				values ($1, $2, 'system', $3::jsonb)`, catalogID, catalogID, data)
			Expect(err).ToNot(HaveOccurred())

			_, err = conn.Exec(ctx, `update add_on_operators set deletion_timestamp = now() where id = $1`, operatorID)
			var pgError *pgconn.PgError
			Expect(errors.As(err, &pgError)).To(BeTrue())
			Expect(pgError.Code).To(Equal("Z0003"))
		},
		Entry("locked", "operator-locked", "catalog-locked", `{"locked":{"items":[{"id":"operator-locked"}]}}`),
		Entry("editable default", "operator-editable", "catalog-editable", `{"editable":{"default_value":{"items":[{"id":"operator-editable"}]}}}`),
	)

	It("allows deletion when no active catalog item references the operator", func(ctx context.Context) {
		_, err := conn.Exec(ctx, `
			insert into add_on_operators (id, name, tenant, data)
			values ('operator-free', 'operator-free', 'shared', '{}'::jsonb)`)
		Expect(err).ToNot(HaveOccurred())
		_, err = conn.Exec(ctx, `update add_on_operators set deletion_timestamp = now() where id = 'operator-free'`)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx, `
			insert into add_on_operators (id, name, tenant, data)
			values ('operator-archived-catalog', 'operator-archived-catalog', 'shared', '{}'::jsonb)`)
		Expect(err).ToNot(HaveOccurred())
		_, err = conn.Exec(ctx, `
			insert into cluster_catalog_items (id, name, tenant, data, deletion_timestamp)
			values ('catalog-archived', 'catalog-archived', 'system', $1::jsonb, now())`,
			`{"fields":{"add_on_operators":{"locked":{"items":[{"id":"operator-archived-catalog"}]}}}}`)
		Expect(err).ToNot(HaveOccurred())
		_, err = conn.Exec(ctx, `update add_on_operators set deletion_timestamp = now() where id = 'operator-archived-catalog'`)
		Expect(err).ToNot(HaveOccurred())
	})

	It("protects unpublishing an operator referenced by an active catalog item", func(ctx context.Context) {
		cases := []struct {
			name, operatorID, catalogID, policy string
		}{
			{name: "locked", operatorID: "operator-unpublish-locked", catalogID: "catalog-unpublish-locked", policy: `{"locked":{"items":[{"id":"operator-unpublish-locked"}]}}`},
			{name: "editable default", operatorID: "operator-unpublish-editable", catalogID: "catalog-unpublish-editable", policy: `{"editable":{"default_value":{"items":[{"id":"operator-unpublish-editable"}]}}}`},
		}

		for _, tc := range cases {
			By(tc.name)
			_, err := conn.Exec(ctx, `
				insert into add_on_operators (id, name, tenant, data)
				values ($1, $2, 'shared', '{"published":true}'::jsonb)`, tc.operatorID, tc.operatorID)
			Expect(err).ToNot(HaveOccurred())
			data := fmt.Sprintf(`{"published":true,"fields":{"add_on_operators":%s}}`, tc.policy)
			_, err = conn.Exec(ctx, `
				insert into cluster_catalog_items (id, name, tenant, data)
				values ($1, $2, 'system', $3::jsonb)`, tc.catalogID, tc.catalogID, data)
			Expect(err).ToNot(HaveOccurred())

			_, err = conn.Exec(ctx, `update add_on_operators set data = jsonb_set(data, '{published}', 'false'::jsonb) where id = $1`, tc.operatorID)
			var pgError *pgconn.PgError
			Expect(errors.As(err, &pgError)).To(BeTrue())
			Expect(pgError.Code).To(Equal("Z0003"))
		}
	})

	It("does not inspect transitive operator dependencies", func(ctx context.Context) {
		_, err := conn.Exec(ctx, `
			insert into add_on_operators (id, name, tenant, data)
			values
				('operator-dependency', 'operator-dependency', 'shared', '{"published":true}'::jsonb),
				('operator-root', 'operator-root', 'shared', '{"published":true,"dependencies":[{"id":"operator-dependency","name":"operator-dependency"}]}'::jsonb)`)
		Expect(err).ToNot(HaveOccurred())
		_, err = conn.Exec(ctx, `
			insert into cluster_catalog_items (id, name, tenant, data)
			values ('catalog-dependency', 'catalog-dependency', 'system',
			'{"fields":{"add_on_operators":{"locked":{"items":[{"id":"operator-root"}]}}}}'::jsonb)`)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx, `update add_on_operators set deletion_timestamp = now() where id = 'operator-dependency'`)
		Expect(err).ToNot(HaveOccurred())
	})
})
