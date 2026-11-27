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

var _ = DescribeMigration("Protect status instance type references", func() {
	insertType := func(ctx context.Context, id string) {
		_, err := conn.Exec(ctx, `insert into instance_types (id, name, tenant, data)
			values ($1, $1, 'system', '{"spec":{"cores":2,"memory_gib":4}}'::jsonb)`, id)
		Expect(err).ToNot(HaveOccurred())
	}

	insertInstance := func(ctx context.Context, id, data string) {
		_, err := conn.Exec(ctx, `insert into compute_instances (id, name, tenant, data)
			values ($1, $1, 'system', $2::jsonb)`, id, data)
		Expect(err).ToNot(HaveOccurred())
	}

	deleteType := func(ctx context.Context, id string) error {
		_, err := conn.Exec(ctx, `update instance_types set deletion_timestamp = now() where id = $1`, id)
		return err
	}

	expectInUse := func(err error) {
		var pgErr *pgconn.PgError
		ExpectWithOffset(1, errors.As(err, &pgErr)).To(BeTrue())
		ExpectWithOffset(1, pgErr.Code).To(Equal("Z0003"))
		ExpectWithOffset(1, pgErr.Message).To(ContainSubstring("in use by at least one compute instance"))
	}

	It("protects an InstanceType held only by the status of a resized instance", func(ctx context.Context) {
		insertType(ctx, "old-type")
		insertType(ctx, "new-type")
		insertInstance(ctx, "instance", `{
			"spec":{"instance_type":{"id":"new-type"}},
			"status":{"instance_type":{"id":"old-type"}}
		}`)
		Expect(tool.Migrate(ctx, 158)).To(Succeed())

		expectInUse(deleteType(ctx, "old-type"))
	})

	It("protects a status reference added after the migration", func(ctx context.Context) {
		insertType(ctx, "old-type")
		insertInstance(ctx, "instance", `{}`)
		Expect(tool.Migrate(ctx, 158)).To(Succeed())

		_, err := conn.Exec(ctx, `update compute_instances set data = jsonb_set(data,
			'{status}', '{
				"instance_type":{"id":"old-type"},
				"instance_type_transition_time":"2030-01-01T00:00:00Z"
			}'::jsonb) where id = 'instance'`)
		Expect(err).ToNot(HaveOccurred())
		expectInUse(deleteType(ctx, "old-type"))
	})

	It("retains the protection of a spec reference", func(ctx context.Context) {
		insertType(ctx, "spec-type")
		insertInstance(ctx, "instance", `{"spec":{"instance_type":{"id":"spec-type"}}}`)
		Expect(tool.Migrate(ctx, 158)).To(Succeed())

		expectInUse(deleteType(ctx, "spec-type"))
	})

	It("retains the protection of a template reference", func(ctx context.Context) {
		insertType(ctx, "template-type")
		_, err := conn.Exec(ctx, `insert into compute_instance_templates (id, name, tenant, data)
			values ('template', 'template', 'system', '{"spec_defaults":{"instance_type":{"id":"template-type"}}}'::jsonb)`)
		Expect(err).ToNot(HaveOccurred())
		Expect(tool.Migrate(ctx, 158)).To(Succeed())

		_, err = conn.Exec(ctx, `update instance_types set deletion_timestamp = now() where id = 'template-type'`)
		var pgErr *pgconn.PgError
		Expect(errors.As(err, &pgErr)).To(BeTrue())
		Expect(pgErr.Code).To(Equal("Z0003"))
		Expect(pgErr.Message).To(ContainSubstring("compute instance template"))
	})

	It("keeps the type of a soft-deleted instance until its row is archived", func(ctx context.Context) {
		insertType(ctx, "old-type")
		insertInstance(ctx, "instance", `{"status":{"instance_type":{"id":"old-type"}}}`)
		Expect(tool.Migrate(ctx, 158)).To(Succeed())

		expectInUse(deleteType(ctx, "old-type"))
		_, err := conn.Exec(ctx, `update compute_instances set deletion_timestamp = now() where id = 'instance'`)
		Expect(err).ToNot(HaveOccurred())

		// The quota counter counts an instance until it is archived, so a soft-deleted
		// instance still holds its type:
		expectInUse(deleteType(ctx, "old-type"))

		_, err = conn.Exec(ctx, `delete from compute_instances where id = 'instance'`)
		Expect(err).ToNot(HaveOccurred())
		Expect(deleteType(ctx, "old-type")).To(Succeed())
	})
})
