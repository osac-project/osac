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
	"fmt"

	"github.com/jackc/pgx/v5"
	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/ginkgo/v2/dsl/table"
	. "github.com/onsi/gomega"
)

var _ = DescribeMigration("Create external IP tables", func() {
	DescribeTable(
		"Creates the expected tables",
		func(ctx context.Context, table string) {
			err := tool.Migrate(ctx, 61)
			Expect(err).ToNot(HaveOccurred())

			quotedTable := pgx.Identifier{table}.Sanitize()

			_, err = conn.Exec(ctx,
				fmt.Sprintf(`insert into %s (id, tenant, data) values ($1, $2, $3)`, quotedTable),
				"test-id", "system", `{}`,
			)
			Expect(err).ToNot(HaveOccurred())

			var count int
			err = conn.QueryRow(ctx,
				fmt.Sprintf(`select count(*) from %s where id = $1`, quotedTable),
				"test-id",
			).Scan(&count)
			Expect(err).ToNot(HaveOccurred())
			Expect(count).To(Equal(1))

			_, err = conn.Exec(ctx,
				fmt.Sprintf(`insert into %s (id, tenant, data) values ($1, $2, $3)`, quotedTable),
				"bad-tenant-id", "no-such-tenant", `{}`,
			)
			Expect(err).To(HaveOccurred())
		},
		Entry("external_ip_pools", "external_ip_pools"),
		Entry("external_ips", "external_ips"),
		Entry("external_ip_attachments", "external_ip_attachments"),
	)

	insertAttachment := func(ctx context.Context, id, externalIP, computeInstance string) error {
		_, err := conn.Exec(
			ctx,
			`insert into external_ip_attachments (id, tenant, data) values ($1, $2, $3)`,
			id, "system",
			`{"spec": {"external_ip": "`+externalIP+`", "compute_instance": "`+computeInstance+`"}}`,
		)
		return err
	}

	softDelete := func(ctx context.Context, id string) {
		_, err := conn.Exec(
			ctx,
			`update external_ip_attachments set deletion_timestamp = now() where id = $1`,
			id,
		)
		Expect(err).ToNot(HaveOccurred())
	}

	It("Rejects duplicate active external IP", func(ctx context.Context) {
		err := tool.Migrate(ctx, 61)
		Expect(err).ToNot(HaveOccurred())

		err = insertAttachment(ctx, "a1", "eip-1", "ci-1")
		Expect(err).ToNot(HaveOccurred())

		err = insertAttachment(ctx, "a2", "eip-1", "ci-2")
		Expect(err).To(HaveOccurred())
	})

	It("Rejects duplicate active compute instance", func(ctx context.Context) {
		err := tool.Migrate(ctx, 61)
		Expect(err).ToNot(HaveOccurred())

		err = insertAttachment(ctx, "a1", "eip-1", "ci-1")
		Expect(err).ToNot(HaveOccurred())

		err = insertAttachment(ctx, "a2", "eip-2", "ci-1")
		Expect(err).To(HaveOccurred())
	})

	It("Allows same external IP after soft delete", func(ctx context.Context) {
		err := tool.Migrate(ctx, 61)
		Expect(err).ToNot(HaveOccurred())

		err = insertAttachment(ctx, "a1", "eip-1", "ci-1")
		Expect(err).ToNot(HaveOccurred())

		softDelete(ctx, "a1")

		err = insertAttachment(ctx, "a2", "eip-1", "ci-2")
		Expect(err).ToNot(HaveOccurred())
	})

	It("Allows same compute instance after soft delete", func(ctx context.Context) {
		err := tool.Migrate(ctx, 61)
		Expect(err).ToNot(HaveOccurred())

		err = insertAttachment(ctx, "a1", "eip-1", "ci-1")
		Expect(err).ToNot(HaveOccurred())

		softDelete(ctx, "a1")

		err = insertAttachment(ctx, "a2", "eip-2", "ci-1")
		Expect(err).ToNot(HaveOccurred())
	})

	It("Allows different external IPs and compute instances", func(ctx context.Context) {
		err := tool.Migrate(ctx, 61)
		Expect(err).ToNot(HaveOccurred())

		err = insertAttachment(ctx, "a1", "eip-1", "ci-1")
		Expect(err).ToNot(HaveOccurred())

		err = insertAttachment(ctx, "a2", "eip-2", "ci-2")
		Expect(err).ToNot(HaveOccurred())
	})
})
