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

var _ = DescribeMigration("Add ExternalIP not-in-use triggers", func() {
	BeforeEach(func(ctx context.Context) {
		err := tool.Migrate(ctx, 103)
		Expect(err).ToNot(HaveOccurred())
	})

	insertPool := func(ctx context.Context, id string) {
		_, err := conn.Exec(ctx,
			`insert into external_ip_pools (id, name, tenant, data) values ($1, $1, 'system', '{}')`, id)
		Expect(err).ToNot(HaveOccurred())
	}

	insertExternalIP := func(ctx context.Context, id, poolID string) {
		_, err := conn.Exec(ctx,
			`insert into external_ips (id, name, tenant, data) values ($1, $1, 'system', $2::jsonb)`,
			id, `{"spec":{"pool":{"id":"`+poolID+`"}}}`)
		Expect(err).ToNot(HaveOccurred())
	}

	insertAttachment := func(ctx context.Context, id, eipID string) error {
		_, err := conn.Exec(ctx,
			`insert into external_ip_attachments (id, name, tenant, data) values ($1, $1, 'system', $2::jsonb)`,
			id, `{"spec":{"external_ip":{"id":"`+eipID+`"},"compute_instance":"ci-`+id+`"}}`)
		return err
	}

	insertNATGateway := func(ctx context.Context, id, eipID string) error {
		_, err := conn.Exec(ctx,
			`insert into nat_gateways (id, name, tenant, data) values ($1, $1, 'system', $2::jsonb)`,
			id, `{"spec":{"virtual_network":"vn-1","external_ip":"`+eipID+`"}}`)
		return err
	}

	softDelete := func(ctx context.Context, table, id string) error {
		_, err := conn.Exec(ctx,
			`update `+table+` set deletion_timestamp = now() where id = $1`, id)
		return err
	}

	expectPgErr := func(err error, code string) *pgconn.PgError {
		Expect(err).To(HaveOccurred())
		var pgErr *pgconn.PgError
		ExpectWithOffset(1, errors.As(err, &pgErr)).To(BeTrue())
		ExpectWithOffset(1, pgErr.Code).To(Equal(code))
		return pgErr
	}

	// -- ExternalIP not-in-use guard --

	It("Prevents deleting an ExternalIP referenced by an active attachment", func(ctx context.Context) {
		insertPool(ctx, "pool-1")
		insertExternalIP(ctx, "eip-1", "pool-1")
		err := insertAttachment(ctx, "att-1", "eip-1")
		Expect(err).ToNot(HaveOccurred())

		err = softDelete(ctx, "external_ips", "eip-1")
		pgErr := expectPgErr(err, "Z0003")
		Expect(pgErr.Message).To(ContainSubstring("eip-1"))
		Expect(pgErr.Message).To(ContainSubstring("ExternalIPAttachment"))
	})

	It("Prevents deleting an ExternalIP referenced by an active NATGateway", func(ctx context.Context) {
		insertPool(ctx, "pool-2")
		insertExternalIP(ctx, "eip-2", "pool-2")

		_, err := conn.Exec(ctx,
			`insert into virtual_networks (id, name, tenant, data) values ('vn-1', 'vn-1', 'system', '{}')`)
		Expect(err).ToNot(HaveOccurred())

		err = insertNATGateway(ctx, "ng-1", "eip-2")
		Expect(err).ToNot(HaveOccurred())

		err = softDelete(ctx, "external_ips", "eip-2")
		pgErr := expectPgErr(err, "Z0003")
		Expect(pgErr.Message).To(ContainSubstring("eip-2"))
		Expect(pgErr.Message).To(ContainSubstring("NATGateway"))
	})

	It("Allows deleting an ExternalIP when attachment is soft-deleted", func(ctx context.Context) {
		insertPool(ctx, "pool-3")
		insertExternalIP(ctx, "eip-3", "pool-3")
		err := insertAttachment(ctx, "att-3", "eip-3")
		Expect(err).ToNot(HaveOccurred())

		err = softDelete(ctx, "external_ip_attachments", "att-3")
		Expect(err).ToNot(HaveOccurred())

		err = softDelete(ctx, "external_ips", "eip-3")
		Expect(err).ToNot(HaveOccurred())
	})

	It("Allows deleting an unreferenced ExternalIP", func(ctx context.Context) {
		insertPool(ctx, "pool-4")
		insertExternalIP(ctx, "eip-4", "pool-4")

		err := softDelete(ctx, "external_ips", "eip-4")
		Expect(err).ToNot(HaveOccurred())
	})

	// -- ExternalIPPool not-in-use guard --

	It("Prevents deleting an ExternalIPPool referenced by an active ExternalIP", func(ctx context.Context) {
		insertPool(ctx, "pool-5")
		insertExternalIP(ctx, "eip-5", "pool-5")

		err := softDelete(ctx, "external_ip_pools", "pool-5")
		pgErr := expectPgErr(err, "Z0003")
		Expect(pgErr.Message).To(ContainSubstring("pool-5"))
		Expect(pgErr.Message).To(ContainSubstring("ExternalIP"))
	})

	It("Allows deleting an ExternalIPPool when ExternalIP is soft-deleted", func(ctx context.Context) {
		insertPool(ctx, "pool-6")
		insertExternalIP(ctx, "eip-6", "pool-6")

		err := softDelete(ctx, "external_ips", "eip-6")
		Expect(err).ToNot(HaveOccurred())

		err = softDelete(ctx, "external_ip_pools", "pool-6")
		Expect(err).ToNot(HaveOccurred())
	})

	// -- ExternalIP ref-exists guard --

	It("Rejects attachment when ExternalIP does not exist", func(ctx context.Context) {
		err := insertAttachment(ctx, "att-bad", "nonexistent-eip")
		pgErr := expectPgErr(err, "Z0002")
		Expect(pgErr.Message).To(ContainSubstring("nonexistent-eip"))
	})

	It("Rejects NATGateway when ExternalIP does not exist", func(ctx context.Context) {
		_, err := conn.Exec(ctx,
			`insert into virtual_networks (id, name, tenant, data) values ('vn-2', 'vn-2', 'system', '{}')`)
		Expect(err).ToNot(HaveOccurred())

		err = insertNATGateway(ctx, "ng-bad", "nonexistent-eip")
		pgErr := expectPgErr(err, "Z0002")
		Expect(pgErr.Message).To(ContainSubstring("nonexistent-eip"))
	})

	It("Rejects attachment when ExternalIP is soft-deleted", func(ctx context.Context) {
		insertPool(ctx, "pool-7")
		insertExternalIP(ctx, "eip-7", "pool-7")
		err := softDelete(ctx, "external_ips", "eip-7")
		Expect(err).ToNot(HaveOccurred())

		err = insertAttachment(ctx, "att-dead", "eip-7")
		pgErr := expectPgErr(err, "Z0002")
		Expect(pgErr.Message).To(ContainSubstring("eip-7"))
	})

	// -- ExternalIPPool ref-exists guard --

	It("Rejects ExternalIP when pool does not exist", func(ctx context.Context) {
		_, err := conn.Exec(ctx,
			`insert into external_ips (id, name, tenant, data) values ('eip-bad', 'eip-bad', 'system', $1::jsonb)`,
			`{"spec":{"pool":{"id":"nonexistent-pool"}}}`)
		pgErr := expectPgErr(err, "Z0002")
		Expect(pgErr.Message).To(ContainSubstring("nonexistent-pool"))
	})

	It("Rejects ExternalIP when pool is soft-deleted", func(ctx context.Context) {
		insertPool(ctx, "pool-8")
		err := softDelete(ctx, "external_ip_pools", "pool-8")
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx,
			`insert into external_ips (id, name, tenant, data) values ('eip-8', 'eip-8', 'system', $1::jsonb)`,
			`{"spec":{"pool":{"id":"pool-8"}}}`)
		pgErr := expectPgErr(err, "Z0002")
		Expect(pgErr.Message).To(ContainSubstring("pool-8"))
	})
})
