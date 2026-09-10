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
	. "github.com/onsi/gomega"
)

var _ = DescribeMigration("Add default networking delete guard", func() {
	BeforeEach(func(ctx context.Context) {
		err := tool.Migrate(ctx, 113)
		Expect(err).ToNot(HaveOccurred())
	})

	defaultLabels := `{"osac.openshift.io/default": "true"}`
	emptyLabels := `{}`

	insertResource := func(ctx context.Context, table, id, labels string) {
		_, err := conn.Exec(ctx,
			fmt.Sprintf(`insert into %s (id, name, tenant, labels, data) values ($1, $1, 'test-tenant', $2::jsonb, '{}')`, table),
			id, labels)
		Expect(err).ToNot(HaveOccurred())
	}

	softDelete := func(ctx context.Context, table, id string) error {
		_, err := conn.Exec(ctx,
			fmt.Sprintf(`update %s set deletion_timestamp = now() where id = $1`, table), id)
		return err
	}

	softDeleteWithBypass := func(ctx context.Context, table, id string) error {
		tx, err := conn.Begin(ctx)
		if err != nil {
			return err
		}
		defer tx.Rollback(ctx) //nolint:errcheck
		_, err = tx.Exec(ctx, `SET LOCAL "osac.deprovision_in_progress" = 'true'`)
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx,
			fmt.Sprintf(`update %s set deletion_timestamp = now() where id = $1`, table), id)
		if err != nil {
			return err
		}
		return tx.Commit(ctx)
	}

	expectBlocked := func(err error, resourceType string) {
		Expect(err).To(HaveOccurred())
		var pgErr *pgconn.PgError
		Expect(errors.As(err, &pgErr)).To(BeTrue())
		Expect(pgErr.Code).To(Equal("Z0003"))
		Expect(pgErr.Message).To(ContainSubstring("default"))
		Expect(pgErr.Message).To(ContainSubstring(resourceType))
		Expect(pgErr.Message).To(ContainSubstring("system-managed"))
	}

	tables := []struct {
		table        string
		resourceType string
	}{
		{"virtual_networks", "virtual network"},
		{"subnets", "subnet"},
		{"nat_gateways", "NAT gateway"},
		{"security_groups", "security group"},
		{"external_ips", "external IP"},
	}

	for _, tt := range tables {
		tt := tt
		Describe(tt.table, func() {
			It(fmt.Sprintf("blocks soft-deleting a default-labeled %s", tt.resourceType), func(ctx context.Context) {
				insertResource(ctx, tt.table, tt.table+"-default-1", defaultLabels)
				err := softDelete(ctx, tt.table, tt.table+"-default-1")
				expectBlocked(err, tt.resourceType)
			})

			It(fmt.Sprintf("allows soft-deleting a non-default %s", tt.resourceType), func(ctx context.Context) {
				insertResource(ctx, tt.table, tt.table+"-normal-1", emptyLabels)
				err := softDelete(ctx, tt.table, tt.table+"-normal-1")
				Expect(err).ToNot(HaveOccurred())
			})

			It(fmt.Sprintf("allows soft-deleting a default-labeled %s when deprovision bypass is set", tt.resourceType), func(ctx context.Context) {
				insertResource(ctx, tt.table, tt.table+"-bypass-1", defaultLabels)
				err := softDeleteWithBypass(ctx, tt.table, tt.table+"-bypass-1")
				Expect(err).ToNot(HaveOccurred())
			})
		})
	}
})
