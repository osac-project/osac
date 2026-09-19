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

var _ = DescribeMigration("Add SSH key delete protection trigger", func() {
	BeforeEach(func(ctx context.Context) {
		err := tool.Migrate(ctx, 117)
		Expect(err).ToNot(HaveOccurred())
	})

	insertKey := func(ctx context.Context, id string) {
		_, err := conn.Exec(ctx,
			`insert into ssh_keys (id, name, tenant, data) values ($1, $1, 'shared', '{"spec":{"public_key":"fixture"}}')`, id)
		Expect(err).ToNot(HaveOccurred())
	}

	deleteKey := func(ctx context.Context, id string) error {
		_, err := conn.Exec(ctx, `update ssh_keys set deletion_timestamp = now() where id = $1`, id)
		return err
	}

	expectInUse := func(err error, id string) {
		Expect(err).To(HaveOccurred())
		var pgErr *pgconn.PgError
		Expect(errors.As(err, &pgErr)).To(BeTrue())
		Expect(pgErr.Code).To(Equal("Z0003"))
		Expect(pgErr.Message).To(ContainSubstring(id))
	}

	It("prevents deletion while an active ComputeInstance references the key", func(ctx context.Context) {
		insertKey(ctx, "key-compute")
		_, err := conn.Exec(ctx, `
			insert into compute_instances (id, name, tenant, data)
			values ('ci-key', 'ci-key', 'shared', '{"spec":{"ssh_key":{"id":"key-compute"}}}')`)
		Expect(err).ToNot(HaveOccurred())

		expectInUse(deleteKey(ctx, "key-compute"), "key-compute")
	})

	It("prevents deletion while an active BareMetalInstance references the key", func(ctx context.Context) {
		insertKey(ctx, "key-baremetal")
		_, err := conn.Exec(ctx, `
			insert into bare_metal_instances (id, name, tenant, data)
			values ('bmi-key', 'bmi-key', 'shared', '{"spec":{"ssh_key":{"id":"key-baremetal"}}}')`)
		Expect(err).ToNot(HaveOccurred())

		expectInUse(deleteKey(ctx, "key-baremetal"), "key-baremetal")
	})

	It("allows deletion after the referencing instance is soft-deleted", func(ctx context.Context) {
		insertKey(ctx, "key-released")
		_, err := conn.Exec(ctx, `
			insert into compute_instances (id, name, tenant, data)
			values ('ci-released', 'ci-released', 'shared', '{"spec":{"ssh_key":{"id":"key-released"}}}')`)
		Expect(err).ToNot(HaveOccurred())

		_, err = conn.Exec(ctx, `update compute_instances set deletion_timestamp = now() where id = 'ci-released'`)
		Expect(err).ToNot(HaveOccurred())
		Expect(deleteKey(ctx, "key-released")).ToNot(HaveOccurred())
	})

	It("allows deletion of an unreferenced key", func(ctx context.Context) {
		insertKey(ctx, "key-unused")
		Expect(deleteKey(ctx, "key-unused")).ToNot(HaveOccurred())
	})
})
