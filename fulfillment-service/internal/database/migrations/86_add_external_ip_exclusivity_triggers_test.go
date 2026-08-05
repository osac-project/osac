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

var _ = DescribeMigration("Add ExternalIP exclusivity triggers", func() {
	insertExternalIP := func(ctx context.Context, id string) {
		_, err := conn.Exec(ctx,
			`insert into external_ips (id, tenant, data) values ($1, $2, $3)`,
			id, "system", `{}`,
		)
		Expect(err).ToNot(HaveOccurred())
	}

	insertVN := func(ctx context.Context, id string) {
		_, err := conn.Exec(ctx,
			`insert into virtual_networks (id, tenant, data) values ($1, $2, $3)`,
			id, "system", `{}`,
		)
		Expect(err).ToNot(HaveOccurred())
	}

	insertAttachment := func(ctx context.Context, id, externalIP string) error {
		_, err := conn.Exec(ctx,
			`insert into external_ip_attachments (id, tenant, data) values ($1, $2, $3)`,
			id, "system",
			`{"spec":{"external_ip":"`+externalIP+`","compute_instance":"ci-`+id+`"}}`,
		)
		return err
	}

	insertNATGateway := func(ctx context.Context, id, virtualNetwork, externalIP string) error {
		_, err := conn.Exec(ctx,
			`insert into nat_gateways (id, tenant, data) values ($1, $2, $3)`,
			id, "system",
			`{"spec":{"virtual_network":"`+virtualNetwork+`","external_ip":"`+externalIP+`"}}`,
		)
		return err
	}

	softDeleteAttachment := func(ctx context.Context, id string) {
		_, err := conn.Exec(ctx,
			`update external_ip_attachments set deletion_timestamp = now() where id = $1`,
			id,
		)
		Expect(err).ToNot(HaveOccurred())
	}

	softDeleteNATGateway := func(ctx context.Context, id string) {
		_, err := conn.Exec(ctx,
			`update nat_gateways set deletion_timestamp = now() where id = $1`,
			id,
		)
		Expect(err).ToNot(HaveOccurred())
	}

	It("Rejects NATGateway when ExternalIP is used by an ExternalIPAttachment", func(ctx context.Context) {
		err := tool.Migrate(ctx, 86)
		Expect(err).ToNot(HaveOccurred())

		insertExternalIP(ctx, "eip-1")
		insertVN(ctx, "vn-1")

		err = insertAttachment(ctx, "a1", "eip-1")
		Expect(err).ToNot(HaveOccurred())

		err = insertNATGateway(ctx, "ng-1", "vn-1", "eip-1")
		Expect(err).To(HaveOccurred())
		var pgErr *pgconn.PgError
		Expect(errors.As(err, &pgErr)).To(BeTrue())
		Expect(pgErr.Code).To(Equal("Z0004"))
		Expect(pgErr.Message).To(ContainSubstring("eip-1"))
		Expect(pgErr.Message).To(ContainSubstring("a1"))
	})

	It("Rejects ExternalIPAttachment when ExternalIP is used by a NATGateway", func(ctx context.Context) {
		err := tool.Migrate(ctx, 86)
		Expect(err).ToNot(HaveOccurred())

		insertExternalIP(ctx, "eip-2")
		insertVN(ctx, "vn-2")

		err = insertNATGateway(ctx, "ng-2", "vn-2", "eip-2")
		Expect(err).ToNot(HaveOccurred())

		err = insertAttachment(ctx, "a2", "eip-2")
		Expect(err).To(HaveOccurred())
		var pgErr *pgconn.PgError
		Expect(errors.As(err, &pgErr)).To(BeTrue())
		Expect(pgErr.Code).To(Equal("Z0004"))
		Expect(pgErr.Message).To(ContainSubstring("eip-2"))
		Expect(pgErr.Message).To(ContainSubstring("ng-2"))
	})

	It("Rejects second ExternalIPAttachment for same ExternalIP", func(ctx context.Context) {
		err := tool.Migrate(ctx, 86)
		Expect(err).ToNot(HaveOccurred())

		insertExternalIP(ctx, "eip-3")

		err = insertAttachment(ctx, "a3", "eip-3")
		Expect(err).ToNot(HaveOccurred())

		err = insertAttachment(ctx, "a4", "eip-3")
		Expect(err).To(HaveOccurred())
		var pgErr *pgconn.PgError
		Expect(errors.As(err, &pgErr)).To(BeTrue())
		Expect(pgErr.Code).To(Equal("Z0004"))
		Expect(pgErr.Message).To(ContainSubstring("eip-3"))
		Expect(pgErr.Message).To(ContainSubstring("a3"))
	})

	It("Rejects second NATGateway for same ExternalIP", func(ctx context.Context) {
		err := tool.Migrate(ctx, 86)
		Expect(err).ToNot(HaveOccurred())

		insertExternalIP(ctx, "eip-4")
		insertVN(ctx, "vn-4")
		insertVN(ctx, "vn-5")

		err = insertNATGateway(ctx, "ng-4", "vn-4", "eip-4")
		Expect(err).ToNot(HaveOccurred())

		err = insertNATGateway(ctx, "ng-5", "vn-5", "eip-4")
		Expect(err).To(HaveOccurred())
		var pgErr *pgconn.PgError
		Expect(errors.As(err, &pgErr)).To(BeTrue())
		Expect(pgErr.Code).To(Equal("Z0004"))
		Expect(pgErr.Message).To(ContainSubstring("eip-4"))
		Expect(pgErr.Message).To(ContainSubstring("ng-4"))
	})

	It("Allows different ExternalIPs for attachment and NATGateway", func(ctx context.Context) {
		err := tool.Migrate(ctx, 86)
		Expect(err).ToNot(HaveOccurred())

		insertExternalIP(ctx, "eip-5")
		insertExternalIP(ctx, "eip-6")
		insertVN(ctx, "vn-6")

		err = insertAttachment(ctx, "a5", "eip-5")
		Expect(err).ToNot(HaveOccurred())

		err = insertNATGateway(ctx, "ng-6", "vn-6", "eip-6")
		Expect(err).ToNot(HaveOccurred())
	})

	It("Allows NATGateway after ExternalIPAttachment is soft-deleted", func(ctx context.Context) {
		err := tool.Migrate(ctx, 86)
		Expect(err).ToNot(HaveOccurred())

		insertExternalIP(ctx, "eip-7")
		insertVN(ctx, "vn-7")

		err = insertAttachment(ctx, "a7", "eip-7")
		Expect(err).ToNot(HaveOccurred())

		softDeleteAttachment(ctx, "a7")

		err = insertNATGateway(ctx, "ng-7", "vn-7", "eip-7")
		Expect(err).ToNot(HaveOccurred())
	})

	It("Allows ExternalIPAttachment after NATGateway is soft-deleted", func(ctx context.Context) {
		err := tool.Migrate(ctx, 86)
		Expect(err).ToNot(HaveOccurred())

		insertExternalIP(ctx, "eip-8")
		insertVN(ctx, "vn-8")

		err = insertNATGateway(ctx, "ng-8", "vn-8", "eip-8")
		Expect(err).ToNot(HaveOccurred())

		softDeleteNATGateway(ctx, "ng-8")

		err = insertAttachment(ctx, "a8", "eip-8")
		Expect(err).ToNot(HaveOccurred())
	})
})
